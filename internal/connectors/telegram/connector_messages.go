package telegram

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/dwizi/agent-runtime/internal/actions"
	"github.com/dwizi/agent-runtime/internal/connectors/contextack"
	"github.com/dwizi/agent-runtime/internal/gateway"
	"github.com/dwizi/agent-runtime/internal/llm"
	llmsafety "github.com/dwizi/agent-runtime/internal/llm/safety"
	"github.com/dwizi/agent-runtime/internal/memorylog"
	"github.com/dwizi/agent-runtime/internal/store"
)

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
			"Pairing token: `%s`\nOpen Agent Runtime TUI and approve this token.\nThis token expires at %s UTC.",
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
	trimmedGatewayReply := strings.TrimSpace(output.Reply)
	if !output.Handled || trimmedGatewayReply == "" {
		c.logger.Info(
			"telegram gateway produced no direct reply",
			"chat_id", message.Chat.ID,
			"message_id", message.MessageID,
			"handled", output.Handled,
			"reply_len", len(trimmedGatewayReply),
		)
		replyToSend := attachmentReply
		shouldReply, isMention := c.shouldAutoReply(message, text)
		if shouldReply {
			llmReply, notice, llmErr := c.generateReply(ctx, contextRecord, message, text, isMention)
			if llmErr != nil {
				c.logger.Error(
					"telegram llm reply generation failed",
					"error", llmErr,
					"chat_id", message.Chat.ID,
					"message_id", message.MessageID,
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
				"telegram message produced no outbound reply",
				"chat_id", message.Chat.ID,
				"message_id", message.MessageID,
				"reason", "no_gateway_reply_and_no_fallback_reply",
			)
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
			c.logger.Info(
				"telegram llm reply skipped by policy",
				"reason", strings.TrimSpace(decision.Reason),
				"context_id", contextRecord.ID,
				"chat_id", strconv.FormatInt(message.Chat.ID, 10),
				"user_id", strconv.FormatInt(message.From.ID, 10),
				"is_dm", message.Chat.Type == "private",
				"is_mention", isMention,
			)
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
	_, ack := contextack.PlanAndGenerate(ctx, c.responder, llm.MessageInput{
		Connector:   "telegram",
		WorkspaceID: contextRecord.WorkspaceID,
		ContextID:   contextRecord.ID,
		ExternalID:  strconv.FormatInt(message.Chat.ID, 10),
		DisplayName: message.Chat.Title,
		FromUserID:  strconv.FormatInt(message.From.ID, 10),
		Text:        prompt,
		IsDM:        message.Chat.Type == "private",
	})
	if ack != "" {
		c.logOutbound(contextRecord, message, ack)
		if ackErr := c.sendMessage(ctx, message.Chat.ID, ack); ackErr != nil {
			c.logger.Error("send context-loading acknowledgement failed", "error", ackErr, "chat_id", message.Chat.ID)
		}
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
	notice := actions.FormatApprovalRequestNotice(approval.ID)
	return "", notice, nil
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
		ActorID:       "agent-runtime",
		DisplayName:   message.Chat.Title,
		Text:          logText,
		Timestamp:     time.Now().UTC(),
	}); err != nil {
		c.logger.Error("outbound log append failed", "error", err, "chat_id", message.Chat.ID)
	}
}
