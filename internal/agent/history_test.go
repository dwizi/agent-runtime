package agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGetRecentHistoryFiltersMetadataAndToolLogs(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "ws-1", "logs", "chats", "telegram", "42.md")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir failed: %v", err)
	}
	logContent := strings.Join([]string{
		"# Chat Log",
		"- connector: `telegram`",
		"- external_id: `42`",
		"## 2026-02-16T10:00:00Z `INBOUND`",
		"- direction: `inbound`",
		"- actor: `u1`",
		"Can you check the release notes?",
		"## 2026-02-16T10:00:05Z `TOOL`",
		"Tool call",
		"- tool: `search_knowledge_base`",
		"- status: `succeeded`",
		"- output: Found 2 results",
		"## 2026-02-16T10:00:10Z `OUTBOUND`",
		"- direction: `outbound`",
		"- actor: `agent-runtime`",
		"I found updates and can summarize them.",
	}, "\n")
	if err := os.WriteFile(path, []byte(logContent), 0o644); err != nil {
		t.Fatalf("write failed: %v", err)
	}

	got := GetRecentHistory(root, "ws-1", "telegram", "42", 10)
	if strings.Contains(strings.ToLower(got), "tool call") {
		t.Fatalf("expected tool logs to be filtered, got %q", got)
	}
	if strings.Contains(strings.ToLower(got), "connector") {
		t.Fatalf("expected metadata to be filtered, got %q", got)
	}
	if !strings.Contains(got, "user: Can you check the release notes?") {
		t.Fatalf("expected inbound line to be preserved, got %q", got)
	}
	if !strings.Contains(got, "assistant: I found updates and can summarize them.") {
		t.Fatalf("expected outbound line to be preserved, got %q", got)
	}
}

func TestGetRecentHistoryRespectsLineLimit(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "ws-1", "logs", "chats", "discord", "abc.md")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir failed: %v", err)
	}
	logContent := strings.Join([]string{
		"first line",
		"second line",
		"third line",
		"fourth line",
	}, "\n")
	if err := os.WriteFile(path, []byte(logContent), 0o644); err != nil {
		t.Fatalf("write failed: %v", err)
	}

	got := GetRecentHistory(root, "ws-1", "discord", "abc", 2)
	if strings.Contains(got, "first line") || strings.Contains(got, "second line") {
		t.Fatalf("expected only recent lines, got %q", got)
	}
	if !strings.Contains(got, "third line") || !strings.Contains(got, "fourth line") {
		t.Fatalf("expected last two lines, got %q", got)
	}
}
