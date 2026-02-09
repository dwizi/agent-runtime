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
	TaskKindGeneral   TaskKind = "general"
	TaskKindReindex   TaskKind = "reindex_markdown"
	TaskKindObjective TaskKind = "objective"
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

type TaskResult struct {
	Summary      string
	ArtifactPath string
}

type TaskExecutor interface {
	Execute(ctx context.Context, task Task) (TaskResult, error)
}

type TaskObserver interface {
	OnTaskQueued(task Task)
	OnTaskStarted(task Task, workerID int)
	OnTaskCompleted(task Task, workerID int, result TaskResult)
	OnTaskFailed(task Task, workerID int, err error)
}

type Engine struct {
	maxConcurrency int
	tasks          chan Task
	logger         *slog.Logger
	startOnce      sync.Once
	executor       TaskExecutor
	observer       TaskObserver
}

func New(maxConcurrency int, logger *slog.Logger) *Engine {
	if maxConcurrency < 1 {
		maxConcurrency = 1
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &Engine{
		maxConcurrency: maxConcurrency,
		tasks:          make(chan Task, maxConcurrency*50),
		logger:         logger,
	}
}

func (e *Engine) SetExecutor(executor TaskExecutor) {
	e.executor = executor
}

func (e *Engine) SetObserver(observer TaskObserver) {
	e.observer = observer
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
		if e.observer != nil {
			e.observer.OnTaskQueued(task)
		}
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
	if e.observer != nil {
		e.observer.OnTaskStarted(task, workerID)
	}
	if e.executor == nil {
		select {
		case <-ctx.Done():
		case <-time.After(150 * time.Millisecond):
		}
		if e.observer != nil {
			e.observer.OnTaskCompleted(task, workerID, TaskResult{Summary: "processed with default noop executor"})
		}
		return
	}
	result, err := e.executor.Execute(ctx, task)
	if err != nil {
		e.logger.Error("task execution failed", "worker_id", workerID, "task_id", task.ID, "error", err)
		if e.observer != nil {
			e.observer.OnTaskFailed(task, workerID, err)
		}
		return
	}
	if e.observer != nil {
		e.observer.OnTaskCompleted(task, workerID, result)
	}
}
