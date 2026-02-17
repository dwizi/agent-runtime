package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

var ErrTaskNotFound = errors.New("task not found")
var ErrTaskRunAlreadyExists = errors.New("task run already exists")
var ErrTaskNotRunningForWorker = errors.New("task not running for worker")

type TaskRecord struct {
	ID               string
	WorkspaceID      string
	ContextID        string
	Kind             string
	Title            string
	Prompt           string
	Status           string
	RouteClass       string
	Priority         string
	DueAt            time.Time
	AssignedLane     string
	SourceConnector  string
	SourceExternalID string
	SourceUserID     string
	SourceText       string
	Attempts         int
	WorkerID         int
	StartedAt        time.Time
	FinishedAt       time.Time
	ResultSummary    string
	ResultPath       string
	ErrorMessage     string
	CreatedAt        time.Time
	UpdatedAt        time.Time
}

type ListTasksInput struct {
	WorkspaceID string
	ContextID   string
	Kind        string
	Status      string
	Limit       int
}

func (s *Store) MarkTaskRunning(ctx context.Context, id string, workerID int, startedAt time.Time) error {
	id = strings.TrimSpace(id)
	if id == "" {
		return ErrTaskNotFound
	}
	if startedAt.IsZero() {
		startedAt = time.Now().UTC()
	}
	result, err := s.db.ExecContext(
		ctx,
		`UPDATE tasks
		 SET status = 'running',
		     attempts = attempts + 1,
		     worker_id = ?,
		     started_at_unix = ?,
		     finished_at_unix = NULL,
		     error_message = NULL,
		     result_summary = NULL,
		     result_path = NULL,
		     updated_at_unix = ?
		 WHERE id = ?`,
		workerID,
		startedAt.Unix(),
		time.Now().UTC().Unix(),
		id,
	)
	if err != nil {
		return fmt.Errorf("mark task running: %w", err)
	}
	rowsAffected, err := result.RowsAffected()
	if err == nil && rowsAffected == 0 {
		return ErrTaskNotFound
	}
	return nil
}

func (s *Store) RequeueTask(ctx context.Context, id string) error {
	id = strings.TrimSpace(id)
	if id == "" {
		return ErrTaskNotFound
	}
	result, err := s.db.ExecContext(
		ctx,
		`UPDATE tasks
		 SET status = 'queued',
		     worker_id = NULL,
		     started_at_unix = NULL,
		     finished_at_unix = NULL,
		     result_summary = NULL,
		     result_path = NULL,
		     error_message = NULL,
		     updated_at_unix = ?
		 WHERE id = ?`,
		time.Now().UTC().Unix(),
		id,
	)
	if err != nil {
		return fmt.Errorf("requeue task: %w", err)
	}
	rowsAffected, err := result.RowsAffected()
	if err == nil && rowsAffected == 0 {
		return ErrTaskNotFound
	}
	return nil
}

func (s *Store) MarkTaskCompleted(ctx context.Context, id string, finishedAt time.Time, summary, resultPath string) error {
	id = strings.TrimSpace(id)
	if id == "" {
		return ErrTaskNotFound
	}
	if finishedAt.IsZero() {
		finishedAt = time.Now().UTC()
	}
	result, err := s.db.ExecContext(
		ctx,
		`UPDATE tasks
		 SET status = 'succeeded',
		     finished_at_unix = ?,
		     result_summary = ?,
		     result_path = ?,
		     error_message = NULL,
		     updated_at_unix = ?
		 WHERE id = ?`,
		finishedAt.Unix(),
		nullIfEmpty(strings.TrimSpace(summary)),
		nullIfEmpty(strings.TrimSpace(resultPath)),
		time.Now().UTC().Unix(),
		id,
	)
	if err != nil {
		return fmt.Errorf("mark task completed: %w", err)
	}
	rowsAffected, err := result.RowsAffected()
	if err == nil && rowsAffected == 0 {
		return ErrTaskNotFound
	}
	return nil
}

func (s *Store) MarkTaskCompletedByWorker(ctx context.Context, id string, workerID int, finishedAt time.Time, summary, resultPath string) error {
	id = strings.TrimSpace(id)
	if id == "" {
		return ErrTaskNotFound
	}
	if workerID < 1 {
		return ErrTaskNotRunningForWorker
	}
	if finishedAt.IsZero() {
		finishedAt = time.Now().UTC()
	}
	result, err := s.db.ExecContext(
		ctx,
		`UPDATE tasks
		 SET status = 'succeeded',
		     finished_at_unix = ?,
		     result_summary = ?,
		     result_path = ?,
		     error_message = NULL,
		     updated_at_unix = ?
		 WHERE id = ? AND status = 'running' AND worker_id = ?`,
		finishedAt.Unix(),
		nullIfEmpty(strings.TrimSpace(summary)),
		nullIfEmpty(strings.TrimSpace(resultPath)),
		time.Now().UTC().Unix(),
		id,
		workerID,
	)
	if err != nil {
		return fmt.Errorf("mark task completed by worker: %w", err)
	}
	rowsAffected, err := result.RowsAffected()
	if err == nil && rowsAffected == 0 {
		return ErrTaskNotRunningForWorker
	}
	return nil
}

func (s *Store) MarkTaskFailed(ctx context.Context, id string, finishedAt time.Time, message string) error {
	id = strings.TrimSpace(id)
	if id == "" {
		return ErrTaskNotFound
	}
	if finishedAt.IsZero() {
		finishedAt = time.Now().UTC()
	}
	result, err := s.db.ExecContext(
		ctx,
		`UPDATE tasks
		 SET status = 'failed',
		     finished_at_unix = ?,
		     error_message = ?,
		     updated_at_unix = ?
		 WHERE id = ?`,
		finishedAt.Unix(),
		nullIfEmpty(strings.TrimSpace(message)),
		time.Now().UTC().Unix(),
		id,
	)
	if err != nil {
		return fmt.Errorf("mark task failed: %w", err)
	}
	rowsAffected, err := result.RowsAffected()
	if err == nil && rowsAffected == 0 {
		return ErrTaskNotFound
	}
	return nil
}

func (s *Store) MarkTaskFailedByWorker(ctx context.Context, id string, workerID int, finishedAt time.Time, message string) error {
	id = strings.TrimSpace(id)
	if id == "" {
		return ErrTaskNotFound
	}
	if workerID < 1 {
		return ErrTaskNotRunningForWorker
	}
	if finishedAt.IsZero() {
		finishedAt = time.Now().UTC()
	}
	result, err := s.db.ExecContext(
		ctx,
		`UPDATE tasks
		 SET status = 'failed',
		     finished_at_unix = ?,
		     error_message = ?,
		     updated_at_unix = ?
		 WHERE id = ? AND status = 'running' AND worker_id = ?`,
		finishedAt.Unix(),
		nullIfEmpty(strings.TrimSpace(message)),
		time.Now().UTC().Unix(),
		id,
		workerID,
	)
	if err != nil {
		return fmt.Errorf("mark task failed by worker: %w", err)
	}
	rowsAffected, err := result.RowsAffected()
	if err == nil && rowsAffected == 0 {
		return ErrTaskNotRunningForWorker
	}
	return nil
}

func (s *Store) LookupTask(ctx context.Context, id string) (TaskRecord, error) {
	row := s.db.QueryRowContext(
		ctx,
		`SELECT id, workspace_id, context_id, kind, title, prompt, status,
		        COALESCE(route_class, ''), COALESCE(priority, ''), COALESCE(due_at_unix, 0),
		        COALESCE(assigned_lane, ''), COALESCE(source_connector, ''), COALESCE(source_external_id, ''), COALESCE(source_user_id, ''), COALESCE(source_text, ''),
		        attempts, COALESCE(worker_id, 0), COALESCE(started_at_unix, 0), COALESCE(finished_at_unix, 0),
		        COALESCE(result_summary, ''), COALESCE(result_path, ''), COALESCE(error_message, ''),
		        created_at, COALESCE(updated_at_unix, 0)
		 FROM tasks
		 WHERE id = ?`,
		strings.TrimSpace(id),
	)
	var record TaskRecord
	var dueAtUnix int64
	var startedUnix int64
	var finishedUnix int64
	var updatedUnix int64
	var createdAtText string
	if err := row.Scan(
		&record.ID,
		&record.WorkspaceID,
		&record.ContextID,
		&record.Kind,
		&record.Title,
		&record.Prompt,
		&record.Status,
		&record.RouteClass,
		&record.Priority,
		&dueAtUnix,
		&record.AssignedLane,
		&record.SourceConnector,
		&record.SourceExternalID,
		&record.SourceUserID,
		&record.SourceText,
		&record.Attempts,
		&record.WorkerID,
		&startedUnix,
		&finishedUnix,
		&record.ResultSummary,
		&record.ResultPath,
		&record.ErrorMessage,
		&createdAtText,
		&updatedUnix,
	); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return TaskRecord{}, ErrTaskNotFound
		}
		return TaskRecord{}, fmt.Errorf("lookup task: %w", err)
	}
	if startedUnix > 0 {
		record.StartedAt = time.Unix(startedUnix, 0).UTC()
	}
	if dueAtUnix > 0 {
		record.DueAt = time.Unix(dueAtUnix, 0).UTC()
	}
	if finishedUnix > 0 {
		record.FinishedAt = time.Unix(finishedUnix, 0).UTC()
	}
	if updatedUnix > 0 {
		record.UpdatedAt = time.Unix(updatedUnix, 0).UTC()
	}
	record.CreatedAt = parseSQLiteDateTime(createdAtText)
	return record, nil
}

func (s *Store) ListTasks(ctx context.Context, input ListTasksInput) ([]TaskRecord, error) {
	limit := input.Limit
	if limit < 1 {
		limit = 100
	}
	if limit > 500 {
		limit = 500
	}

	whereParts := []string{"1=1"}
	args := make([]any, 0, 6)
	if workspaceID := strings.TrimSpace(input.WorkspaceID); workspaceID != "" {
		whereParts = append(whereParts, "workspace_id = ?")
		args = append(args, workspaceID)
	}
	if contextID := strings.TrimSpace(input.ContextID); contextID != "" {
		whereParts = append(whereParts, "context_id = ?")
		args = append(args, contextID)
	}
	if kind := strings.TrimSpace(input.Kind); kind != "" {
		whereParts = append(whereParts, "kind = ?")
		args = append(args, kind)
	}
	if status := strings.TrimSpace(input.Status); status != "" {
		whereParts = append(whereParts, "status = ?")
		args = append(args, status)
	}
	args = append(args, limit)

	rows, err := s.db.QueryContext(
		ctx,
		`SELECT id, workspace_id, context_id, kind, title, prompt, status,
		        COALESCE(route_class, ''), COALESCE(priority, ''), COALESCE(due_at_unix, 0),
		        COALESCE(assigned_lane, ''), COALESCE(source_connector, ''), COALESCE(source_external_id, ''), COALESCE(source_user_id, ''), COALESCE(source_text, ''),
		        attempts, COALESCE(worker_id, 0), COALESCE(started_at_unix, 0), COALESCE(finished_at_unix, 0),
		        COALESCE(result_summary, ''), COALESCE(result_path, ''), COALESCE(error_message, ''), created_at, COALESCE(updated_at_unix, 0)
		 FROM tasks
		 WHERE `+strings.Join(whereParts, " AND ")+`
		 ORDER BY COALESCE(updated_at_unix, 0) DESC, created_at DESC
		 LIMIT ?`,
		args...,
	)
	if err != nil {
		return nil, fmt.Errorf("list tasks: %w", err)
	}
	defer rows.Close()

	results := make([]TaskRecord, 0, limit)
	for rows.Next() {
		var record TaskRecord
		var dueAtUnix int64
		var startedUnix int64
		var finishedUnix int64
		var updatedUnix int64
		var createdAtText string
		if err := rows.Scan(
			&record.ID,
			&record.WorkspaceID,
			&record.ContextID,
			&record.Kind,
			&record.Title,
			&record.Prompt,
			&record.Status,
			&record.RouteClass,
			&record.Priority,
			&dueAtUnix,
			&record.AssignedLane,
			&record.SourceConnector,
			&record.SourceExternalID,
			&record.SourceUserID,
			&record.SourceText,
			&record.Attempts,
			&record.WorkerID,
			&startedUnix,
			&finishedUnix,
			&record.ResultSummary,
			&record.ResultPath,
			&record.ErrorMessage,
			&createdAtText,
			&updatedUnix,
		); err != nil {
			return nil, fmt.Errorf("scan task row: %w", err)
		}
		if startedUnix > 0 {
			record.StartedAt = time.Unix(startedUnix, 0).UTC()
		}
		if dueAtUnix > 0 {
			record.DueAt = time.Unix(dueAtUnix, 0).UTC()
		}
		if finishedUnix > 0 {
			record.FinishedAt = time.Unix(finishedUnix, 0).UTC()
		}
		if updatedUnix > 0 {
			record.UpdatedAt = time.Unix(updatedUnix, 0).UTC()
		}
		record.CreatedAt = parseSQLiteDateTime(createdAtText)
		results = append(results, record)
	}
	return results, nil
}

type UpdateTaskRoutingInput struct {
	ID           string
	RouteClass   string
	Priority     string
	DueAt        time.Time
	AssignedLane string
}

func (s *Store) UpdateTaskRouting(ctx context.Context, input UpdateTaskRoutingInput) (TaskRecord, error) {
	taskID := strings.TrimSpace(input.ID)
	if taskID == "" {
		return TaskRecord{}, ErrTaskNotFound
	}
	routeClass := strings.ToLower(strings.TrimSpace(input.RouteClass))
	priority := strings.ToLower(strings.TrimSpace(input.Priority))
	assignedLane := strings.ToLower(strings.TrimSpace(input.AssignedLane))
	dueAtUnix := int64(0)
	if !input.DueAt.IsZero() {
		dueAtUnix = input.DueAt.UTC().Unix()
	}

	result, err := s.db.ExecContext(
		ctx,
		`UPDATE tasks
		 SET route_class = ?,
		     priority = ?,
		     due_at_unix = ?,
		     assigned_lane = ?,
		     updated_at_unix = ?
		 WHERE id = ?`,
		nullIfEmpty(routeClass),
		nullIfEmpty(priority),
		nullIfZeroInt64(dueAtUnix),
		nullIfEmpty(assignedLane),
		time.Now().UTC().Unix(),
		taskID,
	)
	if err != nil {
		return TaskRecord{}, fmt.Errorf("update task routing: %w", err)
	}
	rowsAffected, err := result.RowsAffected()
	if err == nil && rowsAffected == 0 {
		return TaskRecord{}, ErrTaskNotFound
	}
	return s.LookupTask(ctx, taskID)
}

func parseSQLiteDateTime(input string) time.Time {
	text := strings.TrimSpace(input)
	if text == "" {
		return time.Time{}
	}
	parsed, err := time.ParseInLocation("2006-01-02 15:04:05", text, time.UTC)
	if err != nil {
		return time.Time{}
	}
	return parsed.UTC()
}
