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

// LookupTaskTool allows the agent to inspect task status and details.
type LookupTaskTool struct {
	store Store
}

func NewLookupTaskTool(store Store) *LookupTaskTool {
	return &LookupTaskTool{store: store}
}

func (t *LookupTaskTool) Name() string { return "lookup_task" }
func (t *LookupTaskTool) ToolClass() tools.ToolClass {
	return tools.ToolClassTasking
}
func (t *LookupTaskTool) RequiresApproval() bool { return false }

func (t *LookupTaskTool) Description() string {
	return "Check the status and details of a specific task."
}

func (t *LookupTaskTool) ParametersSchema() string {
	return `{"task_id": "string"}`
}

func (t *LookupTaskTool) Execute(ctx context.Context, rawArgs json.RawMessage) (string, error) {
	var args struct {
		TaskID string `json:"task_id"`
	}
	if err := json.Unmarshal(rawArgs, &args); err != nil {
		return "", fmt.Errorf("invalid arguments: %w", err)
	}
	if strings.TrimSpace(args.TaskID) == "" {
		return "", fmt.Errorf("task_id is required")
	}

	task, err := t.store.LookupTask(ctx, args.TaskID)
	if err != nil {
		return "", fmt.Errorf("lookup failed: %w", err)
	}

	// Verify workspace access (implied by context record usually, but store lookup might be global?)
	// store.LookupTask usually returns task by ID.
	// We should check if it belongs to the current workspace.
	record, ok := ctx.Value(ContextKeyRecord).(store.ContextRecord)
	if ok && record.WorkspaceID != "" {
		if task.WorkspaceID != record.WorkspaceID {
			return "", fmt.Errorf("task not found in this workspace")
		}
	}

	lines := []string{
		fmt.Sprintf("Task ID: %s", task.ID),
		fmt.Sprintf("Title: %s", task.Title),
		fmt.Sprintf("Status: %s", task.Status),
		fmt.Sprintf("Created: %s", task.CreatedAt.Format(time.RFC3339)),
	}
	if !task.DueAt.IsZero() {
		lines = append(lines, fmt.Sprintf("Due: %s", task.DueAt.Format(time.RFC3339)))
	}
	if task.ResultPath != "" {
		lines = append(lines, fmt.Sprintf("Result Path: %s", task.ResultPath))
	}
	if task.ResultSummary != "" {
		lines = append(lines, fmt.Sprintf("Summary: %s", task.ResultSummary))
	} else if task.Prompt != "" {
		prompt := task.Prompt
		if len(prompt) > 200 {
			prompt = prompt[:200] + "..."
		}
		lines = append(lines, fmt.Sprintf("Prompt: %s", prompt))
	}

	return strings.Join(lines, "\n"), nil
}
