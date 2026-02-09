package store

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base32"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
)

var (
	ErrPairingNotFound        = errors.New("pairing request not found")
	ErrPairingNotPending      = errors.New("pairing request is not pending")
	ErrPairingExpired         = errors.New("pairing request expired")
	ErrIdentityAlreadyLinked  = errors.New("identity is already linked")
	ErrPairingInvalidRole     = errors.New("invalid role")
	ErrPairingUserNotFound    = errors.New("target user not found")
	ErrPairingInvalidToken    = errors.New("invalid token")
	ErrPairingInvalidInput    = errors.New("invalid pairing input")
	ErrPairingInvalidReason   = errors.New("denial reason required")
	ErrPairingInvalidApprover = errors.New("approver user id required")
)

type PairingRequest struct {
	ID              string
	TokenHint       string
	Connector       string
	ConnectorUserID string
	DisplayName     string
	Status          string
	ExpiresAt       time.Time
	ApprovedUserID  string
	ApproverUserID  string
	DeniedReason    string
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

type PairingRequestWithToken struct {
	PairingRequest
	Token string
}

type CreatePairingRequestInput struct {
	Connector       string
	ConnectorUserID string
	DisplayName     string
	ExpiresAt       time.Time
}

type ApprovePairingInput struct {
	Token          string
	ApproverUserID string
	Role           string
	TargetUserID   string
}

type ApprovePairingResult struct {
	PairingRequest PairingRequest
	UserID         string
	IdentityID     string
}

type DenyPairingInput struct {
	Token          string
	ApproverUserID string
	Reason         string
}

func (s *Store) CreatePairingRequest(ctx context.Context, input CreatePairingRequestInput) (PairingRequestWithToken, error) {
	if strings.TrimSpace(input.Connector) == "" || strings.TrimSpace(input.ConnectorUserID) == "" || strings.TrimSpace(input.DisplayName) == "" {
		return PairingRequestWithToken{}, ErrPairingInvalidInput
	}
	connector, err := normalizeConnector(input.Connector)
	if err != nil {
		return PairingRequestWithToken{}, err
	}

	now := time.Now().UTC()
	expiresAt := input.ExpiresAt
	if expiresAt.IsZero() {
		expiresAt = now.Add(10 * time.Minute)
	}
	if !expiresAt.After(now) {
		return PairingRequestWithToken{}, ErrPairingInvalidInput
	}

	token, tokenHash, err := generatePairingToken()
	if err != nil {
		return PairingRequestWithToken{}, err
	}

	request := PairingRequestWithToken{
		PairingRequest: PairingRequest{
			ID:              uuid.NewString(),
			TokenHint:       tokenHint(token),
			Connector:       connector,
			ConnectorUserID: strings.TrimSpace(input.ConnectorUserID),
			DisplayName:     strings.TrimSpace(input.DisplayName),
			Status:          "pending",
			ExpiresAt:       expiresAt.UTC(),
			CreatedAt:       now,
			UpdatedAt:       now,
		},
		Token: token,
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return PairingRequestWithToken{}, fmt.Errorf("begin transaction: %w", err)
	}
	defer tx.Rollback()

	if _, err := tx.ExecContext(
		ctx,
		`UPDATE pairing_requests SET status = 'expired', updated_at_unix = ? WHERE connector = ? AND connector_user_id = ? AND status = 'pending'`,
		now.Unix(),
		request.Connector,
		request.ConnectorUserID,
	); err != nil {
		return PairingRequestWithToken{}, fmt.Errorf("expire old pairing requests: %w", err)
	}

	if _, err := tx.ExecContext(
		ctx,
		`INSERT INTO pairing_requests (
			id, token_hash, token_hint, connector, connector_user_id, display_name, status, expires_at_unix, created_at_unix, updated_at_unix
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		request.ID,
		tokenHash,
		request.TokenHint,
		request.Connector,
		request.ConnectorUserID,
		request.DisplayName,
		request.Status,
		request.ExpiresAt.Unix(),
		request.CreatedAt.Unix(),
		request.UpdatedAt.Unix(),
	); err != nil {
		return PairingRequestWithToken{}, fmt.Errorf("insert pairing request: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return PairingRequestWithToken{}, fmt.Errorf("commit pairing request: %w", err)
	}
	return request, nil
}

func (s *Store) LookupPairingByToken(ctx context.Context, token string) (PairingRequest, error) {
	tokenHash, err := hashPairingToken(token)
	if err != nil {
		return PairingRequest{}, err
	}
	request, err := s.lookupPairingByTokenHash(ctx, tokenHash)
	if err != nil {
		return PairingRequest{}, err
	}
	return request, nil
}

func (s *Store) ApprovePairing(ctx context.Context, input ApprovePairingInput) (ApprovePairingResult, error) {
	if strings.TrimSpace(input.ApproverUserID) == "" {
		return ApprovePairingResult{}, ErrPairingInvalidApprover
	}
	tokenHash, err := hashPairingToken(input.Token)
	if err != nil {
		return ApprovePairingResult{}, err
	}
	role, err := normalizeRole(input.Role)
	if err != nil {
		return ApprovePairingResult{}, err
	}
	now := time.Now().UTC()

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return ApprovePairingResult{}, fmt.Errorf("begin transaction: %w", err)
	}
	defer tx.Rollback()

	request, err := lookupPairingByTokenHashTx(ctx, tx, tokenHash)
	if err != nil {
		return ApprovePairingResult{}, err
	}
	if request.Status != "pending" {
		return ApprovePairingResult{}, ErrPairingNotPending
	}
	if request.ExpiresAt.Before(now) {
		if _, updateErr := tx.ExecContext(
			ctx,
			`UPDATE pairing_requests SET status = 'expired', updated_at_unix = ? WHERE id = ?`,
			now.Unix(),
			request.ID,
		); updateErr != nil {
			return ApprovePairingResult{}, fmt.Errorf("expire pairing request: %w", updateErr)
		}
		return ApprovePairingResult{}, ErrPairingExpired
	}

	var identityCount int
	if err := tx.QueryRowContext(
		ctx,
		`SELECT COUNT(*) FROM identities WHERE connector = ? AND connector_user_id = ?`,
		request.Connector,
		request.ConnectorUserID,
	).Scan(&identityCount); err != nil {
		return ApprovePairingResult{}, fmt.Errorf("check identity link: %w", err)
	}
	if identityCount > 0 {
		return ApprovePairingResult{}, ErrIdentityAlreadyLinked
	}

	userID := strings.TrimSpace(input.TargetUserID)
	if userID != "" {
		var userCount int
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM users WHERE id = ?`, userID).Scan(&userCount); err != nil {
			return ApprovePairingResult{}, fmt.Errorf("check user: %w", err)
		}
		if userCount == 0 {
			return ApprovePairingResult{}, ErrPairingUserNotFound
		}
	} else {
		userID = uuid.NewString()
		if _, err := tx.ExecContext(
			ctx,
			`INSERT INTO users (id, display_name, role) VALUES (?, ?, ?)`,
			userID,
			request.DisplayName,
			role,
		); err != nil {
			return ApprovePairingResult{}, fmt.Errorf("create user: %w", err)
		}
	}

	identityID := uuid.NewString()
	if _, err := tx.ExecContext(
		ctx,
		`INSERT INTO identities (id, user_id, connector, connector_user_id, verified) VALUES (?, ?, ?, ?, 1)`,
		identityID,
		userID,
		request.Connector,
		request.ConnectorUserID,
	); err != nil {
		return ApprovePairingResult{}, fmt.Errorf("create identity link: %w", err)
	}

	if _, err := tx.ExecContext(
		ctx,
		`UPDATE pairing_requests SET status = 'approved', approved_user_id = ?, approver_user_id = ?, updated_at_unix = ? WHERE id = ?`,
		userID,
		strings.TrimSpace(input.ApproverUserID),
		now.Unix(),
		request.ID,
	); err != nil {
		return ApprovePairingResult{}, fmt.Errorf("update pairing status: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return ApprovePairingResult{}, fmt.Errorf("commit approval: %w", err)
	}

	request.Status = "approved"
	request.ApprovedUserID = userID
	request.ApproverUserID = strings.TrimSpace(input.ApproverUserID)
	request.UpdatedAt = now

	return ApprovePairingResult{
		PairingRequest: request,
		UserID:         userID,
		IdentityID:     identityID,
	}, nil
}

func (s *Store) DenyPairing(ctx context.Context, input DenyPairingInput) (PairingRequest, error) {
	if strings.TrimSpace(input.ApproverUserID) == "" {
		return PairingRequest{}, ErrPairingInvalidApprover
	}
	if strings.TrimSpace(input.Reason) == "" {
		return PairingRequest{}, ErrPairingInvalidReason
	}
	tokenHash, err := hashPairingToken(input.Token)
	if err != nil {
		return PairingRequest{}, err
	}
	now := time.Now().UTC()

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return PairingRequest{}, fmt.Errorf("begin transaction: %w", err)
	}
	defer tx.Rollback()

	request, err := lookupPairingByTokenHashTx(ctx, tx, tokenHash)
	if err != nil {
		return PairingRequest{}, err
	}
	if request.Status != "pending" {
		return PairingRequest{}, ErrPairingNotPending
	}

	reason := strings.TrimSpace(input.Reason)
	if _, err := tx.ExecContext(
		ctx,
		`UPDATE pairing_requests SET status = 'denied', denied_reason = ?, approver_user_id = ?, updated_at_unix = ? WHERE id = ?`,
		reason,
		strings.TrimSpace(input.ApproverUserID),
		now.Unix(),
		request.ID,
	); err != nil {
		return PairingRequest{}, fmt.Errorf("deny pairing request: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return PairingRequest{}, fmt.Errorf("commit denial: %w", err)
	}
	request.Status = "denied"
	request.DeniedReason = reason
	request.ApproverUserID = strings.TrimSpace(input.ApproverUserID)
	request.UpdatedAt = now
	return request, nil
}

func (s *Store) lookupPairingByTokenHash(ctx context.Context, tokenHash string) (PairingRequest, error) {
	return lookupPairingByTokenHashDB(ctx, s.db, tokenHash)
}

func lookupPairingByTokenHashTx(ctx context.Context, tx *sql.Tx, tokenHash string) (PairingRequest, error) {
	return lookupPairingByTokenHashDB(ctx, tx, tokenHash)
}

type queryRower interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

func lookupPairingByTokenHashDB(ctx context.Context, rower queryRower, tokenHash string) (PairingRequest, error) {
	request := PairingRequest{}
	var expiresAtUnix int64
	var createdAtUnix int64
	var updatedAtUnix int64
	var approvedUserID sql.NullString
	var approverUserID sql.NullString
	var deniedReason sql.NullString
	err := rower.QueryRowContext(
		ctx,
		`SELECT id, token_hint, connector, connector_user_id, display_name, status, expires_at_unix, approved_user_id, approver_user_id, denied_reason, created_at_unix, updated_at_unix
		FROM pairing_requests WHERE token_hash = ?`,
		tokenHash,
	).Scan(
		&request.ID,
		&request.TokenHint,
		&request.Connector,
		&request.ConnectorUserID,
		&request.DisplayName,
		&request.Status,
		&expiresAtUnix,
		&approvedUserID,
		&approverUserID,
		&deniedReason,
		&createdAtUnix,
		&updatedAtUnix,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return PairingRequest{}, ErrPairingNotFound
	}
	if err != nil {
		return PairingRequest{}, fmt.Errorf("lookup pairing request: %w", err)
	}
	request.ExpiresAt = time.Unix(expiresAtUnix, 0).UTC()
	request.CreatedAt = time.Unix(createdAtUnix, 0).UTC()
	request.UpdatedAt = time.Unix(updatedAtUnix, 0).UTC()
	request.ApprovedUserID = approvedUserID.String
	request.ApproverUserID = approverUserID.String
	request.DeniedReason = deniedReason.String
	return request, nil
}

func generatePairingToken() (string, string, error) {
	raw := make([]byte, 10)
	if _, err := rand.Read(raw); err != nil {
		return "", "", fmt.Errorf("generate random pairing token: %w", err)
	}
	token := base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(raw)
	hash, err := hashPairingToken(token)
	if err != nil {
		return "", "", err
	}
	return token, hash, nil
}

func hashPairingToken(token string) (string, error) {
	trimmed := strings.ToUpper(strings.TrimSpace(token))
	if trimmed == "" {
		return "", ErrPairingInvalidToken
	}
	sum := sha256.Sum256([]byte(trimmed))
	return hex.EncodeToString(sum[:]), nil
}

func tokenHint(token string) string {
	trimmed := strings.ToUpper(strings.TrimSpace(token))
	if len(trimmed) <= 8 {
		return trimmed
	}
	return trimmed[:4] + "..." + trimmed[len(trimmed)-4:]
}

func normalizeRole(input string) (string, error) {
	role := strings.ToLower(strings.TrimSpace(input))
	if role == "" {
		return "admin", nil
	}
	switch role {
	case "overlord", "admin", "operator", "member", "viewer":
		return role, nil
	default:
		return "", ErrPairingInvalidRole
	}
}

func normalizeConnector(input string) (string, error) {
	connector := strings.ToLower(strings.TrimSpace(input))
	switch connector {
	case "telegram", "discord":
		return connector, nil
	default:
		return "", fmt.Errorf("%w: unsupported connector", ErrPairingInvalidInput)
	}
}
