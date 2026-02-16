package app

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/dwizi/agent-runtime/internal/connectors"
	"github.com/dwizi/agent-runtime/internal/gateway"
	"github.com/dwizi/agent-runtime/internal/store"
)

type routingNotifier struct {
	workspaceRoot string
	store         *store.Store
	publishers    map[string]connectors.Publisher
	enabled       bool
	logger        *slog.Logger
}

func newRoutingNotifier(
	workspaceRoot string,
	storeRef *store.Store,
	publishers map[string]connectors.Publisher,
	enabled bool,
	logger *slog.Logger,
) *routingNotifier {
	if logger == nil {
		logger = slog.Default()
	}
	clean := map[string]connectors.Publisher{}
	for key, publisher := range publishers {
		name := strings.ToLower(strings.TrimSpace(key))
		if name == "" || publisher == nil {
			continue
		}
		clean[name] = publisher
	}
	return &routingNotifier{
		workspaceRoot: strings.TrimSpace(workspaceRoot),
		store:         storeRef,
		publishers:    clean,
		enabled:       enabled,
		logger:        logger,
	}
}

func (n *routingNotifier) NotifyRoutingDecision(ctx context.Context, decision gateway.RouteDecision) {
	if n == nil || !n.enabled || n.store == nil {
		return
	}
	workspaceID := strings.TrimSpace(decision.WorkspaceID)
	if workspaceID == "" {
		return
	}
	targets, err := n.store.ListWorkspaceAdminDeliveries(ctx, workspaceID, 50)
	if err != nil {
		n.logger.Error("list workspace admin deliveries failed", "workspace_id", workspaceID, "error", err)
		return
	}
	if len(targets) == 0 {
		return
	}
	decisionText := buildRoutingDecisionNotice(decision)
	for _, target := range targets {
		connector := strings.ToLower(strings.TrimSpace(target.Connector))
		publisher := n.publishers[connector]
		if publisher == nil {
			continue
		}
		publishCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
		err := publisher.Publish(publishCtx, target.ExternalID, decisionText)
		cancel()
		if err != nil {
			n.logger.Error("publish triage routing notice failed",
				"workspace_id", workspaceID,
				"connector", connector,
				"external_id", target.ExternalID,
				"error", err,
			)
			continue
		}
		appendOutboundChatLog(n.workspaceRoot, target.WorkspaceID, target.Connector, target.ExternalID, decisionText)
	}
}

func buildRoutingDecisionNotice(decision gateway.RouteDecision) string {
	builder := strings.Builder{}
	builder.WriteString("Routing decision")
	builder.WriteString("\n- task: `")
	builder.WriteString(strings.TrimSpace(decision.TaskID))
	builder.WriteString("`")
	builder.WriteString("\n- class: `")
	builder.WriteString(strings.TrimSpace(string(decision.Class)))
	builder.WriteString("`")
	builder.WriteString("\n- priority: `")
	builder.WriteString(strings.TrimSpace(string(decision.Priority)))
	builder.WriteString("`")
	builder.WriteString("\n- lane: `")
	builder.WriteString(strings.TrimSpace(decision.AssignedLane))
	builder.WriteString("`")
	if !decision.DueAt.IsZero() {
		builder.WriteString("\n- due: `")
		builder.WriteString(decision.DueAt.UTC().Format(time.RFC3339))
		builder.WriteString("`")
	}
	if snippet := truncateSingleLine(decision.SourceText, 220); snippet != "" {
		builder.WriteString("\n- preview: ")
		builder.WriteString(snippet)
	}
	builder.WriteString("\n\nOverride examples:")
	builder.WriteString(fmt.Sprintf("\n- `/route %s moderation p1 2h`", decision.TaskID))
	builder.WriteString(fmt.Sprintf("\n- `/route %s issue p2 8h`", decision.TaskID))
	builder.WriteString(fmt.Sprintf("\n- `/route %s question p3 48h`", decision.TaskID))
	builder.WriteString(fmt.Sprintf("\n- `/route %s noise p3`", decision.TaskID))
	return compactLineBreaks(builder.String(), 1600)
}
