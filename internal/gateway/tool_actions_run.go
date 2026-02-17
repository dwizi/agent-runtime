package gateway

import (
	"context"
	"encoding/json"
	"fmt"
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
	return "Execute a system action like 'run_command' (curl, etc.), 'send_email', or 'webhook'. Use this for external integration."
}

func (t *RunActionTool) ParametersSchema() string {
	return `{"type": "run_command|send_email|webhook", "target": "string", "summary": "brief summary", "payload": {}}`
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
	switch actionType {
	case "run_command", "send_email", "webhook":
	default:
		return fmt.Errorf("%w: type must be run_command, send_email, or webhook", agenterr.ErrToolInvalidArgs)
	}

	if actionType == "run_command" {
		if err := validateRunCommandPreflight(args.Target, args.Payload); err != nil {
			return err
		}
	}

	if actionType == "webhook" && strings.TrimSpace(args.Target) == "" {
		return fmt.Errorf("%w: target is required for webhook", agenterr.ErrToolInvalidArgs)
	}
	return nil
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
