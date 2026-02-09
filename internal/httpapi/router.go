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
	"github.com/carlos/spinner/internal/orchestrator"
	"github.com/carlos/spinner/internal/store"
)

type Dependencies struct {
	Config config.Config
	Store  *store.Store
	Engine *orchestrator.Engine
	Logger *slog.Logger
}

type router struct {
	deps Dependencies
}

func NewRouter(deps Dependencies) http.Handler {
	rt := &router{deps: deps}
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", rt.handleHealth)
	mux.HandleFunc("/readyz", rt.handleReady)
	mux.HandleFunc("/api/v1/info", rt.handleInfo)
	mux.HandleFunc("/api/v1/tasks", rt.handleTasks)
	mux.HandleFunc("/api/v1/pairings/start", rt.handlePairingsStart)
	mux.HandleFunc("/api/v1/pairings/lookup", rt.handlePairingsLookup)
	mux.HandleFunc("/api/v1/pairings/approve", rt.handlePairingsApprove)
	mux.HandleFunc("/api/v1/pairings/deny", rt.handlePairingsDeny)
	mux.HandleFunc("/api/v1/objectives", rt.handleObjectives)
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
	if req.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}

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

	task, err := r.deps.Engine.Enqueue(orchestrator.Task{
		WorkspaceID: payload.WorkspaceID,
		ContextID:   payload.ContextID,
		Title:       payload.Title,
		Prompt:      payload.Prompt,
		Kind:        kind,
	})
	if err != nil {
		status := http.StatusInternalServerError
		if errors.Is(err, orchestrator.ErrQueueFull) {
			status = http.StatusTooManyRequests
		}
		writeJSON(w, status, map[string]string{"error": err.Error()})
		return
	}

	storeCtx, cancel := context.WithTimeout(req.Context(), 3*time.Second)
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
	}

	writeJSON(w, http.StatusAccepted, map[string]any{
		"id":           task.ID,
		"workspace_id": task.WorkspaceID,
		"context_id":   task.ContextID,
		"kind":         task.Kind,
		"status":       "queued",
	})
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
