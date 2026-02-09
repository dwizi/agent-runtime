package orchestrator

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"time"

	"github.com/google/uuid"
)

var ErrQueueFull = errors.New("task queue is full")

type TaskKind string

const (
	TaskKindGeneral TaskKind = "general"
	TaskKindReindex TaskKind = "reindex_markdown"
)

type Task struct {
	ID          string
	WorkspaceID string
	ContextID   string
	Kind        TaskKind
	Title       string
	Prompt      string
	CreatedAt   time.Time
}

type Engine struct {
	maxConcurrency int
	tasks          chan Task
	logger         *slog.Logger
	startOnce      sync.Once
}

func New(maxConcurrency int, logger *slog.Logger) *Engine {
	if maxConcurrency < 1 {
		maxConcurrency = 1
	}
	return &Engine{
		maxConcurrency: maxConcurrency,
		tasks:          make(chan Task, maxConcurrency*50),
		logger:         logger,
	}
}

func (e *Engine) Start(ctx context.Context) error {
	var workers sync.WaitGroup
	e.startOnce.Do(func() {
		for index := 0; index < e.maxConcurrency; index++ {
			workers.Add(1)
			go func(workerID int) {
				defer workers.Done()
				e.worker(ctx, workerID)
			}(index + 1)
		}
	})

	<-ctx.Done()
	workers.Wait()
	return nil
}

func (e *Engine) Enqueue(task Task) (Task, error) {
	if task.ID == "" {
		task.ID = uuid.NewString()
	}
	if task.Kind == "" {
		task.Kind = TaskKindGeneral
	}
	if task.CreatedAt.IsZero() {
		task.CreatedAt = time.Now().UTC()
	}

	select {
	case e.tasks <- task:
		e.logger.Info("task queued", "task_id", task.ID, "workspace_id", task.WorkspaceID, "context_id", task.ContextID, "kind", task.Kind)
		return task, nil
	default:
		return Task{}, ErrQueueFull
	}
}

func (e *Engine) worker(ctx context.Context, workerID int) {
	e.logger.Info("worker started", "worker_id", workerID)
	for {
		select {
		case <-ctx.Done():
			e.logger.Info("worker stopped", "worker_id", workerID)
			return
		case task := <-e.tasks:
			e.processTask(ctx, workerID, task)
		}
	}
}

func (e *Engine) processTask(ctx context.Context, workerID int, task Task) {
	e.logger.Info("processing task", "worker_id", workerID, "task_id", task.ID, "kind", task.Kind, "title", task.Title)
	select {
	case <-ctx.Done():
	case <-time.After(150 * time.Millisecond):
	}
}
