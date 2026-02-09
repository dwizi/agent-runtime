package store

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

type Store struct {
	db *sql.DB
}

type CreateTaskInput struct {
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
}

func New(path string) (*Store, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}
	db.SetMaxOpenConns(1)

	if _, err := db.Exec(`PRAGMA journal_mode=WAL; PRAGMA foreign_keys=ON;`); err != nil {
		db.Close()
		return nil, fmt.Errorf("apply sqlite pragmas: %w", err)
	}
	return &Store{db: db}, nil
}

func (s *Store) AutoMigrate(ctx context.Context) error {
	queries := []string{
		`CREATE TABLE IF NOT EXISTS users (
			id TEXT PRIMARY KEY,
			email TEXT,
			display_name TEXT NOT NULL,
			role TEXT NOT NULL,
			created_at TEXT NOT NULL DEFAULT (datetime('now'))
		);`,
		`CREATE TABLE IF NOT EXISTS identities (
			id TEXT PRIMARY KEY,
			user_id TEXT NOT NULL,
			connector TEXT NOT NULL,
			connector_user_id TEXT NOT NULL,
			verified INTEGER NOT NULL DEFAULT 0,
			created_at TEXT NOT NULL DEFAULT (datetime('now')),
			UNIQUE(connector, connector_user_id),
			FOREIGN KEY(user_id) REFERENCES users(id) ON DELETE CASCADE
		);`,
		`CREATE TABLE IF NOT EXISTS workspaces (
			id TEXT PRIMARY KEY,
			slug TEXT NOT NULL UNIQUE,
			name TEXT NOT NULL,
			kind TEXT NOT NULL,
			owner_user_id TEXT,
			created_at TEXT NOT NULL DEFAULT (datetime('now'))
		);`,
		`CREATE TABLE IF NOT EXISTS contexts (
			id TEXT PRIMARY KEY,
			workspace_id TEXT NOT NULL,
			connector TEXT NOT NULL,
			external_id TEXT NOT NULL,
			system_prompt TEXT NOT NULL DEFAULT '',
			is_admin INTEGER NOT NULL DEFAULT 0,
			created_at TEXT NOT NULL DEFAULT (datetime('now')),
			UNIQUE(workspace_id, connector, external_id),
			FOREIGN KEY(workspace_id) REFERENCES workspaces(id) ON DELETE CASCADE
		);`,
		`CREATE TABLE IF NOT EXISTS tasks (
			id TEXT PRIMARY KEY,
			workspace_id TEXT NOT NULL,
			context_id TEXT NOT NULL,
			kind TEXT NOT NULL,
			title TEXT NOT NULL,
			prompt TEXT NOT NULL,
			status TEXT NOT NULL,
			route_class TEXT,
			priority TEXT,
			due_at_unix INTEGER,
			assigned_lane TEXT,
			source_connector TEXT,
			source_external_id TEXT,
			source_user_id TEXT,
			source_text TEXT,
			attempts INTEGER NOT NULL DEFAULT 0,
			worker_id INTEGER,
			started_at_unix INTEGER,
			finished_at_unix INTEGER,
			result_summary TEXT,
			result_path TEXT,
			error_message TEXT,
			created_at TEXT NOT NULL DEFAULT (datetime('now')),
			updated_at_unix INTEGER
		);`,
		`CREATE TABLE IF NOT EXISTS pairing_requests (
			id TEXT PRIMARY KEY,
			token_hash TEXT NOT NULL UNIQUE,
			token_hint TEXT NOT NULL,
			connector TEXT NOT NULL,
			connector_user_id TEXT NOT NULL,
			display_name TEXT NOT NULL,
			status TEXT NOT NULL,
			expires_at_unix INTEGER NOT NULL,
			approved_user_id TEXT,
			approver_user_id TEXT,
			denied_reason TEXT,
			created_at_unix INTEGER NOT NULL,
			updated_at_unix INTEGER NOT NULL
		);`,
		`CREATE TABLE IF NOT EXISTS action_approvals (
			id TEXT PRIMARY KEY,
			workspace_id TEXT NOT NULL,
			context_id TEXT NOT NULL,
			connector TEXT NOT NULL,
			external_id TEXT NOT NULL,
			requester_user_id TEXT NOT NULL,
			action_type TEXT NOT NULL,
			action_target TEXT,
			action_summary TEXT,
			payload_json TEXT NOT NULL,
			status TEXT NOT NULL,
			approver_user_id TEXT,
			denied_reason TEXT,
			execution_status TEXT NOT NULL DEFAULT 'not_executed',
			execution_message TEXT,
			executor_plugin TEXT,
			executed_at_unix INTEGER,
			created_at_unix INTEGER NOT NULL,
			updated_at_unix INTEGER NOT NULL
		);`,
		`CREATE TABLE IF NOT EXISTS objectives (
			id TEXT PRIMARY KEY,
			workspace_id TEXT NOT NULL,
			context_id TEXT NOT NULL,
			title TEXT NOT NULL,
			prompt TEXT NOT NULL,
			trigger_type TEXT NOT NULL,
			event_key TEXT,
			interval_seconds INTEGER,
			active INTEGER NOT NULL DEFAULT 1,
			next_run_unix INTEGER,
			last_run_unix INTEGER,
			last_error TEXT,
			created_at_unix INTEGER NOT NULL,
			updated_at_unix INTEGER NOT NULL
		);`,
		`CREATE TABLE IF NOT EXISTS imap_ingestions (
			id TEXT PRIMARY KEY,
			account_key TEXT NOT NULL,
			uid INTEGER NOT NULL,
			message_id TEXT,
			workspace_id TEXT NOT NULL,
			context_id TEXT NOT NULL,
			file_path TEXT NOT NULL,
			created_at_unix INTEGER NOT NULL,
			UNIQUE(account_key, uid),
			UNIQUE(account_key, message_id)
		);`,
	}

	for _, query := range queries {
		if _, err := s.db.ExecContext(ctx, query); err != nil {
			return fmt.Errorf("run migration: %w", err)
		}
	}
	alterQueries := []string{
		`ALTER TABLE action_approvals ADD COLUMN execution_status TEXT NOT NULL DEFAULT 'not_executed';`,
		`ALTER TABLE action_approvals ADD COLUMN execution_message TEXT;`,
		`ALTER TABLE action_approvals ADD COLUMN executor_plugin TEXT;`,
		`ALTER TABLE action_approvals ADD COLUMN executed_at_unix INTEGER;`,
		`ALTER TABLE tasks ADD COLUMN attempts INTEGER NOT NULL DEFAULT 0;`,
		`ALTER TABLE tasks ADD COLUMN worker_id INTEGER;`,
		`ALTER TABLE tasks ADD COLUMN started_at_unix INTEGER;`,
		`ALTER TABLE tasks ADD COLUMN finished_at_unix INTEGER;`,
		`ALTER TABLE tasks ADD COLUMN result_summary TEXT;`,
		`ALTER TABLE tasks ADD COLUMN result_path TEXT;`,
		`ALTER TABLE tasks ADD COLUMN error_message TEXT;`,
		`ALTER TABLE tasks ADD COLUMN updated_at_unix INTEGER;`,
		`ALTER TABLE tasks ADD COLUMN route_class TEXT;`,
		`ALTER TABLE tasks ADD COLUMN priority TEXT;`,
		`ALTER TABLE tasks ADD COLUMN due_at_unix INTEGER;`,
		`ALTER TABLE tasks ADD COLUMN assigned_lane TEXT;`,
		`ALTER TABLE tasks ADD COLUMN source_connector TEXT;`,
		`ALTER TABLE tasks ADD COLUMN source_external_id TEXT;`,
		`ALTER TABLE tasks ADD COLUMN source_user_id TEXT;`,
		`ALTER TABLE tasks ADD COLUMN source_text TEXT;`,
	}
	for _, query := range alterQueries {
		if _, err := s.db.ExecContext(ctx, query); err != nil {
			message := strings.ToLower(err.Error())
			if strings.Contains(message, "duplicate column name") || strings.Contains(message, "no such table") {
				continue
			}
			return fmt.Errorf("run migration alter: %w", err)
		}
	}
	return nil
}

func (s *Store) CreateTask(ctx context.Context, input CreateTaskInput) error {
	nowUnix := time.Now().UTC().Unix()
	dueAtUnix := int64(0)
	if !input.DueAt.IsZero() {
		dueAtUnix = input.DueAt.UTC().Unix()
	}
	_, err := s.db.ExecContext(
		ctx,
		`INSERT INTO tasks (
			id, workspace_id, context_id, kind, title, prompt, status,
			route_class, priority, due_at_unix, assigned_lane,
			source_connector, source_external_id, source_user_id, source_text,
			updated_at_unix
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		input.ID,
		input.WorkspaceID,
		input.ContextID,
		input.Kind,
		input.Title,
		input.Prompt,
		input.Status,
		nullIfEmpty(strings.TrimSpace(input.RouteClass)),
		nullIfEmpty(strings.TrimSpace(input.Priority)),
		nullIfZeroInt64(dueAtUnix),
		nullIfEmpty(strings.TrimSpace(input.AssignedLane)),
		nullIfEmpty(strings.TrimSpace(input.SourceConnector)),
		nullIfEmpty(strings.TrimSpace(input.SourceExternalID)),
		nullIfEmpty(strings.TrimSpace(input.SourceUserID)),
		nullIfEmpty(strings.TrimSpace(input.SourceText)),
		nowUnix,
	)
	if err != nil {
		return fmt.Errorf("insert task: %w", err)
	}
	return nil
}

func (s *Store) Ping(ctx context.Context) error {
	return s.db.PingContext(ctx)
}

func (s *Store) Close() error {
	return s.db.Close()
}

func nullIfZeroInt64(value int64) any {
	if value == 0 {
		return nil
	}
	return value
}
