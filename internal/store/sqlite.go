package store

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	_ "modernc.org/sqlite"
)

type Store struct {
	db *sql.DB
}

type CreateTaskInput struct {
	ID          string
	WorkspaceID string
	ContextID   string
	Kind        string
	Title       string
	Prompt      string
	Status      string
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
			created_at TEXT NOT NULL DEFAULT (datetime('now'))
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
	_, err := s.db.ExecContext(
		ctx,
		`INSERT INTO tasks (id, workspace_id, context_id, kind, title, prompt, status) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		input.ID,
		input.WorkspaceID,
		input.ContextID,
		input.Kind,
		input.Title,
		input.Prompt,
		input.Status,
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
