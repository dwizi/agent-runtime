package app

import (
	"context"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"time"

	"github.com/dwizi/agent-runtime/internal/orchestrator"
	"github.com/dwizi/agent-runtime/internal/store"
)

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

func runStaleTaskRecoveryLoop(
	ctx context.Context,
	sqlStore taskRecoveryStore,
	engine taskRecoveryEngine,
	staleRunningAfter time.Duration,
	logger *slog.Logger,
) error {
	if sqlStore == nil || engine == nil {
		<-ctx.Done()
		return nil
	}
	if logger == nil {
		logger = slog.Default()
	}
	if staleRunningAfter <= 0 {
		staleRunningAfter = 10 * time.Minute
	}
	interval := staleRecoveryLoopInterval(staleRunningAfter)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			requeued, err := recoverStaleRunningTasks(ctx, sqlStore, engine, staleRunningAfter, logger)
			if err != nil {
				logger.Error("periodic stale task recovery failed", "error", err)
				continue
			}
			if requeued > 0 {
				logger.Info("periodic stale task recovery requeued tasks", "count", requeued)
			}
		}
	}
}

func staleRecoveryLoopInterval(staleRunningAfter time.Duration) time.Duration {
	if staleRunningAfter <= 0 {
		return 5 * time.Minute
	}
	interval := staleRunningAfter / 2
	if interval < 30*time.Second {
		return 30 * time.Second
	}
	if interval > 10*time.Minute {
		return 10 * time.Minute
	}
	return interval
}

func recoverStaleRunningTasks(
	ctx context.Context,
	sqlStore taskRecoveryStore,
	engine taskRecoveryEngine,
	staleRunningAfter time.Duration,
	logger *slog.Logger,
) (int, error) {
	if sqlStore == nil || engine == nil {
		return 0, nil
	}
	if logger == nil {
		logger = slog.Default()
	}
	if staleRunningAfter <= 0 {
		staleRunningAfter = 10 * time.Minute
	}
	now := time.Now().UTC()
	running, err := sqlStore.ListTasks(ctx, store.ListTasksInput{
		Status: "running",
		Limit:  500,
	})
	if err != nil {
		return 0, fmt.Errorf("list running tasks for stale recovery: %w", err)
	}
	requeued := 0
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
			logger.Error("failed to requeue stale running task", "task_id", taskID, "error", err)
			continue
		}
		_, enqueueErr := engine.Enqueue(orchestrator.Task{
			ID:          item.ID,
			WorkspaceID: item.WorkspaceID,
			ContextID:   item.ContextID,
			Kind:        orchestrator.TaskKind(strings.TrimSpace(item.Kind)),
			Title:       item.Title,
			Prompt:      item.Prompt,
		})
		if enqueueErr != nil {
			logger.Error("failed to enqueue stale requeued task", "task_id", taskID, "error", enqueueErr)
			continue
		}
		requeued++
	}
	return requeued, nil
}
