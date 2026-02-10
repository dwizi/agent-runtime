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
	"github.com/carlos/spinner/internal/qmd"
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
	executor := newTaskWorkerExecutor(tempRoot, nil, &fakeResponder{reply: "summary output"}, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
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

func TestTaskWorkerExecutorQueuesActionApprovalFromTaskOutput(t *testing.T) {
	tempRoot := t.TempDir()
	dbPath := filepath.Join(t.TempDir(), "spinner.sqlite")
	sqlStore, err := store.New(dbPath)
	if err != nil {
		t.Fatalf("open test store: %v", err)
	}
	t.Cleanup(func() { _ = sqlStore.Close() })
	if err := sqlStore.AutoMigrate(context.Background()); err != nil {
		t.Fatalf("migrate store: %v", err)
	}

	task := orchestrator.Task{
		ID:          "task-action-1",
		WorkspaceID: "ws-1",
		ContextID:   "ctx-1",
		Kind:        orchestrator.TaskKindGeneral,
		Title:       "Pricing fetch",
		Prompt:      "Fetch pricing",
		CreatedAt:   time.Now().UTC(),
	}
	if err := sqlStore.CreateTask(context.Background(), store.CreateTaskInput{
		ID:               task.ID,
		WorkspaceID:      task.WorkspaceID,
		ContextID:        task.ContextID,
		Kind:             string(task.Kind),
		Title:            task.Title,
		Prompt:           task.Prompt,
		Status:           "queued",
		SourceConnector:  "discord",
		SourceExternalID: "chan-1",
		SourceUserID:     "user-1",
		SourceText:       "can you run a search in dwizi.com?",
	}); err != nil {
		t.Fatalf("create task: %v", err)
	}

	responder := &fakeResponder{
		reply: "I'll fetch it.\n```action\n{\"type\":\"run_command\",\"target\":\"curl\",\"summary\":\"Fetch dwizi pricing details\",\"args\":[\"-sS\",\"https://dwizi.com/pricing\"]}\n```",
	}
	executor := newTaskWorkerExecutor(tempRoot, sqlStore, responder, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))

	result, err := executor.Execute(context.Background(), task)
	if err != nil {
		t.Fatalf("execute task: %v", err)
	}
	if !strings.Contains(result.Summary, "pending approval") {
		t.Fatalf("expected summary to include pending approval notice, got %s", result.Summary)
	}

	approvals, err := sqlStore.ListPendingActionApprovals(context.Background(), "discord", "chan-1", 5)
	if err != nil {
		t.Fatalf("list pending approvals: %v", err)
	}
	if len(approvals) != 1 {
		t.Fatalf("expected one pending action approval, got %d", len(approvals))
	}
	if approvals[0].ActionType != "run_command" {
		t.Fatalf("expected run_command action type, got %s", approvals[0].ActionType)
	}
	if approvals[0].ActionTarget != "curl" {
		t.Fatalf("expected action target curl, got %s", approvals[0].ActionTarget)
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

func TestTaskWorkerExecutorReindexQueuesDebouncedIndex(t *testing.T) {
	tempRoot := t.TempDir()
	qmdService := qmd.New(qmd.Config{
		WorkspaceRoot: tempRoot,
		Binary:        "definitely-missing-qmd-binary",
		Debounce:      time.Hour,
		IndexTimeout:  time.Second,
		QueryTimeout:  time.Second,
		AutoEmbed:     true,
	}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	defer qmdService.Close()

	executor := newTaskWorkerExecutor(tempRoot, nil, nil, qmdService, slog.New(slog.NewTextHandler(io.Discard, nil)))
	task := orchestrator.Task{
		ID:          "task-reindex-1",
		WorkspaceID: "ws-1",
		ContextID:   "system:filewatcher",
		Kind:        orchestrator.TaskKindReindex,
		Title:       "Reindex markdown",
		Prompt:      "markdown file changed",
		CreatedAt:   time.Now().UTC(),
	}

	result, err := executor.Execute(context.Background(), task)
	if err != nil {
		t.Fatalf("execute reindex task: %v", err)
	}
	if !strings.Contains(strings.ToLower(result.Summary), "scheduled") {
		t.Fatalf("expected scheduled summary, got %q", result.Summary)
	}
}

func TestTaskWorkerExecutorReindexUsesChangedPathFromPrompt(t *testing.T) {
	tempRoot := t.TempDir()
	qmdService := qmd.New(qmd.Config{
		WorkspaceRoot: tempRoot,
		Binary:        "definitely-missing-qmd-binary",
		Debounce:      time.Hour,
		IndexTimeout:  time.Second,
		QueryTimeout:  time.Second,
		AutoEmbed:     true,
		EmbedExclude:  []string{"logs/chats/**"},
	}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	defer qmdService.Close()

	executor := newTaskWorkerExecutor(tempRoot, nil, nil, qmdService, slog.New(slog.NewTextHandler(io.Discard, nil)))
	task := orchestrator.Task{
		ID:          "task-reindex-2",
		WorkspaceID: "ws-2",
		ContextID:   "system:filewatcher",
		Kind:        orchestrator.TaskKindReindex,
		Title:       "Reindex markdown",
		Prompt:      "markdown file changed: /tmp/workspaces/ws-2/logs/chats/discord.md",
		CreatedAt:   time.Now().UTC(),
	}

	result, err := executor.Execute(context.Background(), task)
	if err != nil {
		t.Fatalf("execute reindex task: %v", err)
	}
	if !strings.Contains(strings.ToLower(result.Summary), "scheduled") {
		t.Fatalf("expected scheduled summary, got %q", result.Summary)
	}
}
