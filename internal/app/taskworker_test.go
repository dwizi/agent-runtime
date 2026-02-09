package app

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/carlos/spinner/internal/llm"
	"github.com/carlos/spinner/internal/orchestrator"
	"github.com/carlos/spinner/internal/store"
)

type fakeResponder struct {
	reply string
	err   error
}

func (f *fakeResponder) Reply(ctx context.Context, input llm.MessageInput) (string, error) {
	if f.err != nil {
		return "", f.err
	}
	return f.reply, nil
}

func TestTaskWorkerExecutorWritesArtifact(t *testing.T) {
	tempRoot := t.TempDir()
	executor := newTaskWorkerExecutor(tempRoot, &fakeResponder{reply: "summary output"}, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	task := orchestrator.Task{
		ID:          "task-1",
		WorkspaceID: "ws-1",
		ContextID:   "ctx-1",
		Kind:        orchestrator.TaskKindGeneral,
		Title:       "Review",
		Prompt:      "Analyze markdown changes",
		CreatedAt:   time.Now().UTC(),
	}

	result, err := executor.Execute(context.Background(), task)
	if err != nil {
		t.Fatalf("execute task: %v", err)
	}
	if strings.TrimSpace(result.Summary) == "" {
		t.Fatal("expected task summary")
	}
	if strings.TrimSpace(result.ArtifactPath) == "" {
		t.Fatal("expected artifact path")
	}
	absolutePath := filepath.Join(tempRoot, task.WorkspaceID, filepath.FromSlash(result.ArtifactPath))
	content, err := os.ReadFile(absolutePath)
	if err != nil {
		t.Fatalf("read task artifact: %v", err)
	}
	if !strings.Contains(string(content), "summary output") {
		t.Fatalf("expected artifact to include responder output, got: %s", string(content))
	}
}

func TestTaskObserverPersistsLifecycle(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "spinner.sqlite")
	sqlStore, err := store.New(dbPath)
	if err != nil {
		t.Fatalf("open test store: %v", err)
	}
	t.Cleanup(func() { _ = sqlStore.Close() })
	if err := sqlStore.AutoMigrate(context.Background()); err != nil {
		t.Fatalf("migrate store: %v", err)
	}

	observer := newTaskObserver(sqlStore, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	task := orchestrator.Task{
		ID:          "task-2",
		WorkspaceID: "ws-1",
		ContextID:   "ctx-1",
		Kind:        orchestrator.TaskKindObjective,
		Title:       "Objective",
		Prompt:      "Do objective",
		CreatedAt:   time.Now().UTC(),
	}

	if err := sqlStore.CreateTask(context.Background(), store.CreateTaskInput{
		ID:          task.ID,
		WorkspaceID: task.WorkspaceID,
		ContextID:   task.ContextID,
		Kind:        string(task.Kind),
		Title:       task.Title,
		Prompt:      task.Prompt,
		Status:      "queued",
	}); err != nil {
		t.Fatalf("create task: %v", err)
	}
	observer.OnTaskStarted(task, 3)
	observer.OnTaskCompleted(task, 3, orchestrator.TaskResult{
		Summary:      "done",
		ArtifactPath: "tasks/task-2.md",
	})

	record, err := sqlStore.LookupTask(context.Background(), task.ID)
	if err != nil {
		t.Fatalf("lookup task: %v", err)
	}
	if record.Status != "succeeded" {
		t.Fatalf("expected succeeded status, got %s", record.Status)
	}
	if record.Attempts != 1 {
		t.Fatalf("expected attempts=1, got %d", record.Attempts)
	}
	if record.ResultPath != "tasks/task-2.md" {
		t.Fatalf("unexpected result path: %s", record.ResultPath)
	}
}
