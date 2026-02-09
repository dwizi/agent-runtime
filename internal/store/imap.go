package store

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
)

type MarkIMAPIngestionInput struct {
	AccountKey  string
	UID         uint32
	MessageID   string
	WorkspaceID string
	ContextID   string
	FilePath    string
}

func (s *Store) IsIMAPMessageIngested(ctx context.Context, accountKey string, uid uint32, messageID string) (bool, error) {
	accountKey = normalizeAccountKey(accountKey)
	if accountKey == "" || uid == 0 {
		return false, fmt.Errorf("account key and uid are required")
	}
	var count int
	query := `SELECT COUNT(*) FROM imap_ingestions WHERE account_key = ? AND (uid = ?`
	args := []any{accountKey, uid}
	messageID = strings.TrimSpace(messageID)
	if messageID != "" {
		query += ` OR message_id = ?`
		args = append(args, messageID)
	}
	query += `)`
	if err := s.db.QueryRowContext(ctx, query, args...).Scan(&count); err != nil {
		return false, fmt.Errorf("lookup imap ingestion: %w", err)
	}
	return count > 0, nil
}

func (s *Store) MarkIMAPMessageIngested(ctx context.Context, input MarkIMAPIngestionInput) error {
	accountKey := normalizeAccountKey(input.AccountKey)
	messageID := strings.TrimSpace(input.MessageID)
	workspaceID := strings.TrimSpace(input.WorkspaceID)
	contextID := strings.TrimSpace(input.ContextID)
	filePath := strings.TrimSpace(input.FilePath)
	if accountKey == "" || input.UID == 0 || workspaceID == "" || contextID == "" || filePath == "" {
		return fmt.Errorf("missing imap ingestion fields")
	}
	now := time.Now().UTC()
	_, err := s.db.ExecContext(
		ctx,
		`INSERT INTO imap_ingestions (id, account_key, uid, message_id, workspace_id, context_id, file_path, created_at_unix)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		"ing_"+uuid.NewString(),
		accountKey,
		int64(input.UID),
		nullIfEmpty(messageID),
		workspaceID,
		contextID,
		filePath,
		now.Unix(),
	)
	if err != nil {
		if isSQLiteConstraint(err) {
			return nil
		}
		return fmt.Errorf("insert imap ingestion: %w", err)
	}
	return nil
}

func normalizeAccountKey(input string) string {
	value := strings.ToLower(strings.TrimSpace(input))
	return value
}

func isSQLiteConstraint(err error) bool {
	if err == nil {
		return false
	}
	if err == sql.ErrNoRows {
		return false
	}
	text := strings.ToLower(err.Error())
	return strings.Contains(text, "unique") || strings.Contains(text, "constraint")
}
