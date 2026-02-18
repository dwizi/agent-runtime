package externalcmd

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/dwizi/agent-runtime/internal/store"
)

func TestExecuteJSONResponse(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell script test")
	}
	dir := t.TempDir()
	scriptPath := filepath.Join(dir, "plugin.sh")
	script := `#!/bin/sh
cat >/dev/null
echo '{"message":"external plugin ok","plugin":"echo_ext"}'
`
	if err := os.WriteFile(scriptPath, []byte(script), 0o755); err != nil {
		t.Fatalf("write script: %v", err)
	}

	plugin, err := New(Config{
		ID:          "echo",
		BaseDir:     dir,
		Command:     "./plugin.sh",
		ActionTypes: []string{"echo_action"},
	})
	if err != nil {
		t.Fatalf("new plugin: %v", err)
	}
	result, err := plugin.Execute(context.Background(), store.ActionApproval{
		ActionType: "echo_action",
	})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if result.Plugin != "echo_ext" {
		t.Fatalf("unexpected plugin: %s", result.Plugin)
	}
	if result.Message != "external plugin ok" {
		t.Fatalf("unexpected message: %s", result.Message)
	}
}

func TestExecutePlainTextResponse(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell script test")
	}
	dir := t.TempDir()
	scriptPath := filepath.Join(dir, "plugin.sh")
	script := `#!/bin/sh
cat >/dev/null
echo 'plain output line'
`
	if err := os.WriteFile(scriptPath, []byte(script), 0o755); err != nil {
		t.Fatalf("write script: %v", err)
	}
	plugin, err := New(Config{
		ID:          "echo",
		BaseDir:     dir,
		Command:     "./plugin.sh",
		ActionTypes: []string{"echo_action"},
	})
	if err != nil {
		t.Fatalf("new plugin: %v", err)
	}
	result, err := plugin.Execute(context.Background(), store.ActionApproval{
		ActionType: "echo_action",
	})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !strings.Contains(result.Message, "plain output line") {
		t.Fatalf("unexpected message: %s", result.Message)
	}
}

func TestExecuteFailureIncludesStderr(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell script test")
	}
	dir := t.TempDir()
	scriptPath := filepath.Join(dir, "plugin.sh")
	script := `#!/bin/sh
echo 'failed for test' 1>&2
exit 12
`
	if err := os.WriteFile(scriptPath, []byte(script), 0o755); err != nil {
		t.Fatalf("write script: %v", err)
	}
	plugin, err := New(Config{
		ID:          "echo",
		BaseDir:     dir,
		Command:     "./plugin.sh",
		ActionTypes: []string{"echo_action"},
	})
	if err != nil {
		t.Fatalf("new plugin: %v", err)
	}
	_, err = plugin.Execute(context.Background(), store.ActionApproval{
		ActionType: "echo_action",
	})
	if err == nil {
		t.Fatal("expected execution error")
	}
	if !strings.Contains(err.Error(), "failed for test") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestExecuteUsesRunnerWrapper(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell script test")
	}
	dir := t.TempDir()
	logFile := filepath.Join(dir, "runner.log")
	runnerPath := filepath.Join(dir, "runner.sh")
	runner := `#!/bin/sh
echo "$@" >> "$RUNNER_LOG"
echo '{"message":"runner wrapped","plugin":"runner"}'
`
	if err := os.WriteFile(runnerPath, []byte(runner), 0o755); err != nil {
		t.Fatalf("write runner: %v", err)
	}
	pluginPath := filepath.Join(dir, "plugin.sh")
	if err := os.WriteFile(pluginPath, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatalf("write plugin: %v", err)
	}

	plugin, err := New(Config{
		ID:            "wrapped",
		BaseDir:       dir,
		Command:       "./plugin.sh",
		ActionTypes:   []string{"wrapped_action"},
		RunnerCommand: "./runner.sh",
		RunnerArgs:    []string{"--wrap"},
		Env: map[string]string{
			"RUNNER_LOG": logFile,
		},
	})
	if err != nil {
		t.Fatalf("new plugin: %v", err)
	}
	result, err := plugin.Execute(context.Background(), store.ActionApproval{
		ActionType: "wrapped_action",
	})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if result.Message != "runner wrapped" {
		t.Fatalf("unexpected message: %s", result.Message)
	}
	logBytes, err := os.ReadFile(logFile)
	if err != nil {
		t.Fatalf("read log: %v", err)
	}
	logText := strings.TrimSpace(string(logBytes))
	if !strings.Contains(logText, "--wrap") {
		t.Fatalf("expected runner args, got %q", logText)
	}
	if !strings.Contains(logText, "plugin.sh") {
		t.Fatalf("expected wrapped command in log, got %q", logText)
	}
}

func TestExecuteUVModeUsesSyncAndRun(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell script test")
	}
	dir := t.TempDir()
	logFile := filepath.Join(dir, "runner.log")
	runnerPath := filepath.Join(dir, "runner.sh")
	runner := `#!/bin/sh
echo "$@" >> "$RUNNER_LOG"
case " $* " in
  *" run "*) echo '{"message":"uv run ok","plugin":"uv_runner"}' ;;
esac
`
	if err := os.WriteFile(runnerPath, []byte(runner), 0o755); err != nil {
		t.Fatalf("write runner: %v", err)
	}
	pluginPath := filepath.Join(dir, "plugin.sh")
	if err := os.WriteFile(pluginPath, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatalf("write plugin: %v", err)
	}

	plugin, err := New(Config{
		ID:            "tinyfish",
		BaseDir:       dir,
		Command:       "./plugin.sh",
		ActionTypes:   []string{"agentic_web"},
		RunnerCommand: "./runner.sh",
		RunnerArgs:    []string{"--sandbox"},
		Env: map[string]string{
			"RUNNER_LOG": logFile,
		},
		UV: &UVConfig{
			ProjectDir:      filepath.Join(dir, "proj"),
			CacheDir:        filepath.Join(dir, "cache"),
			VenvDir:         filepath.Join(dir, "cache", "venv"),
			WarmOnBootstrap: true,
			Locked:          true,
		},
	})
	if err != nil {
		t.Fatalf("new plugin: %v", err)
	}
	result, err := plugin.Execute(context.Background(), store.ActionApproval{
		ActionType: "agentic_web",
	})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if result.Message != "uv run ok" {
		t.Fatalf("unexpected message: %s", result.Message)
	}
	logBytes, err := os.ReadFile(logFile)
	if err != nil {
		t.Fatalf("read log: %v", err)
	}
	logText := string(logBytes)
	if !strings.Contains(logText, "uv sync --project") {
		t.Fatalf("expected uv sync call, got %q", logText)
	}
	if !strings.Contains(logText, "uv run --project") {
		t.Fatalf("expected uv run call, got %q", logText)
	}
}
