package app

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/carlos/spinner/internal/connectors"
	"github.com/carlos/spinner/internal/heartbeat"
	"github.com/carlos/spinner/internal/store"
)

type heartbeatNotifier struct {
	store         *store.Store
	publishers    map[string]connectors.Publisher
	workspaceRoot string
	enabled       bool
	logger        *slog.Logger
}

func newHeartbeatNotifier(
	storeRef *store.Store,
	publishers map[string]connectors.Publisher,
	workspaceRoot string,
	enabled bool,
	logger *slog.Logger,
) *heartbeatNotifier {
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
	return &heartbeatNotifier{
		store:         storeRef,
		publishers:    cleanPublishers,
		workspaceRoot: strings.TrimSpace(workspaceRoot),
		enabled:       enabled,
		logger:        logger,
	}
}

func (n *heartbeatNotifier) HandleTransition(ctx context.Context, transition heartbeat.Transition, snapshot heartbeat.Snapshot) {
	if n == nil || n.store == nil || !n.enabled {
		return
	}
	eventType := heartbeatTransitionType(transition)
	if eventType == "" {
		return
	}
	message := buildHeartbeatTransitionMessage(eventType, transition, snapshot)
	workspaceUpdates := map[string]struct{}{}

	records, err := n.store.ListAdminDeliveries(ctx, 200)
	if err != nil {
		n.logger.Error("heartbeat list admin deliveries failed", "error", err)
		return
	}
	uniqueTargets := map[string]store.ContextDelivery{}
	for _, record := range records {
		connector := strings.ToLower(strings.TrimSpace(record.Connector))
		externalID := strings.TrimSpace(record.ExternalID)
		if connector == "" || externalID == "" {
			continue
		}
		uniqueTargets[connector+"::"+externalID] = record
		workspaceID := strings.TrimSpace(record.WorkspaceID)
		if workspaceID != "" {
			workspaceUpdates[workspaceID] = struct{}{}
		}
	}

	for _, target := range uniqueTargets {
		publisher := n.publishers[strings.ToLower(strings.TrimSpace(target.Connector))]
		if publisher == nil {
			continue
		}
		publishCtx, cancel := context.WithTimeout(ctx, 8*time.Second)
		err := publisher.Publish(publishCtx, target.ExternalID, message)
		cancel()
		if err != nil {
			n.logger.Error("heartbeat publish failed",
				"connector", target.Connector,
				"external_id", target.ExternalID,
				"error", err,
			)
			continue
		}
		appendOutboundChatLog(n.workspaceRoot, target.WorkspaceID, target.Connector, target.ExternalID, message)
	}

	logLine := buildHeartbeatLogLine(eventType, transition, snapshot)
	for workspaceID := range workspaceUpdates {
		if err := appendWorkspaceHeartbeatLog(n.workspaceRoot, workspaceID, logLine); err != nil {
			n.logger.Error("heartbeat workspace log append failed", "workspace_id", workspaceID, "error", err)
		}
	}
}

func heartbeatTransitionType(transition heartbeat.Transition) string {
	fromDegraded := heartbeat.IsDegradedState(transition.FromState)
	toDegraded := heartbeat.IsDegradedState(transition.ToState)
	switch {
	case !fromDegraded && toDegraded:
		return "degraded"
	case fromDegraded && strings.EqualFold(strings.TrimSpace(transition.ToState), heartbeat.StateHealthy):
		return "recovered"
	default:
		return ""
	}
}

func buildHeartbeatTransitionMessage(eventType string, transition heartbeat.Transition, snapshot heartbeat.Snapshot) string {
	title := "Heartbeat recovered"
	if eventType == "degraded" {
		title = "Heartbeat degraded"
	}
	builder := strings.Builder{}
	builder.WriteString(title)
	builder.WriteString("\n- component: `")
	builder.WriteString(strings.TrimSpace(transition.Component))
	builder.WriteString("`")
	builder.WriteString("\n- state: `")
	builder.WriteString(strings.TrimSpace(transition.FromState))
	builder.WriteString("` -> `")
	builder.WriteString(strings.TrimSpace(transition.ToState))
	builder.WriteString("`")
	builder.WriteString("\n- overall: `")
	builder.WriteString(strings.TrimSpace(snapshot.Overall))
	builder.WriteString("`")
	if message := strings.TrimSpace(transition.Message); message != "" {
		builder.WriteString("\n- detail: ")
		builder.WriteString(truncateSingleLine(message, 500))
	}
	if errorText := strings.TrimSpace(transition.Error); errorText != "" {
		builder.WriteString("\n- error: ")
		builder.WriteString(truncateSingleLine(errorText, 500))
	}
	builder.WriteString("\n- at: ")
	builder.WriteString(time.Now().UTC().Format(time.RFC3339))
	return compactLineBreaks(builder.String(), 1400)
}

func buildHeartbeatLogLine(eventType string, transition heartbeat.Transition, snapshot heartbeat.Snapshot) string {
	level := "RECOVERED"
	if eventType == "degraded" {
		level = "DEGRADED"
	}
	line := fmt.Sprintf("- %s [%s] component=`%s` state=`%s -> %s` overall=`%s`",
		time.Now().UTC().Format(time.RFC3339),
		level,
		strings.TrimSpace(transition.Component),
		strings.TrimSpace(transition.FromState),
		strings.TrimSpace(transition.ToState),
		strings.TrimSpace(snapshot.Overall),
	)
	if detail := strings.TrimSpace(transition.Message); detail != "" {
		line += " detail=" + truncateSingleLine(detail, 240)
	}
	if errorText := strings.TrimSpace(transition.Error); errorText != "" {
		line += " error=" + truncateSingleLine(errorText, 240)
	}
	return line
}

func appendWorkspaceHeartbeatLog(workspaceRoot, workspaceID, line string) error {
	workspaceRoot = strings.TrimSpace(workspaceRoot)
	workspaceID = strings.TrimSpace(workspaceID)
	if workspaceRoot == "" || workspaceID == "" {
		return nil
	}
	targetDir := filepath.Join(workspaceRoot, workspaceID, "ops")
	if err := os.MkdirAll(targetDir, 0o755); err != nil {
		return err
	}
	targetFile := filepath.Join(targetDir, "heartbeat.md")
	header := "# Heartbeat Log\n\n"
	if _, err := os.Stat(targetFile); err != nil {
		if os.IsNotExist(err) {
			if err := os.WriteFile(targetFile, []byte(header), 0o644); err != nil {
				return err
			}
		} else {
			return err
		}
	}
	entry := strings.TrimSpace(line)
	if entry == "" {
		return nil
	}
	content := entry + "\n"
	file, err := os.OpenFile(targetFile, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer file.Close()
	_, err = file.WriteString(content)
	return err
}
