package sandbox

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/dwizi/agent-runtime/internal/store"
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

func TestExecuteRejectsPathCommand(t *testing.T) {
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
	_, err := plugin.Execute(context.Background(), store.ActionApproval{
		WorkspaceID:  "ws-1",
		ActionType:   "run_command",
		ActionTarget: "/bin/echo",
	})
	if err == nil {
		t.Fatal("expected path command validation error")
	}
	if !strings.Contains(err.Error(), "bare executable") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestExecuteWithRunnerCommand(t *testing.T) {
	root := t.TempDir()
	workspaceDir := filepath.Join(root, "ws-1")
	if err := os.MkdirAll(workspaceDir, 0o755); err != nil {
		t.Fatalf("mkdir workspace: %v", err)
	}
	plugin := New(Config{
		Enabled:         true,
		WorkspaceRoot:   root,
		AllowedCommands: []string{"curl"},
		RunnerCommand:   "echo",
		RunnerArgs:      []string{"runner"},
		Timeout:         10 * time.Second,
	})
	result, err := plugin.Execute(context.Background(), store.ActionApproval{
		WorkspaceID:  "ws-1",
		ActionType:   "run_command",
		ActionTarget: "curl",
		Payload: map[string]any{
			"args": []any{"-sS", "https://example.com"},
		},
	})
	if err != nil {
		t.Fatalf("execute with runner failed: %v", err)
	}
	if !strings.Contains(result.Message, "runner curl -sS https://example.com") {
		t.Fatalf("unexpected runner output: %s", result.Message)
	}
}

func TestExecuteParsesArgsFromPayloadCommandString(t *testing.T) {
	root := t.TempDir()
	workspaceDir := filepath.Join(root, "ws-1")
	if err := os.MkdirAll(workspaceDir, 0o755); err != nil {
		t.Fatalf("mkdir workspace: %v", err)
	}
	plugin := New(Config{
		Enabled:         true,
		WorkspaceRoot:   root,
		AllowedCommands: []string{"curl"},
		RunnerCommand:   "echo",
		RunnerArgs:      []string{"runner"},
		Timeout:         10 * time.Second,
	})
	result, err := plugin.Execute(context.Background(), store.ActionApproval{
		WorkspaceID:  "ws-1",
		ActionType:   "run_command",
		ActionTarget: "curl",
		Payload: map[string]any{
			"command": "curl -sS https://example.com",
		},
	})
	if err != nil {
		t.Fatalf("execute failed: %v", err)
	}
	if !strings.Contains(result.Message, "runner curl -sS https://example.com") {
		t.Fatalf("unexpected parsed command output: %s", result.Message)
	}
}

func TestExecuteParsesArgsFromNestedPayload(t *testing.T) {
	root := t.TempDir()
	workspaceDir := filepath.Join(root, "ws-1")
	if err := os.MkdirAll(workspaceDir, 0o755); err != nil {
		t.Fatalf("mkdir workspace: %v", err)
	}
	plugin := New(Config{
		Enabled:         true,
		WorkspaceRoot:   root,
		AllowedCommands: []string{"curl"},
		RunnerCommand:   "echo",
		RunnerArgs:      []string{"runner"},
		Timeout:         10 * time.Second,
	})
	result, err := plugin.Execute(context.Background(), store.ActionApproval{
		WorkspaceID:  "ws-1",
		ActionType:   "run_command",
		ActionTarget: "curl",
		Payload: map[string]any{
			"payload": map[string]any{
				"args": []any{"-sS", "https://example.com"},
			},
		},
	})
	if err != nil {
		t.Fatalf("execute failed: %v", err)
	}
	if !strings.Contains(result.Message, "runner curl -sS https://example.com") {
		t.Fatalf("unexpected nested payload args output: %s", result.Message)
	}
}

func TestSummarizeCommandOutcomeCurlRedirectHint(t *testing.T) {
	message := summarizeCommandOutcome("curl", []string{"-sS", "https://example.com"}, "Redirecting to https://www.example.com", false)
	if !strings.Contains(message, "curl stopped at an HTTP redirect") {
		t.Fatalf("expected redirect explanation, got %s", message)
	}
	if !strings.Contains(message, "-L") {
		t.Fatalf("expected -L hint, got %s", message)
	}
}

func TestSummarizeCommandOutcomeCurlWithLocationDoesNotWarn(t *testing.T) {
	message := summarizeCommandOutcome("curl", []string{"-sS", "-L", "https://example.com"}, "Redirecting to https://www.example.com", false)
	if strings.Contains(message, "stopped at an HTTP redirect") {
		t.Fatalf("expected no redirect warning when using -L, got %s", message)
	}
	if !strings.Contains(message, "Output:") {
		t.Fatalf("expected output summary, got %s", message)
	}
}
