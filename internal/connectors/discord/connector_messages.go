package discord

import (
	"context"
	"errors"
	"fmt"
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
