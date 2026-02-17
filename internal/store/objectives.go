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
	ErrObjectiveNotFound = errors.New("objective not found")
	ErrObjectiveInvalid  = errors.New("objective input is invalid")
)

const maxRecentObjectiveErrors = 5

const objectiveSelectColumns = `id, workspace_id, context_id, title, prompt, trigger_type, event_key, cron_expr, timezone, active, next_run_unix, last_run_unix, last_error, run_count, success_count, failure_count, consecutive_failures, consecutive_successes, total_run_duration_ms, last_success_unix, last_failure_unix, auto_paused_reason, recent_errors_json, created_at_unix, updated_at_unix`

type ObjectiveTriggerType string

const (
	ObjectiveTriggerSchedule ObjectiveTriggerType = "schedule"
	ObjectiveTriggerEvent    ObjectiveTriggerType = "event"
)

type ObjectiveRunError struct {
	OccurredAt time.Time `json:"occurred_at"`
	Message    string    `json:"message"`
}

type Objective struct {
	ID                   string
	WorkspaceID          string
	ContextID            string
	Title                string
	Prompt               string
	TriggerType          ObjectiveTriggerType
	EventKey             string
	CronExpr             string
	Timezone             string
	Active               bool
	NextRunAt            time.Time
	LastRunAt            time.Time
	LastError            string
	RunCount             int
	SuccessCount         int
	FailureCount         int
	ConsecutiveFailures  int
	ConsecutiveSuccesses int
	TotalRunDurationMs   int64
	LastSuccessAt        time.Time
	LastFailureAt        time.Time
	AutoPausedReason     string
	RecentErrors         []ObjectiveRunError
	CreatedAt            time.Time
	UpdatedAt            time.Time
}

type CreateObjectiveInput struct {
	WorkspaceID string
	ContextID   string
	Title       string
	Prompt      string
	TriggerType ObjectiveTriggerType
	EventKey    string
	CronExpr    string
	Timezone    string
	NextRunAt   time.Time
	Active      *bool
}

type ListObjectivesInput struct {
	WorkspaceID string
	ActiveOnly  bool
	Limit       int
}

type UpdateObjectiveRunInput struct {
	ID               string
	LastRunAt        time.Time
	NextRunAt        time.Time
	LastError        string
	RunDuration      time.Duration
	SkipStats        bool
	Active           *bool
	AutoPausedReason *string
}

type UpdateObjectiveInput struct {
	ID          string
	Title       *string
	Prompt      *string
	TriggerType *ObjectiveTriggerType
	EventKey    *string
	CronExpr    *string
	Timezone    *string
	NextRunAt   *time.Time
	Active      *bool
}

func (s *Store) CreateObjective(ctx context.Context, input CreateObjectiveInput) (Objective, error) {
	now := time.Now().UTC()
	active := true
	if input.Active != nil {
		active = *input.Active
	}
	timezone, err := normalizeObjectiveTimezone(input.Timezone)
	if err != nil {
		return Objective{}, ErrObjectiveInvalid
	}
	record := Objective{
		ID:                   "obj_" + uuid.NewString(),
		WorkspaceID:          strings.TrimSpace(input.WorkspaceID),
		ContextID:            strings.TrimSpace(input.ContextID),
		Title:                strings.TrimSpace(input.Title),
		Prompt:               strings.TrimSpace(input.Prompt),
		TriggerType:          input.TriggerType,
		EventKey:             strings.TrimSpace(strings.ToLower(input.EventKey)),
		CronExpr:             normalizeCronExpr(input.CronExpr),
		Timezone:             timezone,
		Active:               active,
		NextRunAt:            input.NextRunAt.UTC(),
		RunCount:             0,
		SuccessCount:         0,
		FailureCount:         0,
		ConsecutiveFailures:  0,
		ConsecutiveSuccesses: 0,
		TotalRunDurationMs:   0,
		CreatedAt:            now,
		UpdatedAt:            now,
	}
	if record.WorkspaceID == "" || record.ContextID == "" || record.Title == "" || record.Prompt == "" {
		return Objective{}, ErrObjectiveInvalid
	}
	switch record.TriggerType {
	case ObjectiveTriggerSchedule:
		if record.CronExpr == "" {
			return Objective{}, ErrObjectiveInvalid
		}
		if _, err := ComputeScheduleNextRunForTimezone(record.CronExpr, record.Timezone, now); err != nil {
			return Objective{}, ErrObjectiveInvalid
		}
		if record.NextRunAt.IsZero() {
			nextRun, err := ComputeScheduleNextRunForTimezone(record.CronExpr, record.Timezone, now)
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
			id, workspace_id, context_id, title, prompt, trigger_type, event_key, cron_expr, timezone, active,
			next_run_unix, last_run_unix, last_error,
			run_count, success_count, failure_count, consecutive_failures, consecutive_successes, total_run_duration_ms,
			last_success_unix, last_failure_unix, auto_paused_reason, recent_errors_json,
			created_at_unix, updated_at_unix
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		record.ID,
		record.WorkspaceID,
		record.ContextID,
		record.Title,
		record.Prompt,
		string(record.TriggerType),
		nullIfEmpty(record.EventKey),
		nullIfEmpty(record.CronExpr),
		record.Timezone,
		boolToInt(record.Active),
		nullTimeUnix(record.NextRunAt),
		nil,
		nil,
		record.RunCount,
		record.SuccessCount,
		record.FailureCount,
		record.ConsecutiveFailures,
		record.ConsecutiveSuccesses,
		record.TotalRunDurationMs,
		nil,
		nil,
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
	query := `SELECT ` + objectiveSelectColumns + `
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
		`SELECT `+objectiveSelectColumns+`
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
		`SELECT `+objectiveSelectColumns+`
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
	record, err := s.LookupObjective(ctx, id)
	if err != nil {
		return Objective{}, err
	}
	now := time.Now().UTC()
	lastRun := input.LastRunAt.UTC()
	if lastRun.IsZero() {
		lastRun = now
	}
	nextRun := input.NextRunAt.UTC()
	lastError := strings.TrimSpace(input.LastError)

	record.LastRunAt = lastRun
	record.NextRunAt = nextRun
	record.LastError = lastError
	if input.Active != nil {
		record.Active = *input.Active
	}
	if input.AutoPausedReason != nil {
		record.AutoPausedReason = strings.TrimSpace(*input.AutoPausedReason)
	}
	if !input.SkipStats {
		record.RunCount++
		durationMs := input.RunDuration.Milliseconds()
		if durationMs > 0 {
			record.TotalRunDurationMs += durationMs
		}
		if lastError == "" {
			record.SuccessCount++
			record.ConsecutiveFailures = 0
			record.ConsecutiveSuccesses++
			record.LastSuccessAt = lastRun
			if input.AutoPausedReason == nil {
				record.AutoPausedReason = ""
			}
		} else {
			record.FailureCount++
			record.ConsecutiveFailures++
			record.ConsecutiveSuccesses = 0
			record.LastFailureAt = lastRun
			record.RecentErrors = appendObjectiveRecentError(record.RecentErrors, lastRun, lastError)
		}
	}
	record.UpdatedAt = now

	recentErrorsJSON, err := encodeObjectiveRecentErrors(record.RecentErrors)
	if err != nil {
		return Objective{}, err
	}

	_, err = s.db.ExecContext(
		ctx,
		`UPDATE objectives
		 SET active = ?, next_run_unix = ?, last_run_unix = ?, last_error = ?,
		     run_count = ?, success_count = ?, failure_count = ?, consecutive_failures = ?, consecutive_successes = ?,
		     total_run_duration_ms = ?, last_success_unix = ?, last_failure_unix = ?,
		     auto_paused_reason = ?, recent_errors_json = ?, updated_at_unix = ?
		 WHERE id = ?`,
		boolToInt(record.Active),
		nullTimeUnix(record.NextRunAt),
		nullTimeUnix(record.LastRunAt),
		nullIfEmpty(record.LastError),
		record.RunCount,
		record.SuccessCount,
		record.FailureCount,
		record.ConsecutiveFailures,
		record.ConsecutiveSuccesses,
		record.TotalRunDurationMs,
		nullTimeUnix(record.LastSuccessAt),
		nullTimeUnix(record.LastFailureAt),
		nullIfEmpty(record.AutoPausedReason),
		nullIfEmpty(recentErrorsJSON),
		record.UpdatedAt.Unix(),
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
		`SELECT `+objectiveSelectColumns+`
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
	if input.Timezone != nil {
		timezone, tzErr := normalizeObjectiveTimezone(*input.Timezone)
		if tzErr != nil {
			return Objective{}, ErrObjectiveInvalid
		}
		record.Timezone = timezone
	}
	if input.NextRunAt != nil {
		record.NextRunAt = input.NextRunAt.UTC()
	}
	if input.Active != nil {
		record.Active = *input.Active
		if record.Active {
			record.AutoPausedReason = ""
		}
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
		if _, err := ComputeScheduleNextRunForTimezone(record.CronExpr, record.Timezone, now); err != nil {
			return Objective{}, ErrObjectiveInvalid
		}
		if record.Active && record.NextRunAt.IsZero() {
			nextRun, err := ComputeScheduleNextRunForTimezone(record.CronExpr, record.Timezone, now)
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
		 SET title = ?, prompt = ?, trigger_type = ?, event_key = ?, cron_expr = ?, timezone = ?, active = ?, next_run_unix = ?, auto_paused_reason = ?, updated_at_unix = ?
		 WHERE id = ?`,
		record.Title,
		record.Prompt,
		string(record.TriggerType),
		nullIfEmpty(record.EventKey),
		nullIfEmpty(record.CronExpr),
		record.Timezone,
		boolToInt(record.Active),
		nullTimeUnix(record.NextRunAt),
		nullIfEmpty(record.AutoPausedReason),
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
	var timezone sql.NullString
	var active int
	var nextRunUnix sql.NullInt64
	var lastRunUnix sql.NullInt64
	var lastError sql.NullString
	var runCount int
	var successCount int
	var failureCount int
	var consecutiveFailures int
	var consecutiveSuccesses int
	var totalRunDurationMs int64
	var lastSuccessUnix sql.NullInt64
	var lastFailureUnix sql.NullInt64
	var autoPausedReason sql.NullString
	var recentErrorsJSON sql.NullString
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
		&timezone,
		&active,
		&nextRunUnix,
		&lastRunUnix,
		&lastError,
		&runCount,
		&successCount,
		&failureCount,
		&consecutiveFailures,
		&consecutiveSuccesses,
		&totalRunDurationMs,
		&lastSuccessUnix,
		&lastFailureUnix,
		&autoPausedReason,
		&recentErrorsJSON,
		&createdAtUnix,
		&updatedAtUnix,
	); err != nil {
		return Objective{}, err
	}
	record.TriggerType = ObjectiveTriggerType(strings.TrimSpace(triggerType))
	record.EventKey = eventKey.String
	record.CronExpr = normalizeCronExpr(cronExpr.String)
	normalizedTimezone, err := normalizeObjectiveTimezone(timezone.String)
	if err != nil {
		normalizedTimezone = objectiveDefaultTimezone
	}
	record.Timezone = normalizedTimezone
	record.Active = active == 1
	if nextRunUnix.Valid && nextRunUnix.Int64 > 0 {
		record.NextRunAt = time.Unix(nextRunUnix.Int64, 0).UTC()
	}
	if lastRunUnix.Valid && lastRunUnix.Int64 > 0 {
		record.LastRunAt = time.Unix(lastRunUnix.Int64, 0).UTC()
	}
	record.LastError = lastError.String
	record.RunCount = runCount
	record.SuccessCount = successCount
	record.FailureCount = failureCount
	record.ConsecutiveFailures = consecutiveFailures
	record.ConsecutiveSuccesses = consecutiveSuccesses
	record.TotalRunDurationMs = totalRunDurationMs
	if lastSuccessUnix.Valid && lastSuccessUnix.Int64 > 0 {
		record.LastSuccessAt = time.Unix(lastSuccessUnix.Int64, 0).UTC()
	}
	if lastFailureUnix.Valid && lastFailureUnix.Int64 > 0 {
		record.LastFailureAt = time.Unix(lastFailureUnix.Int64, 0).UTC()
	}
	record.AutoPausedReason = strings.TrimSpace(autoPausedReason.String)
	record.RecentErrors = decodeObjectiveRecentErrors(recentErrorsJSON.String)
	record.CreatedAt = time.Unix(createdAtUnix, 0).UTC()
	record.UpdatedAt = time.Unix(updatedAtUnix, 0).UTC()
	return record, nil
}

func appendObjectiveRecentError(existing []ObjectiveRunError, occurredAt time.Time, message string) []ObjectiveRunError {
	message = strings.TrimSpace(message)
	if message == "" {
		return existing
	}
	if occurredAt.IsZero() {
		occurredAt = time.Now().UTC()
	}
	next := append([]ObjectiveRunError{}, existing...)
	next = append(next, ObjectiveRunError{
		OccurredAt: occurredAt.UTC(),
		Message:    message,
	})
	if len(next) <= maxRecentObjectiveErrors {
		return next
	}
	return next[len(next)-maxRecentObjectiveErrors:]
}

func decodeObjectiveRecentErrors(raw string) []ObjectiveRunError {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return nil
	}
	var errorsList []ObjectiveRunError
	if err := json.Unmarshal([]byte(trimmed), &errorsList); err != nil {
		return nil
	}
	clean := make([]ObjectiveRunError, 0, len(errorsList))
	for _, item := range errorsList {
		message := strings.TrimSpace(item.Message)
		if message == "" {
			continue
		}
		occurredAt := item.OccurredAt.UTC()
		if occurredAt.IsZero() {
			occurredAt = time.Now().UTC()
		}
		clean = append(clean, ObjectiveRunError{
			OccurredAt: occurredAt,
			Message:    message,
		})
	}
	if len(clean) <= maxRecentObjectiveErrors {
		return clean
	}
	return clean[len(clean)-maxRecentObjectiveErrors:]
}

func encodeObjectiveRecentErrors(values []ObjectiveRunError) (string, error) {
	if len(values) == 0 {
		return "", nil
	}
	encoded, err := json.Marshal(values)
	if err != nil {
		return "", fmt.Errorf("marshal recent objective errors: %w", err)
	}
	return string(encoded), nil
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
