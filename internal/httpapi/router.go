package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/carlos/spinner/internal/config"
	"github.com/carlos/spinner/internal/heartbeat"
	"github.com/carlos/spinner/internal/orchestrator"
	"github.com/carlos/spinner/internal/store"
)

type Dependencies struct {
	Config              config.Config
	Store               *store.Store
	Engine              *orchestrator.Engine
	Logger              *slog.Logger
	Heartbeat           *heartbeat.Registry
	HeartbeatStaleAfter time.Duration
}

type router struct {
	deps Dependencies
}

func NewRouter(deps Dependencies) http.Handler {
	rt := &router{deps: deps}
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", rt.handleHealth)
	mux.HandleFunc("/readyz", rt.handleReady)
	mux.HandleFunc("/api/v1/heartbeat", rt.handleHeartbeat)
	mux.HandleFunc("/api/v1/info", rt.handleInfo)
	mux.HandleFunc("/api/v1/tasks", rt.handleTasks)
	mux.HandleFunc("/api/v1/tasks/retry", rt.handleTaskRetry)
	mux.HandleFunc("/api/v1/pairings/start", rt.handlePairingsStart)
	mux.HandleFunc("/api/v1/pairings/lookup", rt.handlePairingsLookup)
	mux.HandleFunc("/api/v1/pairings/approve", rt.handlePairingsApprove)
	mux.HandleFunc("/api/v1/pairings/deny", rt.handlePairingsDeny)
	mux.HandleFunc("/api/v1/objectives", rt.handleObjectives)
	mux.HandleFunc("/api/v1/objectives/update", rt.handleObjectivesUpdate)
	mux.HandleFunc("/api/v1/objectives/active", rt.handleObjectivesActive)
	mux.HandleFunc("/api/v1/objectives/delete", rt.handleObjectivesDelete)
	return mux
}

func (r *router) handleHealth(w http.ResponseWriter, req *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (r *router) handleReady(w http.ResponseWriter, req *http.Request) {
	if err := r.deps.Store.Ping(req.Context()); err != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"status": "not-ready", "error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ready"})
}

func (r *router) handleHeartbeat(w http.ResponseWriter, req *http.Request) {
	if r.deps.Heartbeat == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{
			"status": "unavailable",
			"error":  "heartbeat is disabled",
		})
		return
	}
	snapshot := r.deps.Heartbeat.Snapshot(r.deps.HeartbeatStaleAfter)
	writeJSON(w, http.StatusOK, snapshot)
}

func (r *router) handleInfo(w http.ResponseWriter, req *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"name":        "spinner",
		"environment": r.deps.Config.Environment,
		"public_host": r.deps.Config.PublicHost,
		"admin_host":  r.deps.Config.AdminHost,
	})
}

type taskRequest struct {
	WorkspaceID string `json:"workspace_id"`
	ContextID   string `json:"context_id"`
	Title       string `json:"title"`
	Prompt      string `json:"prompt"`
	Kind        string `json:"kind"`
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
	task, err := r.enqueueAndPersistTask(req.Context(), payload.WorkspaceID, payload.ContextID, payload.Title, payload.Prompt, kind)
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
	task, err := r.enqueueAndPersistTask(req.Context(), original.WorkspaceID, original.ContextID, original.Title, original.Prompt, kind)
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

func (r *router) enqueueAndPersistTask(ctx context.Context, workspaceID, contextID, title, prompt string, kind orchestrator.TaskKind) (orchestrator.Task, error) {
	task, err := r.deps.Engine.Enqueue(orchestrator.Task{
		WorkspaceID: strings.TrimSpace(workspaceID),
		ContextID:   strings.TrimSpace(contextID),
		Title:       strings.TrimSpace(title),
		Prompt:      strings.TrimSpace(prompt),
		Kind:        kind,
	})
	if err != nil {
		return orchestrator.Task{}, err
	}

	storeCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	if err := r.deps.Store.CreateTask(storeCtx, store.CreateTaskInput{
		ID:          task.ID,
		WorkspaceID: task.WorkspaceID,
		ContextID:   task.ContextID,
		Kind:        string(task.Kind),
		Title:       task.Title,
		Prompt:      task.Prompt,
		Status:      "queued",
	}); err != nil {
		r.deps.Logger.Error("failed to persist task", "error", err, "task_id", task.ID)
		return orchestrator.Task{}, err
	}
	return task, nil
}

func taskRecordResponse(record store.TaskRecord) map[string]any {
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
		"id":               record.ID,
		"workspace_id":     record.WorkspaceID,
		"context_id":       record.ContextID,
		"kind":             record.Kind,
		"title":            record.Title,
		"prompt":           record.Prompt,
		"status":           record.Status,
		"attempts":         record.Attempts,
		"worker_id":        record.WorkerID,
		"started_at_unix":  startedAtUnix,
		"finished_at_unix": finishedAtUnix,
		"result_summary":   record.ResultSummary,
		"result_path":      record.ResultPath,
		"error_message":    record.ErrorMessage,
		"created_at_unix":  createdAtUnix,
		"updated_at_unix":  updatedAtUnix,
	}
}

type startPairingRequest struct {
	Connector       string `json:"connector"`
	ConnectorUserID string `json:"connector_user_id"`
	DisplayName     string `json:"display_name"`
	ExpiresInSec    int    `json:"expires_in_sec"`
}

func (r *router) handlePairingsStart(w http.ResponseWriter, req *http.Request) {
	if req.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}

	var payload startPairingRequest
	if err := json.NewDecoder(req.Body).Decode(&payload); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid payload"})
		return
	}
	if strings.TrimSpace(payload.Connector) == "" || strings.TrimSpace(payload.ConnectorUserID) == "" || strings.TrimSpace(payload.DisplayName) == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "connector, connector_user_id and display_name are required"})
		return
	}

	expiresIn := payload.ExpiresInSec
	if expiresIn <= 0 {
		expiresIn = 600
	}
	pairing, err := r.deps.Store.CreatePairingRequest(req.Context(), store.CreatePairingRequestInput{
		Connector:       payload.Connector,
		ConnectorUserID: payload.ConnectorUserID,
		DisplayName:     payload.DisplayName,
		ExpiresAt:       time.Now().UTC().Add(time.Duration(expiresIn) * time.Second),
	})
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}

	writeJSON(w, http.StatusCreated, map[string]any{
		"id":                pairing.ID,
		"token":             pairing.Token,
		"token_hint":        pairing.TokenHint,
		"connector":         pairing.Connector,
		"connector_user_id": pairing.ConnectorUserID,
		"display_name":      pairing.DisplayName,
		"status":            pairing.Status,
		"expires_at_unix":   pairing.ExpiresAt.Unix(),
	})
}

func (r *router) handlePairingsLookup(w http.ResponseWriter, req *http.Request) {
	if req.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	token := strings.TrimSpace(req.URL.Query().Get("token"))
	if token == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "token query parameter is required"})
		return
	}

	pairing, err := r.deps.Store.LookupPairingByToken(req.Context(), token)
	if err != nil {
		status := http.StatusBadRequest
		if errors.Is(err, store.ErrPairingNotFound) {
			status = http.StatusNotFound
		}
		writeJSON(w, status, map[string]string{"error": err.Error()})
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"id":                pairing.ID,
		"token_hint":        pairing.TokenHint,
		"connector":         pairing.Connector,
		"connector_user_id": pairing.ConnectorUserID,
		"display_name":      pairing.DisplayName,
		"status":            pairing.Status,
		"expires_at_unix":   pairing.ExpiresAt.Unix(),
		"approved_user_id":  pairing.ApprovedUserID,
		"approver_user_id":  pairing.ApproverUserID,
		"denied_reason":     pairing.DeniedReason,
	})
}

type approvePairingRequest struct {
	Token          string `json:"token"`
	ApproverUserID string `json:"approver_user_id"`
	Role           string `json:"role"`
	TargetUserID   string `json:"target_user_id"`
}

func (r *router) handlePairingsApprove(w http.ResponseWriter, req *http.Request) {
	if req.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}

	var payload approvePairingRequest
	if err := json.NewDecoder(req.Body).Decode(&payload); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid payload"})
		return
	}

	result, err := r.deps.Store.ApprovePairing(req.Context(), store.ApprovePairingInput{
		Token:          payload.Token,
		ApproverUserID: payload.ApproverUserID,
		Role:           payload.Role,
		TargetUserID:   payload.TargetUserID,
	})
	if err != nil {
		status := http.StatusBadRequest
		if errors.Is(err, store.ErrPairingNotFound) {
			status = http.StatusNotFound
		}
		writeJSON(w, status, map[string]string{"error": err.Error()})
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"id":                result.PairingRequest.ID,
		"status":            result.PairingRequest.Status,
		"approved_user_id":  result.UserID,
		"approver_user_id":  result.PairingRequest.ApproverUserID,
		"identity_id":       result.IdentityID,
		"connector":         result.PairingRequest.Connector,
		"connector_user_id": result.PairingRequest.ConnectorUserID,
	})
}

type denyPairingRequest struct {
	Token          string `json:"token"`
	ApproverUserID string `json:"approver_user_id"`
	Reason         string `json:"reason"`
}

func (r *router) handlePairingsDeny(w http.ResponseWriter, req *http.Request) {
	if req.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}

	var payload denyPairingRequest
	if err := json.NewDecoder(req.Body).Decode(&payload); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid payload"})
		return
	}

	result, err := r.deps.Store.DenyPairing(req.Context(), store.DenyPairingInput{
		Token:          payload.Token,
		ApproverUserID: payload.ApproverUserID,
		Reason:         payload.Reason,
	})
	if err != nil {
		status := http.StatusBadRequest
		if errors.Is(err, store.ErrPairingNotFound) {
			status = http.StatusNotFound
		}
		writeJSON(w, status, map[string]string{"error": err.Error()})
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"id":               result.ID,
		"status":           result.Status,
		"approver_user_id": result.ApproverUserID,
		"denied_reason":    result.DeniedReason,
	})
}

type objectiveRequest struct {
	WorkspaceID     string `json:"workspace_id"`
	ContextID       string `json:"context_id"`
	Title           string `json:"title"`
	Prompt          string `json:"prompt"`
	TriggerType     string `json:"trigger_type"`
	EventKey        string `json:"event_key"`
	IntervalSeconds int    `json:"interval_seconds"`
	NextRunUnix     int64  `json:"next_run_unix"`
	Active          *bool  `json:"active"`
}

type objectiveUpdateRequest struct {
	ID              string  `json:"id"`
	Title           *string `json:"title"`
	Prompt          *string `json:"prompt"`
	TriggerType     *string `json:"trigger_type"`
	EventKey        *string `json:"event_key"`
	IntervalSeconds *int    `json:"interval_seconds"`
	NextRunUnix     *int64  `json:"next_run_unix"`
	Active          *bool   `json:"active"`
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
	active := true
	if payload.Active != nil {
		active = *payload.Active
	}
	objective, err := r.deps.Store.CreateObjective(req.Context(), store.CreateObjectiveInput{
		WorkspaceID:     strings.TrimSpace(payload.WorkspaceID),
		ContextID:       strings.TrimSpace(payload.ContextID),
		Title:           strings.TrimSpace(payload.Title),
		Prompt:          strings.TrimSpace(payload.Prompt),
		TriggerType:     triggerType,
		EventKey:        strings.TrimSpace(payload.EventKey),
		IntervalSeconds: payload.IntervalSeconds,
		NextRunAt:       nextRun,
		Active:          active,
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
		ID:              strings.TrimSpace(payload.ID),
		Title:           payload.Title,
		Prompt:          payload.Prompt,
		EventKey:        payload.EventKey,
		IntervalSeconds: payload.IntervalSeconds,
		Active:          payload.Active,
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
	return map[string]any{
		"id":               item.ID,
		"workspace_id":     item.WorkspaceID,
		"context_id":       item.ContextID,
		"title":            item.Title,
		"prompt":           item.Prompt,
		"trigger_type":     item.TriggerType,
		"event_key":        item.EventKey,
		"interval_seconds": item.IntervalSeconds,
		"active":           item.Active,
		"next_run_unix":    item.NextRunAt.Unix(),
		"last_run_unix":    item.LastRunAt.Unix(),
		"last_error":       item.LastError,
	}
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}
