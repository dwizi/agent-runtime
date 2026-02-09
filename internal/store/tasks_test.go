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

func TestListTasksFiltersByWorkspaceAndStatus(t *testing.T) {
	sqlStore := newTestStore(t)
	ctx := context.Background()

	insert := func(id, workspaceID, status string) {
		t.Helper()
		if err := sqlStore.CreateTask(ctx, CreateTaskInput{
			ID:          id,
			WorkspaceID: workspaceID,
			ContextID:   "ctx-1",
			Kind:        "general",
			Title:       "Task " + id,
			Prompt:      "run",
			Status:      status,
		}); err != nil {
			t.Fatalf("create task %s: %v", id, err)
		}
	}
	insert("task-a", "ws-1", "queued")
	insert("task-b", "ws-1", "queued")
	insert("task-c", "ws-2", "queued")
	if err := sqlStore.MarkTaskRunning(ctx, "task-b", 1, time.Now().UTC()); err != nil {
		t.Fatalf("mark task running: %v", err)
	}
	if err := sqlStore.MarkTaskFailed(ctx, "task-b", time.Now().UTC(), "boom"); err != nil {
		t.Fatalf("mark task failed: %v", err)
	}

	items, err := sqlStore.ListTasks(ctx, ListTasksInput{
		WorkspaceID: "ws-1",
		Status:      "failed",
		Limit:       10,
	})
	if err != nil {
		t.Fatalf("list tasks: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("expected 1 failed task, got %d", len(items))
	}
	if items[0].ID != "task-b" {
		t.Fatalf("expected task-b, got %s", items[0].ID)
	}
}

func TestTaskRoutingMetadataPersistAndUpdate(t *testing.T) {
	sqlStore := newTestStore(t)
	ctx := context.Background()
	dueAt := time.Now().UTC().Add(6 * time.Hour)

	if err := sqlStore.CreateTask(ctx, CreateTaskInput{
		ID:               "task-route",
		WorkspaceID:      "ws-1",
		ContextID:        "ctx-1",
		Kind:             "general",
		Title:            "Route me",
		Prompt:           "follow up",
		Status:           "queued",
		RouteClass:       "issue",
		Priority:         "p2",
		DueAt:            dueAt,
		AssignedLane:     "operations",
		SourceConnector:  "telegram",
		SourceExternalID: "42",
		SourceUserID:     "u1",
		SourceText:       "this is broken",
	}); err != nil {
		t.Fatalf("create task: %v", err)
	}

	loaded, err := sqlStore.LookupTask(ctx, "task-route")
	if err != nil {
		t.Fatalf("lookup task: %v", err)
	}
	if loaded.RouteClass != "issue" {
		t.Fatalf("expected route class issue, got %s", loaded.RouteClass)
	}
	if loaded.Priority != "p2" {
		t.Fatalf("expected priority p2, got %s", loaded.Priority)
	}
	if loaded.AssignedLane != "operations" {
		t.Fatalf("expected lane operations, got %s", loaded.AssignedLane)
	}
	if loaded.DueAt.IsZero() {
		t.Fatal("expected due date to be set")
	}
	if loaded.SourceConnector != "telegram" {
		t.Fatalf("expected source connector telegram, got %s", loaded.SourceConnector)
	}

	updated, err := sqlStore.UpdateTaskRouting(ctx, UpdateTaskRoutingInput{
		ID:           "task-route",
		RouteClass:   "moderation",
		Priority:     "p1",
		DueAt:        time.Now().UTC().Add(2 * time.Hour),
		AssignedLane: "moderation",
	})
	if err != nil {
		t.Fatalf("update task routing: %v", err)
	}
	if updated.RouteClass != "moderation" {
		t.Fatalf("expected moderation route class, got %s", updated.RouteClass)
	}
	if updated.Priority != "p1" {
		t.Fatalf("expected p1 priority, got %s", updated.Priority)
	}
	if updated.AssignedLane != "moderation" {
		t.Fatalf("expected moderation lane, got %s", updated.AssignedLane)
	}
}
