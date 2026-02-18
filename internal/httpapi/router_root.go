package httpapi

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"github.com/dwizi/agent-runtime/internal/config"
	"github.com/dwizi/agent-runtime/internal/gateway"
	"github.com/dwizi/agent-runtime/internal/heartbeat"
	"github.com/dwizi/agent-runtime/internal/mcp"
	"github.com/dwizi/agent-runtime/internal/orchestrator"
	"github.com/dwizi/agent-runtime/internal/store"
)

type MessageGateway interface {
	HandleMessage(ctx context.Context, input gateway.MessageInput) (gateway.MessageOutput, error)
}

type MCPStatusProvider interface {
	Summary() mcp.Summary
}

type Dependencies struct {
	Config              config.Config
	Store               *store.Store
	Engine              *orchestrator.Engine
	Gateway             MessageGateway
	MCPStatusProvider   MCPStatusProvider
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
	mux.HandleFunc("/api/v1/chat", rt.handleChat)
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
