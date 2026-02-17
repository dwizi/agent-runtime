package gateway

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/dwizi/agent-runtime/internal/agent/tools"
	"github.com/dwizi/agent-runtime/internal/orchestrator"
	"github.com/dwizi/agent-runtime/internal/store"
)

// CreateTaskTool implements tools.Tool for creating tasks.
type CreateTaskTool struct {
	engine Engine
	store  Store
}

func NewCreateTaskTool(store Store, engine Engine) *CreateTaskTool {
	return &CreateTaskTool{store: store, engine: engine}
}

func (t *CreateTaskTool) Name() string { return "create_task" }
func (t *CreateTaskTool) ToolClass() tools.ToolClass {
	return tools.ToolClassTasking
}
func (t *CreateTaskTool) RequiresApproval() bool { return false }

func (t *CreateTaskTool) Description() string {
	return "Create a background task for complex jobs, investigations, or system changes."
}

func (t *CreateTaskTool) ParametersSchema() string {
	return `{"title": "string", "description": "string", "priority": "p1|p2|p3"}`
}

func (t *CreateTaskTool) ValidateArgs(rawArgs json.RawMessage) error {
	var args struct {
		Title       string `json:"title"`
		Description string `json:"description"`
		Priority    string `json:"priority"`
	}
	if err := strictDecodeArgs(rawArgs, &args); err != nil {
		return err
	}
	args.Title = strings.TrimSpace(args.Title)
	args.Description = strings.TrimSpace(args.Description)
	if args.Title == "" {
		return fmt.Errorf("title is required")
	}
	if len(args.Title) > 120 {
		return fmt.Errorf("title is too long")
	}
	if args.Description == "" {
		return fmt.Errorf("description is required")
	}
	if len(args.Description) > 4000 {
		return fmt.Errorf("description is too long")
	}
	if strings.TrimSpace(args.Priority) != "" {
		if _, ok := normalizeTriagePriority(args.Priority); !ok {
			return fmt.Errorf("priority must be p1, p2, or p3")
		}
	}
	return nil
}

func (t *CreateTaskTool) Execute(ctx context.Context, rawArgs json.RawMessage) (string, error) {
	var args struct {
		Title       string `json:"title"`
		Description string `json:"description"`
		Priority    string `json:"priority"`
	}
	if err := strictDecodeArgs(rawArgs, &args); err != nil {
		return "", fmt.Errorf("invalid arguments: %w", err)
	}

	record, input, err := readToolContext(ctx)
	if err != nil {
		return "", err
	}

	// Check approval if not system/admin
	// CreateTaskTool previously had RequiresApproval() = false,
	// but the Agent loop was blocking sensitive tools if they required approval.
	// Now we moved approval logic inside Execute for other tools.
	// But CreateTaskTool was always false.
	// The complaint is likely about the SUB-TASKS created by the agent or subsequent actions?
	// OR the user means: "I ask agent to do something, it creates 5 subtasks, and I have to approve each one?"
	// If create_task does not require approval (it doesn't), then the user doesn't approve CREATION.
	// But maybe the ACTIONS inside those tasks require approval?
	// If the user is Admin, we already enabled auto-approval for fetch/search etc. in the WORKER.
	// But maybe the user is talking about the CHAT session where the agent proposes actions?
	// The user said: "it needs to approve multiple ones for just one prompt".
	// This usually means the agent proposes multiple `run_action` calls in sequence.
	// And `RunActionTool` (legacy) requires approval.
	// I should update `RunActionTool` to also support auto-approval for Admin!
	// This is the missing piece. I updated specialized tools, but RunActionTool (the generic one used by legacy/chat agent) still blocks.

	priority := "p3"
	if p, ok := normalizeTriagePriority(args.Priority); ok {
		priority = string(p)
	}

	task, err := t.engine.Enqueue(orchestrator.Task{
		WorkspaceID: record.WorkspaceID,
		ContextID:   record.ID,
		Kind:        orchestrator.TaskKindGeneral,
		Title:       args.Title,
		Prompt:      args.Description,
	})
	if err != nil {
		return "", err
	}

	persistErr := t.store.CreateTask(ctx, store.CreateTaskInput{
		ID:               task.ID,
		WorkspaceID:      task.WorkspaceID,
		ContextID:        task.ContextID,
		Kind:             string(task.Kind),
		Title:            task.Title,
		Prompt:           task.Prompt,
		Status:           "queued",
		RouteClass:       string(TriageTask),
		Priority:         priority,
		DueAt:            time.Now().UTC().Add(24 * time.Hour),
		AssignedLane:     "operations",
		SourceConnector:  strings.ToLower(strings.TrimSpace(input.Connector)),
		SourceExternalID: strings.TrimSpace(input.ExternalID),
		SourceUserID:     strings.TrimSpace(input.FromUserID),
		SourceText:       input.Text,
	})
	if persistErr != nil {
		return "", fmt.Errorf("task queued but failed to persist: %w", persistErr)
	}

	return fmt.Sprintf("Task created successfully (ID: %s).", task.ID), nil
}
