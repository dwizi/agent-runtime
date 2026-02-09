package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/google/uuid"
)

var (
	ErrIdentityNotFound = errors.New("identity not found")
	ErrContextNotFound  = errors.New("context not found")
)

type UserIdentity struct {
	UserID      string
	DisplayName string
	Role        string
}

type ContextRecord struct {
	ID          string
	WorkspaceID string
	IsAdmin     bool
}

type ContextPolicy struct {
	ContextID    string
	WorkspaceID  string
	IsAdmin      bool
	SystemPrompt string
}

func (s *Store) LookupUserIdentity(ctx context.Context, connector, connectorUserID string) (UserIdentity, error) {
	row := s.db.QueryRowContext(
		ctx,
		`SELECT u.id, u.display_name, u.role
		 FROM identities i
		 INNER JOIN users u ON u.id = i.user_id
		 WHERE i.connector = ? AND i.connector_user_id = ? AND i.verified = 1`,
		strings.ToLower(strings.TrimSpace(connector)),
		strings.TrimSpace(connectorUserID),
	)

	var identity UserIdentity
	if err := row.Scan(&identity.UserID, &identity.DisplayName, &identity.Role); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return UserIdentity{}, ErrIdentityNotFound
		}
		return UserIdentity{}, fmt.Errorf("lookup user identity: %w", err)
	}
	return identity, nil
}

func (s *Store) EnsureContextForExternalChannel(ctx context.Context, connector, externalID, displayName string) (ContextRecord, error) {
	connector = strings.ToLower(strings.TrimSpace(connector))
	externalID = strings.TrimSpace(externalID)
	displayName = strings.TrimSpace(displayName)
	if connector == "" || externalID == "" {
		return ContextRecord{}, fmt.Errorf("connector and external id are required")
	}
	if displayName == "" {
		displayName = connector + " " + externalID
	}

	workspaceSlug := fmt.Sprintf("community-%s-%s", connector, slugPart(externalID))
	workspaceName := fmt.Sprintf("%s: %s", titleCase(connector), displayName)

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return ContextRecord{}, fmt.Errorf("begin transaction: %w", err)
	}
	defer tx.Rollback()

	workspaceID, err := ensureWorkspaceTx(ctx, tx, workspaceSlug, workspaceName)
	if err != nil {
		return ContextRecord{}, err
	}
	contextRecord, err := ensureContextTx(ctx, tx, workspaceID, connector, externalID)
	if err != nil {
		return ContextRecord{}, err
	}

	if err := tx.Commit(); err != nil {
		return ContextRecord{}, fmt.Errorf("commit context ensure: %w", err)
	}
	return contextRecord, nil
}

func (s *Store) SetContextAdminByExternal(ctx context.Context, connector, externalID string, enabled bool) (ContextRecord, error) {
	contextRecord, err := s.EnsureContextForExternalChannel(ctx, connector, externalID, externalID)
	if err != nil {
		return ContextRecord{}, err
	}
	flag := 0
	if enabled {
		flag = 1
	}
	if _, err := s.db.ExecContext(
		ctx,
		`UPDATE contexts SET is_admin = ? WHERE id = ?`,
		flag,
		contextRecord.ID,
	); err != nil {
		return ContextRecord{}, fmt.Errorf("update context admin flag: %w", err)
	}
	contextRecord.IsAdmin = enabled
	return contextRecord, nil
}

func (s *Store) LookupContextPolicy(ctx context.Context, contextID string) (ContextPolicy, error) {
	row := s.db.QueryRowContext(
		ctx,
		`SELECT id, workspace_id, is_admin, system_prompt
		 FROM contexts
		 WHERE id = ?`,
		strings.TrimSpace(contextID),
	)

	var record ContextPolicy
	var isAdminInt int
	if err := row.Scan(&record.ContextID, &record.WorkspaceID, &isAdminInt, &record.SystemPrompt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ContextPolicy{}, ErrContextNotFound
		}
		return ContextPolicy{}, fmt.Errorf("lookup context policy: %w", err)
	}
	record.IsAdmin = isAdminInt == 1
	return record, nil
}

func (s *Store) LookupContextPolicyByExternal(ctx context.Context, connector, externalID string) (ContextPolicy, error) {
	row := s.db.QueryRowContext(
		ctx,
		`SELECT id, workspace_id, is_admin, system_prompt
		 FROM contexts
		 WHERE connector = ? AND external_id = ?`,
		strings.ToLower(strings.TrimSpace(connector)),
		strings.TrimSpace(externalID),
	)

	var record ContextPolicy
	var isAdminInt int
	if err := row.Scan(&record.ContextID, &record.WorkspaceID, &isAdminInt, &record.SystemPrompt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ContextPolicy{}, ErrContextNotFound
		}
		return ContextPolicy{}, fmt.Errorf("lookup context policy by external: %w", err)
	}
	record.IsAdmin = isAdminInt == 1
	return record, nil
}

func (s *Store) SetContextSystemPromptByExternal(ctx context.Context, connector, externalID, prompt string) (ContextPolicy, error) {
	contextRecord, err := s.EnsureContextForExternalChannel(ctx, connector, externalID, externalID)
	if err != nil {
		return ContextPolicy{}, err
	}
	prompt = strings.TrimSpace(prompt)
	if _, err := s.db.ExecContext(
		ctx,
		`UPDATE contexts SET system_prompt = ? WHERE id = ?`,
		prompt,
		contextRecord.ID,
	); err != nil {
		return ContextPolicy{}, fmt.Errorf("update context system prompt: %w", err)
	}
	return s.LookupContextPolicy(ctx, contextRecord.ID)
}

func ensureWorkspaceTx(ctx context.Context, tx *sql.Tx, slug, name string) (string, error) {
	var workspaceID string
	err := tx.QueryRowContext(
		ctx,
		`SELECT id FROM workspaces WHERE slug = ?`,
		slug,
	).Scan(&workspaceID)
	if err == nil {
		return workspaceID, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return "", fmt.Errorf("lookup workspace: %w", err)
	}

	workspaceID = uuid.NewString()
	if _, err := tx.ExecContext(
		ctx,
		`INSERT INTO workspaces (id, slug, name, kind) VALUES (?, ?, ?, 'community')`,
		workspaceID,
		slug,
		name,
	); err != nil {
		return "", fmt.Errorf("create workspace: %w", err)
	}
	return workspaceID, nil
}

func ensureContextTx(ctx context.Context, tx *sql.Tx, workspaceID, connector, externalID string) (ContextRecord, error) {
	record := ContextRecord{}
	var isAdminInt int
	err := tx.QueryRowContext(
		ctx,
		`SELECT id, workspace_id, is_admin
		 FROM contexts
		 WHERE workspace_id = ? AND connector = ? AND external_id = ?`,
		workspaceID,
		connector,
		externalID,
	).Scan(&record.ID, &record.WorkspaceID, &isAdminInt)
	if err == nil {
		record.IsAdmin = isAdminInt == 1
		return record, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return ContextRecord{}, fmt.Errorf("lookup context: %w", err)
	}

	record = ContextRecord{
		ID:          uuid.NewString(),
		WorkspaceID: workspaceID,
		IsAdmin:     false,
	}
	if _, err := tx.ExecContext(
		ctx,
		`INSERT INTO contexts (id, workspace_id, connector, external_id, is_admin) VALUES (?, ?, ?, ?, 0)`,
		record.ID,
		record.WorkspaceID,
		connector,
		externalID,
	); err != nil {
		return ContextRecord{}, fmt.Errorf("create context: %w", err)
	}
	return record, nil
}

var slugSanitizer = regexp.MustCompile(`[^a-zA-Z0-9_-]+`)

func slugPart(value string) string {
	trimmed := strings.TrimSpace(value)
	trimmed = strings.ReplaceAll(trimmed, " ", "-")
	trimmed = slugSanitizer.ReplaceAllString(trimmed, "-")
	trimmed = strings.Trim(trimmed, "-")
	if trimmed == "" {
		return "default"
	}
	return strings.ToLower(trimmed)
}

func titleCase(value string) string {
	if value == "" {
		return value
	}
	lower := strings.ToLower(value)
	return strings.ToUpper(lower[:1]) + lower[1:]
}
