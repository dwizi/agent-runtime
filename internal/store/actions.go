package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
)

var (
	ErrActionApprovalNotFound = errors.New("action approval not found")
	ErrActionApprovalNotReady = errors.New("action approval is not pending")
)

type CreateActionApprovalInput struct {
	WorkspaceID     string
	ContextID       string
	Connector       string
	ExternalID      string
	RequesterUserID string
	ActionType      string
	ActionTarget    string
	ActionSummary   string
	Payload         map[string]any
}

type ActionApproval struct {
	ID               string
	WorkspaceID      string
	ContextID        string
	Connector        string
	ExternalID       string
	RequesterUserID  string
	ActionType       string
	ActionTarget     string
	ActionSummary    string
	Payload          map[string]any
	Status           string
	ApproverUserID   string
	DeniedReason     string
	ExecutionStatus  string
	ExecutionMessage string
	ExecutorPlugin   string
	ExecutedAt       time.Time
	CreatedAt        time.Time
	UpdatedAt        time.Time
}

type ApproveActionApprovalInput struct {
	ID             string
	ApproverUserID string
}

type DenyActionApprovalInput struct {
	ID             string
	ApproverUserID string
	Reason         string
}

type UpdateActionExecutionInput struct {
	ID               string
	ExecutionStatus  string
	ExecutionMessage string
	ExecutorPlugin   string
	ExecutedAt       time.Time
}

func (s *Store) CreateActionApproval(ctx context.Context, input CreateActionApprovalInput) (ActionApproval, error) {
	now := time.Now().UTC()
	payload := input.Payload
	if payload == nil {
		payload = map[string]any{}
	}
	payloadJSON, err := json.Marshal(payload)
	if err != nil {
		return ActionApproval{}, fmt.Errorf("encode action payload: %w", err)
	}

	record := ActionApproval{
		ID:              "act_" + uuid.NewString(),
		WorkspaceID:     strings.TrimSpace(input.WorkspaceID),
		ContextID:       strings.TrimSpace(input.ContextID),
		Connector:       strings.ToLower(strings.TrimSpace(input.Connector)),
		ExternalID:      strings.TrimSpace(input.ExternalID),
		RequesterUserID: strings.TrimSpace(input.RequesterUserID),
		ActionType:      strings.TrimSpace(input.ActionType),
		ActionTarget:    strings.TrimSpace(input.ActionTarget),
		ActionSummary:   strings.TrimSpace(input.ActionSummary),
		Payload:         payload,
		Status:          "pending",
		ExecutionStatus: "not_executed",
		CreatedAt:       now,
		UpdatedAt:       now,
	}
	if record.WorkspaceID == "" || record.ContextID == "" || record.Connector == "" || record.ExternalID == "" || record.RequesterUserID == "" || record.ActionType == "" {
		return ActionApproval{}, fmt.Errorf("missing required action approval fields")
	}

	if _, err := s.db.ExecContext(
		ctx,
		`INSERT INTO action_approvals (
			id, workspace_id, context_id, connector, external_id, requester_user_id, action_type, action_target, action_summary, payload_json, status, execution_status, execution_message, executor_plugin, executed_at_unix, created_at_unix, updated_at_unix
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		record.ID,
		record.WorkspaceID,
		record.ContextID,
		record.Connector,
		record.ExternalID,
		record.RequesterUserID,
		record.ActionType,
		record.ActionTarget,
		record.ActionSummary,
		string(payloadJSON),
		record.Status,
		"not_executed",
		"",
		"",
		nil,
		record.CreatedAt.Unix(),
		record.UpdatedAt.Unix(),
	); err != nil {
		return ActionApproval{}, fmt.Errorf("insert action approval: %w", err)
	}
	return record, nil
}

func (s *Store) ListPendingActionApprovals(ctx context.Context, connector, externalID string, limit int) ([]ActionApproval, error) {
	if limit < 1 {
		limit = 10
	}
	rows, err := s.db.QueryContext(
		ctx,
		`SELECT id, workspace_id, context_id, connector, external_id, requester_user_id, action_type, action_target, action_summary, payload_json, status, approver_user_id, denied_reason
		 , execution_status, execution_message, executor_plugin, executed_at_unix, created_at_unix, updated_at_unix
		 FROM action_approvals
		 WHERE connector = ? AND external_id = ? AND status = 'pending'
		 ORDER BY created_at_unix ASC
		 LIMIT ?`,
		strings.ToLower(strings.TrimSpace(connector)),
		strings.TrimSpace(externalID),
		limit,
	)
	if err != nil {
		return nil, fmt.Errorf("query pending action approvals: %w", err)
	}
	defer rows.Close()

	results := []ActionApproval{}
	for rows.Next() {
		record, scanErr := scanActionApproval(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		results = append(results, record)
	}
	return results, nil
}

func (s *Store) ListPendingActionApprovalsGlobal(ctx context.Context, limit int) ([]ActionApproval, error) {
	if limit < 1 {
		limit = 10
	}
	rows, err := s.db.QueryContext(
		ctx,
		`SELECT id, workspace_id, context_id, connector, external_id, requester_user_id, action_type, action_target, action_summary, payload_json, status, approver_user_id, denied_reason
		 , execution_status, execution_message, executor_plugin, executed_at_unix, created_at_unix, updated_at_unix
		 FROM action_approvals
		 WHERE status = 'pending'
		 ORDER BY created_at_unix ASC
		 LIMIT ?`,
		limit,
	)
	if err != nil {
		return nil, fmt.Errorf("query global pending action approvals: %w", err)
	}
	defer rows.Close()

	results := []ActionApproval{}
	for rows.Next() {
		record, scanErr := scanActionApproval(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		results = append(results, record)
	}
	return results, nil
}

func (s *Store) LookupActionApproval(ctx context.Context, id string) (ActionApproval, error) {
	row := s.db.QueryRowContext(
		ctx,
		`SELECT id, workspace_id, context_id, connector, external_id, requester_user_id, action_type, action_target, action_summary, payload_json, status, approver_user_id, denied_reason
		 , execution_status, execution_message, executor_plugin, executed_at_unix, created_at_unix, updated_at_unix
		 FROM action_approvals
		 WHERE id = ?`,
		strings.TrimSpace(id),
	)
	record, err := scanActionApproval(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ActionApproval{}, ErrActionApprovalNotFound
		}
		return ActionApproval{}, err
	}
	return record, nil
}

func (s *Store) ApproveActionApproval(ctx context.Context, input ApproveActionApprovalInput) (ActionApproval, error) {
	record, err := s.LookupActionApproval(ctx, input.ID)
	if err != nil {
		return ActionApproval{}, err
	}
	if record.Status != "pending" {
		return ActionApproval{}, ErrActionApprovalNotReady
	}
	now := time.Now().UTC()
	if _, err := s.db.ExecContext(
		ctx,
		`UPDATE action_approvals SET status = 'approved', approver_user_id = ?, updated_at_unix = ? WHERE id = ?`,
		strings.TrimSpace(input.ApproverUserID),
		now.Unix(),
		record.ID,
	); err != nil {
		return ActionApproval{}, fmt.Errorf("approve action approval: %w", err)
	}
	record.Status = "approved"
	record.ApproverUserID = strings.TrimSpace(input.ApproverUserID)
	record.UpdatedAt = now
	return record, nil
}

func (s *Store) DenyActionApproval(ctx context.Context, input DenyActionApprovalInput) (ActionApproval, error) {
	record, err := s.LookupActionApproval(ctx, input.ID)
	if err != nil {
		return ActionApproval{}, err
	}
	if record.Status != "pending" {
		return ActionApproval{}, ErrActionApprovalNotReady
	}
	now := time.Now().UTC()
	reason := strings.TrimSpace(input.Reason)
	if reason == "" {
		reason = "denied by admin"
	}
	if _, err := s.db.ExecContext(
		ctx,
		`UPDATE action_approvals SET status = 'denied', approver_user_id = ?, denied_reason = ?, updated_at_unix = ? WHERE id = ?`,
		strings.TrimSpace(input.ApproverUserID),
		reason,
		now.Unix(),
		record.ID,
	); err != nil {
		return ActionApproval{}, fmt.Errorf("deny action approval: %w", err)
	}
	record.Status = "denied"
	record.ApproverUserID = strings.TrimSpace(input.ApproverUserID)
	record.DeniedReason = reason
	record.UpdatedAt = now
	return record, nil
}

func (s *Store) UpdateActionExecution(ctx context.Context, input UpdateActionExecutionInput) (ActionApproval, error) {
	record, err := s.LookupActionApproval(ctx, input.ID)
	if err != nil {
		return ActionApproval{}, err
	}
	status := strings.TrimSpace(strings.ToLower(input.ExecutionStatus))
	if status == "" {
		return ActionApproval{}, fmt.Errorf("execution status is required")
	}
	executedAt := input.ExecutedAt.UTC()
	if executedAt.IsZero() {
		executedAt = time.Now().UTC()
	}
	now := time.Now().UTC()
	if _, err := s.db.ExecContext(
		ctx,
		`UPDATE action_approvals
		 SET execution_status = ?, execution_message = ?, executor_plugin = ?, executed_at_unix = ?, updated_at_unix = ?
		 WHERE id = ?`,
		status,
		strings.TrimSpace(input.ExecutionMessage),
		strings.TrimSpace(input.ExecutorPlugin),
		executedAt.Unix(),
		now.Unix(),
		record.ID,
	); err != nil {
		return ActionApproval{}, fmt.Errorf("update action execution: %w", err)
	}
	record.ExecutionStatus = status
	record.ExecutionMessage = strings.TrimSpace(input.ExecutionMessage)
	record.ExecutorPlugin = strings.TrimSpace(input.ExecutorPlugin)
	record.ExecutedAt = executedAt
	record.UpdatedAt = now
	return record, nil
}

type actionApprovalScanner interface {
	Scan(dest ...any) error
}

func scanActionApproval(scanner actionApprovalScanner) (ActionApproval, error) {
	var record ActionApproval
	var payloadJSON string
	var approver sql.NullString
	var deniedReason sql.NullString
	var executionMessage sql.NullString
	var executorPlugin sql.NullString
	var executedAtUnix sql.NullInt64
	var createdAtUnix int64
	var updatedAtUnix int64
	err := scanner.Scan(
		&record.ID,
		&record.WorkspaceID,
		&record.ContextID,
		&record.Connector,
		&record.ExternalID,
		&record.RequesterUserID,
		&record.ActionType,
		&record.ActionTarget,
		&record.ActionSummary,
		&payloadJSON,
		&record.Status,
		&approver,
		&deniedReason,
		&record.ExecutionStatus,
		&executionMessage,
		&executorPlugin,
		&executedAtUnix,
		&createdAtUnix,
		&updatedAtUnix,
	)
	if err != nil {
		return ActionApproval{}, err
	}
	record.ApproverUserID = approver.String
	record.DeniedReason = deniedReason.String
	record.ExecutionMessage = executionMessage.String
	record.ExecutorPlugin = executorPlugin.String
	if executedAtUnix.Valid && executedAtUnix.Int64 > 0 {
		record.ExecutedAt = time.Unix(executedAtUnix.Int64, 0).UTC()
	}
	record.CreatedAt = time.Unix(createdAtUnix, 0).UTC()
	record.UpdatedAt = time.Unix(updatedAtUnix, 0).UTC()
	if strings.TrimSpace(payloadJSON) != "" {
		if err := json.Unmarshal([]byte(payloadJSON), &record.Payload); err != nil {
			return ActionApproval{}, fmt.Errorf("decode action payload: %w", err)
		}
	}
	if record.Payload == nil {
		record.Payload = map[string]any{}
	}
	return record, nil
}
