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

	actionexecutor "github.com/dwizi/agent-runtime/internal/actions/executor"
	"github.com/dwizi/agent-runtime/internal/agent"
	"github.com/dwizi/agent-runtime/internal/agent/tools"
	"github.com/dwizi/agent-runtime/internal/config"
	"github.com/dwizi/agent-runtime/internal/gateway"
	"github.com/dwizi/agent-runtime/internal/llm"
	"github.com/dwizi/agent-runtime/internal/orchestrator"
	"github.com/dwizi/agent-runtime/internal/qmd"
	"github.com/dwizi/agent-runtime/internal/store"
)

const (
	autonomousObservationMaxBytes = 1200
)

type taskActionExecutor interface {
	Execute(ctx context.Context, approval store.ActionApproval) (actionexecutor.Result, error)
}

type taskWorkerExecutor struct {
	workspaceRoot  string
	store          *store.Store
	responder      llm.Responder
	qmd            *qmd.Service
	actionExecutor taskActionExecutor
	logger         *slog.Logger
	agent          *agent.Agent
}

func newTaskWorkerExecutor(
	workspaceRoot string,
	storeRef *store.Store,
	responder llm.Responder,
	qmdService *qmd.Service,
	actionExecutor taskActionExecutor,
	registry *tools.Registry,
	cfg config.Config,
	logger *slog.Logger,
) *taskWorkerExecutor {
	if logger == nil {
		logger = slog.Default()
	}

	workerAgent := agent.New(
		logger.With("component", "worker-agent"),
		responder,
		registry,
		"You are an autonomous worker agent. Complete the assigned task efficiently using available tools.",
	)

	// Apply defaults if config is zero (for tests)
	policy := agent.Policy{
		MaxLoopSteps:              cfg.AgentAutonomousMaxLoopSteps,
		MaxTurnDuration:           time.Duration(cfg.AgentAutonomousMaxTurnDurationSec) * time.Second,
		MaxToolCallsPerTurn:       cfg.AgentAutonomousMaxToolCallsPerTurn,
		MaxAutonomousTasksPerHour: cfg.AgentAutonomousMaxTasksPerHour,
		MaxAutonomousTasksPerDay:  cfg.AgentAutonomousMaxTasksPerDay,
		MinFinalConfidence:        cfg.AgentAutonomousMinConfidence,
	}
	if policy.MaxLoopSteps == 0 {
		policy.MaxLoopSteps = 20
	}
	if policy.MaxTurnDuration == 0 {
		policy.MaxTurnDuration = 5 * time.Minute
	}
	if policy.MaxToolCallsPerTurn == 0 {
		policy.MaxToolCallsPerTurn = 50
	}
	if policy.MaxAutonomousTasksPerHour == 0 {
		policy.MaxAutonomousTasksPerHour = 100
	}
	if policy.MaxAutonomousTasksPerDay == 0 {
		policy.MaxAutonomousTasksPerDay = 500
	}
	if policy.MinFinalConfidence == 0 {
		policy.MinFinalConfidence = 0.1
	}

	workerAgent.SetDefaultPolicy(policy)
	// Enable grounding at every step for deep work
	workerAgent.SetGroundingPolicy(true, true)

	return &taskWorkerExecutor{
		workspaceRoot:  strings.TrimSpace(workspaceRoot),
		store:          storeRef,
		responder:      responder,
		qmd:            qmdService,
		actionExecutor: actionExecutor,
		logger:         logger,
		agent:          workerAgent,
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
	if e.agent == nil {
		return orchestrator.TaskResult{
			Summary: "task execution skipped: agent unavailable",
		}, nil
	}
	prompt := strings.TrimSpace(task.Prompt)
	if prompt == "" {
		prompt = strings.TrimSpace(task.Title)
	}
	if prompt == "" {
		return orchestrator.TaskResult{}, fmt.Errorf("task prompt is empty")
	}

	// Lookup task record for metadata
	taskRecord, _ := e.lookupTaskRecord(ctx, task.ID)

	connector := "orchestrator"
	externalID := task.ContextID
	fromUserID := "system:task-worker"

	if strings.TrimSpace(taskRecord.SourceConnector) != "" {
		connector = taskRecord.SourceConnector
	}
	if strings.TrimSpace(taskRecord.SourceExternalID) != "" {
		externalID = taskRecord.SourceExternalID
	}
	if strings.TrimSpace(taskRecord.SourceUserID) != "" {
		fromUserID = taskRecord.SourceUserID
	}

	// Prepare context
	llmInput := llm.MessageInput{
		Connector:     connector,
		WorkspaceID:   task.WorkspaceID,
		ContextID:     task.ContextID,
		ExternalID:    externalID,
		DisplayName:   task.Title,
		FromUserID:    fromUserID,
		Text:          prompt,
		IsDM:          false,
		SkipGrounding: false,
	}

	gatewayInput := gateway.MessageInput{
		Connector:   connector,
		ExternalID:  externalID,
		DisplayName: task.Title,
		FromUserID:  fromUserID,
		Text:        prompt,
	}

	// Construct context with sensitive approval for "autonomous" tasks
	agentCtx := ctx
	// ContextRecord is needed for tools
	contextRecord := store.ContextRecord{
		ID:          task.ContextID,
		WorkspaceID: task.WorkspaceID,
	}
	agentCtx = context.WithValue(agentCtx, gateway.ContextKeyRecord, contextRecord)
	agentCtx = context.WithValue(agentCtx, gateway.ContextKeyInput, gatewayInput)

	// Grant sensitive approval for deep work
	agentCtx = agent.WithSensitiveToolApproval(agentCtx)

	result := e.agent.Execute(agentCtx, llmInput)

	reply := strings.TrimSpace(result.Reply)
	if result.Error != nil {
		e.logger.Error("task agent execution failed", "task_id", task.ID, "error", result.Error)
		reply += fmt.Sprintf("\n\n(Error: %v)", result.Error)
	}
	if reply == "" {
		reply = "Task completed with no output."
	}

	resultPath, err := e.writeTaskResult(task, result)
	if err != nil {
		return orchestrator.TaskResult{}, err
	}
	if e.qmd != nil && strings.TrimSpace(task.WorkspaceID) != "" {
		e.qmd.QueueWorkspaceIndex(task.WorkspaceID)
	}

	summary := summarizeTaskReply(reply)
	if strings.TrimSpace(taskRecord.RouteClass) != "" {
		summary = truncatePreservingLines(reply, 1400)
	}

	return orchestrator.TaskResult{
		Summary:      summary,
		ArtifactPath: resultPath,
	}, nil
}

func (e *taskWorkerExecutor) writeTaskResult(task orchestrator.Task, result agent.Result) (string, error) {
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
	content := buildTaskMarkdown(task, now, result)
	if err := os.WriteFile(absolutePath, []byte(content), 0o644); err != nil {
		return "", fmt.Errorf("write task artifact: %w", err)
	}
	return relativePath, nil
}

func buildTaskMarkdown(task orchestrator.Task, now time.Time, result agent.Result) string {
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
	builder.WriteString("\n\n")

	if len(result.ToolCalls) > 0 {
		builder.WriteString("## Execution Trace\n\n")
		for i, call := range result.ToolCalls {
			builder.WriteString(fmt.Sprintf("### Step %d: `%s`\n", i+1, call.ToolName))
			if call.ToolArgs != "" {
				builder.WriteString("**Args:**\n```json\n" + call.ToolArgs + "\n```\n")
			}
			if call.Error != "" {
				builder.WriteString("**Error:**\n```\n" + call.Error + "\n```\n")
			} else if call.ToolOutput != "" {
				output := call.ToolOutput
				if len(output) > 2000 {
					output = output[:2000] + "\n...(truncated)"
				}
				builder.WriteString("**Output:**\n```\n" + output + "\n```\n")
			}
			builder.WriteString("\n")
		}
	}

	builder.WriteString("## Final Output\n\n")
	builder.WriteString(strings.TrimSpace(result.Reply))
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
	if err := o.store.MarkTaskCompletedByWorker(ctx, task.ID, workerID, time.Now().UTC(), result.Summary, result.ArtifactPath); err != nil {
		if errors.Is(err, store.ErrTaskNotRunningForWorker) {
			o.logger.Warn("skipping stale task completion update", "task_id", task.ID, "worker_id", workerID)
			return
		}
		if !errorsIsTaskNotFound(err) {
			o.logger.Error("mark task completed failed", "task_id", task.ID, "error", err)
		}
		return
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
	if updateErr := o.store.MarkTaskFailedByWorker(ctx, task.ID, workerID, time.Now().UTC(), message); updateErr != nil {
		if errors.Is(updateErr, store.ErrTaskNotRunningForWorker) {
			o.logger.Warn("skipping stale task failure update", "task_id", task.ID, "worker_id", workerID)
			return
		}
		if !errorsIsTaskNotFound(updateErr) {
			o.logger.Error("mark task failed failed", "task_id", task.ID, "error", updateErr)
		}
		return
	}
	if o.notifier != nil {
		o.notifier.NotifyFailed(task, err)
	}
}

func errorsIsTaskNotFound(err error) bool {
	return errors.Is(err, store.ErrTaskNotFound)
}
