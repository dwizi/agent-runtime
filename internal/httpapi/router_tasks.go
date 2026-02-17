package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/dwizi/agent-runtime/internal/orchestrator"
	"github.com/dwizi/agent-runtime/internal/store"
)

type taskRequest struct {
	WorkspaceID      string `json:"workspace_id"`
	ContextID        string `json:"context_id"`
	Title            string `json:"title"`
	Prompt           string `json:"prompt"`
	Kind             string `json:"kind"`
	RouteClass       string `json:"route_class"`
	Priority         string `json:"priority"`
	AssignedLane     string `json:"assigned_lane"`
	DueAtUnix        int64  `json:"due_at_unix"`
	SourceConnector  string `json:"source_connector"`
	SourceExternalID string `json:"source_external_id"`
	SourceUserID     string `json:"source_user_id"`
	SourceText       string `json:"source_text"`
}

func (r *router) handleTasks(w http.ResponseWriter, req *http.Request) {
	switch req.Method {
	case http.MethodPost:
		r.handleTaskCreate(w, req)
	case http.MethodGet:
		r.handleTaskGet(w, req)
	default:
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
	}
}

func (r *router) handleTaskCreate(w http.ResponseWriter, req *http.Request) {
	var payload taskRequest
	if err := json.NewDecoder(req.Body).Decode(&payload); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid payload"})
		return
	}
	if payload.WorkspaceID == "" || payload.ContextID == "" || payload.Title == "" || payload.Prompt == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "workspace_id, context_id, title and prompt are required"})
		return
	}

	kind := orchestrator.TaskKind(payload.Kind)
	if kind == "" {
		kind = orchestrator.TaskKindGeneral
	}
	dueAt := time.Time{}
	if payload.DueAtUnix > 0 {
		dueAt = time.Unix(payload.DueAtUnix, 0).UTC()
	}
	task, err := r.enqueueAndPersistTask(req.Context(), store.CreateTaskInput{
		WorkspaceID:      payload.WorkspaceID,
		ContextID:        payload.ContextID,
		Kind:             string(kind),
		Title:            payload.Title,
		Prompt:           payload.Prompt,
		Status:           "queued",
		RouteClass:       payload.RouteClass,
		Priority:         payload.Priority,
		AssignedLane:     payload.AssignedLane,
		DueAt:            dueAt,
		SourceConnector:  payload.SourceConnector,
		SourceExternalID: payload.SourceExternalID,
		SourceUserID:     payload.SourceUserID,
		SourceText:       payload.SourceText,
	})
	if err != nil {
		status := http.StatusInternalServerError
		if errors.Is(err, orchestrator.ErrQueueFull) {
			status = http.StatusTooManyRequests
		}
		writeJSON(w, status, map[string]string{"error": err.Error()})
		return
	}

	writeJSON(w, http.StatusAccepted, map[string]any{
		"id":           task.ID,
		"workspace_id": task.WorkspaceID,
		"context_id":   task.ContextID,
		"kind":         task.Kind,
		"status":       "queued",
	})
}

func (r *router) handleTaskGet(w http.ResponseWriter, req *http.Request) {
	taskID := strings.TrimSpace(req.URL.Query().Get("id"))
	if taskID != "" {
		record, err := r.deps.Store.LookupTask(req.Context(), taskID)
		if err != nil {
			status := http.StatusInternalServerError
			if errors.Is(err, store.ErrTaskNotFound) {
				status = http.StatusNotFound
			}
			writeJSON(w, status, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, taskRecordResponse(record))
		return
	}

	workspaceID := strings.TrimSpace(req.URL.Query().Get("workspace_id"))
	if workspaceID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "workspace_id query parameter is required"})
		return
	}
	limit := 100
	if limitInput := strings.TrimSpace(req.URL.Query().Get("limit")); limitInput != "" {
		parsed, err := strconv.Atoi(limitInput)
		if err != nil || parsed < 1 {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "limit must be a positive integer"})
			return
		}
		limit = parsed
	}
	items, err := r.deps.Store.ListTasks(req.Context(), store.ListTasksInput{
		WorkspaceID: workspaceID,
		ContextID:   strings.TrimSpace(req.URL.Query().Get("context_id")),
		Kind:        strings.TrimSpace(req.URL.Query().Get("kind")),
		Status:      strings.TrimSpace(req.URL.Query().Get("status")),
		Limit:       limit,
	})
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	resultItems := make([]map[string]any, 0, len(items))
	for _, item := range items {
		resultItems = append(resultItems, taskRecordResponse(item))
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"items": resultItems,
		"count": len(resultItems),
	})
}

type taskRetryRequest struct {
	TaskID string `json:"task_id"`
}

func (r *router) handleTaskRetry(w http.ResponseWriter, req *http.Request) {
	if req.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	var payload taskRetryRequest
	if err := json.NewDecoder(req.Body).Decode(&payload); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid payload"})
		return
	}
	taskID := strings.TrimSpace(payload.TaskID)
	if taskID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "task_id is required"})
		return
	}

	original, err := r.deps.Store.LookupTask(req.Context(), taskID)
	if err != nil {
		status := http.StatusInternalServerError
		if errors.Is(err, store.ErrTaskNotFound) {
			status = http.StatusNotFound
		}
		writeJSON(w, status, map[string]string{"error": err.Error()})
		return
	}
	if strings.ToLower(strings.TrimSpace(original.Status)) != "failed" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "only failed tasks can be retried"})
		return
	}

	kind := orchestrator.TaskKind(strings.TrimSpace(original.Kind))
	if kind == "" {
		kind = orchestrator.TaskKindGeneral
	}
	task, err := r.enqueueAndPersistTask(req.Context(), store.CreateTaskInput{
		WorkspaceID:      original.WorkspaceID,
		ContextID:        original.ContextID,
		Kind:             string(kind),
		Title:            original.Title,
		Prompt:           original.Prompt,
		Status:           "queued",
		RouteClass:       original.RouteClass,
		Priority:         original.Priority,
		DueAt:            original.DueAt,
		AssignedLane:     original.AssignedLane,
		SourceConnector:  original.SourceConnector,
		SourceExternalID: original.SourceExternalID,
		SourceUserID:     original.SourceUserID,
		SourceText:       original.SourceText,
	})
	if err != nil {
		status := http.StatusInternalServerError
		if errors.Is(err, orchestrator.ErrQueueFull) {
			status = http.StatusTooManyRequests
		}
		writeJSON(w, status, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{
		"task_id":       task.ID,
		"retry_of_task": original.ID,
		"workspace_id":  task.WorkspaceID,
		"context_id":    task.ContextID,
		"kind":          task.Kind,
		"status":        "queued",
	})
}

func (r *router) enqueueAndPersistTask(ctx context.Context, input store.CreateTaskInput) (orchestrator.Task, error) {
	task, err := r.deps.Engine.Enqueue(orchestrator.Task{
		WorkspaceID: strings.TrimSpace(input.WorkspaceID),
		ContextID:   strings.TrimSpace(input.ContextID),
		Title:       strings.TrimSpace(input.Title),
		Prompt:      strings.TrimSpace(input.Prompt),
		Kind:        orchestrator.TaskKind(strings.TrimSpace(input.Kind)),
	})
	if err != nil {
		return orchestrator.Task{}, err
	}

	storeCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	input.ID = task.ID
	input.WorkspaceID = task.WorkspaceID
	input.ContextID = task.ContextID
	input.Kind = string(task.Kind)
	input.Title = task.Title
	input.Prompt = task.Prompt
	if strings.TrimSpace(input.Status) == "" {
		input.Status = "queued"
	}
	if err := r.deps.Store.CreateTask(storeCtx, input); err != nil {
		r.deps.Logger.Error("failed to persist task", "error", err, "task_id", task.ID)
		return orchestrator.Task{}, err
	}
	return task, nil
}

func taskRecordResponse(record store.TaskRecord) map[string]any {
	dueAtUnix := int64(0)
	if !record.DueAt.IsZero() {
		dueAtUnix = record.DueAt.Unix()
	}
	startedAtUnix := int64(0)
	if !record.StartedAt.IsZero() {
		startedAtUnix = record.StartedAt.Unix()
	}
	finishedAtUnix := int64(0)
	if !record.FinishedAt.IsZero() {
		finishedAtUnix = record.FinishedAt.Unix()
	}
	createdAtUnix := int64(0)
	if !record.CreatedAt.IsZero() {
		createdAtUnix = record.CreatedAt.Unix()
	}
	updatedAtUnix := int64(0)
	if !record.UpdatedAt.IsZero() {
		updatedAtUnix = record.UpdatedAt.Unix()
	}
	return map[string]any{
		"id":                 record.ID,
		"workspace_id":       record.WorkspaceID,
		"context_id":         record.ContextID,
		"kind":               record.Kind,
		"title":              record.Title,
		"prompt":             record.Prompt,
		"status":             record.Status,
		"route_class":        record.RouteClass,
		"priority":           record.Priority,
		"due_at_unix":        dueAtUnix,
		"assigned_lane":      record.AssignedLane,
		"source_connector":   record.SourceConnector,
		"source_external_id": record.SourceExternalID,
		"source_user_id":     record.SourceUserID,
		"source_text":        record.SourceText,
		"attempts":           record.Attempts,
		"worker_id":          record.WorkerID,
		"started_at_unix":    startedAtUnix,
		"finished_at_unix":   finishedAtUnix,
		"result_summary":     record.ResultSummary,
		"result_path":        record.ResultPath,
		"error_message":      record.ErrorMessage,
		"created_at_unix":    createdAtUnix,
		"updated_at_unix":    updatedAtUnix,
	}
}
