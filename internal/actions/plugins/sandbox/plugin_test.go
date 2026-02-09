package sandbox

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/carlos/spinner/internal/store"
)

func TestExecuteAllowedCommand(t *testing.T) {
	root := t.TempDir()
	workspaceDir := filepath.Join(root, "ws-1")
	if err := os.MkdirAll(workspaceDir, 0o755); err != nil {
		t.Fatalf("mkdir workspace: %v", err)
	}
	plugin := New(Config{
		Enabled:         true,
		WorkspaceRoot:   root,
		AllowedCommands: []string{"echo"},
		Timeout:         10 * time.Second,
	})
	result, err := plugin.Execute(context.Background(), store.ActionApproval{
		WorkspaceID:  "ws-1",
		ActionType:   "run_command",
		ActionTarget: "echo",
		Payload: map[string]any{
			"args": []any{"hello", "sandbox"},
		},
	})
	if err != nil {
		t.Fatalf("execute failed: %v", err)
	}
	if result.Plugin != "sandbox_command" {
		t.Fatalf("unexpected plugin key: %s", result.Plugin)
	}
	if !strings.Contains(result.Message, "hello sandbox") {
		t.Fatalf("unexpected result message: %s", result.Message)
	}
}

func TestExecuteDisallowedCommand(t *testing.T) {
	plugin := New(Config{
		Enabled:         true,
		WorkspaceRoot:   t.TempDir(),
		AllowedCommands: []string{"echo"},
		Timeout:         10 * time.Second,
	})
	_, err := plugin.Execute(context.Background(), store.ActionApproval{
		WorkspaceID:  "ws-1",
		ActionType:   "run_command",
		ActionTarget: "ls",
	})
	if err == nil {
		t.Fatal("expected disallowed command error")
	}
	if !strings.Contains(err.Error(), "not allowed") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestExecuteRejectsCwdEscape(t *testing.T) {
	root := t.TempDir()
	plugin := New(Config{
		Enabled:         true,
		WorkspaceRoot:   root,
		AllowedCommands: []string{"echo"},
		Timeout:         10 * time.Second,
	})
	_, err := plugin.Execute(context.Background(), store.ActionApproval{
		WorkspaceID:  "ws-1",
		ActionType:   "run_command",
		ActionTarget: "echo",
		Payload: map[string]any{
			"cwd": "../../",
		},
	})
	if err == nil {
		t.Fatal("expected cwd boundary error")
	}
	if !strings.Contains(err.Error(), "escapes workspace boundary") {
		t.Fatalf("unexpected error: %v", err)
	}
}
