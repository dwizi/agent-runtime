package imap

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/carlos/spinner/internal/orchestrator"
	"github.com/carlos/spinner/internal/store"
)

type fakeStore struct {
	contextRecord store.ContextRecord
	lastTask      store.CreateTaskInput
	taskCount     int
}

func (f *fakeStore) EnsureContextForExternalChannel(ctx context.Context, connector, externalID, displayName string) (store.ContextRecord, error) {
	if f.contextRecord.ID == "" {
		f.contextRecord = store.ContextRecord{
			ID:          "ctx-imap",
			WorkspaceID: "ws-imap",
		}
	}
	return f.contextRecord, nil
}

func (f *fakeStore) CreateTask(ctx context.Context, input store.CreateTaskInput) error {
	f.lastTask = input
	f.taskCount++
	return nil
}

type fakeEngine struct {
	lastTask orchestrator.Task
}

func (f *fakeEngine) Enqueue(task orchestrator.Task) (orchestrator.Task, error) {
	task.ID = "task-imap-1"
	f.lastTask = task
	return task, nil
}

func TestPollOnceIngestsMessagesAndQueuesTask(t *testing.T) {
	workspace := t.TempDir()
	storeMock := &fakeStore{}
	engineMock := &fakeEngine{}
	connector := New(
		"imap.example.com",
		993,
		"inbox@example.com",
		"secret",
		"INBOX",
		60,
		workspace,
		false,
		storeMock,
		engineMock,
		slog.New(slog.NewTextHandler(io.Discard, nil)),
	)

	var marked []uint32
	connector.fetchUnread = func(ctx context.Context) ([]Message, error) {
		return []Message{
			{
				UID:       42,
				MessageID: "<abc@example.com>",
				From:      "Alice <alice@example.com>",
				Subject:   "Deployment Update",
				Date:      time.Date(2026, 2, 9, 10, 0, 0, 0, time.UTC),
				Body:      "All systems green.",
			},
		}, nil
	}
	connector.markSeen = func(ctx context.Context, uids []uint32) error {
		marked = append(marked, uids...)
		return nil
	}

	if err := connector.pollOnce(context.Background()); err != nil {
		t.Fatalf("pollOnce failed: %v", err)
	}
	if len(marked) != 1 || marked[0] != 42 {
		t.Fatalf("expected UID 42 marked seen, got %+v", marked)
	}
	target := filepath.Join(workspace, "ws-imap", "inbox", "imap", "INBOX", "42-Deployment-Update.md")
	content, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("expected markdown file written: %v", err)
	}
	if !strings.Contains(string(content), "All systems green.") {
		t.Fatalf("expected body in markdown file, got %s", string(content))
	}
	if storeMock.taskCount != 1 {
		t.Fatalf("expected one task persisted, got %d", storeMock.taskCount)
	}
	if engineMock.lastTask.ID != "task-imap-1" {
		t.Fatalf("expected enqueued task id, got %s", engineMock.lastTask.ID)
	}
}

func TestPollOnceNoMessages(t *testing.T) {
	connector := New(
		"imap.example.com",
		993,
		"inbox@example.com",
		"secret",
		"INBOX",
		60,
		t.TempDir(),
		false,
		&fakeStore{},
		&fakeEngine{},
		slog.New(slog.NewTextHandler(io.Discard, nil)),
	)
	connector.fetchUnread = func(ctx context.Context) ([]Message, error) {
		return nil, nil
	}
	markSeenCalled := false
	connector.markSeen = func(ctx context.Context, uids []uint32) error {
		markSeenCalled = true
		return nil
	}
	if err := connector.pollOnce(context.Background()); err != nil {
		t.Fatalf("pollOnce failed: %v", err)
	}
	if markSeenCalled {
		t.Fatal("expected markSeen not to be called")
	}
}
