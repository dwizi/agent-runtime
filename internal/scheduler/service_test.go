package scheduler

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/dwizi/agent-runtime/internal/orchestrator"
	"github.com/dwizi/agent-runtime/internal/store"
)

type fakeStore struct {
	dueObjectives   []store.Objective
	eventObjectives []store.Objective
	lastTask        store.CreateTaskInput
	lastRunUpdate   store.UpdateObjectiveRunInput
}

func (f *fakeStore) ListDueObjectives(ctx context.Context, now time.Time, limit int) ([]store.Objective, error) {
	return f.dueObjectives, nil
}

func (f *fakeStore) ListEventObjectives(ctx context.Context, workspaceID, eventKey string, limit int) ([]store.Objective, error) {
	return f.eventObjectives, nil
}

func (f *fakeStore) UpdateObjectiveRun(ctx context.Context, input store.UpdateObjectiveRunInput) (store.Objective, error) {
	f.lastRunUpdate = input
	return store.Objective{
		ID:        input.ID,
		LastRunAt: input.LastRunAt,
		NextRunAt: input.NextRunAt,
		LastError: input.LastError,
	}, nil
}

func (f *fakeStore) CreateTask(ctx context.Context, input store.CreateTaskInput) error {
	f.lastTask = input
	return nil
}

type fakeEngine struct {
	lastTask   orchestrator.Task
	enqueueErr error
}

func (f *fakeEngine) Enqueue(task orchestrator.Task) (orchestrator.Task, error) {
	if f.enqueueErr != nil {
		return orchestrator.Task{}, f.enqueueErr
	}
	task.ID = "task-1"
	f.lastTask = task
	return task, nil
}

func TestProcessDueQueuesObjectiveTask(t *testing.T) {
	storeMock := &fakeStore{
		dueObjectives: []store.Objective{
			{
				ID:              "obj-1",
				WorkspaceID:     "ws-1",
				ContextID:       "ctx-1",
				Title:           "Daily Review",
				Prompt:          "Review daily updates",
				IntervalSeconds: 300,
			},
		},
	}
	engineMock := &fakeEngine{}
	service := New(storeMock, engineMock, 30*time.Second, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err := service.processDue(context.Background()); err != nil {
		t.Fatalf("processDue failed: %v", err)
	}
	if engineMock.lastTask.Kind != orchestrator.TaskKindObjective {
		t.Fatalf("expected objective task kind, got %s", engineMock.lastTask.Kind)
	}
	if storeMock.lastTask.ID != "task-1" {
		t.Fatalf("expected persisted task id task-1, got %s", storeMock.lastTask.ID)
	}
	if strings.TrimSpace(storeMock.lastRunUpdate.ID) != "obj-1" {
		t.Fatalf("expected run update for obj-1, got %s", storeMock.lastRunUpdate.ID)
	}
}

func TestProcessDueWritesLastErrorOnEnqueueFailure(t *testing.T) {
	storeMock := &fakeStore{
		dueObjectives: []store.Objective{
			{
				ID:              "obj-2",
				WorkspaceID:     "ws-1",
				ContextID:       "ctx-1",
				Title:           "Daily Review",
				Prompt:          "Review daily updates",
				IntervalSeconds: 300,
			},
		},
	}
	engineMock := &fakeEngine{enqueueErr: errors.New("queue full")}
	service := New(storeMock, engineMock, 30*time.Second, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err := service.processDue(context.Background()); err != nil {
		t.Fatalf("processDue failed: %v", err)
	}
	if !strings.Contains(storeMock.lastRunUpdate.LastError, "queue full") {
		t.Fatalf("expected queue error persisted, got %s", storeMock.lastRunUpdate.LastError)
	}
}

func TestHandleMarkdownUpdateQueuesEventObjectives(t *testing.T) {
	storeMock := &fakeStore{
		eventObjectives: []store.Objective{
			{
				ID:          "obj-3",
				WorkspaceID: "ws-1",
				ContextID:   "ctx-1",
				Title:       "React to edits",
				Prompt:      "Inspect updated markdown and create follow-up tasks",
			},
		},
	}
	engineMock := &fakeEngine{}
	service := New(storeMock, engineMock, 30*time.Second, slog.New(slog.NewTextHandler(io.Discard, nil)))
	service.HandleMarkdownUpdate(context.Background(), "ws-1", "memory/notes.md")
	if engineMock.lastTask.ID != "task-1" {
		t.Fatalf("expected enqueued event task id task-1, got %s", engineMock.lastTask.ID)
	}
	if !strings.Contains(engineMock.lastTask.Prompt, "memory/notes.md") {
		t.Fatalf("expected changed path in prompt, got %s", engineMock.lastTask.Prompt)
	}
}
