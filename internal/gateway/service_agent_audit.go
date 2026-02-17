package gateway

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/dwizi/agent-runtime/internal/agent"
	"github.com/dwizi/agent-runtime/internal/llm"
	"github.com/dwizi/agent-runtime/internal/memorylog"
	"github.com/dwizi/agent-runtime/internal/orchestrator"
	"github.com/dwizi/agent-runtime/internal/store"
)

func (s *Service) appendAgentToolCallLogs(contextRecord store.ContextRecord, input MessageInput, result agent.Result) {
	if s == nil || len(result.ToolCalls) == 0 {
		return
	}
	workspaceRoot := strings.TrimSpace(s.workspaceRoot)
	workspaceID := strings.TrimSpace(contextRecord.WorkspaceID)
	connector := strings.ToLower(strings.TrimSpace(input.Connector))
	externalID := strings.TrimSpace(input.ExternalID)
	if workspaceRoot == "" || workspaceID == "" || connector == "" || externalID == "" {
		return
	}
	displayName := strings.TrimSpace(input.DisplayName)
	if displayName == "" {
		displayName = externalID
	}
	for _, call := range result.ToolCalls {
		logText := formatToolCallLog(call)
		if logText == "" {
			continue
		}
		if err := memorylog.Append(memorylog.Entry{
			WorkspaceRoot: workspaceRoot,
			WorkspaceID:   workspaceID,
			Connector:     connector,
			ExternalID:    externalID,
			Direction:     "tool",
			ActorID:       "agent-runtime",
			DisplayName:   displayName,
			Text:          logText,
			Timestamp:     time.Now().UTC(),
		}); err != nil {
			s.logger.Error("tool call log append failed", "error", err, "connector", connector, "external_id", externalID)
		}
	}
}

func formatToolCallLog(call agent.ToolCall) string {
	toolName := strings.TrimSpace(call.ToolName)
	if toolName == "" {
		return ""
	}
	status := strings.TrimSpace(call.Status)
	if status == "" {
		status = "unknown"
	}
	lines := []string{
		"Tool call",
		fmt.Sprintf("- tool: `%s`", toolName),
		fmt.Sprintf("- status: `%s`", status),
	}
	args := strings.TrimSpace(call.ToolArgs)
	if args != "" {
		lines = append(lines, fmt.Sprintf("- args: `%s`", truncateToolLogField(args, 500)))
	}
	if errText := strings.TrimSpace(call.Error); errText != "" {
		lines = append(lines, fmt.Sprintf("- error: %s", truncateToolLogField(errText, 500)))
	}
	if output := strings.TrimSpace(call.ToolOutput); output != "" {
		lines = append(lines, fmt.Sprintf("- output: %s", truncateToolLogField(output, 700)))
	}
	return strings.Join(lines, "\n")
}

func truncateToolLogField(input string, maxLen int) string {
	value := strings.Join(strings.Fields(strings.TrimSpace(input)), " ")
	if value == "" {
		return ""
	}
	if maxLen < 1 || len(value) <= maxLen {
		return value
	}
	return strings.TrimSpace(value[:maxLen]) + "..."
}

func (s *Service) NarrateTaskResult(ctx context.Context, connector, externalID string, task orchestrator.Task, result orchestrator.TaskResult) (string, error) {
	if s.agent == nil {
		return "", fmt.Errorf("agent not configured")
	}

	// 1. Ensure context
	contextRecord, err := s.store.EnsureContextForExternalChannel(ctx, connector, externalID, "")
	if err != nil {
		return "", err
	}

	// 2. Build synthetic input
	narrativePrompt := fmt.Sprintf(
		"BACKGROUND TASK FINISHED\nTask: %s\nResult: %s\n\nExplain this result to the user naturally and decide if any follow-up actions are needed.",
		task.Title, result.Summary,
	)

	// 3. Execute Agent turn
	agentCtx := context.WithValue(ctx, ContextKeyRecord, contextRecord)
	agentCtx = context.WithValue(agentCtx, ContextKeyInput, MessageInput{
		Connector:  connector,
		ExternalID: externalID,
	})

	agentRes := s.agent.Execute(agentCtx, llm.MessageInput{
		Connector:   connector,
		WorkspaceID: contextRecord.WorkspaceID,
		ContextID:   contextRecord.ID,
		ExternalID:  externalID,
		Text:        narrativePrompt,
	})

	if agentRes.Error != nil {
		return "", agentRes.Error
	}

	return agentRes.Reply, nil
}
func (s *Service) persistAgentAuditTraces(ctx context.Context, contextRecord store.ContextRecord, input MessageInput, result agent.Result) {
	if s == nil || s.store == nil || len(result.Trace) == 0 {
		return
	}
	workspaceID := strings.TrimSpace(contextRecord.WorkspaceID)
	contextID := strings.TrimSpace(contextRecord.ID)
	connector := strings.TrimSpace(input.Connector)
	externalID := strings.TrimSpace(input.ExternalID)
	sourceUserID := strings.TrimSpace(input.FromUserID)
	if workspaceID == "" || contextID == "" || connector == "" || externalID == "" {
		return
	}
	for _, entry := range result.Trace {
		stage := strings.TrimSpace(entry.Stage)
		if !strings.HasPrefix(strings.ToLower(stage), "audit.") {
			continue
		}
		eventType := strings.TrimSpace(strings.TrimPrefix(strings.ToLower(stage), "audit."))
		if eventType == "" {
			continue
		}
		meta := parseAuditMetadata(entry.Message)
		toolName := strings.TrimSpace(meta["tool"])
		if toolName == "" {
			toolName = strings.TrimSpace(result.ToolName)
		}
		toolClass := strings.TrimSpace(meta["class"])
		_, _ = s.store.CreateAgentAuditEvent(ctx, store.CreateAgentAuditEventInput{
			WorkspaceID:  workspaceID,
			ContextID:    contextID,
			Connector:    connector,
			ExternalID:   externalID,
			SourceUserID: sourceUserID,
			EventType:    eventType,
			Stage:        stage,
			ToolName:     toolName,
			ToolClass:    toolClass,
			Blocked:      result.Blocked,
			BlockReason:  strings.TrimSpace(result.BlockReason),
			Message:      strings.TrimSpace(entry.Message),
		})
	}
}

func parseAuditMetadata(message string) map[string]string {
	fields := strings.Fields(strings.TrimSpace(message))
	parsed := map[string]string{}
	for _, item := range fields {
		parts := strings.SplitN(item, "=", 2)
		if len(parts) != 2 {
			continue
		}
		key := strings.ToLower(strings.TrimSpace(parts[0]))
		value := strings.TrimSpace(parts[1])
		if key == "" || value == "" {
			continue
		}
		parsed[key] = value
	}
	return parsed
}
