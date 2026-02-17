package scheduler

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/dwizi/agent-runtime/internal/heartbeat"
	"github.com/dwizi/agent-runtime/internal/orchestrator"
	"github.com/dwizi/agent-runtime/internal/store"
	"github.com/google/uuid"
)

const (
	markdownUpdatedEventKey = "markdown.updated"

	objectiveEventDedupeWindow = 30 * time.Second
	objectiveFailureBackoffMin = 1 * time.Minute
	objectiveFailureBackoffMax = 30 * time.Minute
	objectiveAutoPauseAfter    = 5
)

var errObjectiveRunAlreadyQueued = errors.New("objective run already queued")

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
	now := time.Now().UTC()
	for _, objective := range objectives {
		startedAt := time.Now().UTC()
		prompt := strings.TrimSpace(objective.Prompt)
		if prompt == "" {
			s.persistRunResult(ctx, objective, startedAt, time.Time{}, "objective prompt is empty", false)
			continue
		}
		if strings.TrimSpace(changedPath) != "" {
			prompt += "\n\nChanged markdown file: `" + strings.TrimSpace(changedPath) + "`."
		}
		runKey := objectiveEventRunKey(objective.ID, changedPath, now)
		task, taskErr := s.enqueueObjectiveTask(ctx, objective, prompt, runKey)
		if errors.Is(taskErr, errObjectiveRunAlreadyQueued) {
			s.persistRunResult(ctx, objective, startedAt, time.Time{}, "", true)
			s.logger.Info("event objective already queued", "objective_id", objective.ID, "workspace_id", objective.WorkspaceID)
			continue
		}
		if taskErr != nil {
			s.persistRunResult(ctx, objective, startedAt, time.Time{}, taskErr.Error(), false)
			s.logger.Error("event objective enqueue failed", "objective_id", objective.ID, "workspace_id", objective.WorkspaceID, "error", taskErr)
			continue
		}
		s.persistRunResult(ctx, objective, startedAt, time.Time{}, "", false)
		s.logger.Info("event objective queued", "objective_id", objective.ID, "task_id", task.ID, "workspace_id", objective.WorkspaceID)
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
	startedAt := time.Now().UTC()
	prompt := strings.TrimSpace(objective.Prompt)
	nextRun, nextErr := store.ComputeScheduleNextRunForTimezone(objective.CronExpr, objective.Timezone, now)
	if nextErr != nil {
		s.persistRunResult(ctx, objective, startedAt, time.Time{}, nextErr.Error(), false)
		return
	}
	if prompt == "" {
		s.persistRunResult(ctx, objective, startedAt, nextRun, "objective prompt is empty", false)
		return
	}
	task, err := s.enqueueObjectiveTask(ctx, objective, prompt, objectiveScheduleRunKey(objective.ID, objective.NextRunAt))
	if errors.Is(err, errObjectiveRunAlreadyQueued) {
		s.persistRunResult(ctx, objective, startedAt, nextRun, "", true)
		s.logger.Info("scheduled objective already queued", "objective_id", objective.ID, "workspace_id", objective.WorkspaceID)
		return
	}
	if err != nil {
		s.persistRunResult(ctx, objective, startedAt, nextRun, err.Error(), false)
		return
	}
	s.persistRunResult(ctx, objective, startedAt, nextRun, "", false)
	s.logger.Info("scheduled objective queued", "objective_id", objective.ID, "task_id", task.ID, "workspace_id", objective.WorkspaceID)
}

func (s *Service) persistRunResult(
	ctx context.Context,
	objective store.Objective,
	startedAt time.Time,
	nextRunAt time.Time,
	lastError string,
	skipStats bool,
) {
	lastError = strings.TrimSpace(lastError)
	activeOverride, reasonOverride, adjustedNextRun := objectiveFailurePolicy(objective, startedAt, nextRunAt, lastError)
	_, err := s.store.UpdateObjectiveRun(ctx, store.UpdateObjectiveRunInput{
		ID:               objective.ID,
		LastRunAt:        startedAt,
		NextRunAt:        adjustedNextRun,
		LastError:        lastError,
		RunDuration:      time.Since(startedAt),
		SkipStats:        skipStats,
		Active:           activeOverride,
		AutoPausedReason: reasonOverride,
	})
	if err != nil {
		s.logger.Error("update objective run failed", "error", err, "objective_id", objective.ID)
	}
}

func (s *Service) enqueueObjectiveTask(ctx context.Context, objective store.Objective, prompt string, runKey string) (orchestrator.Task, error) {
	title := strings.TrimSpace(objective.Title)
	if title == "" {
		title = "Objective task"
	}
	if len(title) > 72 {
		title = title[:72]
	}
	task := orchestrator.Task{
		ID:          "task-" + uuid.NewString(),
		WorkspaceID: objective.WorkspaceID,
		ContextID:   objective.ContextID,
		Kind:        orchestrator.TaskKindObjective,
		Title:       title,
		Prompt:      prompt,
	}
	if err := s.store.CreateTask(ctx, store.CreateTaskInput{
		ID:          task.ID,
		WorkspaceID: task.WorkspaceID,
		ContextID:   task.ContextID,
		Kind:        string(task.Kind),
		Title:       task.Title,
		Prompt:      task.Prompt,
		RunKey:      runKey,
		Status:      "queued",
	}); err != nil {
		if errors.Is(err, store.ErrTaskRunAlreadyExists) {
			return orchestrator.Task{}, errObjectiveRunAlreadyQueued
		}
		return orchestrator.Task{}, fmt.Errorf("persist objective task: %w", err)
	}
	queuedTask, err := s.engine.Enqueue(task)
	if err != nil {
		// Keep the persisted queued task for startup recovery.
		return orchestrator.Task{}, fmt.Errorf("enqueue objective task: %w", err)
	}
	return queuedTask, nil
}

func objectiveScheduleRunKey(objectiveID string, scheduledFor time.Time) string {
	id := strings.TrimSpace(objectiveID)
	if id == "" {
		id = "objective"
	}
	if scheduledFor.IsZero() {
		scheduledFor = time.Now().UTC()
	}
	return fmt.Sprintf("objective:%s:%d", id, scheduledFor.UTC().Unix())
}

func objectiveEventRunKey(objectiveID, changedPath string, eventTime time.Time) string {
	id := strings.TrimSpace(objectiveID)
	if id == "" {
		id = "objective"
	}
	if eventTime.IsZero() {
		eventTime = time.Now().UTC()
	}
	windowSeconds := int64(objectiveEventDedupeWindow.Seconds())
	if windowSeconds < 1 {
		windowSeconds = 1
	}
	bucket := eventTime.UTC().Unix() / windowSeconds
	path := strings.ToLower(strings.TrimSpace(changedPath))
	if path == "" {
		path = "-"
	}
	sum := sha1.Sum([]byte(path))
	return fmt.Sprintf("objective:%s:event:%d:%s", id, bucket, hex.EncodeToString(sum[:6]))
}

func objectiveFailurePolicy(
	objective store.Objective,
	now time.Time,
	nextRun time.Time,
	lastError string,
) (*bool, *string, time.Time) {
	lastError = strings.TrimSpace(lastError)
	if lastError == "" {
		return nil, nil, nextRun
	}
	consecutive := objective.ConsecutiveFailures + 1
	if consecutive >= objectiveAutoPauseAfter {
		active := false
		reason := fmt.Sprintf("auto-paused after %d consecutive failures", consecutive)
		return &active, &reason, time.Time{}
	}
	if objective.TriggerType != store.ObjectiveTriggerSchedule {
		return nil, nil, time.Time{}
	}
	backoffRun := now.UTC().Add(objectiveFailureBackoff(consecutive))
	if nextRun.IsZero() || backoffRun.After(nextRun) {
		nextRun = backoffRun
	}
	return nil, nil, nextRun
}

func objectiveFailureBackoff(consecutive int) time.Duration {
	if consecutive <= 0 {
		return objectiveFailureBackoffMin
	}
	backoff := objectiveFailureBackoffMin
	for index := 1; index < consecutive; index++ {
		backoff *= 2
		if backoff >= objectiveFailureBackoffMax {
			return objectiveFailureBackoffMax
		}
	}
	if backoff < objectiveFailureBackoffMin {
		return objectiveFailureBackoffMin
	}
	if backoff > objectiveFailureBackoffMax {
		return objectiveFailureBackoffMax
	}
	return backoff
}
