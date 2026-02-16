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

	"github.com/dwizi/agent-runtime/internal/actions"
	actionexecutor "github.com/dwizi/agent-runtime/internal/actions/executor"
	"github.com/dwizi/agent-runtime/internal/llm"
	"github.com/dwizi/agent-runtime/internal/orchestrator"
	"github.com/dwizi/agent-runtime/internal/qmd"
	"github.com/dwizi/agent-runtime/internal/store"
)

const (
	defaultAutonomousTaskMaxSteps = 4
	autonomousObservationMaxBytes = 1200
	autonomousPromptMaxBytes      = 7000
	autonomousSummaryMaxBytes     = 1800
	autonomousSummaryPromptBytes  = 7000
)

type taskActionExecutor interface {
	Execute(ctx context.Context, approval store.ActionApproval) (actionexecutor.Result, error)
}

type taskWorkerExecutor struct {
	workspaceRoot      string
	store              *store.Store
	responder          llm.Responder
	qmd                *qmd.Service
	actionExecutor     taskActionExecutor
	maxAutonomousSteps int
	logger             *slog.Logger
}

func newTaskWorkerExecutor(
	workspaceRoot string,
	storeRef *store.Store,
	responder llm.Responder,
	qmdService *qmd.Service,
	actionExecutor taskActionExecutor,
	logger *slog.Logger,
) *taskWorkerExecutor {
	if logger == nil {
		logger = slog.Default()
	}
	return &taskWorkerExecutor{
		workspaceRoot:      strings.TrimSpace(workspaceRoot),
		store:              storeRef,
		responder:          responder,
		qmd:                qmdService,
		actionExecutor:     actionExecutor,
		maxAutonomousSteps: defaultAutonomousTaskMaxSteps,
		logger:             logger,
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
	if strings.EqualFold(strings.TrimSpace(task.ContextID), "system:filewatcher") {
		return orchestrator.TaskResult{
			Summary: fmt.Sprintf("workspace `%s` reindex already queued by watcher", workspaceID),
		}, nil
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

	taskRecord, hasTaskRecord := e.lookupTaskRecord(ctx, task.ID)
	reply := ""
	if e.shouldRunAutonomousTask(taskRecord, hasTaskRecord) {
		autonomousReply, err := e.runAutonomousTask(ctx, task, taskRecord, prompt)
		if err != nil {
			return orchestrator.TaskResult{}, err
		}
		reply = autonomousReply
	} else {
		skipGrounding := task.Kind == orchestrator.TaskKindGeneral
		directReply, err := e.responder.Reply(ctx, llm.MessageInput{
			Connector:     "orchestrator",
			WorkspaceID:   task.WorkspaceID,
			ContextID:     task.ContextID,
			ExternalID:    task.ContextID,
			DisplayName:   task.Title,
			FromUserID:    "system:task-worker",
			Text:          prompt,
			IsDM:          false,
			SkipGrounding: skipGrounding,
		})
		if err != nil {
			return orchestrator.TaskResult{}, err
		}
		reply = e.resolveActionProposal(ctx, task, strings.TrimSpace(directReply))
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
		Summary:      summarizeTaskReplyForNotification(reply, taskRecord, hasTaskRecord),
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

func summarizeTaskReplyForNotification(reply string, taskRecord store.TaskRecord, hasTaskRecord bool) string {
	if hasTaskRecord && strings.TrimSpace(taskRecord.RouteClass) != "" {
		return truncatePreservingLines(reply, 1400)
	}
	return summarizeTaskReply(reply)
}

func truncatePreservingLines(input string, maxLen int) string {
	trimmed := strings.TrimSpace(input)
	if trimmed == "" {
		return "task completed"
	}
	if maxLen < 1 || len(trimmed) <= maxLen {
		return trimmed
	}
	return strings.TrimSpace(trimmed[:maxLen]) + "..."
}

func (e *taskWorkerExecutor) lookupTaskRecord(ctx context.Context, taskID string) (store.TaskRecord, bool) {
	if e.store == nil || strings.TrimSpace(taskID) == "" {
		return store.TaskRecord{}, false
	}
	record, err := e.store.LookupTask(ctx, taskID)
	if err != nil {
		return store.TaskRecord{}, false
	}
	return record, true
}

func (e *taskWorkerExecutor) shouldRunAutonomousTask(taskRecord store.TaskRecord, hasTaskRecord bool) bool {
	if e.actionExecutor == nil || e.responder == nil {
		return false
	}
	if !hasTaskRecord {
		return false
	}
	return strings.TrimSpace(taskRecord.RouteClass) != ""
}

func (e *taskWorkerExecutor) runAutonomousTask(ctx context.Context, task orchestrator.Task, taskRecord store.TaskRecord, prompt string) (string, error) {
	maxSteps := e.maxAutonomousSteps
	if maxSteps < 1 {
		maxSteps = defaultAutonomousTaskMaxSteps
	}
	observationLog := []string{}
	latestDraft := ""

	for step := 1; step <= maxSteps; step++ {
		iterationPrompt := buildAutonomousPrompt(prompt, latestDraft, observationLog)
		reply, err := e.responder.Reply(ctx, llm.MessageInput{
			Connector:     "orchestrator",
			WorkspaceID:   task.WorkspaceID,
			ContextID:     task.ContextID,
			ExternalID:    task.ContextID,
			DisplayName:   task.Title,
			FromUserID:    "system:task-worker",
			Text:          iterationPrompt,
			IsDM:          false,
			SkipGrounding: true,
		})
		if err != nil {
			return "", err
		}

		cleanReply, proposal := actions.ExtractProposal(strings.TrimSpace(reply))
		cleanReply = strings.TrimSpace(cleanReply)
		if cleanReply != "" {
			latestDraft = cleanReply
		}
		if proposal == nil {
			return strings.TrimSpace(cleanReply), nil
		}
		if !isAutonomousProposalSupported(proposal) {
			return e.resolveActionProposalWithRecord(ctx, task, taskRecord, strings.TrimSpace(reply)), nil
		}

		execResult, err := e.actionExecutor.Execute(ctx, store.ActionApproval{
			WorkspaceID:   strings.TrimSpace(task.WorkspaceID),
			ContextID:     strings.TrimSpace(task.ContextID),
			ActionType:    strings.TrimSpace(proposal.Type),
			ActionTarget:  strings.TrimSpace(proposal.Target),
			ActionSummary: strings.TrimSpace(proposal.Summary),
			Payload:       cloneProposalPayload(proposal),
		})
		if err != nil {
			observationLog = append(observationLog, truncatePreservingLines(fmt.Sprintf("Step %d `%s` failed: %s", step, proposalActionLabel(proposal), err.Error()), autonomousObservationMaxBytes))
			continue
		}
		message := strings.TrimSpace(execResult.Message)
		if message == "" {
			message = "Command completed successfully with no output."
		}
		observationLog = append(observationLog, truncatePreservingLines(fmt.Sprintf("Step %d `%s`: %s", step, proposalActionLabel(proposal), message), autonomousObservationMaxBytes))
	}
	return e.finalizeAutonomousTaskReply(ctx, task, prompt, latestDraft, observationLog), nil
}

func buildAutonomousPrompt(goal, latestDraft string, observations []string) string {
	lines := []string{
		"You are executing a routed task asynchronously and must complete it end-to-end for the user.",
		"Goal:",
		strings.TrimSpace(goal),
		"",
		"Rules:",
		"- If you need more data, return exactly one `action` fenced JSON block.",
		"- Use only `run_command` with target `curl` for autonomous execution.",
		"- Prefer `curl -sSL` for pages so redirects are followed.",
		"- If homepage lacks the answer, fetch likely subpages or run a web-search URL with curl.",
		"- When the answer is sufficient, return a direct natural-language reply with concrete findings.",
		"- If evidence is incomplete after attempts, state uncertainty clearly and provide the best evidence-backed outcome.",
	}
	if len(observations) > 0 {
		lines = append(lines, "", "Executed steps so far:")
		for index, item := range observations {
			lines = append(lines, fmt.Sprintf("%d. %s", index+1, strings.TrimSpace(item)))
		}
	}
	if strings.TrimSpace(latestDraft) != "" {
		lines = append(lines, "", "Latest draft answer (revise if needed):", strings.TrimSpace(latestDraft))
	}
	prompt := strings.TrimSpace(strings.Join(lines, "\n"))
	if len(prompt) > autonomousPromptMaxBytes {
		return prompt[:autonomousPromptMaxBytes]
	}
	return prompt
}

func (e *taskWorkerExecutor) finalizeAutonomousTaskReply(
	ctx context.Context,
	task orchestrator.Task,
	goal string,
	latestDraft string,
	observations []string,
) string {
	if e.responder != nil {
		summaryPrompt := buildAutonomousSummaryPrompt(goal, latestDraft, observations)
		reply, err := e.responder.Reply(ctx, llm.MessageInput{
			Connector:     "orchestrator",
			WorkspaceID:   task.WorkspaceID,
			ContextID:     task.ContextID,
			ExternalID:    task.ContextID,
			DisplayName:   task.Title,
			FromUserID:    "system:task-worker",
			Text:          summaryPrompt,
			IsDM:          false,
			SkipGrounding: true,
		})
		if err == nil {
			cleanReply, _ := actions.ExtractProposal(strings.TrimSpace(reply))
			clean := sanitizeAutonomousSummary(cleanReply)
			if clean != "" {
				return clean
			}
		}
	}
	return fallbackAutonomousSummary(goal, latestDraft, observations)
}

func buildAutonomousSummaryPrompt(goal, latestDraft string, observations []string) string {
	lines := []string{
		"Synthesize the final user-facing task result.",
		"Rules:",
		"- Return plain natural language only (no code fences, no action blocks).",
		"- Ground the answer in the execution observations provided.",
		"- If evidence is incomplete, explicitly state what is known and unknown.",
		"- Keep the response concise but complete.",
		"Task goal:",
		strings.TrimSpace(goal),
	}
	if strings.TrimSpace(latestDraft) != "" {
		lines = append(lines, "Latest draft candidate:", strings.TrimSpace(latestDraft))
	}
	if len(observations) > 0 {
		lines = append(lines, "Execution observations:")
		for index, item := range observations {
			lines = append(lines, fmt.Sprintf("%d. %s", index+1, strings.TrimSpace(item)))
		}
	} else {
		lines = append(lines, "Execution observations:", "none")
	}
	prompt := strings.TrimSpace(strings.Join(lines, "\n"))
	if len(prompt) > autonomousSummaryPromptBytes {
		return prompt[:autonomousSummaryPromptBytes]
	}
	return prompt
}

func sanitizeAutonomousSummary(input string) string {
	trimmed := strings.TrimSpace(input)
	if trimmed == "" {
		return ""
	}
	trimmed = strings.ReplaceAll(trimmed, "```", "")
	trimmed = strings.Join(strings.Fields(trimmed), " ")
	trimmed = strings.Trim(trimmed, "`\"'")
	if trimmed == "" {
		return ""
	}
	if len(trimmed) > autonomousSummaryMaxBytes {
		trimmed = strings.TrimSpace(trimmed[:autonomousSummaryMaxBytes]) + "..."
	}
	return trimmed
}

func fallbackAutonomousSummary(goal, latestDraft string, observations []string) string {
	if strings.TrimSpace(latestDraft) != "" {
		if len(observations) == 0 {
			return sanitizeAutonomousSummary(latestDraft)
		}
		return sanitizeAutonomousSummary(latestDraft + "\n\nThis summary is based on automated retrieval steps completed for the task.")
	}
	lines := []string{
		"I completed automated follow-up for this task but could not produce a definitive final answer yet.",
	}
	if strings.TrimSpace(goal) != "" {
		lines = append(lines, "Goal: "+strings.TrimSpace(goal))
	}
	if len(observations) > 0 {
		lines = append(lines, "Observed results:")
		for _, item := range observations {
			lines = append(lines, "- "+strings.TrimSpace(item))
		}
	} else {
		lines = append(lines, "No usable execution observations were produced.")
	}
	lines = append(lines, "If you want, I can continue with additional retrieval steps or a narrower target.")
	return sanitizeAutonomousSummary(strings.Join(lines, "\n"))
}

func isAutonomousProposalSupported(proposal *actions.Proposal) bool {
	if proposal == nil {
		return false
	}
	actionType := strings.ToLower(strings.TrimSpace(proposal.Type))
	if actionType != "run_command" && actionType != "shell_command" && actionType != "cli_command" {
		return false
	}
	return strings.EqualFold(strings.TrimSpace(proposal.Target), "curl")
}

func cloneProposalPayload(proposal *actions.Proposal) map[string]any {
	payload := map[string]any{}
	if proposal == nil {
		return payload
	}
	for key, value := range proposal.Raw {
		payload[key] = value
	}
	return payload
}

func proposalActionLabel(proposal *actions.Proposal) string {
	if proposal == nil {
		return "action"
	}
	target := strings.TrimSpace(proposal.Target)
	actionType := strings.TrimSpace(proposal.Type)
	if target == "" {
		return actionType
	}
	if actionType == "" {
		return target
	}
	return actionType + " " + target
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
	return e.resolveActionProposalWithRecord(ctx, task, taskRecord, strings.TrimSpace(reply))
}

func (e *taskWorkerExecutor) resolveActionProposalWithRecord(ctx context.Context, task orchestrator.Task, taskRecord store.TaskRecord, reply string) string {
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
	notice := actions.FormatApprovalRequestNotice(approval.ID)
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
	if o.notifier != nil {
		o.notifier.NotifyStarted(task)
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
