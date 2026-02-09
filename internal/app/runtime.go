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
	cfg        config.Config
	logger     *slog.Logger
	store      *store.Store
	engine     *orchestrator.Engine
	httpServer *http.Server
	watcher    *watcher.Service
	scheduler  *scheduler.Service
	qmd        *qmd.Service
	connectors []connectors.Connector
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

	watchService, err := watcher.New(
		[]string{cfg.WorkspaceRoot},
		logger.With("component", "watcher"),
		func(ctx context.Context, path string) {
			if workspaceID := workspaceIDFromPath(cfg.WorkspaceRoot, path); workspaceID != "" {
				qmdService.QueueWorkspaceIndex(workspaceID)
				schedulerService.HandleMarkdownUpdate(ctx, workspaceID, path)
			}
			_, enqueueErr := engine.Enqueue(orchestrator.Task{
				WorkspaceID: "system",
				ContextID:   "system:filewatcher",
				Title:       "Reindex markdown",
				Prompt:      "markdown file changed: " + path,
				Kind:        orchestrator.TaskKindReindex,
			})
			if enqueueErr != nil {
				logger.Error("failed to enqueue reindex task", "path", path, "error", enqueueErr)
			}
		},
	)
	if err != nil {
		sqlStore.Close()
		return nil, err
	}

	handler := httpapi.NewRouter(httpapi.Dependencies{
		Config: cfg,
		Store:  sqlStore,
		Engine: engine,
		Logger: logger.With("component", "api"),
	})
	httpServer := &http.Server{
		Addr:              cfg.HTTPAddr,
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
	}

	connectorList := []connectors.Connector{
		discord.New(cfg.DiscordToken, cfg.DiscordAPI, cfg.DiscordWSURL, cfg.WorkspaceRoot, sqlStore, commandGateway, groundedResponder, llmPolicy, logger.With("connector", "discord")),
		telegram.New(cfg.TelegramToken, cfg.TelegramAPI, cfg.WorkspaceRoot, cfg.TelegramPoll, sqlStore, commandGateway, groundedResponder, llmPolicy, logger.With("connector", "telegram")),
		imap.New(cfg.IMAPHost, cfg.IMAPPort, cfg.IMAPUsername, cfg.IMAPPassword, cfg.IMAPMailbox, cfg.IMAPPollSeconds, cfg.WorkspaceRoot, cfg.IMAPTLSSkipVerify, sqlStore, engine, logger.With("connector", "imap")),
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

	group, groupCtx := errgroup.WithContext(ctx)
	group.Go(func() error {
		return r.engine.Start(groupCtx)
	})
	group.Go(func() error {
		return r.watcher.Start(groupCtx)
	})
	group.Go(func() error {
		return r.scheduler.Start(groupCtx)
	})
	for _, conn := range r.connectors {
		connector := conn
		group.Go(func() error {
			return connector.Start(groupCtx)
		})
	}
	group.Go(func() error {
		err := r.httpServer.ListenAndServe()
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	})
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
