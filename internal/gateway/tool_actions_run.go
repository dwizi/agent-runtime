package gateway

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/dwizi/agent-runtime/internal/agenterr"
	"github.com/dwizi/agent-runtime/internal/store"
)

// RunActionTool implements tools.Tool for executing system actions.
type RunActionTool struct {
	executor ActionExecutor
	store    Store
}

func NewRunActionTool(store Store, executor ActionExecutor) *RunActionTool {
	return &RunActionTool{store: store, executor: executor}
}

func (t *RunActionTool) Name() string { return "run_action" }

func (t *RunActionTool) Description() string {
	return "Execute a system action like 'run_command' (curl, etc.), 'send_email', 'webhook', 'agentic_web' (TinyFish), or any external plugin action type loaded at runtime."
}

func (t *RunActionTool) ParametersSchema() string {
	return `{"type": "string", "target": "string", "summary": "brief summary", "payload": {}}`
}

func (t *RunActionTool) ValidateArgs(rawArgs json.RawMessage) error {
	var args struct {
		Type    string         `json:"type"`
		Target  string         `json:"target"`
		Summary string         `json:"summary"`
		Payload map[string]any `json:"payload"`
	}
	if err := strictDecodeArgs(rawArgs, &args); err != nil {
		return err
	}

	actionType := strings.ToLower(strings.TrimSpace(args.Type))
	if actionType == "" {
		return fmt.Errorf("%w: type is required", agenterr.ErrToolInvalidArgs)
	}

	if actionType == "run_command" {
		if err := validateRunCommandPreflight(args.Target, args.Payload); err != nil {
			return err
		}
	}

	if actionType == "webhook" && strings.TrimSpace(args.Target) == "" {
		return fmt.Errorf("%w: target is required for webhook", agenterr.ErrToolInvalidArgs)
	}
	if isTinyfishActionType(actionType) {
		goal := resolveTinyfishGoal(args.Summary, args.Payload)
		if goal == "" {
			return fmt.Errorf("%w: agentic_web requires summary or payload.goal/payload.task", agenterr.ErrToolInvalidArgs)
		}
		rawURL := resolveTinyfishURL(args.Target, args.Payload)
		if rawURL == "" {
			return fmt.Errorf("%w: agentic_web requires target or payload.url/payload.request.url", agenterr.ErrToolInvalidArgs)
		}
		parsedURL, err := url.ParseRequestURI(rawURL)
		if err != nil || parsedURL == nil || (parsedURL.Scheme != "http" && parsedURL.Scheme != "https") || strings.TrimSpace(parsedURL.Host) == "" {
			return fmt.Errorf("%w: agentic_web target must be a valid http(s) URL", agenterr.ErrToolInvalidArgs)
		}
	}
	return nil
}

func isTinyfishActionType(actionType string) bool {
	switch strings.ToLower(strings.TrimSpace(actionType)) {
	case "agentic_web", "tinyfish_sync", "tinyfish_async":
		return true
	default:
		return false
	}
}

func resolveTinyfishGoal(summary string, payload map[string]any) string {
	goal := strings.TrimSpace(summary)
	if goal != "" {
		return goal
	}
	goal = firstNonEmptyMapString(payload, "goal", "task")
	if goal != "" {
		return goal
	}
	if nestedRaw, ok := payload["payload"]; ok && nestedRaw != nil {
		if nested, ok := nestedRaw.(map[string]any); ok {
			goal = firstNonEmptyMapString(nested, "goal", "task")
			if goal != "" {
				return goal
			}
		}
	}
	if requestRaw, ok := payload["request"]; ok && requestRaw != nil {
		if request, ok := requestRaw.(map[string]any); ok {
			return firstNonEmptyMapString(request, "goal", "task")
		}
	}
	return ""
}

func resolveTinyfishURL(target string, payload map[string]any) string {
	resolved := strings.TrimSpace(target)
	if resolved != "" {
		return resolved
	}
	if payload == nil {
		return ""
	}
	if value, ok := payload["url"]; ok && value != nil {
		resolved = strings.TrimSpace(fmt.Sprintf("%v", value))
		if resolved != "" {
			return resolved
		}
	}
	if requestRaw, ok := payload["request"]; ok && requestRaw != nil {
		if request, ok := requestRaw.(map[string]any); ok {
			if value, ok := request["url"]; ok && value != nil {
				resolved = strings.TrimSpace(fmt.Sprintf("%v", value))
				if resolved != "" {
					return resolved
				}
			}
		}
	}
	if nestedRaw, ok := payload["payload"]; ok && nestedRaw != nil {
		if nested, ok := nestedRaw.(map[string]any); ok {
			if value, ok := nested["url"]; ok && value != nil {
				resolved = strings.TrimSpace(fmt.Sprintf("%v", value))
				if resolved != "" {
					return resolved
				}
			}
		}
	}
	return ""
}

func firstNonEmptyMapString(values map[string]any, keys ...string) string {
	if values == nil {
		return ""
	}
	for _, key := range keys {
		if value, ok := values[key]; ok && value != nil {
			trimmed := strings.TrimSpace(fmt.Sprintf("%v", value))
			if trimmed != "" {
				return trimmed
			}
		}
	}
	return ""
}

func (t *RunActionTool) Execute(ctx context.Context, rawArgs json.RawMessage) (string, error) {
	var args struct {
		Type    string         `json:"type"`
		Target  string         `json:"target"`
		Summary string         `json:"summary"`
		Payload map[string]any `json:"payload"`
	}
	if err := strictDecodeArgs(rawArgs, &args); err != nil {
		return "", fmt.Errorf("invalid arguments: %w", err)
	}
	if err := t.ValidateArgs(rawArgs); err != nil {
		return "", err
	}

	record, ok := ctx.Value(ContextKeyRecord).(store.ContextRecord)
	if !ok {
		return "", fmt.Errorf("internal error: context record missing from context")
	}
	input, ok := ctx.Value(ContextKeyInput).(MessageInput)
	if !ok {
		return "", fmt.Errorf("internal error: message input missing from context")
	}

	// 1. Create the approval record (even if it might be auto-approved in future,
	// for now we follow the system's human-in-the-loop design).
	approval, err := t.store.CreateActionApproval(ctx, store.CreateActionApprovalInput{
		WorkspaceID:     record.WorkspaceID,
		ContextID:       record.ID,
		Connector:       input.Connector,
		ExternalID:      input.ExternalID,
		RequesterUserID: input.FromUserID,
		ActionType:      args.Type,
		ActionTarget:    args.Target,
		ActionSummary:   args.Summary,
		Payload:         args.Payload,
	})
	if err != nil {
		return "", err
	}

	// 2. Check if we can auto-approve
	// We reuse checkAutoApproval logic but don't return error, just bool
	canAutoApprove := false
	if input.FromUserID == "system:task-worker" {
		canAutoApprove = true
	} else if identity, err := t.store.LookupUserIdentity(ctx, input.Connector, input.FromUserID); err == nil {
		if identity.Role == "admin" || identity.Role == "overlord" {
			canAutoApprove = true
		}
	}

	if !canAutoApprove {
		return fmt.Sprintf("Action request created: %s. I need an admin to approve this before I can continue.", approval.ID), nil
	}

	// 3. Auto-approve
	approved, err := t.store.ApproveActionApproval(ctx, store.ApproveActionApprovalInput{
		ID:             approval.ID,
		ApproverUserID: "system:agent",
	})
	if err != nil {
		return "", fmt.Errorf("auto-approve failed: %w", err)
	}

	// 4. Execute
	result, err := t.executor.Execute(ctx, approved)

	status := "succeeded"
	msg := result.Message
	if err != nil {
		status = "failed"
		msg = err.Error()
	}
	_, _ = t.store.UpdateActionExecution(ctx, store.UpdateActionExecutionInput{
		ID:               approved.ID,
		ExecutionStatus:  status,
		ExecutionMessage: msg,
		ExecutorPlugin:   result.Plugin,
		ExecutedAt:       time.Now().UTC(),
	})

	if err != nil {
		return "", err
	}
	return result.Message, nil
}
