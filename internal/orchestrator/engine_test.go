package orchestrator

import (
	"context"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"
)

func TestEnqueueAssignsDefaults(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	engine := New(1, logger)

	task, err := engine.Enqueue(Task{
		WorkspaceID: "ws_1",
		ContextID:   "ctx_1",
		Title:       "Collect docs",
		Prompt:      "Index docs",
	})
	if err != nil {
		t.Fatalf("enqueue returned error: %v", err)
	}
	if task.ID == "" {
		t.Fatal("expected generated task ID")
	}
	if task.Kind != TaskKindGeneral {
		t.Fatalf("expected default task kind %q, got %q", TaskKindGeneral, task.Kind)
	}
	if task.CreatedAt.IsZero() {
		t.Fatal("expected created timestamp")
	}
}

func TestEnqueueQueueFull(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	engine := New(1, logger)

	for index := 0; index < 50; index++ {
		_, err := engine.Enqueue(Task{
			WorkspaceID: "ws_1",
			ContextID:   "ctx_1",
			Title:       "Task",
			Prompt:      "Prompt",
		})
		if err != nil {
			t.Fatalf("unexpected enqueue error before queue full: %v", err)
		}
	}

	_, err := engine.Enqueue(Task{
		WorkspaceID: "ws_1",
		ContextID:   "ctx_1",
		Title:       "Overflow",
		Prompt:      "Prompt",
	})
	if err != ErrQueueFull {
		t.Fatalf("expected ErrQueueFull, got %v", err)
	}
}

type testExecutor struct {
	result TaskResult
	err    error
}

func (e *testExecutor) Execute(ctx context.Context, task Task) (TaskResult, error) {
	return e.result, e.err
}

type testObserver struct {
	mu        sync.Mutex
	queued    []Task
	started   []Task
	completed []TaskResult
	failed    []error
	done      chan struct{}
}

func newTestObserver() *testObserver {
	return &testObserver{done: make(chan struct{}, 1)}
}

func (o *testObserver) OnTaskQueued(task Task) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.queued = append(o.queued, task)
}

func (o *testObserver) OnTaskStarted(task Task, workerID int) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.started = append(o.started, task)
}

func (o *testObserver) OnTaskCompleted(task Task, workerID int, result TaskResult) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.completed = append(o.completed, result)
	select {
	case o.done <- struct{}{}:
	default:
	}
}

func (o *testObserver) OnTaskFailed(task Task, workerID int, err error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.failed = append(o.failed, err)
	select {
	case o.done <- struct{}{}:
	default:
	}
}

func TestEngineWorkerExecutesTask(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	engine := New(1, logger)
	executor := &testExecutor{result: TaskResult{Summary: "ok", ArtifactPath: "tasks/task-1.md"}}
	observer := newTestObserver()
	engine.SetExecutor(executor)
	engine.SetObserver(observer)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		_ = engine.Start(ctx)
	}()

	task, err := engine.Enqueue(Task{
		WorkspaceID: "ws_1",
		ContextID:   "ctx_1",
		Title:       "Task",
		Prompt:      "Prompt",
	})
	if err != nil {
		t.Fatalf("enqueue failed: %v", err)
	}

	select {
	case <-observer.done:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for completion callback")
	}

	observer.mu.Lock()
	defer observer.mu.Unlock()
	if len(observer.queued) != 1 || observer.queued[0].ID != task.ID {
		t.Fatalf("expected queued callback for task %s", task.ID)
	}
	if len(observer.started) != 1 || observer.started[0].ID != task.ID {
		t.Fatalf("expected started callback for task %s", task.ID)
	}
	if len(observer.completed) != 1 {
		t.Fatalf("expected one completed callback, got %d", len(observer.completed))
	}
	if observer.completed[0].Summary != "ok" {
		t.Fatalf("expected completion summary ok, got %s", observer.completed[0].Summary)
	}
	if len(observer.failed) != 0 {
		t.Fatalf("expected no failures, got %d", len(observer.failed))
	}
}

func TestEngineWorkerReportsFailure(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	engine := New(1, logger)
	executor := &testExecutor{err: context.DeadlineExceeded}
	observer := newTestObserver()
	engine.SetExecutor(executor)
	engine.SetObserver(observer)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		_ = engine.Start(ctx)
	}()

	_, err := engine.Enqueue(Task{
		WorkspaceID: "ws_1",
		ContextID:   "ctx_1",
		Title:       "Task",
		Prompt:      "Prompt",
	})
	if err != nil {
		t.Fatalf("enqueue failed: %v", err)
	}

	select {
	case <-observer.done:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for failure callback")
	}

	observer.mu.Lock()
	defer observer.mu.Unlock()
	if len(observer.failed) != 1 {
		t.Fatalf("expected one failed callback, got %d", len(observer.failed))
	}
	if len(observer.completed) != 0 {
		t.Fatalf("expected no completed callbacks, got %d", len(observer.completed))
	}
}
