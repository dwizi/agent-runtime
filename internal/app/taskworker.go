package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/carlos/spinner/internal/llm"
	"github.com/carlos/spinner/internal/orchestrator"
	"github.com/carlos/spinner/internal/qmd"
	"github.com/carlos/spinner/internal/store"
)

type taskWorkerExecutor struct {
	workspaceRoot string
	responder     llm.Responder
	qmd           *qmd.Service
	logger        *slog.Logger
}

func newTaskWorkerExecutor(workspaceRoot string, responder llm.Responder, qmdService *qmd.Service, logger *slog.Logger) *taskWorkerExecutor {
	if logger == nil {
		logger = slog.Default()
	}
	return &taskWorkerExecutor{
		workspaceRoot: strings.TrimSpace(workspaceRoot),
		responder:     responder,
		qmd:           qmdService,
		logger:        logger,
	}
}

func (e *taskWorkerExecutor) Execute(ctx context.Context, task orchestrator.Task) (orchestrator.TaskResult, error) {
	switch task.Kind {
	case orchestrator.TaskKindReindex:
		return e.executeReindex(ctx, task)
	case orchestrator.TaskKindGeneral, orchestrator.TaskKindObjective:
		return e.executeLLMTask(ctx, task)
	default:
		return orchestrator.TaskResult{}, fmt.Errorf("unsupported task kind: %s", task.Kind)
	}
}

func (e *taskWorkerExecutor) executeReindex(ctx context.Context, task orchestrator.Task) (orchestrator.TaskResult, error) {
	if e.qmd == nil {
		return orchestrator.TaskResult{Summary: "qmd indexing skipped: service unavailable"}, nil
	}
	workspaceID := strings.TrimSpace(task.WorkspaceID)
	if workspaceID == "" {
		return orchestrator.TaskResult{}, fmt.Errorf("workspace id is required for reindex")
	}
	if err := e.qmd.IndexWorkspace(ctx, workspaceID); err != nil {
		return orchestrator.TaskResult{}, err
	}
	return orchestrator.TaskResult{
		Summary: fmt.Sprintf("workspace `%s` indexed", workspaceID),
	}, nil
}

func (e *taskWorkerExecutor) executeLLMTask(ctx context.Context, task orchestrator.Task) (orchestrator.TaskResult, error) {
	if e.responder == nil {
		return orchestrator.TaskResult{
			Summary: "task skipped: llm responder unavailable",
		}, nil
	}
	prompt := strings.TrimSpace(task.Prompt)
	if prompt == "" {
		prompt = strings.TrimSpace(task.Title)
	}
	if prompt == "" {
		return orchestrator.TaskResult{}, fmt.Errorf("task prompt is empty")
	}

	reply, err := e.responder.Reply(ctx, llm.MessageInput{
		Connector:   "orchestrator",
		WorkspaceID: task.WorkspaceID,
		ContextID:   task.ContextID,
		ExternalID:  task.ContextID,
		DisplayName: task.Title,
		FromUserID:  "system:task-worker",
		Text:        prompt,
		IsDM:        false,
	})
	if err != nil {
		return orchestrator.TaskResult{}, err
	}
	reply = strings.TrimSpace(reply)
	if reply == "" {
		reply = "No output produced."
	}

	resultPath, err := e.writeTaskResult(task, reply)
	if err != nil {
		return orchestrator.TaskResult{}, err
	}
	if e.qmd != nil && strings.TrimSpace(task.WorkspaceID) != "" {
		e.qmd.QueueWorkspaceIndex(task.WorkspaceID)
	}
	return orchestrator.TaskResult{
		Summary:      summarizeTaskReply(reply),
		ArtifactPath: resultPath,
	}, nil
}

func (e *taskWorkerExecutor) writeTaskResult(task orchestrator.Task, reply string) (string, error) {
	workspaceID := strings.TrimSpace(task.WorkspaceID)
	if workspaceID == "" || e.workspaceRoot == "" {
		return "", nil
	}
	now := time.Now().UTC()
	relativePath := filepath.ToSlash(filepath.Join("tasks", now.Format("2006"), now.Format("01"), now.Format("02"), task.ID+".md"))
	absolutePath := filepath.Join(e.workspaceRoot, workspaceID, filepath.FromSlash(relativePath))
	if err := os.MkdirAll(filepath.Dir(absolutePath), 0o755); err != nil {
		return "", fmt.Errorf("create task artifact directory: %w", err)
	}
	content := buildTaskMarkdown(task, now, reply)
	if err := os.WriteFile(absolutePath, []byte(content), 0o644); err != nil {
		return "", fmt.Errorf("write task artifact: %w", err)
	}
	return relativePath, nil
}

func buildTaskMarkdown(task orchestrator.Task, now time.Time, reply string) string {
	var builder strings.Builder
	builder.WriteString("# Task Result\n\n")
	builder.WriteString("- ID: `" + strings.TrimSpace(task.ID) + "`\n")
	builder.WriteString("- Kind: `" + string(task.Kind) + "`\n")
	builder.WriteString("- Workspace: `" + strings.TrimSpace(task.WorkspaceID) + "`\n")
	builder.WriteString("- Context: `" + strings.TrimSpace(task.ContextID) + "`\n")
	builder.WriteString("- Title: " + strings.TrimSpace(task.Title) + "\n")
	builder.WriteString("- Completed At (UTC): " + now.Format(time.RFC3339) + "\n\n")
	builder.WriteString("## Prompt\n\n")
	builder.WriteString(strings.TrimSpace(task.Prompt))
	builder.WriteString("\n\n## Output\n\n")
	builder.WriteString(reply)
	builder.WriteString("\n")
	return builder.String()
}

func summarizeTaskReply(reply string) string {
	text := strings.Join(strings.Fields(strings.TrimSpace(reply)), " ")
	if len(text) == 0 {
		return "task completed"
	}
	if len(text) > 180 {
		return text[:180] + "..."
	}
	return text
}

type taskObserver struct {
	store    *store.Store
	notifier *taskCompletionNotifier
	logger   *slog.Logger
}

func newTaskObserver(storeRef *store.Store, notifier *taskCompletionNotifier, logger *slog.Logger) *taskObserver {
	if logger == nil {
		logger = slog.Default()
	}
	return &taskObserver{
		store:    storeRef,
		notifier: notifier,
		logger:   logger,
	}
}

func (o *taskObserver) OnTaskQueued(task orchestrator.Task) {
	// Queued task records are persisted by enqueue callers.
	// Observer handles lifecycle transitions once execution starts.
}

func (o *taskObserver) OnTaskStarted(task orchestrator.Task, workerID int) {
	if o.store == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := o.store.MarkTaskRunning(ctx, task.ID, workerID, time.Now().UTC()); err != nil && !errorsIsTaskNotFound(err) {
		o.logger.Error("mark task running failed", "task_id", task.ID, "error", err)
	}
}

func (o *taskObserver) OnTaskCompleted(task orchestrator.Task, workerID int, result orchestrator.TaskResult) {
	if o.store == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := o.store.MarkTaskCompleted(ctx, task.ID, time.Now().UTC(), result.Summary, result.ArtifactPath); err != nil && !errorsIsTaskNotFound(err) {
		o.logger.Error("mark task completed failed", "task_id", task.ID, "error", err)
	}
	if o.notifier != nil {
		o.notifier.NotifyCompleted(task, result)
	}
}

func (o *taskObserver) OnTaskFailed(task orchestrator.Task, workerID int, err error) {
	if o.store == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	message := ""
	if err != nil {
		message = err.Error()
	}
	if updateErr := o.store.MarkTaskFailed(ctx, task.ID, time.Now().UTC(), message); updateErr != nil && !errorsIsTaskNotFound(updateErr) {
		o.logger.Error("mark task failed failed", "task_id", task.ID, "error", updateErr)
	}
	if o.notifier != nil {
		o.notifier.NotifyFailed(task, err)
	}
}

func errorsIsTaskNotFound(err error) bool {
	return errors.Is(err, store.ErrTaskNotFound)
}
