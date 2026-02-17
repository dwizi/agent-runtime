package httpapi

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/dwizi/agent-runtime/internal/gateway"
	"github.com/dwizi/agent-runtime/internal/memorylog"
)

type chatRequest struct {
	Connector   string `json:"connector"`
	ExternalID  string `json:"external_id"`
	DisplayName string `json:"display_name"`
	FromUserID  string `json:"from_user_id"`
	Text        string `json:"text"`
}

func (r *router) handleChat(w http.ResponseWriter, req *http.Request) {
	if req.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	if r.deps.Gateway == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "chat gateway is unavailable"})
		return
	}

	var payload chatRequest
	if err := json.NewDecoder(req.Body).Decode(&payload); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid payload"})
		return
	}

	text := strings.TrimSpace(payload.Text)
	if text == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "text is required"})
		return
	}

	connector := strings.ToLower(strings.TrimSpace(payload.Connector))
	if connector == "" {
		connector = "codex"
	}
	externalID := strings.TrimSpace(payload.ExternalID)
	if externalID == "" {
		externalID = strings.TrimSpace(payload.FromUserID)
	}
	if externalID == "" {
		externalID = "codex-cli"
	}
	fromUserID := strings.TrimSpace(payload.FromUserID)
	if fromUserID == "" {
		fromUserID = externalID
	}
	displayName := strings.TrimSpace(payload.DisplayName)
	if displayName == "" {
		displayName = externalID
	}

	workspaceID := ""
	if r.deps.Store != nil {
		contextRecord, err := r.deps.Store.EnsureContextForExternalChannel(req.Context(), connector, externalID, displayName)
		if err != nil {
			if r.deps.Logger != nil {
				r.deps.Logger.Warn("failed to ensure chat context for api chat", "error", err, "connector", connector, "external_id", externalID)
			}
		} else {
			workspaceID = strings.TrimSpace(contextRecord.WorkspaceID)
		}
	}
	r.appendChatLogEntry(workspaceID, connector, externalID, "inbound", fromUserID, displayName, text)

	output, err := r.deps.Gateway.HandleMessage(req.Context(), gateway.MessageInput{
		Connector:   connector,
		ExternalID:  externalID,
		DisplayName: displayName,
		FromUserID:  fromUserID,
		Text:        text,
	})
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	reply := strings.TrimSpace(output.Reply)
	if reply != "" {
		r.appendChatLogEntry(workspaceID, connector, externalID, "outbound", "agent-runtime", displayName, reply)
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"handled": output.Handled,
		"reply":   reply,
	})
}

func (r *router) appendChatLogEntry(workspaceID, connector, externalID, direction, actorID, displayName, text string) {
	workspaceRoot := strings.TrimSpace(r.deps.Config.WorkspaceRoot)
	workspaceID = strings.TrimSpace(workspaceID)
	if workspaceRoot == "" || workspaceID == "" {
		return
	}
	if err := memorylog.Append(memorylog.Entry{
		WorkspaceRoot: workspaceRoot,
		WorkspaceID:   workspaceID,
		Connector:     connector,
		ExternalID:    externalID,
		Direction:     direction,
		ActorID:       actorID,
		DisplayName:   displayName,
		Text:          text,
		Timestamp:     time.Now().UTC(),
	}); err != nil && r.deps.Logger != nil {
		r.deps.Logger.Warn("failed to append api chat log", "error", err, "connector", connector, "external_id", externalID)
	}
}
