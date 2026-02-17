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
	"github.com/dwizi/agent-runtime/internal/agent/tools"
	"github.com/dwizi/agent-runtime/internal/config"
	"github.com/dwizi/agent-runtime/internal/gateway"
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
	executor := newTaskWorkerExecutor(tempRoot, nil, &fakeResponder{reply: "summary output"}, nil, nil, nil, config.Config{}, slog.New(slog.NewTextHandler(io.Discard, nil)))
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

	// Setup registry with RunActionTool
	registry := tools.NewRegistry()
	registry.Register(gateway.NewRunActionTool(sqlStore, nil))

	responder := &scriptedResponder{
		replies: []string{
			"I'll fetch it.\n```action\n{\"type\":\"run_command\",\"target\":\"curl\",\"summary\":\"Fetch dwizi pricing details\",\"args\":[\"-sS\",\"https://dwizi.com/pricing\"]}\n```",
			"I have queued the action for approval.",
		},
	}
	executor := newTaskWorkerExecutor(tempRoot, sqlStore, responder, nil, nil, registry, config.Config{}, slog.New(slog.NewTextHandler(io.Discard, nil)))

	_, err = executor.Execute(context.Background(), task)
	if err != nil {
		t.Fatalf("execute task: %v", err)
	}
	// The agent might not include the full approval text in the final reply if the tool output was in the history.
	// But RunActionTool output is "Action request created: ...".
	// If the agent summarizes it, good.
	// Let's check if the approval is in the store, which is the important part.
	
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

	actionExec := &fakeTaskActionExecutor{
		results: []actionexecutor.Result{
			{Plugin: "sandbox_command", Message: "The command ran successfully. Output: <html>...Products...</html>"},
			{Plugin: "sandbox_command", Message: "The command ran successfully. Output: <table><tr><td>Starter</td><td>$29/mo</td></tr><tr><td>Pro</td><td>$99/mo</td></tr></table>"},
		},
	}

	// Setup registry with CurlTool
	registry := tools.NewRegistry()
	registry.Register(gateway.NewCurlTool(sqlStore, actionExec))

	responder := &scriptedResponder{
		replies: []string{
			// Agent decides to call tool
			`{"tool":"curl","args":{"args":["-sSL","https://dwizi.com"]}}`,
			// Agent decides to call tool again
			`{"tool":"curl","args":{"args":["-sSL","https://dwizi.com/products"]}}`,
			// Agent returns final answer
			`{"final":"I found the pricing table under the products page. Current costs listed are Starter $29/month and Pro $99/month."}`,
		},
	}
	
	executor := newTaskWorkerExecutor(tempRoot, sqlStore, responder, nil, actionExec, registry, config.Config{}, slog.New(slog.NewTextHandler(io.Discard, nil)))

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

	approvals, err := sqlStore.ListPendingActionApprovals(context.Background(), "discord", "chan-1", 5)
	if err != nil {
		t.Fatalf("list pending approvals: %v", err)
	}
	if len(approvals) != 0 {
		t.Fatalf("expected no approval queue when autonomous execution is supported, got %d", len(approvals))
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

	executor := newTaskWorkerExecutor(tempRoot, nil, nil, qmdService, nil, nil, config.Config{}, slog.New(slog.NewTextHandler(io.Discard, nil)))
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

	executor := newTaskWorkerExecutor(tempRoot, nil, nil, qmdService, nil, nil, config.Config{}, slog.New(slog.NewTextHandler(io.Discard, nil)))
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
