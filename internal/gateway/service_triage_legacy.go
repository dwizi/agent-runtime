package gateway

import (
	"context"
	"fmt"
	"strings"

	"github.com/dwizi/agent-runtime/internal/llm"
	"github.com/dwizi/agent-runtime/internal/orchestrator"
	"github.com/dwizi/agent-runtime/internal/store"
)

func (s *Service) handleLegacyAutoTriage(ctx context.Context, input MessageInput, text string) (MessageOutput, error) {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" || strings.HasPrefix(trimmed, "/") {
		return MessageOutput{}, nil
	}
	if s.store == nil || s.engine == nil {
		return MessageOutput{}, nil
	}
	contextRecord, err := s.store.EnsureContextForExternalChannel(ctx, input.Connector, input.ExternalID, input.DisplayName)
	if err != nil {
		return MessageOutput{}, err
	}
	decision := deriveRouteDecision(input, contextRecord.WorkspaceID, contextRecord.ID, trimmed)
	if decision.Class == TriageNoise {
		return MessageOutput{}, nil
	}
	if !shouldAutoRouteDecision(decision) {
		return MessageOutput{}, nil
	}
	taskTitle := buildRoutedTaskTitle(decision.Class, decision.SourceText)
	taskPrompt := buildRoutedTaskPrompt(decision)
	task, err := s.enqueueAndPersistTask(ctx, store.CreateTaskInput{
		WorkspaceID:      decision.WorkspaceID,
		ContextID:        decision.ContextID,
		Kind:             string(orchestrator.TaskKindGeneral),
		Title:            taskTitle,
		Prompt:           taskPrompt,
		Status:           "queued",
		RouteClass:       string(decision.Class),
		Priority:         string(decision.Priority),
		DueAt:            decision.DueAt,
		AssignedLane:     decision.AssignedLane,
		SourceConnector:  decision.SourceConnector,
		SourceExternalID: decision.SourceExternalID,
		SourceUserID:     decision.SourceUserID,
		SourceText:       decision.SourceText,
	})
	if err != nil {
		return MessageOutput{}, err
	}
	decision.TaskID = task.ID
	if s.routingNotify != nil {
		s.routingNotify.NotifyRoutingDecision(ctx, decision)
	}
	return MessageOutput{
		Handled: true,
		Reply:   s.buildAutoTriageAck(ctx, input, contextRecord, decision),
	}, nil
}

func (s *Service) buildAutoTriageAck(ctx context.Context, input MessageInput, contextRecord store.ContextRecord, decision RouteDecision) string {
	fallback := fallbackAutoTriageAck(decision.Class)
	if s.triageAcknowledger == nil {
		return fallback
	}
	sourceText := strings.TrimSpace(decision.SourceText)
	if len(sourceText) > 300 {
		sourceText = sourceText[:300]
	}
	ackPrompt := strings.Join([]string{
		"Write one short natural acknowledgement for a chat message.",
		"Constraints:",
		"- one sentence",
		"- 8 to 20 words",
		"- confirm you are taking action now",
		"- do not include markdown, task IDs, or internal metadata",
		fmt.Sprintf("Route class: %s", strings.TrimSpace(string(decision.Class))),
		"User message:",
		sourceText,
	}, "\n")
	reply, err := s.triageAcknowledger.Reply(ctx, llm.MessageInput{
		Connector:     strings.TrimSpace(input.Connector),
		WorkspaceID:   strings.TrimSpace(contextRecord.WorkspaceID),
		ContextID:     strings.TrimSpace(contextRecord.ID),
		ExternalID:    strings.TrimSpace(input.ExternalID),
		DisplayName:   strings.TrimSpace(input.DisplayName),
		FromUserID:    strings.TrimSpace(input.FromUserID),
		Text:          ackPrompt,
		IsDM:          false,
		SkipGrounding: true,
	})
	if err != nil {
		return fallback
	}
	clean := sanitizeAutoTriageAck(reply)
	if clean == "" {
		return fallback
	}
	return clean
}

func fallbackAutoTriageAck(class TriageClass) string {
	switch class {
	case TriageIssue:
		return "Thanks for flagging this. I’m investigating now and I’ll report back with findings."
	case TriageModeration:
		return "Received. I’m reviewing this now and I’ll follow up with what I find."
	case TriageQuestion:
		return "Yes, I’m on it. I’ll investigate and come back with an answer."
	default:
		return "Understood. I’m handling this now and I’ll share results shortly."
	}
}

func sanitizeAutoTriageAck(input string) string {
	trimmed := strings.TrimSpace(input)
	if trimmed == "" {
		return ""
	}
	trimmed = strings.ReplaceAll(trimmed, "```", "")
	trimmed = strings.Join(strings.Fields(trimmed), " ")
	trimmed = strings.Trim(trimmed, "`\"'")
	if trimmed == "" {
		return ""
	}
	if len(trimmed) > 220 {
		trimmed = strings.TrimSpace(trimmed[:220]) + "..."
	}
	return trimmed
}
