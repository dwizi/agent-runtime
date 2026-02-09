package store

import (
	"context"
	"testing"
	"time"
)

func TestTaskLifecycle(t *testing.T) {
	sqlStore := newTestStore(t)
	ctx := context.Background()

	if err := sqlStore.CreateTask(ctx, CreateTaskInput{
		ID:          "task-1",
		WorkspaceID: "ws-1",
		ContextID:   "ctx-1",
		Kind:        "general",
		Title:       "Review docs",
		Prompt:      "Review inbox markdown and summarize actions",
		Status:      "queued",
	}); err != nil {
		t.Fatalf("create task: %v", err)
	}

	startedAt := time.Now().UTC().Add(-2 * time.Second)
	if err := sqlStore.MarkTaskRunning(ctx, "task-1", 2, startedAt); err != nil {
		t.Fatalf("mark task running: %v", err)
	}

	finishedAt := time.Now().UTC()
	if err := sqlStore.MarkTaskCompleted(ctx, "task-1", finishedAt, "completed summary", "tasks/task-1.md"); err != nil {
		t.Fatalf("mark task completed: %v", err)
	}

	loaded, err := sqlStore.LookupTask(ctx, "task-1")
	if err != nil {
		t.Fatalf("lookup task: %v", err)
	}
	if loaded.Status != "succeeded" {
		t.Fatalf("expected succeeded task status, got %s", loaded.Status)
	}
	if loaded.Attempts != 1 {
		t.Fatalf("expected attempts=1, got %d", loaded.Attempts)
	}
	if loaded.WorkerID != 2 {
		t.Fatalf("expected worker_id=2, got %d", loaded.WorkerID)
	}
	if loaded.ResultPath != "tasks/task-1.md" {
		t.Fatalf("expected result path tasks/task-1.md, got %s", loaded.ResultPath)
	}
	if loaded.ResultSummary != "completed summary" {
		t.Fatalf("unexpected result summary: %s", loaded.ResultSummary)
	}
}

func TestTaskMarkFailed(t *testing.T) {
	sqlStore := newTestStore(t)
	ctx := context.Background()

	if err := sqlStore.CreateTask(ctx, CreateTaskInput{
		ID:          "task-2",
		WorkspaceID: "ws-1",
		ContextID:   "ctx-1",
		Kind:        "general",
		Title:       "Broken task",
		Prompt:      "run",
		Status:      "queued",
	}); err != nil {
		t.Fatalf("create task: %v", err)
	}

	if err := sqlStore.MarkTaskRunning(ctx, "task-2", 1, time.Now().UTC()); err != nil {
		t.Fatalf("mark task running: %v", err)
	}
	if err := sqlStore.MarkTaskFailed(ctx, "task-2", time.Now().UTC(), "network timeout"); err != nil {
		t.Fatalf("mark task failed: %v", err)
	}

	loaded, err := sqlStore.LookupTask(ctx, "task-2")
	if err != nil {
		t.Fatalf("lookup task: %v", err)
	}
	if loaded.Status != "failed" {
		t.Fatalf("expected failed status, got %s", loaded.Status)
	}
	if loaded.ErrorMessage != "network timeout" {
		t.Fatalf("unexpected error message: %s", loaded.ErrorMessage)
	}
}
