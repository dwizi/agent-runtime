package store

import (
	"context"
	"testing"
)

func TestIMAPIngestionMarkAndLookup(t *testing.T) {
	sqlStore := newTestStore(t)
	ctx := context.Background()

	ingested, err := sqlStore.IsIMAPMessageIngested(ctx, "inbox@example.com:INBOX", 1001, "<msg-1@example.com>")
	if err != nil {
		t.Fatalf("lookup ingested before insert: %v", err)
	}
	if ingested {
		t.Fatal("expected not ingested before mark")
	}

	if err := sqlStore.MarkIMAPMessageIngested(ctx, MarkIMAPIngestionInput{
		AccountKey:  "inbox@example.com:INBOX",
		UID:         1001,
		MessageID:   "<msg-1@example.com>",
		WorkspaceID: "ws-1",
		ContextID:   "ctx-1",
		FilePath:    "inbox/imap/INBOX/1001-hello.md",
	}); err != nil {
		t.Fatalf("mark ingested: %v", err)
	}

	ingested, err = sqlStore.IsIMAPMessageIngested(ctx, "inbox@example.com:INBOX", 1001, "<msg-1@example.com>")
	if err != nil {
		t.Fatalf("lookup ingested after insert: %v", err)
	}
	if !ingested {
		t.Fatal("expected ingested after mark")
	}
}

func TestIMAPIngestionDuplicateIsIdempotent(t *testing.T) {
	sqlStore := newTestStore(t)
	ctx := context.Background()

	input := MarkIMAPIngestionInput{
		AccountKey:  "inbox@example.com:INBOX",
		UID:         2002,
		MessageID:   "<msg-2@example.com>",
		WorkspaceID: "ws-1",
		ContextID:   "ctx-1",
		FilePath:    "inbox/imap/INBOX/2002-hello.md",
	}
	if err := sqlStore.MarkIMAPMessageIngested(ctx, input); err != nil {
		t.Fatalf("first mark: %v", err)
	}
	if err := sqlStore.MarkIMAPMessageIngested(ctx, input); err != nil {
		t.Fatalf("second mark should be idempotent: %v", err)
	}
}
