package telegram

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/carlos/spinner/internal/actions"
	"github.com/carlos/spinner/internal/gateway"
	"github.com/carlos/spinner/internal/llm"
	llmsafety "github.com/carlos/spinner/internal/llm/safety"
	"github.com/carlos/spinner/internal/memorylog"
	"github.com/carlos/spinner/internal/store"
)

const pairingMessage = "pair"

type PairingStore interface {
	CreatePairingRequest(ctx context.Context, input store.CreatePairingRequestInput) (store.PairingRequestWithToken, error)
	EnsureContextForExternalChannel(ctx context.Context, connector, externalID, displayName string) (store.ContextRecord, error)
	LookupUserIdentity(ctx context.Context, connector, connectorUserID string) (store.UserIdentity, error)
	CreateActionApproval(ctx context.Context, input store.CreateActionApprovalInput) (store.ActionApproval, error)
}

type CommandGateway interface {
	HandleMessage(ctx context.Context, input gateway.MessageInput) (gateway.MessageOutput, error)
}

type Responder interface {
	Reply(ctx context.Context, input llm.MessageInput) (string, error)
}

type SafetyPolicy interface {
	Check(input llmsafety.Request) llmsafety.Decision
}

type Connector struct {
	token       string
	apiBase     string
	workspace   string
	pollSeconds int
	pairings    PairingStore
	gateway     CommandGateway
	responder   Responder
	policy      SafetyPolicy
	httpClient  *http.Client
	logger      *slog.Logger
	botUsername string
	offset      int64
}

func New(token, apiBase, workspaceRoot string, pollSeconds int, pairings PairingStore, commandGateway CommandGateway, responder Responder, policy SafetyPolicy, logger *slog.Logger) *Connector {
	if strings.TrimSpace(apiBase) == "" {
		apiBase = "https://api.telegram.org"
	}
	if pollSeconds < 1 {
		pollSeconds = 25
	}
	return &Connector{
		token:       strings.TrimSpace(token),
		apiBase:     strings.TrimRight(strings.TrimSpace(apiBase), "/"),
		workspace:   strings.TrimSpace(workspaceRoot),
		pollSeconds: pollSeconds,
		pairings:    pairings,
		gateway:     commandGateway,
		responder:   responder,
		policy:      policy,
		httpClient: &http.Client{
			Timeout: time.Duration(pollSeconds+10) * time.Second,
		},
		logger: logger,
		offset: 0,
	}
}

func (c *Connector) Name() string {
	return "telegram"
}

func (c *Connector) Start(ctx context.Context) error {
	if c.token == "" {
		c.logger.Info("connector disabled, token missing")
		<-ctx.Done()
		return nil
	}
	if c.pairings == nil {
		c.logger.Info("connector disabled, pairing store missing")
		<-ctx.Done()
		return nil
	}
	if c.gateway == nil {
		c.logger.Info("connector disabled, gateway missing")
		<-ctx.Done()
		return nil
	}

	c.logger.Info("connector started", "api_base", c.apiBase)
	if username, err := c.fetchBotUsername(ctx); err == nil {
		c.botUsername = username
		if c.botUsername != "" {
			c.logger.Info("telegram bot identity loaded", "username", c.botUsername)
		}
	} else {
		c.logger.Warn("telegram bot username lookup failed", "error", err)
	}

	for {
		if ctx.Err() != nil {
			c.logger.Info("connector stopped")
			return nil
		}
		if err := c.pollOnce(ctx); err != nil && ctx.Err() == nil {
			c.logger.Error("poll failed", "error", err)
			select {
			case <-ctx.Done():
				c.logger.Info("connector stopped")
				return nil
			case <-time.After(1500 * time.Millisecond):
			}
		}
	}
}

func (c *Connector) pollOnce(ctx context.Context) error {
	url := fmt.Sprintf("%s/bot%s/getUpdates?timeout=%d&offset=%d", c.apiBase, c.token, c.pollSeconds, c.offset)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}

	res, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()

	var payload getUpdatesResponse
	if err := json.NewDecoder(res.Body).Decode(&payload); err != nil {
		return fmt.Errorf("decode getUpdates: %w", err)
	}
	if !payload.OK {
		return fmt.Errorf("telegram getUpdates failed")
	}

	for _, update := range payload.Result {
		if update.UpdateID >= c.offset {
			c.offset = update.UpdateID + 1
		}
		if update.Message == nil {
			continue
		}
		if err := c.handleMessage(ctx, *update.Message); err != nil {
			c.logger.Error("handle message failed", "error", err, "update_id", update.UpdateID)
		}
	}
	return nil
}

func (c *Connector) handleMessage(ctx context.Context, message telegramMessage) error {
	contextRecord, contextErr := c.pairings.EnsureContextForExternalChannel(
		ctx,
		"telegram",
		strconv.FormatInt(message.Chat.ID, 10),
		message.Chat.Title,
	)
	if contextErr != nil {
		c.logger.Error("ensure context failed", "error", contextErr, "chat_id", message.Chat.ID)
	}

	text := strings.TrimSpace(message.Text)
	if text == "" {
		text = strings.TrimSpace(message.Caption)
	}
	c.logInbound(contextRecord, message, text)
	trimmed := normalizeIncoming(text)
	if message.Chat.Type == "private" && trimmed == pairingMessage {
		displayName := userDisplayName(message.From)
		pairing, err := c.pairings.CreatePairingRequest(ctx, store.CreatePairingRequestInput{
			Connector:       "telegram",
			ConnectorUserID: strconv.FormatInt(message.From.ID, 10),
			DisplayName:     displayName,
		})
		if err != nil {
			return err
		}

		reply := fmt.Sprintf(
			"Pairing token: `%s`\nOpen Spinner TUI and approve this token.\nThis token expires at %s UTC.",
			pairing.Token,
			pairing.ExpiresAt.Format("2006-01-02 15:04:05"),
		)
		c.logOutbound(contextRecord, message, reply)
		return c.sendMessage(ctx, message.Chat.ID, reply)
	}

	attachmentReply := ""
	if message.Document != nil {
		reply, err := c.ingestMarkdownDocument(ctx, message, *message.Document)
		if err != nil {
			c.logger.Error("markdown attachment ingest failed", "error", err, "chat_id", message.Chat.ID, "message_id", message.MessageID)
		} else {
			attachmentReply = strings.TrimSpace(reply)
		}
	}

	if strings.TrimSpace(text) == "" {
		if attachmentReply == "" {
			return nil
		}
		c.logOutbound(contextRecord, message, attachmentReply)
		return c.sendMessage(ctx, message.Chat.ID, attachmentReply)
	}

	output, err := c.gateway.HandleMessage(ctx, gateway.MessageInput{
		Connector:   "telegram",
		ExternalID:  strconv.FormatInt(message.Chat.ID, 10),
		DisplayName: message.Chat.Title,
		FromUserID:  strconv.FormatInt(message.From.ID, 10),
		Text:        text,
	})
	if err != nil {
		return err
	}
	if !output.Handled || strings.TrimSpace(output.Reply) == "" {
		replyToSend := attachmentReply
		shouldReply, isMention := c.shouldAutoReply(message, text)
		if shouldReply {
			llmReply, notice, llmErr := c.generateReply(ctx, contextRecord, message, text, isMention)
			if llmErr != nil {
				if replyToSend == "" {
					return nil
				}
				return c.sendMessage(ctx, message.Chat.ID, replyToSend)
			}
			if strings.TrimSpace(notice) != "" {
				if replyToSend != "" {
					replyToSend = strings.TrimSpace(notice) + "\n\n" + replyToSend
				} else {
					replyToSend = strings.TrimSpace(notice)
				}
			}
			if strings.TrimSpace(llmReply) != "" {
				if replyToSend != "" {
					replyToSend = strings.TrimSpace(llmReply) + "\n\n" + replyToSend
				} else {
					replyToSend = strings.TrimSpace(llmReply)
				}
			}
		}
		if replyToSend == "" {
			return nil
		}
		c.logOutbound(contextRecord, message, replyToSend)
		return c.sendMessage(ctx, message.Chat.ID, replyToSend)
	}
	if attachmentReply != "" {
		output.Reply = strings.TrimSpace(output.Reply) + "\n\n" + attachmentReply
	}
	if strings.TrimSpace(output.Reply) == "" {
		return nil
	}
	c.logOutbound(contextRecord, message, output.Reply)
	return c.sendMessage(ctx, message.Chat.ID, output.Reply)
}

func (c *Connector) shouldAutoReply(message telegramMessage, text string) (bool, bool) {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" || strings.HasPrefix(trimmed, "/") {
		return false, false
	}
	if message.Chat.Type == "private" {
		return true, false
	}
	isMention := false
	if c.botUsername == "" {
		return true, false
	}
	mention := "@" + strings.ToLower(strings.TrimSpace(c.botUsername))
	isMention = strings.Contains(strings.ToLower(trimmed), mention)
	return true, isMention
}

func (c *Connector) generateReply(ctx context.Context, contextRecord store.ContextRecord, message telegramMessage, text string, isMention bool) (string, string, error) {
	if c.responder == nil {
		return "", "", nil
	}
	role := ""
	identity, err := c.pairings.LookupUserIdentity(ctx, "telegram", strconv.FormatInt(message.From.ID, 10))
	if err == nil {
		role = identity.Role
	} else if !errors.Is(err, store.ErrIdentityNotFound) {
		c.logger.Error("telegram identity lookup failed", "error", err)
	}
	if c.policy != nil {
		decision := c.policy.Check(llmsafety.Request{
			Connector: "telegram",
			ContextID: contextRecord.ID,
			UserID:    strconv.FormatInt(message.From.ID, 10),
			UserRole:  role,
			IsDM:      message.Chat.Type == "private",
			IsMention: isMention,
		})
		if !decision.Allowed {
			return "", strings.TrimSpace(decision.Notify), nil
		}
	}
	prompt := strings.TrimSpace(text)
	if c.botUsername != "" {
		prompt = strings.ReplaceAll(prompt, "@"+c.botUsername, "")
		prompt = strings.ReplaceAll(prompt, "@"+strings.ToLower(c.botUsername), "")
		prompt = strings.TrimSpace(prompt)
	}
	if prompt == "" {
		return "", "", nil
	}
	reply, err := c.responder.Reply(ctx, llm.MessageInput{
		Connector:   "telegram",
		WorkspaceID: contextRecord.WorkspaceID,
		ContextID:   contextRecord.ID,
		ExternalID:  strconv.FormatInt(message.Chat.ID, 10),
		DisplayName: message.Chat.Title,
		FromUserID:  strconv.FormatInt(message.From.ID, 10),
		Text:        prompt,
		IsDM:        message.Chat.Type == "private",
	})
	if err != nil {
		c.logger.Error("telegram llm reply failed", "error", err)
		return "", "", err
	}
	cleanReply, proposal := actions.ExtractProposal(strings.TrimSpace(reply))
	if proposal == nil {
		return strings.TrimSpace(cleanReply), "", nil
	}
	approval, err := c.pairings.CreateActionApproval(ctx, store.CreateActionApprovalInput{
		WorkspaceID:     contextRecord.WorkspaceID,
		ContextID:       contextRecord.ID,
		Connector:       "telegram",
		ExternalID:      strconv.FormatInt(message.Chat.ID, 10),
		RequesterUserID: strconv.FormatInt(message.From.ID, 10),
		ActionType:      proposal.Type,
		ActionTarget:    proposal.Target,
		ActionSummary:   proposal.Summary,
		Payload:         proposal.Raw,
	})
	if err != nil {
		c.logger.Error("create action approval failed", "error", err)
		return strings.TrimSpace(cleanReply), "", nil
	}
	notice := fmt.Sprintf("Action request pending approval: `%s`. Admin can run `/pending-actions`, `/approve-action %s`, or `/deny-action %s`.", approval.ID, approval.ID, approval.ID)
	if strings.TrimSpace(cleanReply) == "" {
		return "", notice, nil
	}
	return strings.TrimSpace(cleanReply), notice, nil
}

func (c *Connector) ingestMarkdownDocument(ctx context.Context, message telegramMessage, document telegramDocument) (string, error) {
	if c.workspace == "" || c.pairings == nil {
		return "", nil
	}
	filename := sanitizeFilename(document.FileName)
	if !isMarkdown(filename, document.MimeType) {
		return "", nil
	}

	contextRecord, err := c.pairings.EnsureContextForExternalChannel(
		ctx,
		"telegram",
		strconv.FormatInt(message.Chat.ID, 10),
		message.Chat.Title,
	)
	if err != nil {
		return "", err
	}
	workspacePath := filepath.Join(c.workspace, contextRecord.WorkspaceID)
	targetDir := filepath.Join(workspacePath, "inbox", "telegram", strconv.FormatInt(message.Chat.ID, 10))
	if err := os.MkdirAll(targetDir, 0o755); err != nil {
		return "", err
	}

	filePath, err := c.lookupFilePath(ctx, document.FileID)
	if err != nil {
		return "", err
	}
	fileContent, err := c.downloadFile(ctx, filePath)
	if err != nil {
		return "", err
	}

	targetName := fmt.Sprintf("%d-%s", message.MessageID, filename)
	targetPath := filepath.Join(targetDir, targetName)
	if err := os.WriteFile(targetPath, fileContent, 0o644); err != nil {
		return "", err
	}

	relativePath, err := filepath.Rel(workspacePath, targetPath)
	if err != nil {
		relativePath = targetName
	}
	return fmt.Sprintf("Attachment saved: `%s`", filepath.ToSlash(relativePath)), nil
}

func (c *Connector) lookupFilePath(ctx context.Context, fileID string) (string, error) {
	url := fmt.Sprintf("%s/bot%s/getFile?file_id=%s", c.apiBase, c.token, fileID)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	res, err := c.httpClient.Do(req)
	if err != nil {
		return "", err
	}
	defer res.Body.Close()

	var payload struct {
		OK     bool `json:"ok"`
		Result struct {
			FilePath string `json:"file_path"`
		} `json:"result"`
	}
	if err := json.NewDecoder(res.Body).Decode(&payload); err != nil {
		return "", fmt.Errorf("decode getFile: %w", err)
	}
	if !payload.OK || strings.TrimSpace(payload.Result.FilePath) == "" {
		return "", fmt.Errorf("telegram getFile failed")
	}
	return payload.Result.FilePath, nil
}

func (c *Connector) downloadFile(ctx context.Context, filePath string) ([]byte, error) {
	url := fmt.Sprintf("%s/file/bot%s/%s", c.apiBase, c.token, strings.TrimLeft(filePath, "/"))
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	res, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return nil, fmt.Errorf("telegram file download failed with status %d", res.StatusCode)
	}
	return ioReadAllLimited(res.Body, 2<<20)
}

func (c *Connector) fetchBotUsername(ctx context.Context) (string, error) {
	url := fmt.Sprintf("%s/bot%s/getMe", c.apiBase, c.token)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	res, err := c.httpClient.Do(req)
	if err != nil {
		return "", err
	}
	defer res.Body.Close()

	var payload struct {
		OK     bool `json:"ok"`
		Result struct {
			Username string `json:"username"`
		} `json:"result"`
	}
	if err := json.NewDecoder(res.Body).Decode(&payload); err != nil {
		return "", err
	}
	if !payload.OK {
		return "", fmt.Errorf("telegram getMe failed")
	}
	return strings.TrimSpace(payload.Result.Username), nil
}

func (c *Connector) logInbound(contextRecord store.ContextRecord, message telegramMessage, text string) {
	logText := strings.TrimSpace(text)
	if logText == "" && message.Document != nil {
		logText = fmt.Sprintf("[attachment] %s", strings.TrimSpace(message.Document.FileName))
	}
	if logText == "" {
		return
	}
	if err := memorylog.Append(memorylog.Entry{
		WorkspaceRoot: c.workspace,
		WorkspaceID:   contextRecord.WorkspaceID,
		Connector:     "telegram",
		ExternalID:    strconv.FormatInt(message.Chat.ID, 10),
		Direction:     "inbound",
		ActorID:       strconv.FormatInt(message.From.ID, 10),
		DisplayName:   message.Chat.Title,
		Text:          logText,
		Timestamp:     time.Now().UTC(),
	}); err != nil {
		c.logger.Error("inbound log append failed", "error", err, "chat_id", message.Chat.ID)
	}
}

func (c *Connector) logOutbound(contextRecord store.ContextRecord, message telegramMessage, text string) {
	logText := strings.TrimSpace(text)
	if logText == "" {
		return
	}
	if err := memorylog.Append(memorylog.Entry{
		WorkspaceRoot: c.workspace,
		WorkspaceID:   contextRecord.WorkspaceID,
		Connector:     "telegram",
		ExternalID:    strconv.FormatInt(message.Chat.ID, 10),
		Direction:     "outbound",
		ActorID:       "spinner",
		DisplayName:   message.Chat.Title,
		Text:          logText,
		Timestamp:     time.Now().UTC(),
	}); err != nil {
		c.logger.Error("outbound log append failed", "error", err, "chat_id", message.Chat.ID)
	}
}

func (c *Connector) sendMessage(ctx context.Context, chatID int64, text string) error {
	endpoint := fmt.Sprintf("%s/bot%s/sendMessage", c.apiBase, c.token)
	body := map[string]any{
		"chat_id":    chatID,
		"text":       text,
		"parse_mode": "Markdown",
	}
	payload, err := json.Marshal(body)
	if err != nil {
		return err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(payload))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")

	res, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()

	var response struct {
		OK bool `json:"ok"`
	}
	if err := json.NewDecoder(res.Body).Decode(&response); err != nil {
		return fmt.Errorf("decode sendMessage: %w", err)
	}
	if !response.OK {
		return fmt.Errorf("telegram sendMessage failed")
	}
	return nil
}

func normalizeIncoming(input string) string {
	text := strings.TrimSpace(strings.ToLower(input))
	if strings.HasPrefix(text, "/") {
		text = strings.TrimPrefix(text, "/")
	}
	if strings.HasPrefix(text, "pair@") {
		return pairingMessage
	}
	return text
}

func userDisplayName(user telegramUser) string {
	parts := []string{strings.TrimSpace(user.FirstName), strings.TrimSpace(user.LastName)}
	fullName := strings.TrimSpace(strings.Join(parts, " "))
	if fullName != "" {
		return fullName
	}
	if strings.TrimSpace(user.Username) != "" {
		return user.Username
	}
	return strconv.FormatInt(user.ID, 10)
}

type getUpdatesResponse struct {
	OK     bool             `json:"ok"`
	Result []telegramUpdate `json:"result"`
}

type telegramUpdate struct {
	UpdateID int64            `json:"update_id"`
	Message  *telegramMessage `json:"message"`
}

type telegramMessage struct {
	MessageID int64             `json:"message_id"`
	From      telegramUser      `json:"from"`
	Chat      telegramChat      `json:"chat"`
	Text      string            `json:"text"`
	Caption   string            `json:"caption"`
	Document  *telegramDocument `json:"document"`
}

type telegramChat struct {
	ID    int64  `json:"id"`
	Type  string `json:"type"`
	Title string `json:"title"`
}

type telegramUser struct {
	ID        int64  `json:"id"`
	FirstName string `json:"first_name"`
	LastName  string `json:"last_name"`
	Username  string `json:"username"`
}

type telegramDocument struct {
	FileID   string `json:"file_id"`
	FileName string `json:"file_name"`
	MimeType string `json:"mime_type"`
}

var filenameSanitizer = regexp.MustCompile(`[^a-zA-Z0-9._-]+`)

func sanitizeFilename(input string) string {
	base := strings.TrimSpace(filepath.Base(input))
	base = filenameSanitizer.ReplaceAllString(base, "-")
	base = strings.Trim(base, "-.")
	if base == "" {
		return "attachment.md"
	}
	return base
}

func isMarkdown(filename, mimeType string) bool {
	extension := strings.ToLower(strings.TrimSpace(filepath.Ext(filename)))
	if extension == ".md" || extension == ".markdown" {
		return true
	}
	mimeType = strings.ToLower(strings.TrimSpace(mimeType))
	return mimeType == "text/markdown" || mimeType == "text/x-markdown"
}

func ioReadAllLimited(body io.Reader, maxBytes int64) ([]byte, error) {
	limited := &io.LimitedReader{R: body, N: maxBytes + 1}
	data, err := io.ReadAll(limited)
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > maxBytes {
		return nil, fmt.Errorf("attachment too large")
	}
	return data, nil
}
