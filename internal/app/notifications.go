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
	store         *store.Store
	publishers    map[string]connectors.Publisher
	successPolicy string
	failurePolicy string
	logger        *slog.Logger
}

func newTaskCompletionNotifier(
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
		store:         storeRef,
		publishers:    cleanPublishers,
		successPolicy: success,
		failurePolicy: failure,
		logger:        logger,
	}
}

func (n *taskCompletionNotifier) NotifyCompleted(task orchestrator.Task, result orchestrator.TaskResult) {
	n.notify(task, buildTaskSuccessMessage(task, result), n.successPolicy)
}

func (n *taskCompletionNotifier) NotifyFailed(task orchestrator.Task, taskErr error) {
	n.notify(task, buildTaskFailureMessage(task, taskErr), n.failurePolicy)
}

func (n *taskCompletionNotifier) notify(task orchestrator.Task, message string, policy string) {
	if n == nil || n.store == nil || len(n.publishers) == 0 {
		return
	}
	message = strings.TrimSpace(message)
	if message == "" {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	defer cancel()

	targets := n.resolveTargets(ctx, task, policy)
	for _, target := range targets {
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
		}
	}
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

func buildTaskSuccessMessage(task orchestrator.Task, result orchestrator.TaskResult) string {
	kind := string(task.Kind)
	if kind == "" {
		kind = "general"
	}
	title := strings.TrimSpace(task.Title)
	if title == "" {
		title = "Task"
	}
	summary := strings.TrimSpace(result.Summary)
	if summary == "" {
		summary = "completed"
	}
	message := fmt.Sprintf("Task completed\n- id: `%s`\n- kind: `%s`\n- title: %s\n- summary: %s", strings.TrimSpace(task.ID), kind, title, truncateSingleLine(summary, 900))
	if path := strings.TrimSpace(result.ArtifactPath); path != "" {
		message += fmt.Sprintf("\n- output: `%s`", path)
	}
	return compactLineBreaks(message, 1400)
}

func buildTaskFailureMessage(task orchestrator.Task, taskErr error) string {
	kind := string(task.Kind)
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
	message := fmt.Sprintf("Task failed\n- id: `%s`\n- kind: `%s`\n- title: %s\n- error: %s", strings.TrimSpace(task.ID), kind, title, truncateSingleLine(errorText, 900))
	return compactLineBreaks(message, 1400)
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
