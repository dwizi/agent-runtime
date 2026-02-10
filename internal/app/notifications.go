package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/carlos/spinner/internal/connectors"
	"github.com/carlos/spinner/internal/orchestrator"
	"github.com/carlos/spinner/internal/store"
)

type taskCompletionNotifier struct {
	workspaceRoot string
	store         *store.Store
	publishers    map[string]connectors.Publisher
	successPolicy string
	failurePolicy string
	logger        *slog.Logger
}

func newTaskCompletionNotifier(
	workspaceRoot string,
	storeRef *store.Store,
	publishers map[string]connectors.Publisher,
	defaultPolicy string,
	successPolicy string,
	failurePolicy string,
	logger *slog.Logger,
) *taskCompletionNotifier {
	if logger == nil {
		logger = slog.Default()
	}
	cleanPublishers := map[string]connectors.Publisher{}
	for key, publisher := range publishers {
		name := strings.ToLower(strings.TrimSpace(key))
		if name == "" || publisher == nil {
			continue
		}
		cleanPublishers[name] = publisher
	}
	basePolicy := normalizeTaskNotifyPolicy(defaultPolicy)
	success := normalizeTaskNotifyPolicyWithFallback(successPolicy, basePolicy)
	failure := normalizeTaskNotifyPolicyWithFallback(failurePolicy, basePolicy)

	return &taskCompletionNotifier{
		workspaceRoot: strings.TrimSpace(workspaceRoot),
		store:         storeRef,
		publishers:    cleanPublishers,
		successPolicy: success,
		failurePolicy: failure,
		logger:        logger,
	}
}

func (n *taskCompletionNotifier) NotifyCompleted(task orchestrator.Task, result orchestrator.TaskResult) {
	n.notify(task, result, nil, n.successPolicy)
}

func (n *taskCompletionNotifier) NotifyFailed(task orchestrator.Task, taskErr error) {
	n.notify(task, orchestrator.TaskResult{}, taskErr, n.failurePolicy)
}

func (n *taskCompletionNotifier) NotifyStarted(task orchestrator.Task) {
	if n == nil || n.store == nil || len(n.publishers) == 0 {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()

	taskRecord, hasTaskRecord := n.lookupTaskRecord(ctx, task.ID)
	if !hasTaskRecord {
		return
	}
	if strings.TrimSpace(taskRecord.RouteClass) == "" {
		return
	}
	if taskRecord.Attempts > 1 {
		return
	}

	message := buildTaskStartedMessage(taskRecord)
	if message == "" {
		return
	}
	targets := n.resolveTargets(ctx, task, n.successPolicy)
	for _, target := range targets {
		if target.IsAdmin {
			continue
		}
		publisher := n.publishers[strings.ToLower(strings.TrimSpace(target.Connector))]
		if publisher == nil {
			continue
		}
		if err := publisher.Publish(ctx, target.ExternalID, message); err != nil {
			n.logger.Error("task start notification publish failed",
				"task_id", task.ID,
				"connector", target.Connector,
				"external_id", target.ExternalID,
				"error", err,
			)
			continue
		}
		appendOutboundChatLog(n.workspaceRoot, target.WorkspaceID, target.Connector, target.ExternalID, message)
	}
}

func (n *taskCompletionNotifier) notify(task orchestrator.Task, result orchestrator.TaskResult, taskErr error, policy string) {
	if n == nil || n.store == nil || len(n.publishers) == 0 {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	defer cancel()

	taskRecord, hasTaskRecord := n.lookupTaskRecord(ctx, task.ID)
	routedTask := hasTaskRecord && strings.TrimSpace(taskRecord.RouteClass) != ""
	if routedTask && taskErr != nil {
		policy = "admin"
	}
	targets := n.resolveTargets(ctx, task, policy)
	for _, target := range targets {
		if taskErr != nil && !target.IsAdmin {
			continue
		}
		message := n.messageForTarget(task, taskRecord, hasTaskRecord, result, taskErr, target)
		message = strings.TrimSpace(message)
		if message == "" {
			continue
		}
		publisher := n.publishers[strings.ToLower(strings.TrimSpace(target.Connector))]
		if publisher == nil {
			continue
		}
		if err := publisher.Publish(ctx, target.ExternalID, message); err != nil {
			n.logger.Error("task notification publish failed",
				"task_id", task.ID,
				"connector", target.Connector,
				"external_id", target.ExternalID,
				"error", err,
			)
			continue
		}
		appendOutboundChatLog(n.workspaceRoot, target.WorkspaceID, target.Connector, target.ExternalID, message)
	}
}

func (n *taskCompletionNotifier) lookupTaskRecord(ctx context.Context, taskID string) (store.TaskRecord, bool) {
	taskID = strings.TrimSpace(taskID)
	if taskID == "" {
		return store.TaskRecord{}, false
	}
	record, err := n.store.LookupTask(ctx, taskID)
	if err != nil {
		if !errors.Is(err, store.ErrTaskNotFound) {
			n.logger.Error("task notification lookup failed", "task_id", taskID, "error", err)
		}
		return store.TaskRecord{}, false
	}
	return record, true
}

func (n *taskCompletionNotifier) messageForTarget(
	task orchestrator.Task,
	taskRecord store.TaskRecord,
	hasTaskRecord bool,
	result orchestrator.TaskResult,
	taskErr error,
	target store.ContextDelivery,
) string {
	if taskErr != nil {
		return buildTaskFailureMessage(task, taskErr, hasTaskRecord, taskRecord, target.IsAdmin)
	}
	return buildTaskSuccessMessage(task, result, hasTaskRecord, taskRecord)
}

func (n *taskCompletionNotifier) resolveTargets(ctx context.Context, task orchestrator.Task, policy string) []store.ContextDelivery {
	unique := map[string]store.ContextDelivery{}
	add := func(record store.ContextDelivery) {
		connector := strings.ToLower(strings.TrimSpace(record.Connector))
		externalID := strings.TrimSpace(record.ExternalID)
		if connector == "" || externalID == "" {
			return
		}
		key := connector + "::" + externalID
		unique[key] = store.ContextDelivery{
			ContextID:   strings.TrimSpace(record.ContextID),
			WorkspaceID: strings.TrimSpace(record.WorkspaceID),
			Connector:   connector,
			ExternalID:  externalID,
			IsAdmin:     record.IsAdmin,
		}
	}

	contextID := strings.TrimSpace(task.ContextID)
	if includeOriginTarget(policy) && contextID != "" && !strings.HasPrefix(contextID, "system:") {
		record, err := n.store.LookupContextDelivery(ctx, contextID)
		if err == nil {
			add(record)
		} else if !errors.Is(err, store.ErrContextNotFound) {
			n.logger.Error("task notification context lookup failed", "task_id", task.ID, "context_id", contextID, "error", err)
		}
	}

	if includeAdminTargets(policy) {
		adminContexts, err := n.store.ListWorkspaceAdminDeliveries(ctx, strings.TrimSpace(task.WorkspaceID), 50)
		if err != nil {
			n.logger.Error("task notification admin context list failed", "task_id", task.ID, "workspace_id", task.WorkspaceID, "error", err)
		} else {
			for _, record := range adminContexts {
				add(record)
			}
		}
	}

	results := make([]store.ContextDelivery, 0, len(unique))
	for _, record := range unique {
		results = append(results, record)
	}
	return results
}

func buildTaskSuccessMessage(task orchestrator.Task, result orchestrator.TaskResult, hasTaskRecord bool, taskRecord store.TaskRecord) string {
	summary := strings.TrimSpace(result.Summary)
	if summary == "" {
		summary = "Done."
	}
	if hasTaskRecord && strings.TrimSpace(taskRecord.RouteClass) != "" {
		return truncateWithEllipsis(summary, 1400)
	}
	kind := strings.TrimSpace(string(task.Kind))
	if kind == "" {
		kind = "general"
	}
	title := strings.TrimSpace(task.Title)
	if title == "" {
		title = "Task"
	}
	return compactLineBreaks(fmt.Sprintf("%s (%s): %s", title, kind, truncateSingleLine(summary, 1200)), 1400)
}

func buildTaskFailureMessage(task orchestrator.Task, taskErr error, hasTaskRecord bool, taskRecord store.TaskRecord, isAdminTarget bool) string {
	if !isAdminTarget {
		return ""
	}
	kind := strings.TrimSpace(string(task.Kind))
	if kind == "" {
		kind = "general"
	}
	title := strings.TrimSpace(task.Title)
	if title == "" {
		title = "Task"
	}
	errorText := "unknown error"
	if taskErr != nil {
		errorText = strings.TrimSpace(taskErr.Error())
	}
	if hasTaskRecord && strings.TrimSpace(taskRecord.RouteClass) != "" {
		class := strings.TrimSpace(taskRecord.RouteClass)
		if class == "" {
			class = "task"
		}
		return compactLineBreaks(fmt.Sprintf("Routed %s follow-up failed (`%s`): %s", class, strings.TrimSpace(task.ID), truncateSingleLine(errorText, 1100)), 1400)
	}
	return compactLineBreaks(fmt.Sprintf("Task `%s` failed: %s", strings.TrimSpace(task.ID), truncateSingleLine(errorText, 1200)), 1400)
}

func buildTaskStartedMessage(taskRecord store.TaskRecord) string {
	if strings.TrimSpace(taskRecord.RouteClass) == "" {
		return ""
	}
	return "I ran some tools and I'm still working on this."
}

func includeOriginTarget(policy string) bool {
	return policy == "both" || policy == "origin"
}

func includeAdminTargets(policy string) bool {
	return policy == "both" || policy == "admin"
}

func normalizeTaskNotifyPolicy(input string) string {
	switch strings.ToLower(strings.TrimSpace(input)) {
	case "admin":
		return "admin"
	case "origin":
		return "origin"
	default:
		return "both"
	}
}

func normalizeTaskNotifyPolicyWithFallback(input, fallback string) string {
	value := strings.TrimSpace(input)
	if value == "" {
		return normalizeTaskNotifyPolicy(fallback)
	}
	return normalizeTaskNotifyPolicy(value)
}

func truncateSingleLine(input string, maxLen int) string {
	single := strings.Join(strings.Fields(strings.TrimSpace(input)), " ")
	if maxLen < 1 || len(single) <= maxLen {
		return single
	}
	return strings.TrimSpace(single[:maxLen]) + "..."
}

func compactLineBreaks(input string, maxLen int) string {
	trimmed := strings.TrimSpace(input)
	if maxLen < 1 || len(trimmed) <= maxLen {
		return trimmed
	}
	return strings.TrimSpace(trimmed[:maxLen]) + "..."
}

func truncateWithEllipsis(input string, maxLen int) string {
	trimmed := strings.TrimSpace(input)
	if maxLen < 1 || len(trimmed) <= maxLen {
		return trimmed
	}
	return strings.TrimSpace(trimmed[:maxLen]) + "..."
}
