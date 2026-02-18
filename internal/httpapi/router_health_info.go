package httpapi

import "net/http"

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
	payload := map[string]any{
		"name":        "agent-runtime",
		"environment": r.deps.Config.Environment,
		"public_host": r.deps.Config.PublicHost,
		"admin_host":  r.deps.Config.AdminHost,
	}
	if r.deps.MCPStatusProvider != nil {
		payload["mcp"] = r.deps.MCPStatusProvider.Summary()
	}
	writeJSON(w, http.StatusOK, payload)
}
