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

	actionexecutor "github.com/dwizi/agent-runtime/internal/actions/executor"
	"github.com/dwizi/agent-runtime/internal/llm"
	"github.com/dwizi/agent-runtime/internal/orchestrator"
	"github.com/dwizi/agent-runtime/internal/qmd"
	"github.com/dwizi/agent-runtime/internal/store"
)

type fakeResponder struct {
	reply      string
	err        error
	lastInput  llm.MessageInput
	replyCount int
}

func (f *fakeResponder) Reply(ctx context.Context, input llm.MessageInput) (string, error) {
	f.lastInput = input
	f.replyCount++
	if f.err != nil {
		return "", f.err
	}
	return f.reply, nil
}

type scriptedResponder struct {
	replies []string
	index   int
}

func (s *scriptedResponder) Reply(ctx context.Context, input llm.MessageInput) (string, error) {
	if s.index >= len(s.replies) {
		return "", nil
	}
	reply := s.replies[s.index]
	s.index++
	return reply, nil
}

type fakeTaskActionExecutor struct {
	calls   []store.ActionApproval
	results []actionexecutor.Result
}

func (f *fakeTaskActionExecutor) Execute(ctx context.Context, approval store.ActionApproval) (actionexecutor.Result, error) {
	f.calls = append(f.calls, approval)
	if len(f.results) == 0 {
		return actionexecutor.Result{}, nil
	}
	result := f.results[0]
	f.results = f.results[1:]
	return result, nil
}

func TestTaskWorkerExecutorWritesArtifact(t *testing.T) {
	tempRoot := t.TempDir()
	executor := newTaskWorkerExecutor(tempRoot, nil, &fakeResponder{reply: "summary output"}, nil, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
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
	dbPath := filepath.Join(t.TempDir(), "agent-runtime.sqlite")
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
	executor := newTaskWorkerExecutor(tempRoot, sqlStore, responder, nil, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))

	result, err := executor.Execute(context.Background(), task)
	if err != nil {
		t.Fatalf("execute task: %v", err)
	}
	if !strings.Contains(result.Summary, "Admin approval required.") {
		t.Fatalf("expected summary to include compact approval notice, got %s", result.Summary)
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

func TestTaskWorkerExecutorGeneralTaskSkipsGrounding(t *testing.T) {
	tempRoot := t.TempDir()
	responder := &fakeResponder{reply: "done"}
	executor := newTaskWorkerExecutor(tempRoot, nil, responder, nil, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	task := orchestrator.Task{
		ID:          "task-skip-grounding-1",
		WorkspaceID: "ws-1",
		ContextID:   "ctx-1",
		Kind:        orchestrator.TaskKindGeneral,
		Title:       "Quick question",
		Prompt:      "Can you check pricing?",
		CreatedAt:   time.Now().UTC(),
	}

	if _, err := executor.Execute(context.Background(), task); err != nil {
		t.Fatalf("execute task: %v", err)
	}
	if responder.replyCount != 1 {
		t.Fatalf("expected one responder call, got %d", responder.replyCount)
	}
	if !responder.lastInput.SkipGrounding {
		t.Fatal("expected general task responder input to skip grounding")
	}
}

func TestTaskWorkerExecutorRunsAutonomousCurlLoopForRoutedTask(t *testing.T) {
	tempRoot := t.TempDir()
	dbPath := filepath.Join(t.TempDir(), "agent-runtime.sqlite")
	sqlStore, err := store.New(dbPath)
	if err != nil {
		t.Fatalf("open test store: %v", err)
	}
	t.Cleanup(func() { _ = sqlStore.Close() })
	if err := sqlStore.AutoMigrate(context.Background()); err != nil {
		t.Fatalf("migrate store: %v", err)
	}

	task := orchestrator.Task{
		ID:          "task-auto-1",
		WorkspaceID: "ws-1",
		ContextID:   "ctx-1",
		Kind:        orchestrator.TaskKindGeneral,
		Title:       "Find pricing",
		Prompt:      "Can you find pricing for dwizi.com?",
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
		RouteClass:       "question",
		Priority:         "p3",
		AssignedLane:     "support",
		SourceConnector:  "discord",
		SourceExternalID: "chan-1",
		SourceUserID:     "user-1",
		SourceText:       "Can you find pricing for dwizi.com?",
	}); err != nil {
		t.Fatalf("create task: %v", err)
	}

	responder := &scriptedResponder{
		replies: []string{
			"I will check the homepage first.\n```action\n{\"type\":\"run_command\",\"target\":\"curl\",\"summary\":\"Fetch dwizi home page\",\"args\":[\"-sSL\",\"https://dwizi.com\"]}\n```",
			"I found a products link, checking it now.\n```action\n{\"type\":\"run_command\",\"target\":\"curl\",\"summary\":\"Fetch products page\",\"args\":[\"-sSL\",\"https://dwizi.com/products\"]}\n```",
			"I found the pricing table under the products page. Current costs listed are Starter $29/month and Pro $99/month.",
		},
	}
	actionExec := &fakeTaskActionExecutor{
		results: []actionexecutor.Result{
			{Plugin: "sandbox_command", Message: "The command ran successfully. Output: <html>...Products...</html>"},
			{Plugin: "sandbox_command", Message: "The command ran successfully. Output: <table><tr><td>Starter</td><td>$29/mo</td></tr><tr><td>Pro</td><td>$99/mo</td></tr></table>"},
		},
	}
	executor := newTaskWorkerExecutor(tempRoot, sqlStore, responder, nil, actionExec, slog.New(slog.NewTextHandler(io.Discard, nil)))

	result, err := executor.Execute(context.Background(), task)
	if err != nil {
		t.Fatalf("execute task: %v", err)
	}
	if len(actionExec.calls) != 2 {
		t.Fatalf("expected 2 autonomous command executions, got %d", len(actionExec.calls))
	}
	if !strings.Contains(strings.ToLower(result.Summary), "pricing table") {
		t.Fatalf("expected natural language pricing summary, got %q", result.Summary)
	}
	if len(result.Summary) < 40 {
		t.Fatalf("expected routed summary to preserve answer details, got %q", result.Summary)
	}

	approvals, err := sqlStore.ListPendingActionApprovals(context.Background(), "discord", "chan-1", 5)
	if err != nil {
		t.Fatalf("list pending approvals: %v", err)
	}
	if len(approvals) != 0 {
		t.Fatalf("expected no approval queue when autonomous execution is supported, got %d", len(approvals))
	}
}

func TestTaskWorkerExecutorAutonomousFinalSummaryUsesExecutionEvidence(t *testing.T) {
	tempRoot := t.TempDir()
	dbPath := filepath.Join(t.TempDir(), "agent-runtime.sqlite")
	sqlStore, err := store.New(dbPath)
	if err != nil {
		t.Fatalf("open test store: %v", err)
	}
	t.Cleanup(func() { _ = sqlStore.Close() })
	if err := sqlStore.AutoMigrate(context.Background()); err != nil {
		t.Fatalf("migrate store: %v", err)
	}

	task := orchestrator.Task{
		ID:          "task-auto-summary-1",
		WorkspaceID: "ws-1",
		ContextID:   "ctx-1",
		Kind:        orchestrator.TaskKindGeneral,
		Title:       "Service status check",
		Prompt:      "Check service health endpoint",
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
		RouteClass:       "question",
		Priority:         "p3",
		AssignedLane:     "support",
		SourceConnector:  "discord",
		SourceExternalID: "chan-1",
		SourceUserID:     "user-1",
		SourceText:       "Can you check current service status?",
	}); err != nil {
		t.Fatalf("create task: %v", err)
	}

	responder := &scriptedResponder{
		replies: []string{
			"Running a quick check.\n```action\n{\"type\":\"run_command\",\"target\":\"curl\",\"summary\":\"Check health\",\"args\":[\"-sSL\",\"https://example.com/health\"]}\n```",
			"I checked the endpoint and it currently reports a 503 status with maintenance notice.",
		},
	}
	actionExec := &fakeTaskActionExecutor{
		results: []actionexecutor.Result{
			{Plugin: "sandbox_command", Message: "The command ran successfully. Output: {\"status\":503,\"message\":\"maintenance\"}"},
		},
	}
	executor := newTaskWorkerExecutor(tempRoot, sqlStore, responder, nil, actionExec, slog.New(slog.NewTextHandler(io.Discard, nil)))
	executor.maxAutonomousSteps = 1

	result, err := executor.Execute(context.Background(), task)
	if err != nil {
		t.Fatalf("execute task: %v", err)
	}
	if !strings.Contains(strings.ToLower(result.Summary), "503") {
		t.Fatalf("expected summary to include evidence-backed status, got %q", result.Summary)
	}
	if strings.Contains(strings.ToLower(result.Summary), "pricing details") {
		t.Fatalf("summary should not include unrelated hardcoded text, got %q", result.Summary)
	}
}

func TestTaskWorkerExecutorAutonomousFallbackSummaryIsGeneric(t *testing.T) {
	tempRoot := t.TempDir()
	dbPath := filepath.Join(t.TempDir(), "agent-runtime.sqlite")
	sqlStore, err := store.New(dbPath)
	if err != nil {
		t.Fatalf("open test store: %v", err)
	}
	t.Cleanup(func() { _ = sqlStore.Close() })
	if err := sqlStore.AutoMigrate(context.Background()); err != nil {
		t.Fatalf("migrate store: %v", err)
	}

	task := orchestrator.Task{
		ID:          "task-auto-summary-2",
		WorkspaceID: "ws-1",
		ContextID:   "ctx-1",
		Kind:        orchestrator.TaskKindGeneral,
		Title:       "Lookup documentation",
		Prompt:      "Find docs about API limits",
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
		RouteClass:       "question",
		Priority:         "p3",
		AssignedLane:     "support",
		SourceConnector:  "discord",
		SourceExternalID: "chan-1",
		SourceUserID:     "user-1",
		SourceText:       "Can you find API limits docs?",
	}); err != nil {
		t.Fatalf("create task: %v", err)
	}

	responder := &scriptedResponder{
		replies: []string{
			"Searching docs site now.\n```action\n{\"type\":\"run_command\",\"target\":\"curl\",\"summary\":\"Fetch docs\",\"args\":[\"-sSL\",\"https://example.com/docs\"]}\n```",
		},
	}
	actionExec := &fakeTaskActionExecutor{
		results: []actionexecutor.Result{
			{Plugin: "sandbox_command", Message: "The command ran successfully. Output: <html>Docs index</html>"},
		},
	}
	executor := newTaskWorkerExecutor(tempRoot, sqlStore, responder, nil, actionExec, slog.New(slog.NewTextHandler(io.Discard, nil)))
	executor.maxAutonomousSteps = 1

	result, err := executor.Execute(context.Background(), task)
	if err != nil {
		t.Fatalf("execute task: %v", err)
	}
	if !strings.Contains(strings.ToLower(result.Summary), "automated retrieval steps") {
		t.Fatalf("expected generic fallback summary, got %q", result.Summary)
	}
	if strings.Contains(strings.ToLower(result.Summary), "pricing details") {
		t.Fatalf("fallback summary should not be hardcoded to pricing, got %q", result.Summary)
	}
}

func TestTaskObserverPersistsLifecycle(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "agent-runtime.sqlite")
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

func TestTaskWorkerExecutorReindexSkipsDuplicateQueueForFileWatcherContext(t *testing.T) {
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

	executor := newTaskWorkerExecutor(tempRoot, nil, nil, qmdService, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
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
	if !strings.Contains(strings.ToLower(result.Summary), "already queued") {
		t.Fatalf("expected already queued summary, got %q", result.Summary)
	}
}

func TestTaskWorkerExecutorReindexUsesChangedPathFromPromptForManualTask(t *testing.T) {
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

	executor := newTaskWorkerExecutor(tempRoot, nil, nil, qmdService, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	task := orchestrator.Task{
		ID:          "task-reindex-2",
		WorkspaceID: "ws-2",
		ContextID:   "system:manual",
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
