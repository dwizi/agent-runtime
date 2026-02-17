package sandbox

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
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

func TestExecuteAllowsWorkspaceFileCreation(t *testing.T) {
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash not available in test environment")
	}
	root := t.TempDir()
	workspaceDir := filepath.Join(root, "ws-1")
	if err := os.MkdirAll(workspaceDir, 0o755); err != nil {
		t.Fatalf("mkdir workspace: %v", err)
	}

	plugin := New(Config{
		Enabled:         true,
		WorkspaceRoot:   root,
		AllowedCommands: []string{"bash"},
		Timeout:         10 * time.Second,
	})
	result, err := plugin.Execute(context.Background(), store.ActionApproval{
		WorkspaceID:  "ws-1",
		ActionType:   "run_command",
		ActionTarget: "bash",
		Payload: map[string]any{
			"args": []any{
				"-lc",
				"mkdir -p memory && printf 'launch code: LUMEN-42\\nchecksum phrase: blue-otter\\n' > memory/sandbox-tool-memory-report.md && grep -n 'LUMEN-42\\|blue-otter' memory/sandbox-tool-memory-report.md",
			},
		},
	})
	if err != nil {
		t.Fatalf("execute failed: %v", err)
	}
	if !strings.Contains(result.Message, "1:launch code: LUMEN-42") {
		t.Fatalf("expected grep output for launch code, got %s", result.Message)
	}
	if !strings.Contains(result.Message, "2:checksum phrase: blue-otter") {
		t.Fatalf("expected grep output for checksum phrase, got %s", result.Message)
	}

	reportPath := filepath.Join(workspaceDir, "memory", "sandbox-tool-memory-report.md")
	reportBytes, readErr := os.ReadFile(reportPath)
	if readErr != nil {
		t.Fatalf("read created report: %v", readErr)
	}
	report := string(reportBytes)
	if !strings.Contains(report, "launch code: LUMEN-42") {
		t.Fatalf("expected launch code in report, got %q", report)
	}
	if !strings.Contains(report, "checksum phrase: blue-otter") {
		t.Fatalf("expected checksum phrase in report, got %q", report)
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

func TestTranslateRGToGrep(t *testing.T) {
	args, ok := translateRGToGrep([]string{"-n", "monitor", "/tmp/ws"})
	if !ok {
		t.Fatal("expected rg fallback translation")
	}
	got := strings.Join(args, " ")
	if !strings.Contains(got, "-R -n -- monitor /tmp/ws") {
		t.Fatalf("unexpected grep args: %s", got)
	}
}

func TestTranslateCurlToWget(t *testing.T) {
	args, ok := translateCurlToWget([]string{"-fsSL", "https://example.com"})
	if !ok {
		t.Fatal("expected curl fallback translation")
	}
	got := strings.Join(args, " ")
	if !strings.Contains(got, "-q -O - https://example.com") {
		t.Fatalf("unexpected wget args: %s", got)
	}
}

func TestExecuteFallbackToGrepWhenRGMissing(t *testing.T) {
	root := t.TempDir()
	workspaceDir := filepath.Join(root, "ws-1")
	if err := os.MkdirAll(workspaceDir, 0o755); err != nil {
		t.Fatalf("mkdir workspace: %v", err)
	}
	if err := os.WriteFile(filepath.Join(workspaceDir, "notes.md"), []byte("monitor objective\n"), 0o644); err != nil {
		t.Fatalf("write notes: %v", err)
	}
	grepPath, err := exec.LookPath("grep")
	if err != nil {
		t.Skip("grep not available on host")
	}
	binDir := filepath.Join(root, "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatalf("mkdir bin: %v", err)
	}
	if err := os.Symlink(grepPath, filepath.Join(binDir, "grep")); err != nil {
		t.Fatalf("symlink grep: %v", err)
	}
	t.Setenv("PATH", binDir)

	plugin := New(Config{
		Enabled:         true,
		WorkspaceRoot:   root,
		AllowedCommands: []string{"rg"},
		Timeout:         10 * time.Second,
	})
	result, err := plugin.Execute(context.Background(), store.ActionApproval{
		WorkspaceID:  "ws-1",
		ActionType:   "run_command",
		ActionTarget: "rg",
		Payload: map[string]any{
			"args": []any{"-n", "monitor", "."},
		},
	})
	if err != nil {
		t.Fatalf("execute failed: %v", err)
	}
	if !strings.Contains(strings.ToLower(result.Message), "fallback: rg -> grep") {
		t.Fatalf("expected fallback hint, got %s", result.Message)
	}
	if !strings.Contains(result.Message, "notes.md") {
		t.Fatalf("expected grep output to include notes.md, got %s", result.Message)
	}
}

func TestExecuteFallbackToWgetWhenCurlMissing(t *testing.T) {
	root := t.TempDir()
	workspaceDir := filepath.Join(root, "ws-1")
	if err := os.MkdirAll(workspaceDir, 0o755); err != nil {
		t.Fatalf("mkdir workspace: %v", err)
	}
	wgetPath, err := exec.LookPath("wget")
	if err != nil {
		t.Skip("wget not available on host")
	}
	binDir := filepath.Join(root, "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatalf("mkdir bin: %v", err)
	}
	if err := os.Symlink(wgetPath, filepath.Join(binDir, "wget")); err != nil {
		t.Fatalf("symlink wget: %v", err)
	}
	t.Setenv("PATH", binDir)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("hello from test server"))
	}))
	defer server.Close()

	plugin := New(Config{
		Enabled:         true,
		WorkspaceRoot:   root,
		AllowedCommands: []string{"curl"},
		Timeout:         10 * time.Second,
	})
	result, err := plugin.Execute(context.Background(), store.ActionApproval{
		WorkspaceID:  "ws-1",
		ActionType:   "run_command",
		ActionTarget: "curl",
		Payload: map[string]any{
			"args": []any{"-fsSL", server.URL},
		},
	})
	if err != nil {
		t.Fatalf("execute failed: %v", err)
	}
	if !strings.Contains(strings.ToLower(result.Message), "fallback: curl -> wget") {
		t.Fatalf("expected fallback hint, got %s", result.Message)
	}
	if !strings.Contains(result.Message, "hello from test server") {
		t.Fatalf("expected wget output body, got %s", result.Message)
	}
}

func TestRetryGitDiffNoIndexDetectsOutsideRepoFailure(t *testing.T) {
	retryArgs, fallback, ok := retryGitDiffNoIndex(
		"git",
		[]string{"diff", "old.md", "new.md"},
		&exec.ExitError{},
		"warning: Not a git repository. Use --no-index to compare two paths outside a working tree",
	)
	if !ok {
		t.Fatal("expected git diff retry to be enabled")
	}
	if fallback != "git diff -> git diff --no-index" {
		t.Fatalf("unexpected fallback hint: %s", fallback)
	}
	got := strings.Join(retryArgs, " ")
	if got != "diff --no-index old.md new.md" {
		t.Fatalf("unexpected retry args: %s", got)
	}
}

func TestExecuteGitDiffTreatsExitCodeOneAsSuccess(t *testing.T) {
	root := t.TempDir()
	workspaceDir := filepath.Join(root, "ws-1")
	if err := os.MkdirAll(workspaceDir, 0o755); err != nil {
		t.Fatalf("mkdir workspace: %v", err)
	}
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available on host")
	}

	oldPath := filepath.Join(workspaceDir, "old.md")
	newPath := filepath.Join(workspaceDir, "new.md")
	if err := os.WriteFile(oldPath, []byte("line one\nline two\n"), 0o644); err != nil {
		t.Fatalf("write old markdown: %v", err)
	}
	if err := os.WriteFile(newPath, []byte("line one\nline changed\n"), 0o644); err != nil {
		t.Fatalf("write new markdown: %v", err)
	}

	plugin := New(Config{
		Enabled:         true,
		WorkspaceRoot:   root,
		AllowedCommands: []string{"git"},
		Timeout:         10 * time.Second,
	})
	result, err := plugin.Execute(context.Background(), store.ActionApproval{
		WorkspaceID:  "ws-1",
		ActionType:   "run_command",
		ActionTarget: "git",
		Payload: map[string]any{
			"args": []any{"diff", "old.md", "new.md"},
		},
	})
	if err != nil {
		t.Fatalf("execute failed: %v", err)
	}
	if !strings.Contains(result.Message, "-line two") || !strings.Contains(result.Message, "+line changed") {
		t.Fatalf("expected git diff output, got %s", result.Message)
	}
}
