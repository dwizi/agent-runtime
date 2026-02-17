package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"golang.org/x/sync/errgroup"

	"github.com/dwizi/agent-runtime/internal/actions/executor"
	"github.com/dwizi/agent-runtime/internal/actions/plugins/sandbox"
	"github.com/dwizi/agent-runtime/internal/actions/plugins/smtp"
	"github.com/dwizi/agent-runtime/internal/actions/plugins/webhook"
	"github.com/dwizi/agent-runtime/internal/config"
	"github.com/dwizi/agent-runtime/internal/connectors"
	"github.com/dwizi/agent-runtime/internal/connectors/discord"
	"github.com/dwizi/agent-runtime/internal/connectors/imap"
	"github.com/dwizi/agent-runtime/internal/connectors/telegram"
	"github.com/dwizi/agent-runtime/internal/gateway"
	"github.com/dwizi/agent-runtime/internal/heartbeat"
	"github.com/dwizi/agent-runtime/internal/httpapi"
	"github.com/dwizi/agent-runtime/internal/llm"
	"github.com/dwizi/agent-runtime/internal/llm/anthropic"
	"github.com/dwizi/agent-runtime/internal/llm/grounded"
	"github.com/dwizi/agent-runtime/internal/llm/openai"
	"github.com/dwizi/agent-runtime/internal/llm/promptpolicy"
	"github.com/dwizi/agent-runtime/internal/llm/safety"
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
	heartbeat        *heartbeat.Registry
	heartbeatMonitor *heartbeat.Monitor
}

type heartbeatAware interface {
	SetHeartbeatReporter(reporter heartbeat.Reporter)
}

func New(cfg config.Config, logger *slog.Logger) (*Runtime, error) {
	if err := os.MkdirAll(filepath.Dir(cfg.DBPath), 0o755); err != nil {
		return nil, fmt.Errorf("create db directory: %w", err)
	}
	if err := os.MkdirAll(cfg.WorkspaceRoot, 0o755); err != nil {
		return nil, fmt.Errorf("create workspace root: %w", err)
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

	plugins := []executor.Plugin{
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
		plugins = append(plugins, sandbox.New(sandbox.Config{
			Enabled:         true,
			WorkspaceRoot:   cfg.WorkspaceRoot,
			AllowedCommands: parseCSVList(cfg.SandboxAllowedCommandsCSV),
			RunnerCommand:   cfg.SandboxRunnerCommand,
			RunnerArgs:      parseShellArgs(cfg.SandboxRunnerArgs),
			Timeout:         time.Duration(cfg.SandboxTimeoutSec) * time.Second,
			MaxOutputBytes:  cfg.SandboxMaxOutputBytes,
		}))
	}
	actionExecutor := executor.NewRegistry(plugins...)
	commandGateway := gateway.New(sqlStore, engine, qmdService, actionExecutor, cfg.WorkspaceRoot, logger.With("component", "gateway"))
	commandGateway.SetTriageEnabled(cfg.TriageEnabled)
	if cfg.AgentMaxTurnDurationSec > 0 {
		commandGateway.SetAgentMaxTurnDuration(time.Duration(cfg.AgentMaxTurnDurationSec) * time.Second)
	}
	commandGateway.SetAgentGroundingPolicy(cfg.AgentGroundingFirstStep, cfg.AgentGroundingEveryStep)
	commandGateway.SetSensitiveApprovalTTL(time.Duration(cfg.AgentSensitiveApprovalTTLSeconds) * time.Second)

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
		TopK:           cfg.LLMGroundingTopK,
		MaxDocExcerpt:  cfg.LLMGroundingMaxDocExcerpt,
		MaxPromptBytes: cfg.LLMGroundingMaxPromptBytes,
		ChatTailLines:  cfg.LLMGroundingChatTailLines,
		ChatTailBytes:  cfg.LLMGroundingChatTailBytes,
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
	}, nil
}

func (r *Runtime) Run(ctx context.Context) error {
	r.logger.Info("agent-runtime runtime starting", "addr", r.cfg.HTTPAddr, "workspace_root", r.cfg.WorkspaceRoot)
	if r.heartbeat != nil {
		r.heartbeat.Beat("runtime", "runtime loop started")
	}

	group, groupCtx := errgroup.WithContext(ctx)
	group.Go(func() error {
		if r.heartbeat != nil {
			r.heartbeat.Starting("orchestrator", "workers starting")
		}
		return runMonitored(groupCtx, r.heartbeat, "orchestrator", 20*time.Second, func(runCtx context.Context) error {
			return r.engine.Start(runCtx)
		})
	})
	recoveryStaleAfter := time.Duration(r.cfg.TaskRecoveryRunningStaleSec) * time.Second
	if err := recoverPendingTasks(groupCtx, r.store, r.engine, recoveryStaleAfter, r.logger.With("component", "task-recovery")); err != nil {
		r.logger.Error("startup task recovery failed", "error", err)
	}
	group.Go(func() error {
		return runMonitored(groupCtx, r.heartbeat, "watcher", 0, func(runCtx context.Context) error {
			return r.watcher.Start(runCtx)
		})
	})
	group.Go(func() error {
		return runMonitored(groupCtx, r.heartbeat, "scheduler", 0, func(runCtx context.Context) error {
			return r.scheduler.Start(runCtx)
		})
	})
	for _, conn := range r.connectors {
		connector := conn
		group.Go(func() error {
			componentName := "connector:" + strings.ToLower(strings.TrimSpace(connector.Name()))
			return runMonitored(groupCtx, r.heartbeat, componentName, 0, func(runCtx context.Context) error {
				return connector.Start(runCtx)
			})
		})
	}
	group.Go(func() error {
		return runMonitored(groupCtx, r.heartbeat, "api", 20*time.Second, func(runCtx context.Context) error {
			err := r.httpServer.ListenAndServe()
			if errors.Is(err, http.ErrServerClosed) {
				return nil
			}
			return err
		})
	})
	if r.heartbeatMonitor != nil {
		group.Go(func() error {
			return r.heartbeatMonitor.Start(groupCtx)
		})
	}
	group.Go(func() error {
		<-groupCtx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		return r.httpServer.Shutdown(shutdownCtx)
	})

	return group.Wait()
}

func (r *Runtime) Close() error {
	if r.qmd != nil {
		r.qmd.Close()
	}
	if r.store == nil {
		return nil
	}
	return r.store.Close()
}

func workspaceIDFromPath(workspaceRoot, changedPath string) string {
	root := filepath.Clean(strings.TrimSpace(workspaceRoot))
	path := filepath.Clean(strings.TrimSpace(changedPath))
	if root == "" || path == "" {
		return ""
	}
	relative, err := filepath.Rel(root, path)
	if err != nil {
		return ""
	}
	if strings.HasPrefix(relative, "..") || relative == "." {
		return ""
	}
	parts := strings.Split(relative, string(os.PathSeparator))
	if len(parts) == 0 {
		return ""
	}
	workspaceID := strings.TrimSpace(parts[0])
	if workspaceID == "" || workspaceID == "." {
		return ""
	}
	return workspaceID
}

func shouldQueueQMDForPath(workspaceRoot, changedPath string) bool {
	workspaceRelative, ok := workspaceRelativeMarkdownPath(workspaceRoot, changedPath)
	if !ok {
		return false
	}
	if strings.HasPrefix(workspaceRelative, ".qmd/") {
		return false
	}
	if workspaceRelative == "logs" || strings.HasPrefix(workspaceRelative, "logs/") {
		return false
	}
	if workspaceRelative == "ops/heartbeat.md" {
		return false
	}
	return true
}

func shouldTriggerObjectiveEventForPath(workspaceRoot, changedPath string) bool {
	workspaceRelative, ok := workspaceRelativeMarkdownPath(workspaceRoot, changedPath)
	if !ok {
		return false
	}
	if strings.HasPrefix(workspaceRelative, ".qmd/") {
		return false
	}
	if workspaceRelative == "logs" || strings.HasPrefix(workspaceRelative, "logs/") {
		return false
	}
	if workspaceRelative == "tasks" || strings.HasPrefix(workspaceRelative, "tasks/") {
		return false
	}
	if workspaceRelative == "ops" || strings.HasPrefix(workspaceRelative, "ops/") {
		return false
	}
	return true
}

func workspaceRelativeMarkdownPath(workspaceRoot, changedPath string) (string, bool) {
	root := filepath.Clean(strings.TrimSpace(workspaceRoot))
	path := filepath.Clean(strings.TrimSpace(changedPath))
	if root == "" || path == "" {
		return "", false
	}
	if strings.ToLower(filepath.Ext(path)) != ".md" {
		return "", false
	}
	relative, err := filepath.Rel(root, path)
	if err != nil {
		return "", false
	}
	if strings.HasPrefix(relative, "..") || relative == "." {
		return "", false
	}
	relative = filepath.ToSlash(relative)
	separator := strings.Index(relative, "/")
	if separator < 0 || separator+1 >= len(relative) {
		return "", false
	}
	workspaceRelative := strings.ToLower(strings.TrimSpace(relative[separator+1:]))
	if workspaceRelative == "" {
		return "", false
	}
	return workspaceRelative, true
}

func hasPendingReindexTask(ctx context.Context, sqlStore *store.Store, workspaceID string) (bool, error) {
	if sqlStore == nil {
		return false, nil
	}
	workspaceID = strings.TrimSpace(workspaceID)
	if workspaceID == "" {
		return false, nil
	}
	kind := string(orchestrator.TaskKindReindex)
	queued, err := sqlStore.ListTasks(ctx, store.ListTasksInput{
		WorkspaceID: workspaceID,
		Kind:        kind,
		Status:      "queued",
		Limit:       1,
	})
	if err != nil {
		return false, err
	}
	if len(queued) > 0 {
		return true, nil
	}
	running, err := sqlStore.ListTasks(ctx, store.ListTasksInput{
		WorkspaceID: workspaceID,
		Kind:        kind,
		Status:      "running",
		Limit:       1,
	})
	if err != nil {
		return false, err
	}
	return len(running) > 0, nil
}

type taskRecoveryStore interface {
	ListTasks(ctx context.Context, input store.ListTasksInput) ([]store.TaskRecord, error)
	RequeueTask(ctx context.Context, id string) error
}

type taskRecoveryEngine interface {
	Enqueue(task orchestrator.Task) (orchestrator.Task, error)
}

func recoverPendingTasks(
	ctx context.Context,
	sqlStore taskRecoveryStore,
	engine taskRecoveryEngine,
	staleRunningAfter time.Duration,
	logger *slog.Logger,
) error {
	if sqlStore == nil || engine == nil {
		return nil
	}
	if logger == nil {
		logger = slog.Default()
	}
	if staleRunningAfter <= 0 {
		staleRunningAfter = 10 * time.Minute
	}
	now := time.Now().UTC()
	queued, err := sqlStore.ListTasks(ctx, store.ListTasksInput{
		Status: "queued",
		Limit:  500,
	})
	if err != nil {
		return fmt.Errorf("list queued tasks for recovery: %w", err)
	}
	running, err := sqlStore.ListTasks(ctx, store.ListTasksInput{
		Status: "running",
		Limit:  500,
	})
	if err != nil {
		return fmt.Errorf("list running tasks for recovery: %w", err)
	}
	candidates := make([]store.TaskRecord, 0, len(queued)+len(running))
	seen := map[string]struct{}{}
	for _, item := range queued {
		if strings.TrimSpace(item.ID) == "" {
			continue
		}
		if _, exists := seen[item.ID]; exists {
			continue
		}
		seen[item.ID] = struct{}{}
		candidates = append(candidates, item)
	}
	staleRequeued := 0
	for _, item := range running {
		taskID := strings.TrimSpace(item.ID)
		if taskID == "" {
			continue
		}
		startedAt := item.StartedAt.UTC()
		isStale := startedAt.IsZero() || now.Sub(startedAt) >= staleRunningAfter
		if !isStale {
			continue
		}
		if err := sqlStore.RequeueTask(ctx, taskID); err != nil {
			logger.Error("failed to requeue stale running task during startup recovery", "task_id", taskID, "error", err)
			continue
		}
		item.Status = "queued"
		item.WorkerID = 0
		item.StartedAt = time.Time{}
		item.FinishedAt = time.Time{}
		item.ErrorMessage = ""
		if _, exists := seen[taskID]; exists {
			continue
		}
		seen[taskID] = struct{}{}
		candidates = append(candidates, item)
		staleRequeued++
	}
	sort.Slice(candidates, func(i, j int) bool {
		left := candidates[i].CreatedAt.UTC()
		right := candidates[j].CreatedAt.UTC()
		if left.Equal(right) {
			return candidates[i].ID < candidates[j].ID
		}
		return left.Before(right)
	})
	recovered := 0
	for _, item := range candidates {
		_, enqueueErr := engine.Enqueue(orchestrator.Task{
			ID:          item.ID,
			WorkspaceID: item.WorkspaceID,
			ContextID:   item.ContextID,
			Kind:        orchestrator.TaskKind(strings.TrimSpace(item.Kind)),
			Title:       item.Title,
			Prompt:      item.Prompt,
		})
		if enqueueErr != nil {
			logger.Error("failed to enqueue recovered task", "task_id", item.ID, "error", enqueueErr)
			continue
		}
		recovered++
	}
	logger.Info(
		"startup task recovery completed",
		"queued_candidates", len(queued),
		"stale_running_requeued", staleRequeued,
		"recovered_enqueued", recovered,
	)
	return nil
}

func runMonitored(
	ctx context.Context,
	reporter heartbeat.Reporter,
	component string,
	beatInterval time.Duration,
	run func(context.Context) error,
) error {
	if run == nil {
		return nil
	}
	if reporter != nil {
		reporter.Starting(component, "starting")
		reporter.Beat(component, "running")
	}

	var stopHeartbeat func()
	if reporter != nil && beatInterval > 0 {
		heartbeatCtx, cancel := context.WithCancel(ctx)
		stopHeartbeat = cancel
		go func() {
			ticker := time.NewTicker(beatInterval)
			defer ticker.Stop()
			for {
				select {
				case <-heartbeatCtx.Done():
					return
				case <-ticker.C:
					reporter.Beat(component, "running")
				}
			}
		}()
	}

	err := run(ctx)
	if stopHeartbeat != nil {
		stopHeartbeat()
	}
	if reporter == nil {
		return err
	}
	if err != nil && ctx.Err() == nil {
		reporter.Degrade(component, "component failed", err)
		return err
	}
	reporter.Stopped(component, "stopped")
	return err
}

func parseCSVSet(input string) map[string]struct{} {
	trimmed := strings.TrimSpace(input)
	if trimmed == "" {
		return nil
	}
	parts := strings.Split(trimmed, ",")
	set := make(map[string]struct{}, len(parts))
	for _, part := range parts {
		value := strings.ToLower(strings.TrimSpace(part))
		if value == "" {
			continue
		}
		set[value] = struct{}{}
	}
	if len(set) == 0 {
		return nil
	}
	return set
}

func parseCSVList(input string) []string {
	trimmed := strings.TrimSpace(input)
	if trimmed == "" {
		return nil
	}
	parts := strings.Split(trimmed, ",")
	result := make([]string, 0, len(parts))
	seen := map[string]struct{}{}
	for _, part := range parts {
		value := strings.ToLower(strings.TrimSpace(part))
		if value == "" {
			continue
		}
		if _, exists := seen[value]; exists {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	return result
}

func parseCSVTrimList(input string) []string {
	trimmed := strings.TrimSpace(input)
	if trimmed == "" {
		return nil
	}
	parts := strings.Split(trimmed, ",")
	result := make([]string, 0, len(parts))
	seen := map[string]struct{}{}
	for _, part := range parts {
		value := strings.TrimSpace(part)
		if value == "" {
			continue
		}
		key := strings.ToLower(value)
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		result = append(result, value)
	}
	return result
}

func parseShellArgs(input string) []string {
	trimmed := strings.TrimSpace(input)
	if trimmed == "" {
		return nil
	}
	return strings.Fields(trimmed)
}
