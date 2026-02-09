package memorylog

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestAppendCreatesMarkdownLog(t *testing.T) {
	root := t.TempDir()
	err := Append(Entry{
		WorkspaceRoot: root,
		WorkspaceID:   "ws-1",
		Connector:     "telegram",
		ExternalID:    "42",
		Direction:     "inbound",
		ActorID:       "user-1",
		DisplayName:   "ops",
		Text:          "hello",
		Timestamp:     time.Unix(1700000000, 0).UTC(),
	})
	if err != nil {
		t.Fatalf("append failed: %v", err)
	}

	logPath := filepath.Join(root, "ws-1", "logs", "chats", "telegram", "42.md")
	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("read log failed: %v", err)
	}
	content := string(data)
	if !strings.Contains(content, "# Chat Log") {
		t.Fatalf("expected markdown header, got %s", content)
	}
	if !strings.Contains(content, "hello") {
		t.Fatalf("expected message body, got %s", content)
	}
}

func TestAppendSkipsEmptyText(t *testing.T) {
	root := t.TempDir()
	err := Append(Entry{
		WorkspaceRoot: root,
		WorkspaceID:   "ws-1",
		Connector:     "telegram",
		ExternalID:    "42",
		Text:          "   ",
	})
	if err != nil {
		t.Fatalf("append failed: %v", err)
	}
	logPath := filepath.Join(root, "ws-1", "logs", "chats", "telegram", "42.md")
	if _, err := os.Stat(logPath); !os.IsNotExist(err) {
		t.Fatalf("expected no file for empty text, got err=%v", err)
	}
}
