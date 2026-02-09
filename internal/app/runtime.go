package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/sync/errgroup"

	"github.com/carlos/spinner/internal/actions/executor"
	"github.com/carlos/spinner/internal/actions/plugins/sandbox"
	"github.com/carlos/spinner/internal/actions/plugins/smtp"
	"github.com/carlos/spinner/internal/actions/plugins/webhook"
	"github.com/carlos/spinner/internal/config"
	"github.com/carlos/spinner/internal/connectors"
	"github.com/carlos/spinner/internal/connectors/discord"
	"github.com/carlos/spinner/internal/connectors/imap"
	"github.com/carlos/spinner/internal/connectors/telegram"
	"github.com/carlos/spinner/internal/gateway"
	"github.com/carlos/spinner/internal/heartbeat"
	"github.com/carlos/spinner/internal/httpapi"
	"github.com/carlos/spinner/internal/llm/grounded"
	"github.com/carlos/spinner/internal/llm/promptpolicy"
	"github.com/carlos/spinner/internal/llm/safety"
	"github.com/carlos/spinner/internal/llm/zai"
	"github.com/carlos/spinner/internal/orchestrator"
	"github.com/carlos/spinner/internal/qmd"
	"github.com/carlos/spinner/internal/scheduler"
	"github.com/carlos/spinner/internal/store"
	"github.com/carlos/spinner/internal/watcher"
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
		WorkspaceRoot: cfg.WorkspaceRoot,
		Binary:        cfg.QMDBinary,
		IndexName:     cfg.QMDIndexName,
		Collection:    cfg.QMDCollectionName,
		SearchLimit:   cfg.QMDSearchLimit,
		OpenMaxBytes:  cfg.QMDOpenMaxBytes,
		Debounce:      time.Duration(cfg.QMDDebounceSeconds) * time.Second,
		IndexTimeout:  time.Duration(cfg.QMDIndexTimeoutSec) * time.Second,
		QueryTimeout:  time.Duration(cfg.QMDQueryTimeoutSec) * time.Second,
		AutoEmbed:     cfg.QMDAutoEmbed,
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
		}))
	}
	actionExecutor := executor.NewRegistry(plugins...)
	commandGateway := gateway.New(sqlStore, engine, qmdService, actionExecutor)
	commandGateway.SetTriageEnabled(cfg.TriageEnabled)
	responder := zai.New(zai.Config{
		APIKey:  cfg.ZAIAPIKey,
		BaseURL: cfg.ZAIBaseURL,
		Model:   cfg.ZAIModel,
		Timeout: time.Duration(cfg.ZAITimeoutSec) * time.Second,
	}, logger.With("component", "llm-zai"))
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
		MaxSkills:            5,
		MaxSkillBytes:        1400,
		MaxSystemPromptBytes: 12000,
	})
	groundedResponder := grounded.New(policyResponder, qmdService, grounded.Config{
		TopK:           3,
		MaxDocExcerpt:  1200,
		MaxPromptBytes: 8000,
	}, logger.With("component", "llm-grounding"))
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
	engine.SetExecutor(newTaskWorkerExecutor(cfg.WorkspaceRoot, groundedResponder, qmdService, logger.With("component", "task-executor")))
	if heartbeatRegistry != nil {
		schedulerService.SetHeartbeatReporter(heartbeatRegistry)
	}

	watchService, err := watcher.New(
		[]string{cfg.WorkspaceRoot},
		logger.With("component", "watcher"),
		func(ctx context.Context, path string) {
			workspaceID := workspaceIDFromPath(cfg.WorkspaceRoot, path)
			if workspaceID != "" {
				qmdService.QueueWorkspaceIndex(workspaceID)
				schedulerService.HandleMarkdownUpdate(ctx, workspaceID, path)
			}
			if workspaceID == "" {
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
		connectorList = append(connectorList, discord.New(cfg.DiscordToken, cfg.DiscordAPI, cfg.DiscordWSURL, cfg.WorkspaceRoot, sqlStore, commandGateway, groundedResponder, llmPolicy, logger.With("connector", "discord")))
	} else if heartbeatRegistry != nil {
		heartbeatRegistry.Disabled("connector:discord", "token missing")
	}
	if strings.TrimSpace(cfg.TelegramToken) != "" {
		connectorList = append(connectorList, telegram.New(cfg.TelegramToken, cfg.TelegramAPI, cfg.WorkspaceRoot, cfg.TelegramPoll, sqlStore, commandGateway, groundedResponder, llmPolicy, logger.With("connector", "telegram")))
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
	commandGateway.SetRoutingNotifier(newRoutingNotifier(
		sqlStore,
		publishers,
		cfg.TriageNotifyAdmin,
		logger.With("component", "routing-notifier"),
	))
	notifier := newTaskCompletionNotifier(
		sqlStore,
		publishers,
		cfg.TaskNotifyPolicy,
		cfg.TaskNotifySuccessPolicy,
		cfg.TaskNotifyFailurePolicy,
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
	r.logger.Info("spinner runtime starting", "addr", r.cfg.HTTPAddr, "workspace_root", r.cfg.WorkspaceRoot)
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

func parseShellArgs(input string) []string {
	trimmed := strings.TrimSpace(input)
	if trimmed == "" {
		return nil
	}
	return strings.Fields(trimmed)
}
