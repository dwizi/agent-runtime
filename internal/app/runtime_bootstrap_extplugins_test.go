package app

import (
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"github.com/dwizi/agent-runtime/internal/config"
)

func TestNewRuntimeExternalPluginWarmupFailureIsNonFatal(t *testing.T) {
	root := t.TempDir()
	pluginDir := filepath.Join(root, "tinyfish")
	if err := os.MkdirAll(pluginDir, 0o755); err != nil {
		t.Fatalf("mkdir plugin dir: %v", err)
	}
	manifest := `{
  "schema_version": "v1",
  "name": "TinyFish",
  "plugin_key": "tinyfish_agentic_web",
  "action_types": ["agentic_web"],
  "runtime": {
    "command": "./run.sh",
    "isolation": {
      "mode": "uv",
      "project": ".",
      "warm_on_bootstrap": true,
      "locked": true
    }
  }
}`
	if err := os.WriteFile(filepath.Join(pluginDir, "plugin.json"), []byte(manifest), 0o644); err != nil {
		t.Fatalf("write manifest: %v", err)
	}
	if err := os.WriteFile(filepath.Join(pluginDir, "run.sh"), []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatalf("write run.sh: %v", err)
	}
	if err := os.WriteFile(filepath.Join(pluginDir, "pyproject.toml"), []byte("[project]\nname='p'\nversion='0.1.0'\n"), 0o644); err != nil {
		t.Fatalf("write pyproject: %v", err)
	}
	if err := os.WriteFile(filepath.Join(pluginDir, "uv.lock"), []byte("version = 1\n"), 0o644); err != nil {
		t.Fatalf("write uv.lock: %v", err)
	}
	pluginsConfig := `{
  "external_plugins": [
    {"id":"tinyfish","enabled":true,"manifest":"tinyfish/plugin.json"}
  ]
}`
	pluginsConfigPath := filepath.Join(root, "plugins.json")
	if err := os.WriteFile(pluginsConfigPath, []byte(pluginsConfig), 0o644); err != nil {
		t.Fatalf("write plugins config: %v", err)
	}

	cfg := config.FromEnv()
	cfg.DataDir = root
	cfg.DBPath = filepath.Join(root, "meta.sqlite")
	cfg.WorkspaceRoot = filepath.Join(root, "workspaces")
	cfg.ExtPluginsConfigPath = pluginsConfigPath
	cfg.ExtPluginCacheDir = filepath.Join(root, "ext-plugin-cache")
	cfg.ExtPluginWarmOnBootstrap = true
	cfg.SandboxRunnerCommand = filepath.Join(root, "missing-runner")
	cfg.SandboxRunnerArgs = ""

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	runtime, err := New(cfg, logger)
	if err != nil {
		t.Fatalf("new runtime: %v", err)
	}
	t.Cleanup(func() { _ = runtime.Close() })
}
