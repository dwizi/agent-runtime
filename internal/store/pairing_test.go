package store

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"
)

func TestPairingLifecycleApprove(t *testing.T) {
	sqlStore := newTestStore(t)

	ctx := context.Background()
	request, err := sqlStore.CreatePairingRequest(ctx, CreatePairingRequestInput{
		Connector:       "telegram",
		ConnectorUserID: "tg_123",
		DisplayName:     "Alice",
	})
	if err != nil {
		t.Fatalf("create pairing request: %v", err)
	}
	if request.Token == "" {
		t.Fatal("expected token in pairing response")
	}

	lookup, err := sqlStore.LookupPairingByToken(ctx, request.Token)
	if err != nil {
		t.Fatalf("lookup pairing request: %v", err)
	}
	if lookup.Status != "pending" {
		t.Fatalf("expected pending status, got %s", lookup.Status)
	}

	result, err := sqlStore.ApprovePairing(ctx, ApprovePairingInput{
		Token:          request.Token,
		ApproverUserID: "tui-admin",
		Role:           "admin",
	})
	if err != nil {
		t.Fatalf("approve pairing request: %v", err)
	}
	if result.UserID == "" || result.IdentityID == "" {
		t.Fatal("expected user and identity IDs after approval")
	}

	approved, err := sqlStore.LookupPairingByToken(ctx, request.Token)
	if err != nil {
		t.Fatalf("lookup approved pairing request: %v", err)
	}
	if approved.Status != "approved" {
		t.Fatalf("expected approved status, got %s", approved.Status)
	}
}

func TestPairingApproveExpiredToken(t *testing.T) {
	sqlStore := newTestStore(t)
	ctx := context.Background()
	request, err := sqlStore.CreatePairingRequest(ctx, CreatePairingRequestInput{
		Connector:       "discord",
		ConnectorUserID: "dc_123",
		DisplayName:     "Bob",
		ExpiresAt:       time.Now().UTC().Add(1 * time.Second),
	})
	if err != nil {
		t.Fatalf("create pairing request: %v", err)
	}

	time.Sleep(1200 * time.Millisecond)
	_, err = sqlStore.ApprovePairing(ctx, ApprovePairingInput{
		Token:          request.Token,
		ApproverUserID: "tui-admin",
	})
	if !errors.Is(err, ErrPairingExpired) {
		t.Fatalf("expected ErrPairingExpired, got %v", err)
	}
}

func TestPairingDeny(t *testing.T) {
	sqlStore := newTestStore(t)
	ctx := context.Background()

	request, err := sqlStore.CreatePairingRequest(ctx, CreatePairingRequestInput{
		Connector:       "telegram",
		ConnectorUserID: "tg_999",
		DisplayName:     "Carol",
	})
	if err != nil {
		t.Fatalf("create pairing request: %v", err)
	}
	denied, err := sqlStore.DenyPairing(ctx, DenyPairingInput{
		Token:          request.Token,
		ApproverUserID: "tui-admin",
		Reason:         "not authorized",
	})
	if err != nil {
		t.Fatalf("deny pairing request: %v", err)
	}
	if denied.Status != "denied" {
		t.Fatalf("expected denied status, got %s", denied.Status)
	}
}

func newTestStore(t *testing.T) *Store {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "agent_runtime_test.sqlite")
	sqlStore, err := New(dbPath)
	if err != nil {
		t.Fatalf("open test store: %v", err)
	}
	t.Cleanup(func() { _ = sqlStore.Close() })
	if err := sqlStore.AutoMigrate(context.Background()); err != nil {
		t.Fatalf("migrate test store: %v", err)
	}
	return sqlStore
}
