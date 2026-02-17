package httpapi

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/dwizi/agent-runtime/internal/store"
)

type objectiveRequest struct {
	WorkspaceID string `json:"workspace_id"`
	ContextID   string `json:"context_id"`
	Title       string `json:"title"`
	Prompt      string `json:"prompt"`
	TriggerType string `json:"trigger_type"`
	EventKey    string `json:"event_key"`
	CronExpr    string `json:"cron_expr"`
	Timezone    string `json:"timezone"`
	NextRunUnix int64  `json:"next_run_unix"`
	Active      *bool  `json:"active"`
}

type objectiveUpdateRequest struct {
	ID          string  `json:"id"`
	Title       *string `json:"title"`
	Prompt      *string `json:"prompt"`
	TriggerType *string `json:"trigger_type"`
	EventKey    *string `json:"event_key"`
	CronExpr    *string `json:"cron_expr"`
	Timezone    *string `json:"timezone"`
	NextRunUnix *int64  `json:"next_run_unix"`
	Active      *bool   `json:"active"`
}

type objectiveActiveRequest struct {
	ID     string `json:"id"`
	Active bool   `json:"active"`
}

type objectiveDeleteRequest struct {
	ID string `json:"id"`
}

func (r *router) handleObjectives(w http.ResponseWriter, req *http.Request) {
	switch req.Method {
	case http.MethodPost:
		r.handleObjectivesCreate(w, req)
	case http.MethodGet:
		r.handleObjectivesList(w, req)
	default:
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
	}
}

func (r *router) handleObjectivesCreate(w http.ResponseWriter, req *http.Request) {
	var payload objectiveRequest
	if err := json.NewDecoder(req.Body).Decode(&payload); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid payload"})
		return
	}
	triggerType := store.ObjectiveTriggerType(strings.ToLower(strings.TrimSpace(payload.TriggerType)))
	nextRun := time.Time{}
	if payload.NextRunUnix > 0 {
		nextRun = time.Unix(payload.NextRunUnix, 0).UTC()
	}
	objective, err := r.deps.Store.CreateObjective(req.Context(), store.CreateObjectiveInput{
		WorkspaceID: strings.TrimSpace(payload.WorkspaceID),
		ContextID:   strings.TrimSpace(payload.ContextID),
		Title:       strings.TrimSpace(payload.Title),
		Prompt:      strings.TrimSpace(payload.Prompt),
		TriggerType: triggerType,
		EventKey:    strings.TrimSpace(payload.EventKey),
		CronExpr:    strings.TrimSpace(payload.CronExpr),
		Timezone:    strings.TrimSpace(payload.Timezone),
		NextRunAt:   nextRun,
		Active:      payload.Active,
	})
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusCreated, objectiveToMap(objective))
}

func (r *router) handleObjectivesList(w http.ResponseWriter, req *http.Request) {
	workspaceID := strings.TrimSpace(req.URL.Query().Get("workspace_id"))
	if workspaceID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "workspace_id query parameter is required"})
		return
	}
	activeOnly := true
	if raw := strings.TrimSpace(strings.ToLower(req.URL.Query().Get("active_only"))); raw == "false" || raw == "0" || raw == "no" {
		activeOnly = false
	}
	limit := 50
	if raw := strings.TrimSpace(req.URL.Query().Get("limit")); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err == nil && parsed > 0 {
			limit = parsed
		}
	}
	items, err := r.deps.Store.ListObjectives(req.Context(), store.ListObjectivesInput{
		WorkspaceID: workspaceID,
		ActiveOnly:  activeOnly,
		Limit:       limit,
	})
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	payload := make([]map[string]any, 0, len(items))
	for _, item := range items {
		payload = append(payload, objectiveToMap(item))
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"items": payload,
		"count": len(payload),
	})
}

func (r *router) handleObjectivesUpdate(w http.ResponseWriter, req *http.Request) {
	if req.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	var payload objectiveUpdateRequest
	if err := json.NewDecoder(req.Body).Decode(&payload); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid payload"})
		return
	}
	input := store.UpdateObjectiveInput{
		ID:       strings.TrimSpace(payload.ID),
		Title:    payload.Title,
		Prompt:   payload.Prompt,
		EventKey: payload.EventKey,
		CronExpr: payload.CronExpr,
		Timezone: payload.Timezone,
		Active:   payload.Active,
	}
	if payload.TriggerType != nil {
		normalized := store.ObjectiveTriggerType(strings.ToLower(strings.TrimSpace(*payload.TriggerType)))
		input.TriggerType = &normalized
	}
	if payload.NextRunUnix != nil {
		nextRun := time.Time{}
		if *payload.NextRunUnix > 0 {
			nextRun = time.Unix(*payload.NextRunUnix, 0).UTC()
		}
		input.NextRunAt = &nextRun
	}
	objective, err := r.deps.Store.UpdateObjective(req.Context(), input)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, objectiveToMap(objective))
}

func (r *router) handleObjectivesActive(w http.ResponseWriter, req *http.Request) {
	if req.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	var payload objectiveActiveRequest
	if err := json.NewDecoder(req.Body).Decode(&payload); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid payload"})
		return
	}
	objective, err := r.deps.Store.SetObjectiveActive(req.Context(), strings.TrimSpace(payload.ID), payload.Active)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, objectiveToMap(objective))
}

func (r *router) handleObjectivesDelete(w http.ResponseWriter, req *http.Request) {
	if req.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	var payload objectiveDeleteRequest
	if err := json.NewDecoder(req.Body).Decode(&payload); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid payload"})
		return
	}
	if err := r.deps.Store.DeleteObjective(req.Context(), strings.TrimSpace(payload.ID)); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"id":      strings.TrimSpace(payload.ID),
		"deleted": true,
	})
}

func objectiveToMap(item store.Objective) map[string]any {
	avgRunDurationMs := int64(0)
	if item.RunCount > 0 {
		avgRunDurationMs = item.TotalRunDurationMs / int64(item.RunCount)
	}
	healthState := "healthy"
	if !item.Active && strings.TrimSpace(item.AutoPausedReason) != "" {
		healthState = "auto_paused"
	} else if !item.Active {
		healthState = "paused"
	} else if item.ConsecutiveFailures > 0 {
		healthState = "degraded"
	}
	return map[string]any{
		"id":                    item.ID,
		"workspace_id":          item.WorkspaceID,
		"context_id":            item.ContextID,
		"title":                 item.Title,
		"prompt":                item.Prompt,
		"trigger_type":          item.TriggerType,
		"event_key":             item.EventKey,
		"cron_expr":             item.CronExpr,
		"timezone":              item.Timezone,
		"active":                item.Active,
		"next_run_unix":         unixOrNil(item.NextRunAt),
		"last_run_unix":         unixOrNil(item.LastRunAt),
		"last_error":            item.LastError,
		"run_count":             item.RunCount,
		"success_count":         item.SuccessCount,
		"failure_count":         item.FailureCount,
		"consecutive_failures":  item.ConsecutiveFailures,
		"consecutive_successes": item.ConsecutiveSuccesses,
		"success_streak":        item.ConsecutiveSuccesses,
		"failure_streak":        item.ConsecutiveFailures,
		"total_run_duration_ms": item.TotalRunDurationMs,
		"avg_run_duration_ms":   avgRunDurationMs,
		"last_success_unix":     unixOrNil(item.LastSuccessAt),
		"last_failure_unix":     unixOrNil(item.LastFailureAt),
		"auto_paused_reason":    nullIfBlank(item.AutoPausedReason),
		"recent_errors":         objectiveRecentErrorsToMap(item.RecentErrors),
		"next_runs_unix":        objectiveNextRunsUnix(item, 5),
		"health_state":          healthState,
	}
}

func unixOrNil(value time.Time) any {
	if value.IsZero() {
		return nil
	}
	return value.UTC().Unix()
}

func nullIfBlank(value string) any {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	return value
}

func objectiveRecentErrorsToMap(errors []store.ObjectiveRunError) []map[string]any {
	results := make([]map[string]any, 0, len(errors))
	for _, item := range errors {
		if strings.TrimSpace(item.Message) == "" {
			continue
		}
		results = append(results, map[string]any{
			"at_unix": unixOrNil(item.OccurredAt),
			"error":   item.Message,
		})
	}
	return results
}

func objectiveNextRunsUnix(item store.Objective, limit int) []int64 {
	if limit < 1 {
		return []int64{}
	}
	if item.TriggerType != store.ObjectiveTriggerSchedule || strings.TrimSpace(item.CronExpr) == "" {
		return []int64{}
	}
	nextRuns := make([]int64, 0, limit)
	cursor := item.NextRunAt.UTC()
	if cursor.IsZero() {
		first, err := store.ComputeScheduleNextRunForTimezone(item.CronExpr, item.Timezone, time.Now().UTC())
		if err != nil || first.IsZero() {
			return []int64{}
		}
		cursor = first
	}
	nextRuns = append(nextRuns, cursor.Unix())
	for len(nextRuns) < limit {
		next, err := store.ComputeScheduleNextRunForTimezone(item.CronExpr, item.Timezone, cursor)
		if err != nil || next.IsZero() || !next.After(cursor) {
			break
		}
		nextRuns = append(nextRuns, next.Unix())
		cursor = next
	}
	return nextRuns
}
