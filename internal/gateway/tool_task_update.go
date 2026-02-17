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

type UpdateTaskTool struct {
	store Store
}

func NewUpdateTaskTool(store Store) *UpdateTaskTool {
	return &UpdateTaskTool{store: store}
}

func (t *UpdateTaskTool) Name() string { return "update_task" }
func (t *UpdateTaskTool) ToolClass() tools.ToolClass {
	return tools.ToolClassTasking
}
func (t *UpdateTaskTool) RequiresApproval() bool { return false }

func (t *UpdateTaskTool) Description() string {
	return "Update task routing metadata or close a task with a completion summary."
}

func (t *UpdateTaskTool) ParametersSchema() string {
	return `{"task_id":"string","status":"open|closed(optional)","route_class":"question|issue|task|moderation|noise(optional)","priority":"p1|p2|p3(optional)","lane":"string(optional)","due_in":"duration like 2h or 1d(optional)","summary":"string(optional)"}`
}

func (t *UpdateTaskTool) ValidateArgs(rawArgs json.RawMessage) error {
	var args struct {
		TaskID     string `json:"task_id"`
		Status     string `json:"status"`
		RouteClass string `json:"route_class"`
		Priority   string `json:"priority"`
		Lane       string `json:"lane"`
		DueIn      string `json:"due_in"`
		Summary    string `json:"summary"`
	}
	if err := strictDecodeArgs(rawArgs, &args); err != nil {
		return err
	}
	if strings.TrimSpace(args.TaskID) == "" {
		return fmt.Errorf("task_id is required")
	}
	if status := strings.ToLower(strings.TrimSpace(args.Status)); status != "" {
		if status != "open" && status != "closed" {
			return fmt.Errorf("status must be open or closed")
		}
	}
	if cls := strings.TrimSpace(args.RouteClass); cls != "" {
		if _, ok := normalizeTriageClass(cls); !ok {
			return fmt.Errorf("invalid route_class")
		}
	}
	if pr := strings.TrimSpace(args.Priority); pr != "" {
		if _, ok := normalizeTriagePriority(pr); !ok {
			return fmt.Errorf("invalid priority")
		}
	}
	if due := strings.TrimSpace(args.DueIn); due != "" {
		if _, err := parseDueWindow(due); err != nil {
			return fmt.Errorf("invalid due_in: %w", err)
		}
	}
	if strings.TrimSpace(args.Status) == "" &&
		strings.TrimSpace(args.RouteClass) == "" &&
		strings.TrimSpace(args.Priority) == "" &&
		strings.TrimSpace(args.Lane) == "" &&
		strings.TrimSpace(args.DueIn) == "" &&
		strings.TrimSpace(args.Summary) == "" {
		return fmt.Errorf("at least one update field must be provided")
	}
	return nil
}

func (t *UpdateTaskTool) Execute(ctx context.Context, rawArgs json.RawMessage) (string, error) {
	var args struct {
		TaskID     string `json:"task_id"`
		Status     string `json:"status"`
		RouteClass string `json:"route_class"`
		Priority   string `json:"priority"`
		Lane       string `json:"lane"`
		DueIn      string `json:"due_in"`
		Summary    string `json:"summary"`
	}
	if err := strictDecodeArgs(rawArgs, &args); err != nil {
		return "", fmt.Errorf("invalid arguments: %w", err)
	}

	if err := checkAutoApproval(ctx, t.store); err != nil {
		return "", fmt.Errorf("approval required: %w", err)
	}

	taskID := strings.TrimSpace(args.TaskID)
	status := strings.ToLower(strings.TrimSpace(args.Status))
	if status == "closed" {
		summary := strings.TrimSpace(args.Summary)
		if summary == "" {
			summary = "Closed by assistant."
		}
		if err := t.store.MarkTaskCompleted(ctx, taskID, time.Now().UTC(), summary, ""); err != nil {
			return "", err
		}
		return fmt.Sprintf("Task closed successfully (ID: %s).", taskID), nil
	}

	record, err := t.store.LookupTask(ctx, taskID)
	if err != nil {
		return "", err
	}
	class := TriageTask
	if cls := strings.TrimSpace(record.RouteClass); cls != "" {
		if normalized, ok := normalizeTriageClass(cls); ok {
			class = normalized
		}
	}
	if cls := strings.TrimSpace(args.RouteClass); cls != "" {
		normalized, _ := normalizeTriageClass(cls)
		class = normalized
	}

	priority := TriagePriorityP3
	if p := strings.TrimSpace(record.Priority); p != "" {
		if normalized, ok := normalizeTriagePriority(p); ok {
			priority = normalized
		}
	}
	if p := strings.TrimSpace(args.Priority); p != "" {
		normalized, _ := normalizeTriagePriority(p)
		priority = normalized
	}

	dueAt := record.DueAt
	if dueAt.IsZero() {
		dueAt = time.Now().UTC().Add(24 * time.Hour)
	}
	if due := strings.TrimSpace(args.DueIn); due != "" {
		window, err := parseDueWindow(due)
		if err != nil {
			return "", err
		}
		dueAt = time.Now().UTC().Add(window)
	}

	lane := strings.TrimSpace(record.AssignedLane)
	if lane == "" {
		_, _, lane = routingDefaults(class)
	}
	if updatedLane := strings.TrimSpace(args.Lane); updatedLane != "" {
		lane = updatedLane
	}
	if status == "open" && strings.TrimSpace(args.Summary) != "" {
		// Summary is accepted for compatibility but open status does not mutate completion fields.
	}

	if _, err := t.store.UpdateTaskRouting(ctx, store.UpdateTaskRoutingInput{
		ID:           taskID,
		RouteClass:   string(class),
		Priority:     string(priority),
		DueAt:        dueAt,
		AssignedLane: lane,
	}); err != nil {
		return "", err
	}
	return fmt.Sprintf("Task updated successfully (ID: %s).", taskID), nil
}
