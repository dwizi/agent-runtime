package app

import (
	"log/slog"
	"net/http"

	"github.com/dwizi/agent-runtime/internal/config"
	"github.com/dwizi/agent-runtime/internal/connectors"
	"github.com/dwizi/agent-runtime/internal/heartbeat"
	"github.com/dwizi/agent-runtime/internal/mcp"
	"github.com/dwizi/agent-runtime/internal/orchestrator"
	"github.com/dwizi/agent-runtime/internal/qmd"
	"github.com/dwizi/agent-runtime/internal/scheduler"
	"github.com/dwizi/agent-runtime/internal/store"
	"github.com/dwizi/agent-runtime/internal/watcher"
)

type Runtime struct {
	cfg              config.Config
	logger           *slog.Logger
	store            *store.Store
	engine           *orchestrator.Engine
	httpServer       *http.Server
	watcher          *watcher.Service
	scheduler        *scheduler.Service
	qmd              *qmd.Service
	connectors       []connectors.Connector
	mcp              *mcp.Manager
	heartbeat        *heartbeat.Registry
	heartbeatMonitor *heartbeat.Monitor
}

type heartbeatAware interface {
	SetHeartbeatReporter(reporter heartbeat.Reporter)
}
