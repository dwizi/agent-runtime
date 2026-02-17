package gateway

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/dwizi/agent-runtime/internal/actions"
	"github.com/dwizi/agent-runtime/internal/agent/tools"
	"github.com/dwizi/agent-runtime/internal/store"
)

// PythonCodeTool executes Python code via the sandbox.
type PythonCodeTool struct {
	store          Store
	actionExecutor ActionExecutor
	workspaceRoot  string
}

func NewPythonCodeTool(store Store, executor ActionExecutor, workspaceRoot string) *PythonCodeTool {
	return &PythonCodeTool{store: store, actionExecutor: executor, workspaceRoot: workspaceRoot}
}

func (t *PythonCodeTool) Name() string { return "python_code" }
func (t *PythonCodeTool) ToolClass() tools.ToolClass {
	return tools.ToolClassGeneral
}
func (t *PythonCodeTool) RequiresApproval() bool { return false }

func (t *PythonCodeTool) Description() string {
	return "Execute Python code for data analysis or complex logic. Returns stdout/stderr."
}

func (t *PythonCodeTool) ParametersSchema() string {
	return `{"code": "string"}`
}

func (t *PythonCodeTool) Execute(ctx context.Context, rawArgs json.RawMessage) (string, error) {
	var args struct {
		Code string `json:"code"`
	}
	if err := json.Unmarshal(rawArgs, &args); err != nil {
		return "", fmt.Errorf("invalid arguments: %w", err)
	}
	if strings.TrimSpace(args.Code) == "" {
		return "", fmt.Errorf("code is required")
	}

	record, ok := ctx.Value(ContextKeyRecord).(store.ContextRecord)
	if !ok {
		return "", fmt.Errorf("internal error: context record missing from context")
	}
	input, ok := ctx.Value(ContextKeyInput).(MessageInput)
	if !ok {
		return "", fmt.Errorf("internal error: message input missing from context")
	}

	// 1. Write code to a temp file in scratch
	scratchDir := filepath.Join(t.workspaceRoot, record.WorkspaceID, "scratch")
	if err := os.MkdirAll(scratchDir, 0o755); err != nil {
		return "", fmt.Errorf("create scratch dir: %w", err)
	}
	fileName := fmt.Sprintf("__py_%d.py", time.Now().UnixNano())
	fullPath := filepath.Join(scratchDir, fileName)
	if err := os.WriteFile(fullPath, []byte(args.Code), 0o644); err != nil {
		return "", fmt.Errorf("write python script: %w", err)
	}
	
	// Relative path for execution (relative to workspace root)
	relPath := filepath.Join("scratch", fileName)

	// 2. Create approval
	approval, err := t.store.CreateActionApproval(ctx, store.CreateActionApprovalInput{
		WorkspaceID:     record.WorkspaceID,
		ContextID:       record.ID,
		Connector:       input.Connector,
		ExternalID:      input.ExternalID,
		RequesterUserID: input.FromUserID,
		ActionType:      "run_command",
		ActionTarget:    "python3",
		ActionSummary:   fmt.Sprintf("run python code (%d bytes)", len(args.Code)),
		Payload: map[string]any{
			"command": "python3",
			"args":    []string{relPath},
			"cwd":     record.WorkspaceID,
		},
	})
	if err != nil {
		return "", err
	}

	// 3. Check if we can auto-approve
	canAutoApprove := false
	if input.FromUserID == "system:task-worker" {
		canAutoApprove = true
	} else {
		if identity, err := t.store.LookupUserIdentity(ctx, input.Connector, input.FromUserID); err == nil {
			if identity.Role == "admin" || identity.Role == "overlord" {
				canAutoApprove = true
			}
		}
	}
	
	if !canAutoApprove {
		return actions.FormatApprovalRequestNotice(approval.ID), nil
	}

	// 4. Auto-approve
	approved, err := t.store.ApproveActionApproval(ctx, store.ApproveActionApprovalInput{
		ID:             approval.ID,
		ApproverUserID: "system:agent",
	})
	if err != nil {
		return "", fmt.Errorf("auto-approve failed: %w", err)
	}

	// 5. Execute
	result, err := t.actionExecutor.Execute(ctx, approved)
	
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
