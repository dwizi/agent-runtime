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

	"github.com/carlos/spinner/internal/actions"
	"github.com/carlos/spinner/internal/llm"
	"github.com/carlos/spinner/internal/orchestrator"
	"github.com/carlos/spinner/internal/qmd"
	"github.com/carlos/spinner/internal/store"
)

type taskWorkerExecutor struct {
	workspaceRoot string
	store         *store.Store
	responder     llm.Responder
	qmd           *qmd.Service
	logger        *slog.Logger
}

func newTaskWorkerExecutor(workspaceRoot string, storeRef *store.Store, responder llm.Responder, qmdService *qmd.Service, logger *slog.Logger) *taskWorkerExecutor {
	if logger == nil {
		logger = slog.Default()
	}
	return &taskWorkerExecutor{
		workspaceRoot: strings.TrimSpace(workspaceRoot),
		store:         storeRef,
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
	const prefix = "markdown file changed:"
	trimmedPrompt := strings.TrimSpace(task.Prompt)
	if strings.HasPrefix(strings.ToLower(trimmedPrompt), prefix) {
		changedPath := strings.TrimSpace(trimmedPrompt[len(prefix):])
		e.qmd.QueueWorkspaceIndexForPath(workspaceID, changedPath)
	} else {
		// Fallback when path metadata is unavailable.
		e.qmd.QueueWorkspaceIndex(workspaceID)
	}
	return orchestrator.TaskResult{
		Summary: fmt.Sprintf("workspace `%s` reindex scheduled", workspaceID),
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
	reply = e.resolveActionProposal(ctx, task, reply)
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

func (e *taskWorkerExecutor) resolveActionProposal(ctx context.Context, task orchestrator.Task, reply string) string {
	cleanReply, proposal := actions.ExtractProposal(strings.TrimSpace(reply))
	if proposal == nil {
		return strings.TrimSpace(cleanReply)
	}
	if e.store == nil {
		if strings.TrimSpace(cleanReply) == "" {
			return "Action request generated but approvals storage is unavailable."
		}
		return strings.TrimSpace(cleanReply)
	}
	taskRecord, err := e.store.LookupTask(ctx, task.ID)
	if err != nil {
		e.logger.Error("lookup task for action approval failed", "task_id", task.ID, "error", err)
		if strings.TrimSpace(cleanReply) == "" {
			return "Action request generated but could not be linked to a channel."
		}
		return strings.TrimSpace(cleanReply)
	}
	connector := strings.TrimSpace(taskRecord.SourceConnector)
	externalID := strings.TrimSpace(taskRecord.SourceExternalID)
	requesterUserID := strings.TrimSpace(taskRecord.SourceUserID)
	if connector == "" || externalID == "" || requesterUserID == "" {
		if strings.TrimSpace(cleanReply) == "" {
			return "Action request generated but this task has no linked source channel for approval."
		}
		return strings.TrimSpace(cleanReply)
	}
	approval, err := e.store.CreateActionApproval(ctx, store.CreateActionApprovalInput{
		WorkspaceID:     task.WorkspaceID,
		ContextID:       task.ContextID,
		Connector:       connector,
		ExternalID:      externalID,
		RequesterUserID: requesterUserID,
		ActionType:      proposal.Type,
		ActionTarget:    proposal.Target,
		ActionSummary:   proposal.Summary,
		Payload:         proposal.Raw,
	})
	if err != nil {
		e.logger.Error("create action approval from task output failed", "task_id", task.ID, "error", err)
		if strings.TrimSpace(cleanReply) == "" {
			return "Action request could not be queued for approval."
		}
		return strings.TrimSpace(cleanReply)
	}
	notice := fmt.Sprintf("Action request pending approval: `%s`. Admin can run `/pending-actions`, `/approve-action %s`, or `/deny-action %s`.", approval.ID, approval.ID, approval.ID)
	if strings.TrimSpace(cleanReply) == "" {
		return notice
	}
	return notice + "\n\n" + strings.TrimSpace(cleanReply)
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
