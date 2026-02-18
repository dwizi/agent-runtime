package app

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/dwizi/agent-runtime/internal/actions/executor"
	"github.com/dwizi/agent-runtime/internal/actions/plugins/externalcmd"
	"github.com/dwizi/agent-runtime/internal/actions/plugins/sandbox"
	"github.com/dwizi/agent-runtime/internal/actions/plugins/smtp"
	"github.com/dwizi/agent-runtime/internal/actions/plugins/webhook"
	"github.com/dwizi/agent-runtime/internal/config"
	"github.com/dwizi/agent-runtime/internal/connectors"
	"github.com/dwizi/agent-runtime/internal/connectors/discord"
	"github.com/dwizi/agent-runtime/internal/connectors/imap"
	"github.com/dwizi/agent-runtime/internal/connectors/telegram"
	"github.com/dwizi/agent-runtime/internal/extplugins"
	"github.com/dwizi/agent-runtime/internal/gateway"
	"github.com/dwizi/agent-runtime/internal/heartbeat"
	"github.com/dwizi/agent-runtime/internal/httpapi"
	"github.com/dwizi/agent-runtime/internal/llm"
	"github.com/dwizi/agent-runtime/internal/llm/anthropic"
	"github.com/dwizi/agent-runtime/internal/llm/grounded"
	"github.com/dwizi/agent-runtime/internal/llm/openai"
	"github.com/dwizi/agent-runtime/internal/llm/promptpolicy"
	"github.com/dwizi/agent-runtime/internal/llm/safety"
	"github.com/dwizi/agent-runtime/internal/mcp"
	"github.com/dwizi/agent-runtime/internal/orchestrator"
	"github.com/dwizi/agent-runtime/internal/qmd"
	"github.com/dwizi/agent-runtime/internal/scheduler"
	"github.com/dwizi/agent-runtime/internal/store"
	"github.com/dwizi/agent-runtime/internal/watcher"
)

func New(cfg config.Config, logger *slog.Logger) (*Runtime, error) {
	if err := os.MkdirAll(filepath.Dir(cfg.DBPath), 0o755); err != nil {
		return nil, fmt.Errorf("create db directory: %w", err)
	}
	if err := os.MkdirAll(cfg.WorkspaceRoot, 0o755); err != nil {
		return nil, fmt.Errorf("create workspace root: %w", err)
	}
	if err := os.MkdirAll(cfg.ExtPluginCacheDir, 0o755); err != nil {
		return nil, fmt.Errorf("create external plugin cache dir: %w", err)
	}

	sqlStore, err := store.New(cfg.DBPath)
	if err != nil {
		return nil, err
	}
	if err := sqlStore.AutoMigrate(context.Background()); err != nil {
		sqlStore.Close()
		return nil, err
	}

	engine := orchestrator.New(cfg.DefaultConcurrency, logger.With("component", "orchestrator"))
	var heartbeatRegistry *heartbeat.Registry
	if cfg.HeartbeatEnabled {
		heartbeatRegistry = heartbeat.NewRegistry()
		heartbeatRegistry.Starting("runtime", "booting")
		heartbeatRegistry.Starting("orchestrator", "initializing")
		heartbeatRegistry.Starting("scheduler", "initializing")
		heartbeatRegistry.Starting("watcher", "initializing")
		heartbeatRegistry.Starting("api", "initializing")
		heartbeatRegistry.Starting("qmd", "initializing")
	}
	qmdService := qmd.New(qmd.Config{
		WorkspaceRoot:   cfg.WorkspaceRoot,
		Binary:          cfg.QMDBinary,
		SidecarURL:      cfg.QMDSidecarURL,
		IndexName:       cfg.QMDIndexName,
		Collection:      cfg.QMDCollectionName,
		SharedModelsDir: cfg.QMDSharedModelsDir,
		EmbedExclude:    parseCSVTrimList(cfg.QMDEmbedExcludeGlobsCSV),
		SearchLimit:     cfg.QMDSearchLimit,
		OpenMaxBytes:    cfg.QMDOpenMaxBytes,
		Debounce:        time.Duration(cfg.QMDDebounceSeconds) * time.Second,
		IndexTimeout:    time.Duration(cfg.QMDIndexTimeoutSec) * time.Second,
		QueryTimeout:    time.Duration(cfg.QMDQueryTimeoutSec) * time.Second,
		AutoEmbed:       cfg.QMDAutoEmbed,
	}, logger.With("component", "qmd"))
	if heartbeatRegistry != nil {
		heartbeatRegistry.Beat("qmd", "qmd service initialized")
	}

	actionPlugins := []executor.Plugin{
		webhook.New(15 * time.Second),
		smtp.New(smtp.Config{
			Host:     cfg.SMTPHost,
			Port:     cfg.SMTPPort,
			Username: cfg.SMTPUsername,
			Password: cfg.SMTPPassword,
			From:     cfg.SMTPFrom,
		}),
	}
	if cfg.SandboxEnabled {
		actionPlugins = append(actionPlugins, sandbox.New(sandbox.Config{
			Enabled:         true,
			WorkspaceRoot:   cfg.WorkspaceRoot,
			AllowedCommands: parseCSVList(cfg.SandboxAllowedCommandsCSV),
			RunnerCommand:   cfg.SandboxRunnerCommand,
			RunnerArgs:      parseShellArgs(cfg.SandboxRunnerArgs),
			Timeout:         time.Duration(cfg.SandboxTimeoutSec) * time.Second,
			MaxOutputBytes:  cfg.SandboxMaxOutputBytes,
		}))
	}

	externalPluginConfig, err := extplugins.LoadConfig(cfg.ExtPluginsConfigPath)
	if err != nil {
		return nil, fmt.Errorf("load external plugins config: %w", err)
	}
	resolvedExternalPlugins, err := extplugins.ResolveExternalPlugins(cfg.ExtPluginsConfigPath, externalPluginConfig.ExternalPlugins)
	if err != nil {
		return nil, fmt.Errorf("resolve external plugins: %w", err)
	}
	for _, resolved := range resolvedExternalPlugins {
		timeout := time.Duration(resolved.Manifest.Runtime.TimeoutSeconds) * time.Second
		uvRuntime, uvErr := extplugins.BuildUVRuntime(
			resolved.ID,
			resolved.BaseDir,
			cfg.ExtPluginCacheDir,
			resolved.Manifest.Runtime.Isolation,
			cfg.ExtPluginWarmOnBootstrap,
		)
		if uvErr != nil {
			return nil, fmt.Errorf("configure external plugin %s uv runtime: %w", resolved.ID, uvErr)
		}
		var uvConfig *externalcmd.UVConfig
		if uvRuntime.Enabled {
			uvConfig = &externalcmd.UVConfig{
				ProjectDir:      uvRuntime.ProjectDir,
				CacheDir:        uvRuntime.CacheDir,
				VenvDir:         uvRuntime.VenvDir,
				WarmOnBootstrap: uvRuntime.WarmOnBootstrap,
				Locked:          uvRuntime.Locked,
			}
		}
		plugin, createErr := externalcmd.New(externalcmd.Config{
			ID:            resolved.ID,
			PluginKey:     resolved.Manifest.PluginKey,
			BaseDir:       resolved.BaseDir,
			Command:       resolved.Manifest.Runtime.Command,
			Args:          resolved.Manifest.Runtime.Args,
			Env:           resolved.Manifest.Runtime.Env,
			ActionTypes:   resolved.Manifest.ActionTypes,
			Timeout:       timeout,
			RunnerCommand: cfg.SandboxRunnerCommand,
			RunnerArgs:    parseShellArgs(cfg.SandboxRunnerArgs),
			UV:            uvConfig,
		})
		if createErr != nil {
			return nil, fmt.Errorf("configure external plugin %s: %w", resolved.ID, createErr)
		}
		if cfg.ExtPluginWarmOnBootstrap {
			if warmErr := plugin.Warmup(context.Background()); warmErr != nil {
				logger.Warn(
					"external plugin warmup failed",
					"plugin_id", resolved.ID,
					"error", warmErr,
				)
			}
		}
		actionPlugins = append(actionPlugins, plugin)
		logger.Info(
			"external executable plugin enabled",
			"plugin_id", resolved.ID,
			"manifest", resolved.ManifestPath,
			"action_types", strings.Join(resolved.Manifest.ActionTypes, ","),
			"isolation_mode", resolved.Manifest.Runtime.Isolation.Mode,
		)
	}

	actionExecutor := executor.NewRegistry(actionPlugins...)
	commandGateway := gateway.New(sqlStore, engine, qmdService, actionExecutor, cfg.WorkspaceRoot, logger.With("component", "gateway"))
	commandGateway.SetTriageEnabled(cfg.TriageEnabled)
	if cfg.AgentMaxTurnDurationSec > 0 {
		commandGateway.SetAgentMaxTurnDuration(time.Duration(cfg.AgentMaxTurnDurationSec) * time.Second)
	}
	commandGateway.SetAgentGroundingPolicy(cfg.AgentGroundingFirstStep, cfg.AgentGroundingEveryStep)
	commandGateway.SetSensitiveApprovalTTL(time.Duration(cfg.AgentSensitiveApprovalTTLSeconds) * time.Second)

	mcpManager, err := mcp.NewManager(mcp.ManagerConfig{
		ConfigPath:             cfg.MCPConfigPath,
		WorkspaceRoot:          cfg.WorkspaceRoot,
		WorkspaceConfigRelPath: cfg.MCPWorkspaceConfigRelPath,
		DefaultRefreshSeconds:  cfg.MCPRefreshSeconds,
		DefaultHTTPTimeoutSec:  cfg.MCPHTTPTimeoutSec,
	}, logger.With("component", "mcp"))
	if err != nil {
		return nil, fmt.Errorf("configure mcp manager: %w", err)
	}
	commandGateway.SetMCPRuntime(mcpManager)
	mcpManager.SetToolUpdateHandler(func(update mcp.ToolUpdate) {
		namespace := "mcp:" + strings.ToLower(strings.TrimSpace(update.ServerID))
		dynamicTools := gateway.BuildMCPDynamicTools(func() gateway.MCPRuntime { return mcpManager }, update.Tools)
		commandGateway.Registry().ReplaceNamespace(namespace, dynamicTools)
	})
	mcpManager.Bootstrap(context.Background())
	mcpSummary := mcpManager.Summary()
	logger.Info(
		"mcp bootstrap complete",
		"enabled_servers", mcpSummary.EnabledServers,
		"healthy_servers", mcpSummary.HealthyServers,
		"degraded_servers", mcpSummary.DegradedServers,
	)

	// Load Reasoning Prompt
	if cfg.ReasoningPromptFile != "" {
		promptBytes, err := os.ReadFile(cfg.ReasoningPromptFile)
		if err == nil {
			commandGateway.SetReasoningPromptTemplate(string(promptBytes))
		} else if !os.IsNotExist(err) {
			logger.Warn("failed to read reasoning prompt file", "path", cfg.ReasoningPromptFile, "error", err)
		} else {
			// If relative path fails, try relative to workspace root?
			// For now, assume absolute or CWD relative.
			// In docker, /context/REASONING.md is absolute.
			// Locally, it might be relative.
		}
	}

	var responder llm.Responder
	switch strings.ToLower(cfg.LLMProvider) {
	case "anthropic", "claude":
		responder = anthropic.New(anthropic.Config{
			APIKey:  cfg.LLMAPIKey,
			BaseURL: cfg.LLMBaseURL,
			Model:   cfg.LLMModel,
			Timeout: time.Duration(cfg.LLMTimeoutSec) * time.Second,
		}, logger.With("component", "llm-anthropic"))
	case "openai", "z.ai", "local":
		// Default to OpenAI adapter for z.ai and local as well
		responder = openai.New(openai.Config{
			APIKey:  cfg.LLMAPIKey,
			BaseURL: cfg.LLMBaseURL,
			Model:   cfg.LLMModel,
			Timeout: time.Duration(cfg.LLMTimeoutSec) * time.Second,
		}, logger.With("component", "llm-openai"))
	default:
		// Fallback to OpenAI
		responder = openai.New(openai.Config{
			APIKey:  cfg.LLMAPIKey,
			BaseURL: cfg.LLMBaseURL,
			Model:   cfg.LLMModel,
			Timeout: time.Duration(cfg.LLMTimeoutSec) * time.Second,
		}, logger.With("component", "llm-openai"))
	}

	policyResponder := promptpolicy.New(responder, sqlStore, promptpolicy.Config{
		WorkspaceRoot:        cfg.WorkspaceRoot,
		AdminSystemPrompt:    cfg.LLMAdminSystemPrompt,
		PublicSystemPrompt:   cfg.LLMPublicSystemPrompt,
		GlobalSoulPath:       cfg.SoulGlobalFile,
		WorkspaceSoulRelPath: cfg.SoulWorkspaceRelPath,
		ContextSoulRelPath:   cfg.SoulContextRelPath,
		GlobalSystemPrompt:   cfg.SystemPromptGlobalFile,
		WorkspacePromptPath:  cfg.SystemPromptWorkspacePath,
		ContextPromptPath:    cfg.SystemPromptContextPath,
		GlobalSkillsRoot:     cfg.SkillsGlobalRoot,
		MaxSkills:            5,
		MaxSkillBytes:        1400,
		MaxSystemPromptBytes: 12000,
	})
	groundedResponder := grounded.New(policyResponder, qmdService, grounded.Config{
		WorkspaceRoot:               cfg.WorkspaceRoot,
		TopK:                        cfg.LLMGroundingTopK,
		MaxDocExcerpt:               cfg.LLMGroundingMaxDocExcerpt,
		MaxPromptBytes:              cfg.LLMGroundingMaxPromptBytes,
		MaxPromptTokens:             cfg.LLMGroundingMaxPromptTokens,
		UserPromptMaxTokens:         cfg.LLMGroundingUserMaxTokens,
		MemorySummaryMaxTokens:      cfg.LLMGroundingSummaryMaxTokens,
		ChatTailMaxTokens:           cfg.LLMGroundingChatTailMaxTokens,
		QMDContextMaxTokens:         cfg.LLMGroundingQMDMaxTokens,
		ChatTailLines:               cfg.LLMGroundingChatTailLines,
		ChatTailBytes:               cfg.LLMGroundingChatTailBytes,
		MemorySummaryRefreshTurns:   cfg.LLMGroundingSummaryRefreshTurns,
		MemorySummaryMaxItems:       cfg.LLMGroundingSummaryMaxItems,
		MemorySummarySourceMaxLines: cfg.LLMGroundingSummarySourceMaxLines,
	}, logger.With("component", "llm-grounding"))
	commandGateway.SetTriageAcknowledger(groundedResponder)
	llmPolicy := safety.New(safety.Config{
		Enabled:                cfg.LLMEnabled,
		AllowedRoles:           parseCSVSet(cfg.LLMAllowedRolesCSV),
		AllowedContextIDs:      parseCSVSet(cfg.LLMAllowedContextIDsCSV),
		AllowDM:                cfg.LLMAllowDM,
		RequireMentionInGroups: cfg.LLMRequireMentionInGroups,
		RateLimitPerWindow:     cfg.LLMRateLimitPerWindow,
		RateLimitWindow:        time.Duration(cfg.LLMRateLimitWindowSec) * time.Second,
	})
	schedulerService := scheduler.New(sqlStore, engine, time.Duration(cfg.ObjectivePollSec)*time.Second, logger.With("component", "scheduler"))
	engine.SetExecutor(newTaskWorkerExecutor(cfg.WorkspaceRoot, sqlStore, groundedResponder, qmdService, actionExecutor, commandGateway.Registry(), cfg, logger.With("component", "task-executor")))
	if heartbeatRegistry != nil {
		schedulerService.SetHeartbeatReporter(heartbeatRegistry)
	}
	var reindexMu sync.Mutex
	reindexLastQueued := map[string]time.Time{}
	const reindexTaskDebounce = 2 * time.Second

	watchService, err := watcher.New(
		[]string{cfg.WorkspaceRoot},
		logger.With("component", "watcher"),
		func(ctx context.Context, path string) {
			workspaceID := workspaceIDFromPath(cfg.WorkspaceRoot, path)
			if workspaceID != "" {
				if shouldTriggerObjectiveEventForPath(cfg.WorkspaceRoot, path) {
					schedulerService.HandleMarkdownUpdate(ctx, workspaceID, path)
				} else {
					logger.Debug("skipping objective event trigger for ignored markdown path", "workspace_id", workspaceID, "path", path)
				}
				if shouldQueueQMDForPath(cfg.WorkspaceRoot, path) {
					qmdService.QueueWorkspaceIndexForPath(workspaceID, path)
				} else {
					logger.Debug("skipping qmd index queue for ignored markdown path", "workspace_id", workspaceID, "path", path)
				}
			}
			if workspaceID == "" {
				return
			}
			if !shouldQueueQMDForPath(cfg.WorkspaceRoot, path) {
				return
			}
			reindexMu.Lock()
			defer reindexMu.Unlock()
			now := time.Now().UTC()
			if last, ok := reindexLastQueued[workspaceID]; ok && now.Sub(last) < reindexTaskDebounce {
				logger.Debug("reindex task recently queued; skipping enqueue", "workspace_id", workspaceID, "path", path)
				return
			}

			pending, pendingErr := hasPendingReindexTask(ctx, sqlStore, workspaceID)
			if pendingErr != nil {
				logger.Error("failed to check pending reindex task", "workspace_id", workspaceID, "path", path, "error", pendingErr)
			} else if pending {
				logger.Debug("reindex task already pending; skipping enqueue", "workspace_id", workspaceID, "path", path)
				return
			}
			task, enqueueErr := engine.Enqueue(orchestrator.Task{
				WorkspaceID: workspaceID,
				ContextID:   "system:filewatcher",
				Title:       "Reindex markdown",
				Prompt:      "markdown file changed: " + path,
				Kind:        orchestrator.TaskKindReindex,
			})
			if enqueueErr != nil {
				logger.Error("failed to enqueue reindex task", "path", path, "error", enqueueErr)
				return
			}
			if persistErr := sqlStore.CreateTask(ctx, store.CreateTaskInput{
				ID:          task.ID,
				WorkspaceID: task.WorkspaceID,
				ContextID:   task.ContextID,
				Kind:        string(task.Kind),
				Title:       task.Title,
				Prompt:      task.Prompt,
				Status:      "queued",
			}); persistErr != nil {
				logger.Error("failed to persist reindex task", "path", path, "task_id", task.ID, "error", persistErr)
			}
			reindexLastQueued[workspaceID] = now
		},
	)
	if err != nil {
		sqlStore.Close()
		return nil, err
	}
	if heartbeatRegistry != nil {
		watchService.SetHeartbeatReporter(heartbeatRegistry)
	}

	handler := httpapi.NewRouter(httpapi.Dependencies{
		Config:              cfg,
		Store:               sqlStore,
		Engine:              engine,
		Gateway:             commandGateway,
		MCPStatusProvider:   mcpManager,
		Logger:              logger.With("component", "api"),
		Heartbeat:           heartbeatRegistry,
		HeartbeatStaleAfter: time.Duration(cfg.HeartbeatStaleSec) * time.Second,
	})
	httpServer := &http.Server{
		Addr:              cfg.HTTPAddr,
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
	}

	connectorList := []connectors.Connector{}
	if strings.TrimSpace(cfg.DiscordToken) != "" {
		connectorList = append(connectorList, discord.New(
			cfg.DiscordToken,
			cfg.DiscordAPI,
			cfg.DiscordWSURL,
			cfg.WorkspaceRoot,
			sqlStore,
			commandGateway,
			groundedResponder,
			llmPolicy,
			logger.With("connector", "discord"),
			discord.WithCommandSync(cfg.CommandSyncEnabled),
			discord.WithCommandGuildIDs(parseCSVTrimList(cfg.DiscordCommandGuildIDsCSV)),
			discord.WithApplicationID(cfg.DiscordApplicationID),
		))
	} else if heartbeatRegistry != nil {
		heartbeatRegistry.Disabled("connector:discord", "token missing")
	}
	if strings.TrimSpace(cfg.TelegramToken) != "" {
		connectorList = append(connectorList, telegram.New(
			cfg.TelegramToken,
			cfg.TelegramAPI,
			cfg.WorkspaceRoot,
			cfg.TelegramPoll,
			sqlStore,
			commandGateway,
			groundedResponder,
			llmPolicy,
			logger.With("connector", "telegram"),
			telegram.WithCommandSync(cfg.CommandSyncEnabled),
		))
	} else if heartbeatRegistry != nil {
		heartbeatRegistry.Disabled("connector:telegram", "token missing")
	}
	if strings.TrimSpace(cfg.IMAPHost) != "" && strings.TrimSpace(cfg.IMAPUsername) != "" && strings.TrimSpace(cfg.IMAPPassword) != "" {
		connectorList = append(connectorList, imap.New(cfg.IMAPHost, cfg.IMAPPort, cfg.IMAPUsername, cfg.IMAPPassword, cfg.IMAPMailbox, cfg.IMAPPollSeconds, cfg.WorkspaceRoot, cfg.IMAPTLSSkipVerify, sqlStore, engine, logger.With("connector", "imap")))
	} else if heartbeatRegistry != nil {
		heartbeatRegistry.Disabled("connector:imap", "credentials missing")
	}
	if heartbeatRegistry != nil {
		for _, connector := range connectorList {
			reportingConnector, ok := connector.(heartbeatAware)
			if !ok {
				continue
			}
			reportingConnector.SetHeartbeatReporter(heartbeatRegistry)
		}
	}
	publishers := map[string]connectors.Publisher{}
	for _, connector := range connectorList {
		publisher, ok := connector.(connectors.Publisher)
		if !ok {
			continue
		}
		publishers[strings.ToLower(strings.TrimSpace(connector.Name()))] = publisher
	}
	if _, exists := publishers["codex"]; !exists {
		publishers["codex"] = newCodexPublisherFromConfig(cfg, logger.With("connector", "codex"))
	}
	commandGateway.SetRoutingNotifier(newRoutingNotifier(
		cfg.WorkspaceRoot,
		sqlStore,
		publishers,
		cfg.TriageNotifyAdmin,
		logger.With("component", "routing-notifier"),
	))
	notifier := newTaskCompletionNotifier(
		cfg.WorkspaceRoot,
		sqlStore,
		publishers,
		cfg.TaskNotifyPolicy,
		cfg.TaskNotifySuccessPolicy,
		cfg.TaskNotifyFailurePolicy,
		commandGateway,
		logger.With("component", "task-notifier"),
	)
	engine.SetObserver(newTaskObserver(sqlStore, notifier, logger.With("component", "task-observer")))
	if heartbeatRegistry != nil {
		heartbeatNotifier := newHeartbeatNotifier(
			sqlStore,
			publishers,
			cfg.WorkspaceRoot,
			cfg.HeartbeatNotifyAdmin,
			logger.With("component", "heartbeat-notifier"),
		)
		heartbeatMonitor := heartbeat.NewMonitor(heartbeatRegistry, heartbeat.MonitorConfig{
			Interval:   time.Duration(cfg.HeartbeatIntervalSec) * time.Second,
			StaleAfter: time.Duration(cfg.HeartbeatStaleSec) * time.Second,
			Logger:     logger.With("component", "heartbeat-monitor"),
			OnTransition: func(ctx context.Context, transition heartbeat.Transition, snapshot heartbeat.Snapshot) {
				heartbeatNotifier.HandleTransition(ctx, transition, snapshot)
			},
		})
		return &Runtime{
			cfg:              cfg,
			logger:           logger,
			store:            sqlStore,
			engine:           engine,
			httpServer:       httpServer,
			watcher:          watchService,
			scheduler:        schedulerService,
			qmd:              qmdService,
			connectors:       connectorList,
			mcp:              mcpManager,
			heartbeat:        heartbeatRegistry,
			heartbeatMonitor: heartbeatMonitor,
		}, nil
	}

	return &Runtime{
		cfg:        cfg,
		logger:     logger,
		store:      sqlStore,
		engine:     engine,
		httpServer: httpServer,
		watcher:    watchService,
		scheduler:  schedulerService,
		qmd:        qmdService,
		connectors: connectorList,
		mcp:        mcpManager,
	}, nil
}
