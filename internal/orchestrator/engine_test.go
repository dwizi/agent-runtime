package orchestrator

import (
	"io"
	"log/slog"
	"testing"
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
