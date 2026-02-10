package contextack

import (
	"context"
	"strings"

	"github.com/carlos/spinner/internal/llm"
	llmgrounded "github.com/carlos/spinner/internal/llm/grounded"
)

func PlanAndGenerate(ctx context.Context, responder llm.Responder, input llm.MessageInput) (llmgrounded.MemoryDecision, string) {
	decision := llmgrounded.DecideMemoryStrategy(input)
	if !decision.Acknowledge {
		return decision, ""
	}
	if responder == nil {
		return decision, fallbackAck(decision)
	}

	ackPrompt := buildAckPrompt(input.Text, decision)
	if strings.TrimSpace(ackPrompt) == "" {
		return decision, fallbackAck(decision)
	}

	reply, err := responder.Reply(ctx, llm.MessageInput{
		Connector:     strings.TrimSpace(input.Connector),
		WorkspaceID:   strings.TrimSpace(input.WorkspaceID),
		ContextID:     strings.TrimSpace(input.ContextID),
		ExternalID:    strings.TrimSpace(input.ExternalID),
		DisplayName:   strings.TrimSpace(input.DisplayName),
		FromUserID:    strings.TrimSpace(input.FromUserID),
		Text:          ackPrompt,
		IsDM:          input.IsDM,
		SkipGrounding: true,
	})
	if err != nil {
		return decision, fallbackAck(decision)
	}
	clean := sanitizeAck(reply)
	if clean == "" {
		return decision, fallbackAck(decision)
	}
	return decision, clean
}

func buildAckPrompt(userText string, decision llmgrounded.MemoryDecision) string {
	userText = strings.TrimSpace(userText)
	strategyHint := "pulling additional context"
	switch decision.Strategy {
	case llmgrounded.StrategyTail:
		strategyHint = "checking recent conversation memory"
	case llmgrounded.StrategyQMD:
		strategyHint = "retrieving relevant workspace memory"
	}
	lines := []string{
		"Write one short natural acknowledgement for a chat message.",
		"Constraints:",
		"- one sentence",
		"- 6 to 16 words",
		"- confirm you are taking action now",
		"- mention that you are " + strategyHint,
		"- no markdown, no code fences, no task IDs, no internal metadata",
		"User message:",
		userText,
	}
	return strings.Join(lines, "\n")
}

func sanitizeAck(input string) string {
	trimmed := strings.TrimSpace(input)
	if trimmed == "" {
		return ""
	}
	trimmed = strings.ReplaceAll(trimmed, "```", "")
	trimmed = strings.Join(strings.Fields(trimmed), " ")
	trimmed = strings.Trim(trimmed, "`\"'")
	if strings.Contains(trimmed, "action") && strings.Contains(trimmed, "{") {
		return ""
	}
	if len(trimmed) > 180 {
		trimmed = strings.TrimSpace(trimmed[:180]) + "..."
	}
	return trimmed
}

func fallbackAck(decision llmgrounded.MemoryDecision) string {
	switch decision.Strategy {
	case llmgrounded.StrategyTail:
		return "Let me pull some recent context first."
	case llmgrounded.StrategyQMD:
		return "Give me a minute to pull data from memory."
	default:
		return ""
	}
}

