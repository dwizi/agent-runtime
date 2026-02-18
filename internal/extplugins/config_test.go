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
	if len(cfg.ExternalPlugins) != 0 {
		t.Fatalf("expected no plugins, got %d", len(cfg.ExternalPlugins))
	}
}

func TestLoadConfigValid(t *testing.T) {
	path := filepath.Join(t.TempDir(), "plugins.json")
	content := `{
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
	if len(cfg.ExternalPlugins) != 1 {
		t.Fatalf("expected one external plugin config, got %d", len(cfg.ExternalPlugins))
	}
}

func TestLoadConfigRejectsUnknownFields(t *testing.T) {
	path := filepath.Join(t.TempDir(), "plugins.json")
	content := `{
  "tinyfish": {
    "enabled": true
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
	if manifest.Runtime.Isolation.Mode != "none" {
		t.Fatalf("expected default isolation mode none, got %s", manifest.Runtime.Isolation.Mode)
	}
	if manifest.Runtime.Isolation.Project != "." {
		t.Fatalf("expected default isolation project ., got %s", manifest.Runtime.Isolation.Project)
	}
}

func TestLoadManifestValidIsolation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "plugin.json")
	content := `{
  "schema_version": "v1",
  "name": "TinyFish",
  "plugin_key": "tinyfish_agentic_web",
  "action_types": ["agentic_web"],
  "runtime": {
    "command": "./run.sh",
    "isolation": {
      "mode": "uv",
      "project": "python",
      "warm_on_bootstrap": false,
      "locked": false
    }
  }
}`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write manifest: %v", err)
	}
	manifest, err := LoadManifest(path)
	if err != nil {
		t.Fatalf("load manifest: %v", err)
	}
	if manifest.Runtime.Isolation.Mode != "uv" {
		t.Fatalf("expected isolation mode uv, got %s", manifest.Runtime.Isolation.Mode)
	}
	if manifest.Runtime.Isolation.Project != "python" {
		t.Fatalf("expected project python, got %s", manifest.Runtime.Isolation.Project)
	}
	if manifest.Runtime.Isolation.WarmOnBootstrapValue(true) {
		t.Fatal("expected warm_on_bootstrap false")
	}
	if manifest.Runtime.Isolation.LockedValue(true) {
		t.Fatal("expected locked false")
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
