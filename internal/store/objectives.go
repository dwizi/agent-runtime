package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
)

var (
	ErrObjectiveNotFound = errors.New("objective not found")
	ErrObjectiveInvalid  = errors.New("objective input is invalid")
)

type ObjectiveTriggerType string

const (
	ObjectiveTriggerSchedule ObjectiveTriggerType = "schedule"
	ObjectiveTriggerEvent    ObjectiveTriggerType = "event"
)

type Objective struct {
	ID          string
	WorkspaceID string
	ContextID   string
	Title       string
	Prompt      string
	TriggerType ObjectiveTriggerType
	EventKey    string
	CronExpr    string
	Active      bool
	NextRunAt   time.Time
	LastRunAt   time.Time
	LastError   string
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

type CreateObjectiveInput struct {
	WorkspaceID string
	ContextID   string
	Title       string
	Prompt      string
	TriggerType ObjectiveTriggerType
	EventKey    string
	CronExpr    string
	NextRunAt   time.Time
	Active      bool
}

type ListObjectivesInput struct {
	WorkspaceID string
	ActiveOnly  bool
	Limit       int
}

type UpdateObjectiveRunInput struct {
	ID        string
	LastRunAt time.Time
	NextRunAt time.Time
	LastError string
}

type UpdateObjectiveInput struct {
	ID          string
	Title       *string
	Prompt      *string
	TriggerType *ObjectiveTriggerType
	EventKey    *string
	CronExpr    *string
	NextRunAt   *time.Time
	Active      *bool
}

func (s *Store) CreateObjective(ctx context.Context, input CreateObjectiveInput) (Objective, error) {
	now := time.Now().UTC()
	record := Objective{
		ID:          "obj_" + uuid.NewString(),
		WorkspaceID: strings.TrimSpace(input.WorkspaceID),
		ContextID:   strings.TrimSpace(input.ContextID),
		Title:       strings.TrimSpace(input.Title),
		Prompt:      strings.TrimSpace(input.Prompt),
		TriggerType: input.TriggerType,
		EventKey:    strings.TrimSpace(strings.ToLower(input.EventKey)),
		CronExpr:    normalizeCronExpr(input.CronExpr),
		Active:      input.Active,
		NextRunAt:   input.NextRunAt.UTC(),
		CreatedAt:   now,
		UpdatedAt:   now,
	}
	if !record.Active {
		record.Active = true
	}
	if record.WorkspaceID == "" || record.ContextID == "" || record.Title == "" || record.Prompt == "" {
		return Objective{}, ErrObjectiveInvalid
	}
	switch record.TriggerType {
	case ObjectiveTriggerSchedule:
		if record.CronExpr == "" {
			return Objective{}, ErrObjectiveInvalid
		}
		if _, err := ComputeScheduleNextRun(record.CronExpr, now); err != nil {
			return Objective{}, ErrObjectiveInvalid
		}
		if record.NextRunAt.IsZero() {
			nextRun, err := ComputeScheduleNextRun(record.CronExpr, now)
			if err != nil {
				return Objective{}, ErrObjectiveInvalid
			}
			record.NextRunAt = nextRun
		}
	case ObjectiveTriggerEvent:
		if record.EventKey == "" {
			return Objective{}, ErrObjectiveInvalid
		}
		record.NextRunAt = time.Time{}
		record.CronExpr = ""
	default:
		return Objective{}, ErrObjectiveInvalid
	}

	if _, err := s.db.ExecContext(
		ctx,
		`INSERT INTO objectives (
			id, workspace_id, context_id, title, prompt, trigger_type, event_key, cron_expr, active, next_run_unix, last_run_unix, last_error, created_at_unix, updated_at_unix
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		record.ID,
		record.WorkspaceID,
		record.ContextID,
		record.Title,
		record.Prompt,
		string(record.TriggerType),
		nullIfEmpty(record.EventKey),
		nullIfEmpty(record.CronExpr),
		boolToInt(record.Active),
		nullTimeUnix(record.NextRunAt),
		nil,
		nil,
		record.CreatedAt.Unix(),
		record.UpdatedAt.Unix(),
	); err != nil {
		return Objective{}, fmt.Errorf("insert objective: %w", err)
	}
	return record, nil
}

func (s *Store) ListObjectives(ctx context.Context, input ListObjectivesInput) ([]Objective, error) {
	workspaceID := strings.TrimSpace(input.WorkspaceID)
	if workspaceID == "" {
		return nil, ErrObjectiveInvalid
	}
	limit := input.Limit
	if limit < 1 {
		limit = 50
	}
	whereParts := []string{"workspace_id = ?"}
	args := []any{workspaceID}
	if input.ActiveOnly {
		whereParts = append(whereParts, "active = 1")
	}
	query := `SELECT id, workspace_id, context_id, title, prompt, trigger_type, event_key, cron_expr, active, next_run_unix, last_run_unix, last_error, created_at_unix, updated_at_unix
		FROM objectives
		WHERE ` + strings.Join(whereParts, " AND ") + `
		ORDER BY created_at_unix ASC
		LIMIT ?`
	args = append(args, limit)

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("query objectives: %w", err)
	}
	defer rows.Close()

	results := []Objective{}
	for rows.Next() {
		record, scanErr := scanObjective(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		results = append(results, record)
	}
	return results, nil
}

func (s *Store) ListDueObjectives(ctx context.Context, now time.Time, limit int) ([]Objective, error) {
	if limit < 1 {
		limit = 20
	}
	current := now.UTC()
	rows, err := s.db.QueryContext(
		ctx,
		`SELECT id, workspace_id, context_id, title, prompt, trigger_type, event_key, cron_expr, active, next_run_unix, last_run_unix, last_error, created_at_unix, updated_at_unix
		 FROM objectives
		 WHERE active = 1
		   AND trigger_type = ?
		   AND next_run_unix IS NOT NULL
		   AND next_run_unix <= ?
		 ORDER BY next_run_unix ASC
		 LIMIT ?`,
		string(ObjectiveTriggerSchedule),
		current.Unix(),
		limit,
	)
	if err != nil {
		return nil, fmt.Errorf("query due objectives: %w", err)
	}
	defer rows.Close()
	results := []Objective{}
	for rows.Next() {
		record, scanErr := scanObjective(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		results = append(results, record)
	}
	return results, nil
}

func (s *Store) ListEventObjectives(ctx context.Context, workspaceID, eventKey string, limit int) ([]Objective, error) {
	workspaceID = strings.TrimSpace(workspaceID)
	eventKey = strings.TrimSpace(strings.ToLower(eventKey))
	if workspaceID == "" || eventKey == "" {
		return nil, ErrObjectiveInvalid
	}
	if limit < 1 {
		limit = 20
	}
	rows, err := s.db.QueryContext(
		ctx,
		`SELECT id, workspace_id, context_id, title, prompt, trigger_type, event_key, cron_expr, active, next_run_unix, last_run_unix, last_error, created_at_unix, updated_at_unix
		 FROM objectives
		 WHERE active = 1
		   AND workspace_id = ?
		   AND trigger_type = ?
		   AND event_key = ?
		 ORDER BY created_at_unix ASC
		 LIMIT ?`,
		workspaceID,
		string(ObjectiveTriggerEvent),
		eventKey,
		limit,
	)
	if err != nil {
		return nil, fmt.Errorf("query event objectives: %w", err)
	}
	defer rows.Close()
	results := []Objective{}
	for rows.Next() {
		record, scanErr := scanObjective(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		results = append(results, record)
	}
	return results, nil
}

func (s *Store) UpdateObjectiveRun(ctx context.Context, input UpdateObjectiveRunInput) (Objective, error) {
	id := strings.TrimSpace(input.ID)
	if id == "" {
		return Objective{}, ErrObjectiveInvalid
	}
	now := time.Now().UTC()
	lastRun := input.LastRunAt.UTC()
	if lastRun.IsZero() {
		lastRun = now
	}
	nextRun := input.NextRunAt.UTC()
	_, err := s.db.ExecContext(
		ctx,
		`UPDATE objectives
		 SET last_run_unix = ?, next_run_unix = ?, last_error = ?, updated_at_unix = ?
		 WHERE id = ?`,
		lastRun.Unix(),
		nullTimeUnix(nextRun),
		nullIfEmpty(strings.TrimSpace(input.LastError)),
		now.Unix(),
		id,
	)
	if err != nil {
		return Objective{}, fmt.Errorf("update objective run: %w", err)
	}
	return s.LookupObjective(ctx, id)
}

func (s *Store) LookupObjective(ctx context.Context, id string) (Objective, error) {
	row := s.db.QueryRowContext(
		ctx,
		`SELECT id, workspace_id, context_id, title, prompt, trigger_type, event_key, cron_expr, active, next_run_unix, last_run_unix, last_error, created_at_unix, updated_at_unix
		 FROM objectives
		 WHERE id = ?`,
		strings.TrimSpace(id),
	)
	record, err := scanObjective(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Objective{}, ErrObjectiveNotFound
		}
		return Objective{}, err
	}
	return record, nil
}

func (s *Store) UpdateObjective(ctx context.Context, input UpdateObjectiveInput) (Objective, error) {
	record, err := s.LookupObjective(ctx, input.ID)
	if err != nil {
		return Objective{}, err
	}
	if input.Title != nil {
		record.Title = strings.TrimSpace(*input.Title)
	}
	if input.Prompt != nil {
		record.Prompt = strings.TrimSpace(*input.Prompt)
	}
	if input.TriggerType != nil {
		record.TriggerType = *input.TriggerType
	}
	if input.EventKey != nil {
		record.EventKey = strings.TrimSpace(strings.ToLower(*input.EventKey))
	}
	if input.CronExpr != nil {
		record.CronExpr = normalizeCronExpr(*input.CronExpr)
	}
	if input.NextRunAt != nil {
		record.NextRunAt = input.NextRunAt.UTC()
	}
	if input.Active != nil {
		record.Active = *input.Active
	}

	now := time.Now().UTC()
	if strings.TrimSpace(record.Title) == "" || strings.TrimSpace(record.Prompt) == "" {
		return Objective{}, ErrObjectiveInvalid
	}
	switch record.TriggerType {
	case ObjectiveTriggerSchedule:
		record.EventKey = ""
		if record.CronExpr == "" {
			return Objective{}, ErrObjectiveInvalid
		}
		if _, err := ComputeScheduleNextRun(record.CronExpr, now); err != nil {
			return Objective{}, ErrObjectiveInvalid
		}
		if record.Active && record.NextRunAt.IsZero() {
			nextRun, err := ComputeScheduleNextRun(record.CronExpr, now)
			if err != nil {
				return Objective{}, ErrObjectiveInvalid
			}
			record.NextRunAt = nextRun
		}
	case ObjectiveTriggerEvent:
		if strings.TrimSpace(record.EventKey) == "" {
			return Objective{}, ErrObjectiveInvalid
		}
		record.CronExpr = ""
		record.NextRunAt = time.Time{}
	default:
		return Objective{}, ErrObjectiveInvalid
	}
	record.UpdatedAt = now

	if _, err := s.db.ExecContext(
		ctx,
		`UPDATE objectives
		 SET title = ?, prompt = ?, trigger_type = ?, event_key = ?, cron_expr = ?, active = ?, next_run_unix = ?, updated_at_unix = ?
		 WHERE id = ?`,
		record.Title,
		record.Prompt,
		string(record.TriggerType),
		nullIfEmpty(record.EventKey),
		nullIfEmpty(record.CronExpr),
		boolToInt(record.Active),
		nullTimeUnix(record.NextRunAt),
		record.UpdatedAt.Unix(),
		record.ID,
	); err != nil {
		return Objective{}, fmt.Errorf("update objective: %w", err)
	}
	return s.LookupObjective(ctx, record.ID)
}

func (s *Store) SetObjectiveActive(ctx context.Context, id string, active bool) (Objective, error) {
	return s.UpdateObjective(ctx, UpdateObjectiveInput{
		ID:     strings.TrimSpace(id),
		Active: &active,
	})
}

func (s *Store) DeleteObjective(ctx context.Context, id string) error {
	id = strings.TrimSpace(id)
	if id == "" {
		return ErrObjectiveInvalid
	}
	result, err := s.db.ExecContext(ctx, `DELETE FROM objectives WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("delete objective: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("delete objective rows affected: %w", err)
	}
	if affected < 1 {
		return ErrObjectiveNotFound
	}
	return nil
}

type objectiveScanner interface {
	Scan(dest ...any) error
}

func scanObjective(scanner objectiveScanner) (Objective, error) {
	var record Objective
	var triggerType string
	var eventKey sql.NullString
	var cronExpr sql.NullString
	var active int
	var nextRunUnix sql.NullInt64
	var lastRunUnix sql.NullInt64
	var lastError sql.NullString
	var createdAtUnix int64
	var updatedAtUnix int64
	if err := scanner.Scan(
		&record.ID,
		&record.WorkspaceID,
		&record.ContextID,
		&record.Title,
		&record.Prompt,
		&triggerType,
		&eventKey,
		&cronExpr,
		&active,
		&nextRunUnix,
		&lastRunUnix,
		&lastError,
		&createdAtUnix,
		&updatedAtUnix,
	); err != nil {
		return Objective{}, err
	}
	record.TriggerType = ObjectiveTriggerType(strings.TrimSpace(triggerType))
	record.EventKey = eventKey.String
	record.CronExpr = normalizeCronExpr(cronExpr.String)
	record.Active = active == 1
	if nextRunUnix.Valid && nextRunUnix.Int64 > 0 {
		record.NextRunAt = time.Unix(nextRunUnix.Int64, 0).UTC()
	}
	if lastRunUnix.Valid && lastRunUnix.Int64 > 0 {
		record.LastRunAt = time.Unix(lastRunUnix.Int64, 0).UTC()
	}
	record.LastError = lastError.String
	record.CreatedAt = time.Unix(createdAtUnix, 0).UTC()
	record.UpdatedAt = time.Unix(updatedAtUnix, 0).UTC()
	return record, nil
}

func boolToInt(value bool) int {
	if value {
		return 1
	}
	return 0
}

func nullIfEmpty(value string) any {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	return value
}

func nullTimeUnix(value time.Time) any {
	if value.IsZero() {
		return nil
	}
	return value.UTC().Unix()
}
