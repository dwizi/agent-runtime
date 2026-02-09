package discord

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
	"sync"
	"time"

	"github.com/carlos/spinner/internal/actions"
	"github.com/gorilla/websocket"

	"github.com/carlos/spinner/internal/gateway"
	"github.com/carlos/spinner/internal/llm"
	llmsafety "github.com/carlos/spinner/internal/llm/safety"
	"github.com/carlos/spinner/internal/memorylog"
	"github.com/carlos/spinner/internal/store"
)

const (
	pairCommand = "pair"

	discordIntentGuilds          = 1 << 0
	discordIntentGuildMessages   = 1 << 9
	discordIntentDirectMessages  = 1 << 12
	discordIntentMessageContents = 1 << 15
)

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
	token      string
	apiBase    string
	gatewayURL string
	workspace  string
	pairings   PairingStore
	gateway    CommandGateway
	responder  Responder
	policy     SafetyPolicy
	httpClient *http.Client
	logger     *slog.Logger
	botUserID  string
}

func New(token, apiBase, gatewayURL, workspaceRoot string, pairings PairingStore, commandGateway CommandGateway, responder Responder, policy SafetyPolicy, logger *slog.Logger) *Connector {
	if strings.TrimSpace(apiBase) == "" {
		apiBase = "https://discord.com/api/v10"
	}
	if strings.TrimSpace(gatewayURL) == "" {
		gatewayURL = "wss://gateway.discord.gg/?v=10&encoding=json"
	}
	return &Connector{
		token:      strings.TrimSpace(token),
		apiBase:    strings.TrimRight(strings.TrimSpace(apiBase), "/"),
		gatewayURL: strings.TrimSpace(gatewayURL),
		workspace:  strings.TrimSpace(workspaceRoot),
		pairings:   pairings,
		gateway:    commandGateway,
		responder:  responder,
		policy:     policy,
		httpClient: &http.Client{Timeout: 12 * time.Second},
		logger:     logger,
	}
}

func (c *Connector) Name() string {
	return "discord"
}

func (c *Connector) Publish(ctx context.Context, externalID, text string) error {
	channelID := strings.TrimSpace(externalID)
	if channelID == "" {
		return fmt.Errorf("discord external id is required")
	}
	content := strings.TrimSpace(text)
	if content == "" {
		return nil
	}
	return c.sendChannelMessage(ctx, channelID, content)
}

func (c *Connector) Start(ctx context.Context) error {
	if c.token == "" {
		c.logger.Info("connector disabled, token missing")
		<-ctx.Done()
		return nil
	}
	if c.pairings == nil || c.gateway == nil {
		c.logger.Info("connector disabled, dependencies missing")
		<-ctx.Done()
		return nil
	}

	c.logger.Info("connector started", "mode", "gateway")
	for {
		if ctx.Err() != nil {
			c.logger.Info("connector stopped")
			return nil
		}
		if err := c.runSession(ctx); err != nil {
			if ctx.Err() != nil {
				c.logger.Info("connector stopped")
				return nil
			}
			c.logger.Error("discord session ended, reconnecting", "error", err)
			select {
			case <-ctx.Done():
				c.logger.Info("connector stopped")
				return nil
			case <-time.After(2 * time.Second):
			}
		}
	}
}

func (c *Connector) runSession(ctx context.Context) error {
	conn, _, err := websocket.DefaultDialer.DialContext(ctx, c.gatewayURL, nil)
	if err != nil {
		return fmt.Errorf("dial discord gateway: %w", err)
	}
	defer conn.Close()

	var (
		writeMu      sync.Mutex
		sequence     int64
		heartbeatSec = 30 * time.Second
	)

	readHelloDone := false
	for !readHelloDone {
		_, data, err := conn.ReadMessage()
		if err != nil {
			return fmt.Errorf("read hello: %w", err)
		}
		var envelope gatewayEnvelope
		if err := json.Unmarshal(data, &envelope); err != nil {
			return fmt.Errorf("decode hello payload: %w", err)
		}
		if envelope.Op != 10 {
			continue
		}
		var hello discordHello
		if err := json.Unmarshal(envelope.D, &hello); err != nil {
			return fmt.Errorf("decode hello body: %w", err)
		}
		heartbeatSec = time.Duration(hello.HeartbeatIntervalMS) * time.Millisecond
		readHelloDone = true
	}

	if err := c.sendIdentify(conn, &writeMu); err != nil {
		return err
	}

	heartbeatCtx, cancelHeartbeat := context.WithCancel(ctx)
	defer cancelHeartbeat()
	go c.heartbeatLoop(heartbeatCtx, conn, &writeMu, &sequence, heartbeatSec)

	for {
		_, data, err := conn.ReadMessage()
		if err != nil {
			return fmt.Errorf("read gateway message: %w", err)
		}

		var envelope gatewayEnvelope
		if err := json.Unmarshal(data, &envelope); err != nil {
			c.logger.Error("decode gateway envelope failed", "error", err)
			continue
		}
		if envelope.S != nil {
			sequence = *envelope.S
		}

		switch envelope.Op {
		case 0:
			if envelope.T == "READY" {
				var ready discordReady
				if err := json.Unmarshal(envelope.D, &ready); err == nil {
					c.botUserID = strings.TrimSpace(ready.User.ID)
				}
			}
			if envelope.T == "MESSAGE_CREATE" {
				var message discordMessageCreate
				if err := json.Unmarshal(envelope.D, &message); err != nil {
					c.logger.Error("decode message create failed", "error", err)
					continue
				}
				if err := c.handleMessageCreate(ctx, message); err != nil {
					c.logger.Error("handle discord message failed", "error", err)
				}
			}
		case 1:
			if err := c.sendHeartbeat(conn, &writeMu, sequence); err != nil {
				return err
			}
		case 7:
			return fmt.Errorf("gateway requested reconnect")
		case 9:
			return fmt.Errorf("gateway invalid session")
		}
	}
}

func (c *Connector) heartbeatLoop(ctx context.Context, conn *websocket.Conn, writeMu *sync.Mutex, seq *int64, interval time.Duration) {
	if interval < time.Second {
		interval = 5 * time.Second
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := c.sendHeartbeat(conn, writeMu, *seq); err != nil {
				c.logger.Error("heartbeat failed", "error", err)
				return
			}
		}
	}
}

func (c *Connector) sendIdentify(conn *websocket.Conn, writeMu *sync.Mutex) error {
	payload := map[string]any{
		"op": 2,
		"d": map[string]any{
			"token": c.token,
			"intents": discordIntentGuilds |
				discordIntentGuildMessages |
				discordIntentDirectMessages |
				discordIntentMessageContents,
			"properties": map[string]string{
				"os":      "linux",
				"browser": "spinner",
				"device":  "spinner",
			},
		},
	}
	writeMu.Lock()
	defer writeMu.Unlock()
	if err := conn.WriteJSON(payload); err != nil {
		return fmt.Errorf("send identify: %w", err)
	}
	return nil
}

func (c *Connector) sendHeartbeat(conn *websocket.Conn, writeMu *sync.Mutex, seq int64) error {
	writeMu.Lock()
	defer writeMu.Unlock()
	payload := map[string]any{
		"op": 1,
		"d":  seq,
	}
	if err := conn.WriteJSON(payload); err != nil {
		return fmt.Errorf("send heartbeat: %w", err)
	}
	return nil
}

func (c *Connector) handleMessageCreate(ctx context.Context, message discordMessageCreate) error {
	if message.Author.Bot {
		return nil
	}
	displayName := message.ChannelID
	if message.GuildID != "" {
		displayName = message.GuildID
	}
	contextRecord, contextErr := c.pairings.EnsureContextForExternalChannel(
		ctx,
		"discord",
		message.ChannelID,
		displayName,
	)
	if contextErr != nil {
		c.logger.Error("ensure context failed", "error", contextErr, "channel_id", message.ChannelID)
	}

	text := strings.TrimSpace(message.Content)
	c.logInbound(contextRecord, message, text)
	attachmentReply, err := c.ingestMarkdownAttachments(ctx, message)
	if err != nil {
		c.logger.Error("discord attachment ingest failed", "error", err, "channel_id", message.ChannelID, "message_id", message.ID)
	}
	if text == "" {
		if attachmentReply != "" {
			c.logOutbound(contextRecord, message, attachmentReply)
			return c.sendChannelMessage(ctx, message.ChannelID, attachmentReply)
		}
		return nil
	}

	if message.GuildID == "" && normalize(text) == pairCommand {
		pairing, err := c.pairings.CreatePairingRequest(ctx, store.CreatePairingRequestInput{
			Connector:       "discord",
			ConnectorUserID: message.Author.ID,
			DisplayName:     discordDisplayName(message.Author),
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
		return c.sendChannelMessage(ctx, message.ChannelID, reply)
	}

	output, err := c.gateway.HandleMessage(ctx, gateway.MessageInput{
		Connector:   "discord",
		ExternalID:  message.ChannelID,
		DisplayName: displayName,
		FromUserID:  message.Author.ID,
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
			if llmErr == nil && strings.TrimSpace(notice) != "" {
				if replyToSend != "" {
					replyToSend = strings.TrimSpace(notice) + "\n\n" + replyToSend
				} else {
					replyToSend = strings.TrimSpace(notice)
				}
			}
			if llmErr == nil && strings.TrimSpace(llmReply) != "" {
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
		return c.sendChannelMessage(ctx, message.ChannelID, replyToSend)
	}
	if attachmentReply != "" {
		output.Reply = strings.TrimSpace(output.Reply) + "\n\n" + attachmentReply
	}
	if strings.TrimSpace(output.Reply) == "" {
		return nil
	}
	c.logOutbound(contextRecord, message, output.Reply)
	return c.sendChannelMessage(ctx, message.ChannelID, output.Reply)
}

func (c *Connector) shouldAutoReply(message discordMessageCreate, text string) (bool, bool) {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" || strings.HasPrefix(trimmed, "/") {
		return false, false
	}
	if message.GuildID == "" {
		return true, false
	}
	for _, mention := range message.Mentions {
		if strings.TrimSpace(mention.ID) == strings.TrimSpace(c.botUserID) && c.botUserID != "" {
			return true, true
		}
	}
	if c.botUserID != "" && strings.Contains(trimmed, "<@"+c.botUserID+">") {
		return true, true
	}
	if c.botUserID != "" && strings.Contains(trimmed, "<@!"+c.botUserID+">") {
		return true, true
	}
	return true, false
}

func (c *Connector) generateReply(ctx context.Context, contextRecord store.ContextRecord, message discordMessageCreate, text string, isMention bool) (string, string, error) {
	if c.responder == nil {
		return "", "", nil
	}
	role := ""
	identity, err := c.pairings.LookupUserIdentity(ctx, "discord", message.Author.ID)
	if err == nil {
		role = identity.Role
	} else if !errors.Is(err, store.ErrIdentityNotFound) {
		c.logger.Error("discord identity lookup failed", "error", err)
	}
	if c.policy != nil {
		decision := c.policy.Check(llmsafety.Request{
			Connector: "discord",
			ContextID: contextRecord.ID,
			UserID:    message.Author.ID,
			UserRole:  role,
			IsDM:      message.GuildID == "",
			IsMention: isMention,
		})
		if !decision.Allowed {
			return "", strings.TrimSpace(decision.Notify), nil
		}
	}
	displayName := message.ChannelID
	if message.GuildID != "" {
		displayName = message.GuildID
	}
	prompt := strings.TrimSpace(text)
	if c.botUserID != "" {
		prompt = strings.ReplaceAll(prompt, "<@"+c.botUserID+">", "")
		prompt = strings.ReplaceAll(prompt, "<@!"+c.botUserID+">", "")
		prompt = strings.TrimSpace(prompt)
	}
	if prompt == "" {
		return "", "", nil
	}
	reply, err := c.responder.Reply(ctx, llm.MessageInput{
		Connector:   "discord",
		WorkspaceID: contextRecord.WorkspaceID,
		ContextID:   contextRecord.ID,
		ExternalID:  message.ChannelID,
		DisplayName: displayName,
		FromUserID:  message.Author.ID,
		Text:        prompt,
		IsDM:        message.GuildID == "",
	})
	if err != nil {
		c.logger.Error("discord llm reply failed", "error", err)
		return "", "", err
	}
	cleanReply, proposal := actions.ExtractProposal(strings.TrimSpace(reply))
	if proposal == nil {
		return strings.TrimSpace(cleanReply), "", nil
	}
	approval, err := c.pairings.CreateActionApproval(ctx, store.CreateActionApprovalInput{
		WorkspaceID:     contextRecord.WorkspaceID,
		ContextID:       contextRecord.ID,
		Connector:       "discord",
		ExternalID:      message.ChannelID,
		RequesterUserID: message.Author.ID,
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

func (c *Connector) ingestMarkdownAttachments(ctx context.Context, message discordMessageCreate) (string, error) {
	if c.workspace == "" || c.pairings == nil || len(message.Attachments) == 0 {
		return "", nil
	}
	displayName := message.ChannelID
	if message.GuildID != "" {
		displayName = message.GuildID
	}
	contextRecord, err := c.pairings.EnsureContextForExternalChannel(
		ctx,
		"discord",
		message.ChannelID,
		displayName,
	)
	if err != nil {
		return "", err
	}

	workspacePath := filepath.Join(c.workspace, contextRecord.WorkspaceID)
	targetDir := filepath.Join(workspacePath, "inbox", "discord", message.ChannelID)
	if err := os.MkdirAll(targetDir, 0o755); err != nil {
		return "", err
	}

	saved := []string{}
	for _, attachment := range message.Attachments {
		filename := sanitizeFilename(attachment.Filename)
		if !isMarkdown(filename, attachment.ContentType) {
			continue
		}
		content, err := c.downloadAttachment(ctx, attachment.URL)
		if err != nil {
			c.logger.Error("download discord attachment failed", "error", err, "url", attachment.URL)
			continue
		}
		targetName := fmt.Sprintf("%s-%s", message.ID, filename)
		targetPath := filepath.Join(targetDir, targetName)
		if err := os.WriteFile(targetPath, content, 0o644); err != nil {
			return "", err
		}
		relativePath, err := filepath.Rel(workspacePath, targetPath)
		if err != nil {
			relativePath = targetName
		}
		saved = append(saved, filepath.ToSlash(relativePath))
	}
	if len(saved) == 0 {
		return "", nil
	}
	if len(saved) == 1 {
		return fmt.Sprintf("Attachment saved: `%s`", saved[0]), nil
	}
	return fmt.Sprintf("Saved %d markdown attachments.", len(saved)), nil
}

func (c *Connector) downloadAttachment(ctx context.Context, url string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bot "+c.token)

	res, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return nil, fmt.Errorf("discord attachment download failed with status %d", res.StatusCode)
	}
	return ioReadAllLimited(res.Body, 2<<20)
}

func (c *Connector) sendChannelMessage(ctx context.Context, channelID, content string) error {
	endpoint := fmt.Sprintf("%s/channels/%s/messages", c.apiBase, channelID)
	body := map[string]string{"content": content}
	payload, err := json.Marshal(body)
	if err != nil {
		return err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(payload))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bot "+c.token)
	req.Header.Set("User-Agent", "spinner/0.1")

	res, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		bodyBytes, _ := io.ReadAll(io.LimitReader(res.Body, 1024))
		return fmt.Errorf("discord send message failed: status=%d body=%s", res.StatusCode, string(bodyBytes))
	}
	return nil
}

func normalize(input string) string {
	text := strings.TrimSpace(strings.ToLower(input))
	if strings.HasPrefix(text, "/") {
		text = strings.TrimPrefix(text, "/")
	}
	if strings.HasPrefix(text, "pair@") {
		return pairCommand
	}
	return text
}

func discordDisplayName(author discordAuthor) string {
	if strings.TrimSpace(author.GlobalName) != "" {
		return author.GlobalName
	}
	if strings.TrimSpace(author.Username) != "" {
		return author.Username
	}
	if strings.TrimSpace(author.ID) != "" {
		return author.ID
	}
	return strconv.FormatInt(time.Now().Unix(), 10)
}

type gatewayEnvelope struct {
	Op int             `json:"op"`
	T  string          `json:"t"`
	S  *int64          `json:"s"`
	D  json.RawMessage `json:"d"`
}

type discordHello struct {
	HeartbeatIntervalMS int64 `json:"heartbeat_interval"`
}

type discordReady struct {
	User discordAuthor `json:"user"`
}

type discordMessageCreate struct {
	ID          string              `json:"id"`
	ChannelID   string              `json:"channel_id"`
	GuildID     string              `json:"guild_id"`
	Content     string              `json:"content"`
	Author      discordAuthor       `json:"author"`
	Attachments []discordAttachment `json:"attachments"`
	Mentions    []discordAuthor     `json:"mentions"`
}

type discordAuthor struct {
	ID         string `json:"id"`
	Username   string `json:"username"`
	GlobalName string `json:"global_name"`
	Bot        bool   `json:"bot"`
}

type discordAttachment struct {
	ID          string `json:"id"`
	Filename    string `json:"filename"`
	ContentType string `json:"content_type"`
	URL         string `json:"url"`
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

func (c *Connector) logInbound(contextRecord store.ContextRecord, message discordMessageCreate, text string) {
	logText := strings.TrimSpace(text)
	if logText == "" && len(message.Attachments) > 0 {
		names := make([]string, 0, len(message.Attachments))
		for _, attachment := range message.Attachments {
			name := strings.TrimSpace(attachment.Filename)
			if name != "" {
				names = append(names, name)
			}
		}
		if len(names) > 0 {
			logText = "[attachments] " + strings.Join(names, ", ")
		}
	}
	if logText == "" {
		return
	}
	displayName := message.ChannelID
	if message.GuildID != "" {
		displayName = message.GuildID
	}
	if err := memorylog.Append(memorylog.Entry{
		WorkspaceRoot: c.workspace,
		WorkspaceID:   contextRecord.WorkspaceID,
		Connector:     "discord",
		ExternalID:    message.ChannelID,
		Direction:     "inbound",
		ActorID:       message.Author.ID,
		DisplayName:   displayName,
		Text:          logText,
		Timestamp:     time.Now().UTC(),
	}); err != nil {
		c.logger.Error("inbound log append failed", "error", err, "channel_id", message.ChannelID)
	}
}

func (c *Connector) logOutbound(contextRecord store.ContextRecord, message discordMessageCreate, text string) {
	logText := strings.TrimSpace(text)
	if logText == "" {
		return
	}
	displayName := message.ChannelID
	if message.GuildID != "" {
		displayName = message.GuildID
	}
	if err := memorylog.Append(memorylog.Entry{
		WorkspaceRoot: c.workspace,
		WorkspaceID:   contextRecord.WorkspaceID,
		Connector:     "discord",
		ExternalID:    message.ChannelID,
		Direction:     "outbound",
		ActorID:       "spinner",
		DisplayName:   displayName,
		Text:          logText,
		Timestamp:     time.Now().UTC(),
	}); err != nil {
		c.logger.Error("outbound log append failed", "error", err, "channel_id", message.ChannelID)
	}
}
