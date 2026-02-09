package scheduler

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/carlos/spinner/internal/heartbeat"
	"github.com/carlos/spinner/internal/orchestrator"
	"github.com/carlos/spinner/internal/store"
)

const markdownUpdatedEventKey = "markdown.updated"

type Store interface {
	ListDueObjectives(ctx context.Context, now time.Time, limit int) ([]store.Objective, error)
	ListEventObjectives(ctx context.Context, workspaceID, eventKey string, limit int) ([]store.Objective, error)
	UpdateObjectiveRun(ctx context.Context, input store.UpdateObjectiveRunInput) (store.Objective, error)
	CreateTask(ctx context.Context, input store.CreateTaskInput) error
}

type Engine interface {
	Enqueue(task orchestrator.Task) (orchestrator.Task, error)
}

type Service struct {
	store        Store
	engine       Engine
	logger       *slog.Logger
	pollInterval time.Duration
	reporter     heartbeat.Reporter
}

func New(store Store, engine Engine, pollInterval time.Duration, logger *slog.Logger) *Service {
	if pollInterval < time.Second {
		pollInterval = 15 * time.Second
	}
	return &Service{
		store:        store,
		engine:       engine,
		logger:       logger,
		pollInterval: pollInterval,
	}
}

func (s *Service) SetHeartbeatReporter(reporter heartbeat.Reporter) {
	s.reporter = reporter
}

func (s *Service) Start(ctx context.Context) error {
	if s.store == nil || s.engine == nil {
		if s.reporter != nil {
			s.reporter.Disabled("scheduler", "dependencies missing")
		}
		<-ctx.Done()
		return nil
	}
	ticker := time.NewTicker(s.pollInterval)
	defer ticker.Stop()
	if s.reporter != nil {
		s.reporter.Starting("scheduler", "started")
		s.reporter.Beat("scheduler", "polling objectives")
	}
	s.logger.Info("scheduler started", "poll_interval", s.pollInterval.String())
	for {
		if ctx.Err() != nil {
			if s.reporter != nil {
				s.reporter.Stopped("scheduler", "stopped")
			}
			s.logger.Info("scheduler stopped")
			return nil
		}
		if err := s.processDue(ctx); err != nil {
			if s.reporter != nil {
				s.reporter.Degrade("scheduler", "process due failed", err)
			}
			s.logger.Error("scheduler process due failed", "error", err)
		} else if s.reporter != nil {
			s.reporter.Beat("scheduler", "poll cycle completed")
		}
		select {
		case <-ctx.Done():
			if s.reporter != nil {
				s.reporter.Stopped("scheduler", "stopped")
			}
			s.logger.Info("scheduler stopped")
			return nil
		case <-ticker.C:
		}
	}
}

func (s *Service) HandleMarkdownUpdate(ctx context.Context, workspaceID, changedPath string) {
	if s.store == nil || s.engine == nil {
		return
	}
	workspaceID = strings.TrimSpace(workspaceID)
	if workspaceID == "" {
		return
	}
	objectives, err := s.store.ListEventObjectives(ctx, workspaceID, markdownUpdatedEventKey, 20)
	if err != nil {
		s.logger.Error("list event objectives failed", "error", err, "workspace_id", workspaceID)
		return
	}
	for _, objective := range objectives {
		prompt := strings.TrimSpace(objective.Prompt)
		if prompt == "" {
			continue
		}
		if strings.TrimSpace(changedPath) != "" {
			prompt += "\n\nChanged markdown file: `" + strings.TrimSpace(changedPath) + "`."
		}
		s.enqueueObjectiveTask(ctx, objective, prompt)
	}
}

func (s *Service) processDue(ctx context.Context) error {
	now := time.Now().UTC()
	objectives, err := s.store.ListDueObjectives(ctx, now, 20)
	if err != nil {
		return err
	}
	for _, objective := range objectives {
		s.runScheduledObjective(ctx, objective, now)
	}
	return nil
}

func (s *Service) runScheduledObjective(ctx context.Context, objective store.Objective, now time.Time) {
	prompt := strings.TrimSpace(objective.Prompt)
	if prompt == "" {
		_, _ = s.store.UpdateObjectiveRun(ctx, store.UpdateObjectiveRunInput{
			ID:        objective.ID,
			LastRunAt: now,
			NextRunAt: now.Add(time.Duration(objective.IntervalSeconds) * time.Second),
			LastError: "objective prompt is empty",
		})
		return
	}
	task, err := s.enqueueObjectiveTask(ctx, objective, prompt)
	nextRun := now.Add(time.Duration(objective.IntervalSeconds) * time.Second)
	if objective.IntervalSeconds < 1 {
		nextRun = time.Time{}
	}
	lastError := ""
	if err != nil {
		lastError = err.Error()
	}
	_, updateErr := s.store.UpdateObjectiveRun(ctx, store.UpdateObjectiveRunInput{
		ID:        objective.ID,
		LastRunAt: now,
		NextRunAt: nextRun,
		LastError: lastError,
	})
	if updateErr != nil {
		s.logger.Error("update objective run failed", "error", updateErr, "objective_id", objective.ID)
	}
	if err != nil {
		return
	}
	s.logger.Info("scheduled objective queued", "objective_id", objective.ID, "task_id", task.ID, "workspace_id", objective.WorkspaceID)
}

func (s *Service) enqueueObjectiveTask(ctx context.Context, objective store.Objective, prompt string) (orchestrator.Task, error) {
	title := strings.TrimSpace(objective.Title)
	if title == "" {
		title = "Objective task"
	}
	if len(title) > 72 {
		title = title[:72]
	}
	task, err := s.engine.Enqueue(orchestrator.Task{
		WorkspaceID: objective.WorkspaceID,
		ContextID:   objective.ContextID,
		Kind:        orchestrator.TaskKindObjective,
		Title:       title,
		Prompt:      prompt,
	})
	if err != nil {
		return orchestrator.Task{}, err
	}
	if err := s.store.CreateTask(ctx, store.CreateTaskInput{
		ID:          task.ID,
		WorkspaceID: task.WorkspaceID,
		ContextID:   task.ContextID,
		Kind:        string(task.Kind),
		Title:       task.Title,
		Prompt:      task.Prompt,
		Status:      "queued",
	}); err != nil {
		return orchestrator.Task{}, fmt.Errorf("persist objective task: %w", err)
	}
	return task, nil
}
