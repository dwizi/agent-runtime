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

	"github.com/dwizi/agent-runtime/internal/actions"
	"github.com/dwizi/agent-runtime/internal/connectors/contextack"
	"github.com/dwizi/agent-runtime/internal/heartbeat"
	"github.com/gorilla/websocket"

	"github.com/dwizi/agent-runtime/internal/gateway"
	"github.com/dwizi/agent-runtime/internal/llm"
	llmsafety "github.com/dwizi/agent-runtime/internal/llm/safety"
	"github.com/dwizi/agent-runtime/internal/memorylog"
	"github.com/dwizi/agent-runtime/internal/store"
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
	token           string
	apiBase         string
	gatewayURL      string
	workspace       string
	commandSync     bool
	commandGuildIDs []string
	applicationID   string
	pairings        PairingStore
	gateway         CommandGateway
	responder       Responder
	policy          SafetyPolicy
	httpClient      *http.Client
	logger          *slog.Logger
	botUserID       string
	reporter        heartbeat.Reporter
}

type Option func(*Connector)

func WithCommandSync(enabled bool) Option {
	return func(connector *Connector) {
		connector.commandSync = enabled
	}
}

func WithCommandGuildIDs(guildIDs []string) Option {
	return func(connector *Connector) {
		clean := make([]string, 0, len(guildIDs))
		seen := map[string]struct{}{}
		for _, guildID := range guildIDs {
			value := strings.TrimSpace(guildID)
			if value == "" {
				continue
			}
			if _, exists := seen[value]; exists {
				continue
			}
			seen[value] = struct{}{}
			clean = append(clean, value)
		}
		connector.commandGuildIDs = clean
	}
}

func WithApplicationID(applicationID string) Option {
	return func(connector *Connector) {
		connector.applicationID = strings.TrimSpace(applicationID)
	}
}

func New(token, apiBase, gatewayURL, workspaceRoot string, pairings PairingStore, commandGateway CommandGateway, responder Responder, policy SafetyPolicy, logger *slog.Logger, opts ...Option) *Connector {
	if strings.TrimSpace(apiBase) == "" {
		apiBase = "https://discord.com/api/v10"
	}
	if strings.TrimSpace(gatewayURL) == "" {
		gatewayURL = "wss://gateway.discord.gg/?v=10&encoding=json"
	}
	connector := &Connector{
		token:       strings.TrimSpace(token),
		apiBase:     strings.TrimRight(strings.TrimSpace(apiBase), "/"),
		gatewayURL:  strings.TrimSpace(gatewayURL),
		workspace:   strings.TrimSpace(workspaceRoot),
		commandSync: true,
		pairings:    pairings,
		gateway:     commandGateway,
		responder:   responder,
		policy:      policy,
		httpClient:  &http.Client{Timeout: 12 * time.Second},
		logger:      logger,
	}
	for _, opt := range opts {
		if opt != nil {
			opt(connector)
		}
	}
	return connector
}

func (c *Connector) Name() string {
	return "discord"
}

func (c *Connector) SetHeartbeatReporter(reporter heartbeat.Reporter) {
	c.reporter = reporter
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
	if c.reporter != nil {
		c.reporter.Starting("connector:discord", "starting")
	}
	if c.token == "" {
		if c.reporter != nil {
			c.reporter.Disabled("connector:discord", "token missing")
		}
		c.logger.Info("connector disabled, token missing")
		<-ctx.Done()
		return nil
	}
	if c.pairings == nil || c.gateway == nil {
		if c.reporter != nil {
			c.reporter.Disabled("connector:discord", "dependencies missing")
		}
		c.logger.Info("connector disabled, dependencies missing")
		<-ctx.Done()
		return nil
	}

	if c.reporter != nil {
		c.reporter.Beat("connector:discord", "gateway session loop active")
	}
	c.logger.Info("connector started", "mode", "gateway")
	if c.commandSync {
		if err := c.syncCommands(ctx); err != nil {
			c.logger.Warn("discord command sync failed", "error", err)
		} else {
			c.logger.Info("discord commands synced", "guild_count", len(c.commandGuildIDs))
		}
	}
	for {
		if ctx.Err() != nil {
			if c.reporter != nil {
				c.reporter.Stopped("connector:discord", "stopped")
			}
			c.logger.Info("connector stopped")
			return nil
		}
		if err := c.runSession(ctx); err != nil {
			if ctx.Err() != nil {
				if c.reporter != nil {
					c.reporter.Stopped("connector:discord", "stopped")
				}
				c.logger.Info("connector stopped")
				return nil
			}
			if c.reporter != nil {
				c.reporter.Degrade("connector:discord", "gateway session error", err)
			}
			c.logger.Error("discord session ended, reconnecting", "error", err)
			select {
			case <-ctx.Done():
				if c.reporter != nil {
					c.reporter.Stopped("connector:discord", "stopped")
				}
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
	if c.reporter != nil {
		c.reporter.Beat("connector:discord", "gateway session established")
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
			if c.reporter != nil {
				c.reporter.Beat("connector:discord", "gateway event received")
			}
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
			if envelope.T == "INTERACTION_CREATE" {
				var interaction discordInteractionCreate
				if err := json.Unmarshal(envelope.D, &interaction); err != nil {
					c.logger.Error("decode interaction create failed", "error", err)
					continue
				}
				if err := c.handleInteractionCreate(ctx, interaction); err != nil {
					c.logger.Error("handle discord interaction failed", "error", err)
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
				"browser": "agent-runtime",
				"device":  "agent-runtime",
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
			"Pairing token: `%s`\nOpen Agent Runtime TUI and approve this token.\nThis token expires at %s UTC.",
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
	trimmedGatewayReply := strings.TrimSpace(output.Reply)
	if !output.Handled || trimmedGatewayReply == "" {
		c.logger.Info(
			"discord gateway produced no direct reply",
			"channel_id", message.ChannelID,
			"message_id", message.ID,
			"handled", output.Handled,
			"reply_len", len(trimmedGatewayReply),
		)
		replyToSend := attachmentReply
		shouldReply, isMention := c.shouldAutoReply(message, text)
		if shouldReply {
			llmReply, notice, llmErr := c.generateReply(ctx, contextRecord, message, text, isMention)
			if llmErr != nil {
				c.logger.Error(
					"discord llm reply generation failed",
					"error", llmErr,
					"channel_id", message.ChannelID,
					"message_id", message.ID,
					"is_mention", isMention,
				)
				if replyToSend == "" {
					replyToSend = "I started working on that but ran into an internal error. Please try again in a moment."
				}
			} else {
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
		}
		if replyToSend == "" {
			c.logger.Info(
				"discord message produced no outbound reply",
				"channel_id", message.ChannelID,
				"message_id", message.ID,
				"reason", "no_gateway_reply_and_no_fallback_reply",
			)
			return nil
		}
		c.logOutbound(contextRecord, message, replyToSend)
		return c.sendChannelMessage(ctx, message.ChannelID, replyToSend)
	}
	if attachmentReply != "" {
		output.Reply = strings.TrimSpace(output.Reply) + "\n\n" + attachmentReply
	}
	if strings.TrimSpace(output.Reply) == "" {
		c.logger.Info(
			"discord message produced no outbound reply",
			"channel_id", message.ChannelID,
			"message_id", message.ID,
			"reason", "gateway_reply_empty_after_attachment_merge",
		)
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
			c.logger.Info(
				"discord llm reply skipped by policy",
				"reason", strings.TrimSpace(decision.Reason),
				"context_id", contextRecord.ID,
				"channel_id", message.ChannelID,
				"user_id", message.Author.ID,
				"is_dm", message.GuildID == "",
				"is_mention", isMention,
			)
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
	_, ack := contextack.PlanAndGenerate(ctx, c.responder, llm.MessageInput{
		Connector:   "discord",
		WorkspaceID: contextRecord.WorkspaceID,
		ContextID:   contextRecord.ID,
		ExternalID:  message.ChannelID,
		DisplayName: displayName,
		FromUserID:  message.Author.ID,
		Text:        prompt,
		IsDM:        message.GuildID == "",
	})
	if ack != "" {
		c.logOutbound(contextRecord, message, ack)
		if ackErr := c.sendChannelMessage(ctx, message.ChannelID, ack); ackErr != nil {
			c.logger.Error("send context-loading acknowledgement failed", "error", ackErr, "channel_id", message.ChannelID)
		}
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
	notice := actions.FormatApprovalRequestNotice(approval.ID)
	return "", notice, nil
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
	req.Header.Set("User-Agent", "agent-runtime/0.1")

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

func (c *Connector) syncCommands(ctx context.Context) error {
	applicationID, err := c.resolveApplicationID(ctx)
	if err != nil {
		return err
	}
	commandsPayload := buildDiscordCommandPayload(gateway.SlashCommands())
	if len(commandsPayload) == 0 {
		return nil
	}
	if len(c.commandGuildIDs) == 0 {
		return c.upsertGlobalCommands(ctx, applicationID, commandsPayload)
	}
	for _, guildID := range c.commandGuildIDs {
		if err := c.upsertGuildCommands(ctx, applicationID, guildID, commandsPayload); err != nil {
			return err
		}
	}
	return nil
}

func (c *Connector) resolveApplicationID(ctx context.Context) (string, error) {
	if strings.TrimSpace(c.applicationID) != "" {
		return c.applicationID, nil
	}
	applicationID, err := c.fetchApplicationID(ctx)
	if err != nil {
		return "", err
	}
	c.applicationID = applicationID
	return applicationID, nil
}

func (c *Connector) fetchApplicationID(ctx context.Context) (string, error) {
	url := fmt.Sprintf("%s/oauth2/applications/@me", c.apiBase)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bot "+c.token)
	req.Header.Set("User-Agent", "agent-runtime/0.1")

	res, err := c.httpClient.Do(req)
	if err != nil {
		return "", err
	}
	defer res.Body.Close()
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		bodyBytes, _ := io.ReadAll(io.LimitReader(res.Body, 1024))
		return "", fmt.Errorf("discord application lookup failed: status=%d body=%s", res.StatusCode, strings.TrimSpace(string(bodyBytes)))
	}

	var payload struct {
		ID string `json:"id"`
	}
	if err := json.NewDecoder(res.Body).Decode(&payload); err != nil {
		return "", fmt.Errorf("decode discord application lookup: %w", err)
	}
	applicationID := strings.TrimSpace(payload.ID)
	if applicationID == "" {
		return "", fmt.Errorf("discord application lookup returned empty id")
	}
	return applicationID, nil
}

func (c *Connector) upsertGlobalCommands(ctx context.Context, applicationID string, payload []map[string]any) error {
	url := fmt.Sprintf("%s/applications/%s/commands", c.apiBase, applicationID)
	return c.putCommands(ctx, url, payload)
}

func (c *Connector) upsertGuildCommands(ctx context.Context, applicationID, guildID string, payload []map[string]any) error {
	url := fmt.Sprintf("%s/applications/%s/guilds/%s/commands", c.apiBase, applicationID, strings.TrimSpace(guildID))
	return c.putCommands(ctx, url, payload)
}

func (c *Connector) putCommands(ctx context.Context, url string, payload []map[string]any) error {
	bodyBytes, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, url, bytes.NewReader(bodyBytes))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bot "+c.token)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "agent-runtime/0.1")

	res, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		responseBody, _ := io.ReadAll(io.LimitReader(res.Body, 2048))
		return fmt.Errorf("discord command upsert failed: status=%d body=%s", res.StatusCode, strings.TrimSpace(string(responseBody)))
	}
	return nil
}

func buildDiscordCommandPayload(commands []gateway.SlashCommand) []map[string]any {
	payload := make([]map[string]any, 0, len(commands))
	for _, command := range commands {
		name := strings.TrimSpace(command.Name)
		if name == "" {
			continue
		}
		entry := map[string]any{
			"name":        name,
			"description": discordCommandDescription(command.Description),
			"type":        1,
		}
		if strings.TrimSpace(command.ArgumentName) != "" {
			entry["options"] = []map[string]any{
				{
					"type":        3,
					"name":        command.ArgumentName,
					"description": discordCommandDescription(command.ArgumentDescription),
					"required":    command.ArgumentRequired,
				},
			}
		}
		payload = append(payload, entry)
	}
	return payload
}

func discordCommandDescription(description string) string {
	trimmed := strings.TrimSpace(description)
	if trimmed == "" {
		return "Agent Runtime command"
	}
	if len(trimmed) > 100 {
		return strings.TrimSpace(trimmed[:100])
	}
	return trimmed
}

func (c *Connector) handleInteractionCreate(ctx context.Context, interaction discordInteractionCreate) error {
	if interaction.Type != 2 {
		return nil
	}
	commandText := interactionToCommandText(interaction)
	if commandText == "" {
		return c.sendInteractionResponse(ctx, interaction.ID, interaction.Token, "Unsupported command payload.")
	}
	userID := interaction.userID()
	if userID == "" {
		return c.sendInteractionResponse(ctx, interaction.ID, interaction.Token, "Missing user context.")
	}
	displayName := strings.TrimSpace(interaction.GuildID)
	if displayName == "" {
		displayName = strings.TrimSpace(interaction.ChannelID)
	}
	output, err := c.gateway.HandleMessage(ctx, gateway.MessageInput{
		Connector:   "discord",
		ExternalID:  strings.TrimSpace(interaction.ChannelID),
		DisplayName: displayName,
		FromUserID:  userID,
		Text:        commandText,
	})
	if err != nil {
		return c.sendInteractionResponse(ctx, interaction.ID, interaction.Token, "I hit an error while running that command.")
	}
	reply := strings.TrimSpace(output.Reply)
	if reply == "" {
		reply = "Command received."
	}
	return c.sendInteractionResponse(ctx, interaction.ID, interaction.Token, clipDiscordMessage(reply))
}

func interactionToCommandText(interaction discordInteractionCreate) string {
	name := strings.TrimSpace(interaction.Data.Name)
	if name == "" {
		return ""
	}
	parts := []string{"/" + name}
	for _, option := range interaction.Data.Options {
		value := strings.TrimSpace(option.valueAsString())
		if value == "" {
			continue
		}
		parts = append(parts, value)
	}
	return strings.TrimSpace(strings.Join(parts, " "))
}

func (c *Connector) sendInteractionResponse(ctx context.Context, interactionID, interactionToken, content string) error {
	if strings.TrimSpace(interactionID) == "" || strings.TrimSpace(interactionToken) == "" {
		return fmt.Errorf("missing interaction id or token")
	}
	endpoint := fmt.Sprintf("%s/interactions/%s/%s/callback", c.apiBase, interactionID, interactionToken)
	body := map[string]any{
		"type": 4,
		"data": map[string]any{
			"content": content,
		},
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
	req.Header.Set("User-Agent", "agent-runtime/0.1")

	res, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		bodyBytes, _ := io.ReadAll(io.LimitReader(res.Body, 1024))
		return fmt.Errorf("discord interaction response failed: status=%d body=%s", res.StatusCode, strings.TrimSpace(string(bodyBytes)))
	}
	return nil
}

func clipDiscordMessage(content string) string {
	trimmed := strings.TrimSpace(content)
	if len(trimmed) <= 2000 {
		return trimmed
	}
	return strings.TrimSpace(trimmed[:1997]) + "..."
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

type discordInteractionCreate struct {
	ID        string                   `json:"id"`
	Type      int                      `json:"type"`
	Token     string                   `json:"token"`
	ChannelID string                   `json:"channel_id"`
	GuildID   string                   `json:"guild_id"`
	Data      discordInteractionData   `json:"data"`
	Member    discordInteractionMember `json:"member"`
	User      discordAuthor            `json:"user"`
}

func (interaction discordInteractionCreate) userID() string {
	if strings.TrimSpace(interaction.Member.User.ID) != "" {
		return strings.TrimSpace(interaction.Member.User.ID)
	}
	return strings.TrimSpace(interaction.User.ID)
}

type discordInteractionData struct {
	Name    string                     `json:"name"`
	Options []discordInteractionOption `json:"options"`
}

type discordInteractionOption struct {
	Name  string `json:"name"`
	Type  int    `json:"type"`
	Value any    `json:"value"`
}

func (option discordInteractionOption) valueAsString() string {
	if option.Value == nil {
		return ""
	}
	switch value := option.Value.(type) {
	case string:
		return value
	case float64:
		if value == float64(int64(value)) {
			return strconv.FormatInt(int64(value), 10)
		}
		return strconv.FormatFloat(value, 'f', -1, 64)
	case bool:
		if value {
			return "true"
		}
		return "false"
	default:
		return fmt.Sprintf("%v", value)
	}
}

type discordInteractionMember struct {
	User discordAuthor `json:"user"`
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
		ActorID:       "agent-runtime",
		DisplayName:   displayName,
		Text:          logText,
		Timestamp:     time.Now().UTC(),
	}); err != nil {
		c.logger.Error("outbound log append failed", "error", err, "channel_id", message.ChannelID)
	}
}
