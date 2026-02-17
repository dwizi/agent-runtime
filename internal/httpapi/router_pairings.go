package httpapi

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/dwizi/agent-runtime/internal/store"
)

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
