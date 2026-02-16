package store

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
)

type AgentAuditEvent struct {
	ID           string
	WorkspaceID  string
	ContextID    string
	Connector    string
	ExternalID   string
	SourceUserID string
	EventType    string
	Stage        string
	ToolName     string
	ToolClass    string
	Blocked      bool
	BlockReason  string
	Message      string
	CreatedAt    time.Time
}

type CreateAgentAuditEventInput struct {
	WorkspaceID  string
	ContextID    string
	Connector    string
	ExternalID   string
	SourceUserID string
	EventType    string
	Stage        string
	ToolName     string
	ToolClass    string
	Blocked      bool
	BlockReason  string
	Message      string
}

type ListAgentAuditEventsInput struct {
	WorkspaceID string
	ContextID   string
	Connector   string
	ExternalID  string
	EventType   string
	BlockedOnly bool
	Limit       int
}

func (s *Store) CreateAgentAuditEvent(ctx context.Context, input CreateAgentAuditEventInput) (AgentAuditEvent, error) {
	now := time.Now().UTC()
	record := AgentAuditEvent{
		ID:           "audit_" + uuid.NewString(),
		WorkspaceID:  strings.TrimSpace(input.WorkspaceID),
		ContextID:    strings.TrimSpace(input.ContextID),
		Connector:    strings.ToLower(strings.TrimSpace(input.Connector)),
		ExternalID:   strings.TrimSpace(input.ExternalID),
		SourceUserID: strings.TrimSpace(input.SourceUserID),
		EventType:    strings.TrimSpace(strings.ToLower(input.EventType)),
		Stage:        strings.TrimSpace(input.Stage),
		ToolName:     strings.TrimSpace(input.ToolName),
		ToolClass:    strings.TrimSpace(strings.ToLower(input.ToolClass)),
		Blocked:      input.Blocked,
		BlockReason:  strings.TrimSpace(input.BlockReason),
		Message:      strings.TrimSpace(input.Message),
		CreatedAt:    now,
	}
	if record.WorkspaceID == "" || record.ContextID == "" || record.Connector == "" || record.ExternalID == "" || record.EventType == "" || record.Stage == "" {
		return AgentAuditEvent{}, fmt.Errorf("missing required agent audit event fields")
	}

	if _, err := s.db.ExecContext(
		ctx,
		`INSERT INTO agent_audit_events (
			id, workspace_id, context_id, connector, external_id, source_user_id, event_type, stage, tool_name, tool_class, blocked, block_reason, message, created_at_unix
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		record.ID,
		record.WorkspaceID,
		record.ContextID,
		record.Connector,
		record.ExternalID,
		nullIfEmpty(record.SourceUserID),
		record.EventType,
		record.Stage,
		nullIfEmpty(record.ToolName),
		nullIfEmpty(record.ToolClass),
		boolToInt(record.Blocked),
		nullIfEmpty(record.BlockReason),
		nullIfEmpty(record.Message),
		record.CreatedAt.Unix(),
	); err != nil {
		return AgentAuditEvent{}, fmt.Errorf("insert agent audit event: %w", err)
	}
	return record, nil
}

func (s *Store) ListAgentAuditEvents(ctx context.Context, input ListAgentAuditEventsInput) ([]AgentAuditEvent, error) {
	limit := input.Limit
	if limit < 1 {
		limit = 100
	}
	if limit > 1000 {
		limit = 1000
	}
	whereParts := []string{"1=1"}
	args := make([]any, 0, 8)

	if workspaceID := strings.TrimSpace(input.WorkspaceID); workspaceID != "" {
		whereParts = append(whereParts, "workspace_id = ?")
		args = append(args, workspaceID)
	}
	if contextID := strings.TrimSpace(input.ContextID); contextID != "" {
		whereParts = append(whereParts, "context_id = ?")
		args = append(args, contextID)
	}
	if connector := strings.ToLower(strings.TrimSpace(input.Connector)); connector != "" {
		whereParts = append(whereParts, "connector = ?")
		args = append(args, connector)
	}
	if externalID := strings.TrimSpace(input.ExternalID); externalID != "" {
		whereParts = append(whereParts, "external_id = ?")
		args = append(args, externalID)
	}
	if eventType := strings.ToLower(strings.TrimSpace(input.EventType)); eventType != "" {
		whereParts = append(whereParts, "event_type = ?")
		args = append(args, eventType)
	}
	if input.BlockedOnly {
		whereParts = append(whereParts, "blocked = 1")
	}
	args = append(args, limit)

	rows, err := s.db.QueryContext(
		ctx,
		`SELECT id, workspace_id, context_id, connector, external_id, COALESCE(source_user_id, ''), event_type, stage, COALESCE(tool_name, ''), COALESCE(tool_class, ''), blocked, COALESCE(block_reason, ''), COALESCE(message, ''), created_at_unix
		 FROM agent_audit_events
		 WHERE `+strings.Join(whereParts, " AND ")+`
		 ORDER BY created_at_unix DESC
		 LIMIT ?`,
		args...,
	)
	if err != nil {
		return nil, fmt.Errorf("query agent audit events: %w", err)
	}
	defer rows.Close()

	events := make([]AgentAuditEvent, 0, limit)
	for rows.Next() {
		var event AgentAuditEvent
		var blocked int
		var createdAtUnix int64
		if err := rows.Scan(
			&event.ID,
			&event.WorkspaceID,
			&event.ContextID,
			&event.Connector,
			&event.ExternalID,
			&event.SourceUserID,
			&event.EventType,
			&event.Stage,
			&event.ToolName,
			&event.ToolClass,
			&blocked,
			&event.BlockReason,
			&event.Message,
			&createdAtUnix,
		); err != nil {
			return nil, err
		}
		event.Blocked = blocked == 1
		if createdAtUnix > 0 {
			event.CreatedAt = time.Unix(createdAtUnix, 0).UTC()
		}
		events = append(events, event)
	}
	return events, nil
}
