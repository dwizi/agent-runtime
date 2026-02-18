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
