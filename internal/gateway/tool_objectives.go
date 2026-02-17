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

type CreateObjectiveTool struct {
	store Store
}

func NewCreateObjectiveTool(store Store) *CreateObjectiveTool {
	return &CreateObjectiveTool{store: store}
}

func (t *CreateObjectiveTool) Name() string { return "create_objective" }
func (t *CreateObjectiveTool) ToolClass() tools.ToolClass {
	return tools.ToolClassObjective
}
func (t *CreateObjectiveTool) RequiresApproval() bool { return false }

func (t *CreateObjectiveTool) Description() string {
	return "Create a proactive monitoring objective for recurring community checks."
}

func (t *CreateObjectiveTool) ParametersSchema() string {
	return `{"title":"string","prompt":"string","cron_expr":"string(optional, default: 0 */6 * * *)","timezone":"string(optional, IANA timezone)","active":"boolean(optional)"}`
}

func (t *CreateObjectiveTool) ValidateArgs(rawArgs json.RawMessage) error {
	var args struct {
		Title    string `json:"title"`
		Prompt   string `json:"prompt"`
		CronExpr string `json:"cron_expr"`
		Timezone string `json:"timezone"`
		Active   *bool  `json:"active"`
	}
	if err := strictDecodeArgs(rawArgs, &args); err != nil {
		return err
	}
	if strings.TrimSpace(args.Title) == "" {
		return fmt.Errorf("title is required")
	}
	if strings.TrimSpace(args.Prompt) == "" {
		return fmt.Errorf("prompt is required")
	}
	timezone := strings.TrimSpace(args.Timezone)
	if timezone != "" {
		if _, err := time.LoadLocation(timezone); err != nil {
			return fmt.Errorf("timezone is invalid")
		}
	}
	if cronExpr := strings.TrimSpace(args.CronExpr); cronExpr != "" {
		if _, err := store.ComputeScheduleNextRunForTimezone(cronExpr, timezone, time.Now().UTC()); err != nil {
			return fmt.Errorf("cron_expr is invalid")
		}
	}
	return nil
}

func (t *CreateObjectiveTool) Execute(ctx context.Context, rawArgs json.RawMessage) (string, error) {
	var args struct {
		Title    string `json:"title"`
		Prompt   string `json:"prompt"`
		CronExpr string `json:"cron_expr"`
		Timezone string `json:"timezone"`
		Active   *bool  `json:"active"`
	}
	if err := strictDecodeArgs(rawArgs, &args); err != nil {
		return "", fmt.Errorf("invalid arguments: %w", err)
	}
	record, _, err := readToolContext(ctx)
	if err != nil {
		return "", err
	}

	// Check approval if not system/admin
	if err := checkAutoApproval(ctx, t.store); err != nil {
		// For objective creation, we don't have a specific ActionApproval flow wired up for 'create_objective' tool class yet?
		// Wait, 'CreateObjectiveTool' is ToolClassObjective.
		// If RequiresApproval is false, the Agent calls it.
		// If we want to gate it, we must implement internal gating or rely on Agent policy.
		// Agent policy 'RequiresApproval' was true. Now false.
		// So we must gate internally if we want to restrict non-admins.
		// But 'checkAutoApproval' will return error if not admin.
		return "", fmt.Errorf("approval required: %w", err)
	}

	cronExpr := strings.TrimSpace(args.CronExpr)
	if cronExpr == "" {
		cronExpr = defaultObjectiveCronExpr
	}
	obj, err := t.store.CreateObjective(ctx, store.CreateObjectiveInput{
		WorkspaceID: record.WorkspaceID,
		ContextID:   record.ID,
		Title:       strings.TrimSpace(args.Title),
		Prompt:      strings.TrimSpace(args.Prompt),
		TriggerType: store.ObjectiveTriggerSchedule,
		CronExpr:    cronExpr,
		Timezone:    strings.TrimSpace(args.Timezone),
		Active:      args.Active,
	})
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("Objective created successfully (ID: %s).", obj.ID), nil
}

type UpdateObjectiveTool struct {
	store Store
}

func NewUpdateObjectiveTool(store Store) *UpdateObjectiveTool {
	return &UpdateObjectiveTool{store: store}
}

func (t *UpdateObjectiveTool) Name() string { return "update_objective" }
func (t *UpdateObjectiveTool) ToolClass() tools.ToolClass {
	return tools.ToolClassObjective
}
func (t *UpdateObjectiveTool) RequiresApproval() bool { return false }

func (t *UpdateObjectiveTool) Description() string {
	return "Update objective settings such as title, prompt, schedule, trigger type, or active state."
}

func (t *UpdateObjectiveTool) ParametersSchema() string {
	return `{"objective_id":"string","title":"string(optional)","prompt":"string(optional)","trigger_type":"schedule|event(optional)","event_key":"string(optional)","cron_expr":"string(optional)","timezone":"string(optional, IANA timezone)","active":"boolean(optional)"}`
}

func (t *UpdateObjectiveTool) ValidateArgs(rawArgs json.RawMessage) error {
	var args struct {
		ObjectiveID string  `json:"objective_id"`
		Title       string  `json:"title"`
		Prompt      string  `json:"prompt"`
		TriggerType string  `json:"trigger_type"`
		EventKey    string  `json:"event_key"`
		CronExpr    *string `json:"cron_expr"`
		Timezone    *string `json:"timezone"`
		Active      *bool   `json:"active"`
	}
	if err := strictDecodeArgs(rawArgs, &args); err != nil {
		return err
	}
	if strings.TrimSpace(args.ObjectiveID) == "" {
		return fmt.Errorf("objective_id is required")
	}
	trigger := strings.ToLower(strings.TrimSpace(args.TriggerType))
	if trigger != "" {
		if trigger != string(store.ObjectiveTriggerSchedule) && trigger != string(store.ObjectiveTriggerEvent) {
			return fmt.Errorf("trigger_type must be schedule or event")
		}
		if trigger == string(store.ObjectiveTriggerEvent) && strings.TrimSpace(args.EventKey) == "" {
			return fmt.Errorf("event_key is required when trigger_type is event")
		}
	}
	timezone := ""
	if args.Timezone != nil {
		timezone = strings.TrimSpace(*args.Timezone)
		if timezone != "" {
			if _, err := time.LoadLocation(timezone); err != nil {
				return fmt.Errorf("timezone is invalid")
			}
		}
	}
	if args.CronExpr != nil {
		cronExpr := strings.TrimSpace(*args.CronExpr)
		if cronExpr == "" {
			if trigger != string(store.ObjectiveTriggerEvent) {
				return fmt.Errorf("cron_expr cannot be empty for schedule objectives")
			}
		} else if _, err := store.ComputeScheduleNextRunForTimezone(cronExpr, timezone, time.Now().UTC()); err != nil {
			return fmt.Errorf("cron_expr is invalid")
		}
	}
	if strings.TrimSpace(args.Title) == "" &&
		strings.TrimSpace(args.Prompt) == "" &&
		strings.TrimSpace(args.TriggerType) == "" &&
		strings.TrimSpace(args.EventKey) == "" &&
		args.CronExpr == nil &&
		args.Timezone == nil &&
		args.Active == nil {
		return fmt.Errorf("at least one field must be provided")
	}
	return nil
}

func (t *UpdateObjectiveTool) Execute(ctx context.Context, rawArgs json.RawMessage) (string, error) {
	var args struct {
		ObjectiveID string  `json:"objective_id"`
		Title       string  `json:"title"`
		Prompt      string  `json:"prompt"`
		TriggerType string  `json:"trigger_type"`
		EventKey    string  `json:"event_key"`
		CronExpr    *string `json:"cron_expr"`
		Timezone    *string `json:"timezone"`
		Active      *bool   `json:"active"`
	}
	if err := strictDecodeArgs(rawArgs, &args); err != nil {
		return "", fmt.Errorf("invalid arguments: %w", err)
	}

	if err := checkAutoApproval(ctx, t.store); err != nil {
		return "", fmt.Errorf("approval required: %w", err)
	}

	update := store.UpdateObjectiveInput{
		ID: strings.TrimSpace(args.ObjectiveID),
	}
	if title := strings.TrimSpace(args.Title); title != "" {
		update.Title = &title
	}
	if prompt := strings.TrimSpace(args.Prompt); prompt != "" {
		update.Prompt = &prompt
	}
	if trigger := strings.ToLower(strings.TrimSpace(args.TriggerType)); trigger != "" {
		triggerType := store.ObjectiveTriggerType(trigger)
		update.TriggerType = &triggerType
	}
	if eventKey := strings.TrimSpace(args.EventKey); eventKey != "" {
		update.EventKey = &eventKey
	}
	if args.CronExpr != nil {
		cronExpr := strings.TrimSpace(*args.CronExpr)
		update.CronExpr = &cronExpr
	}
	if args.Timezone != nil {
		timezone := strings.TrimSpace(*args.Timezone)
		update.Timezone = &timezone
	}
	if args.Active != nil {
		update.Active = args.Active
	}
	obj, err := t.store.UpdateObjective(ctx, update)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("Objective updated successfully (ID: %s, active=%t).", obj.ID, obj.Active), nil
}
