package extplugins

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadConfigMissingFile(t *testing.T) {
	cfg, err := LoadConfig(filepath.Join(t.TempDir(), "missing.json"))
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if cfg.Tinyfish.Enabled {
		t.Fatal("expected tinyfish disabled by default")
	}
}

func TestLoadConfigValid(t *testing.T) {
	path := filepath.Join(t.TempDir(), "plugins.json")
	content := `{
  "tinyfish": {
    "enabled": true,
    "base_url": "https://agent.tinyfish.ai",
    "api_key_env": "AGENT_RUNTIME_TINYFISH_API_KEY",
    "timeout_seconds": 90
  },
  "external_plugins": [
    {"id": "echo", "enabled": true, "manifest": "echo/plugin.json"}
  ]
}`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if !cfg.Tinyfish.Enabled {
		t.Fatal("expected tinyfish enabled")
	}
	if cfg.Tinyfish.BaseURL != "https://agent.tinyfish.ai" {
		t.Fatalf("unexpected base url: %s", cfg.Tinyfish.BaseURL)
	}
	if cfg.Tinyfish.APIKeyEnv != "AGENT_RUNTIME_TINYFISH_API_KEY" {
		t.Fatalf("unexpected api key env: %s", cfg.Tinyfish.APIKeyEnv)
	}
	if cfg.Tinyfish.TimeoutSeconds != 90 {
		t.Fatalf("unexpected timeout: %d", cfg.Tinyfish.TimeoutSeconds)
	}
	if len(cfg.ExternalPlugins) != 1 {
		t.Fatalf("expected one external plugin config, got %d", len(cfg.ExternalPlugins))
	}
}

func TestLoadConfigRejectsUnknownFields(t *testing.T) {
	path := filepath.Join(t.TempDir(), "plugins.json")
	content := `{
  "tinyfish": {
    "enabled": true,
    "nope": true
  }
}`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	if _, err := LoadConfig(path); err == nil {
		t.Fatal("expected decode error for unknown field")
	}
}

func TestLoadManifestValid(t *testing.T) {
	path := filepath.Join(t.TempDir(), "plugin.json")
	content := `{
  "schema_version": "v1",
  "name": "Echo",
  "plugin_key": "echo_ext",
  "action_types": ["echo_action", "ECHO_ACTION"],
  "runtime": {
    "command": "./run.sh",
    "args": ["--json"],
    "timeout_seconds": 30
  }
}`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write manifest: %v", err)
	}
	manifest, err := LoadManifest(path)
	if err != nil {
		t.Fatalf("load manifest: %v", err)
	}
	if len(manifest.ActionTypes) != 1 || manifest.ActionTypes[0] != "echo_action" {
		t.Fatalf("unexpected action types: %#v", manifest.ActionTypes)
	}
	if manifest.Runtime.Command != "./run.sh" {
		t.Fatalf("unexpected command: %s", manifest.Runtime.Command)
	}
}

func TestResolveExternalPlugins(t *testing.T) {
	root := t.TempDir()
	configPath := filepath.Join(root, "plugins.json")
	pluginDir := filepath.Join(root, "echo")
	if err := os.MkdirAll(pluginDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	manifestPath := filepath.Join(pluginDir, "plugin.json")
	manifest := `{
  "schema_version": "v1",
  "name": "Echo",
  "plugin_key": "echo_ext",
  "action_types": ["echo_action"],
  "runtime": {
    "command": "./run.sh"
  }
}`
	if err := os.WriteFile(manifestPath, []byte(manifest), 0o644); err != nil {
		t.Fatalf("write manifest: %v", err)
	}

	refs := []ExternalPluginRef{
		{Enabled: true, Manifest: "echo/plugin.json"},
	}
	resolved, err := ResolveExternalPlugins(configPath, refs)
	if err != nil {
		t.Fatalf("resolve plugins: %v", err)
	}
	if len(resolved) != 1 {
		t.Fatalf("expected one resolved plugin, got %d", len(resolved))
	}
	if resolved[0].ManifestPath != manifestPath {
		t.Fatalf("unexpected manifest path: %s", resolved[0].ManifestPath)
	}
	if resolved[0].ID != "echo_ext" {
		t.Fatalf("unexpected id: %s", resolved[0].ID)
	}
}
