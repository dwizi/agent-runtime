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
	createTaskErr   error
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
	if f.createTaskErr != nil {
		return f.createTaskErr
	}
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
	if strings.TrimSpace(task.ID) == "" {
		task.ID = "task-1"
	}
	f.lastTask = task
	return task, nil
}

func TestProcessDueQueuesObjectiveTask(t *testing.T) {
	storeMock := &fakeStore{
		dueObjectives: []store.Objective{
			{
				ID:          "obj-1",
				WorkspaceID: "ws-1",
				ContextID:   "ctx-1",
				Title:       "Daily Review",
				Prompt:      "Review daily updates",
				TriggerType: store.ObjectiveTriggerSchedule,
				CronExpr:    "* * * * *",
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
	if strings.TrimSpace(storeMock.lastTask.ID) == "" {
		t.Fatal("expected persisted task id")
	}
	if strings.TrimSpace(storeMock.lastTask.RunKey) == "" {
		t.Fatal("expected objective schedule run key")
	}
	if strings.TrimSpace(storeMock.lastRunUpdate.ID) != "obj-1" {
		t.Fatalf("expected run update for obj-1, got %s", storeMock.lastRunUpdate.ID)
	}
}

func TestProcessDueWritesLastErrorOnEnqueueFailure(t *testing.T) {
	storeMock := &fakeStore{
		dueObjectives: []store.Objective{
			{
				ID:          "obj-2",
				WorkspaceID: "ws-1",
				ContextID:   "ctx-1",
				Title:       "Daily Review",
				Prompt:      "Review daily updates",
				TriggerType: store.ObjectiveTriggerSchedule,
				CronExpr:    "* * * * *",
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
				TriggerType: store.ObjectiveTriggerEvent,
			},
		},
	}
	engineMock := &fakeEngine{}
	service := New(storeMock, engineMock, 30*time.Second, slog.New(slog.NewTextHandler(io.Discard, nil)))
	service.HandleMarkdownUpdate(context.Background(), "ws-1", "memory/notes.md")
	if strings.TrimSpace(engineMock.lastTask.ID) == "" {
		t.Fatalf("expected enqueued event task id, got empty")
	}
	if !strings.Contains(engineMock.lastTask.Prompt, "memory/notes.md") {
		t.Fatalf("expected changed path in prompt, got %s", engineMock.lastTask.Prompt)
	}
	if !strings.Contains(storeMock.lastTask.RunKey, ":event:") {
		t.Fatalf("expected event run key for objective task, got %s", storeMock.lastTask.RunKey)
	}
	if strings.TrimSpace(storeMock.lastRunUpdate.ID) != "obj-3" {
		t.Fatalf("expected run update for obj-3, got %s", storeMock.lastRunUpdate.ID)
	}
}

func TestProcessDueTreatsDuplicateRunAsIdempotent(t *testing.T) {
	storeMock := &fakeStore{
		dueObjectives: []store.Objective{
			{
				ID:          "obj-dup",
				TriggerType: store.ObjectiveTriggerSchedule,
				CronExpr:    "* * * * *",
				Prompt:      "run",
				NextRunAt:   time.Now().UTC().Add(-time.Minute),
			},
		},
		createTaskErr: store.ErrTaskRunAlreadyExists,
	}
	engineMock := &fakeEngine{}
	service := New(storeMock, engineMock, 30*time.Second, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err := service.processDue(context.Background()); err != nil {
		t.Fatalf("processDue failed: %v", err)
	}
	if strings.TrimSpace(engineMock.lastTask.ID) != "" {
		t.Fatalf("expected duplicate run to skip enqueue, got task id %s", engineMock.lastTask.ID)
	}
	if strings.TrimSpace(storeMock.lastRunUpdate.LastError) != "" {
		t.Fatalf("expected duplicate run to leave last_error empty, got %q", storeMock.lastRunUpdate.LastError)
	}
}

func TestProcessDueAutoPausesAfterConsecutiveFailures(t *testing.T) {
	storeMock := &fakeStore{
		dueObjectives: []store.Objective{
			{
				ID:                  "obj-pause",
				WorkspaceID:         "ws-1",
				ContextID:           "ctx-1",
				TriggerType:         store.ObjectiveTriggerSchedule,
				CronExpr:            "* * * * *",
				Prompt:              "run",
				NextRunAt:           time.Now().UTC().Add(-time.Minute),
				ConsecutiveFailures: 4,
			},
		},
	}
	engineMock := &fakeEngine{enqueueErr: errors.New("boom")}
	service := New(storeMock, engineMock, 30*time.Second, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err := service.processDue(context.Background()); err != nil {
		t.Fatalf("processDue failed: %v", err)
	}
	if storeMock.lastRunUpdate.Active == nil || *storeMock.lastRunUpdate.Active {
		t.Fatalf("expected objective to be auto-paused after repeated failures")
	}
	if storeMock.lastRunUpdate.AutoPausedReason == nil || strings.TrimSpace(*storeMock.lastRunUpdate.AutoPausedReason) == "" {
		t.Fatalf("expected auto pause reason to be recorded")
	}
}

func TestProcessDueBacksOffAfterFailure(t *testing.T) {
	storeMock := &fakeStore{
		dueObjectives: []store.Objective{
			{
				ID:                  "obj-backoff",
				WorkspaceID:         "ws-1",
				ContextID:           "ctx-1",
				TriggerType:         store.ObjectiveTriggerSchedule,
				CronExpr:            "* * * * *",
				Prompt:              "run",
				NextRunAt:           time.Now().UTC().Add(-time.Minute),
				ConsecutiveFailures: 2,
			},
		},
	}
	engineMock := &fakeEngine{enqueueErr: errors.New("queue full")}
	service := New(storeMock, engineMock, 30*time.Second, slog.New(slog.NewTextHandler(io.Discard, nil)))
	started := time.Now().UTC()
	if err := service.processDue(context.Background()); err != nil {
		t.Fatalf("processDue failed: %v", err)
	}
	minNext := started.Add(time.Minute)
	if storeMock.lastRunUpdate.NextRunAt.Before(minNext) {
		t.Fatalf("expected failure backoff to delay next run, got %s", storeMock.lastRunUpdate.NextRunAt)
	}
}
