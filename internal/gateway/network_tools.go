package gateway

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/dwizi/agent-runtime/internal/agent/tools"
	"github.com/dwizi/agent-runtime/internal/store"
)

// CurlTool implements a tool for immediate curl execution (requires sensitive approval in context).
type CurlTool struct {
	store          Store
	actionExecutor ActionExecutor
}

func NewCurlTool(store Store, executor ActionExecutor) *CurlTool {
	return &CurlTool{store: store, actionExecutor: executor}
}

func (t *CurlTool) Name() string { return "curl" }
func (t *CurlTool) ToolClass() tools.ToolClass {
	return tools.ToolClassGeneral
}
func (t *CurlTool) RequiresApproval() bool { return true }

func (t *CurlTool) Description() string {
	return "Execute a curl command to fetch data from the web. Only available in autonomous mode."
}

func (t *CurlTool) ParametersSchema() string {
	return `{"args": ["string"]}`
}

func (t *CurlTool) ValidateArgs(rawArgs json.RawMessage) error {
	var args struct {
		Args []string `json:"args"`
	}
	if err := json.Unmarshal(rawArgs, &args); err != nil {
		return fmt.Errorf("invalid arguments: %w", err)
	}
	if len(args.Args) == 0 {
		return fmt.Errorf("args cannot be empty")
	}
	return nil
}

func (t *CurlTool) Execute(ctx context.Context, rawArgs json.RawMessage) (string, error) {
	var args struct {
		Args []string `json:"args"`
	}
	if err := json.Unmarshal(rawArgs, &args); err != nil {
		return "", fmt.Errorf("invalid arguments: %w", err)
	}

	record, ok := ctx.Value(ContextKeyRecord).(store.ContextRecord)
	if !ok {
		return "", fmt.Errorf("internal error: context record missing from context")
	}
	input, ok := ctx.Value(ContextKeyInput).(MessageInput)
	if !ok {
		return "", fmt.Errorf("internal error: message input missing from context")
	}

	// 1. Create the approval record
	approval, err := t.store.CreateActionApproval(ctx, store.CreateActionApprovalInput{
		WorkspaceID:     record.WorkspaceID,
		ContextID:       record.ID,
		Connector:       input.Connector,
		ExternalID:      input.ExternalID,
		RequesterUserID: input.FromUserID,
		ActionType:      "run_command",
		ActionTarget:    "curl",
		ActionSummary:   fmt.Sprintf("curl %s", strings.Join(args.Args, " ")),
		Payload: map[string]any{
			"command": "curl",
			"args":    args.Args,
		},
	})
	if err != nil {
		return "", err
	}

	// 2. Auto-approve it (since we are in a sensitive-approved context if we got here)
	approved, err := t.store.ApproveActionApproval(ctx, store.ApproveActionApprovalInput{
		ID:             approval.ID,
		ApproverUserID: "system:agent",
	})
	if err != nil {
		return "", fmt.Errorf("auto-approve failed: %w", err)
	}

	// 3. Execute it
	result, err := t.actionExecutor.Execute(ctx, approved)
	
	// 4. Update status
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
