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

type TaskRecord struct {
	ID            string
	WorkspaceID   string
	ContextID     string
	Kind          string
	Title         string
	Prompt        string
	Status        string
	Attempts      int
	WorkerID      int
	StartedAt     time.Time
	FinishedAt    time.Time
	ResultSummary string
	ResultPath    string
	ErrorMessage  string
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

func (s *Store) LookupTask(ctx context.Context, id string) (TaskRecord, error) {
	row := s.db.QueryRowContext(
		ctx,
		`SELECT id, workspace_id, context_id, kind, title, prompt, status, attempts, COALESCE(worker_id, 0), COALESCE(started_at_unix, 0), COALESCE(finished_at_unix, 0), COALESCE(result_summary, ''), COALESCE(result_path, ''), COALESCE(error_message, '')
		 FROM tasks
		 WHERE id = ?`,
		strings.TrimSpace(id),
	)
	var record TaskRecord
	var startedUnix int64
	var finishedUnix int64
	if err := row.Scan(
		&record.ID,
		&record.WorkspaceID,
		&record.ContextID,
		&record.Kind,
		&record.Title,
		&record.Prompt,
		&record.Status,
		&record.Attempts,
		&record.WorkerID,
		&startedUnix,
		&finishedUnix,
		&record.ResultSummary,
		&record.ResultPath,
		&record.ErrorMessage,
	); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return TaskRecord{}, ErrTaskNotFound
		}
		return TaskRecord{}, fmt.Errorf("lookup task: %w", err)
	}
	if startedUnix > 0 {
		record.StartedAt = time.Unix(startedUnix, 0).UTC()
	}
	if finishedUnix > 0 {
		record.FinishedAt = time.Unix(finishedUnix, 0).UTC()
	}
	return record, nil
}
