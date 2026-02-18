package mcp

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadCatalogMissingFileReturnsEmpty(t *testing.T) {
	catalog, err := LoadCatalog(filepath.Join(t.TempDir(), "missing.json"), 120, 30)
	if err != nil {
		t.Fatalf("load catalog: %v", err)
	}
	if len(catalog.Servers) != 0 {
		t.Fatalf("expected no servers, got %d", len(catalog.Servers))
	}
}

func TestLoadCatalogWithEnvExpansion(t *testing.T) {
	t.Setenv("AGENT_RUNTIME_TEST_TOKEN", "secret")
	root := t.TempDir()
	path := filepath.Join(root, "servers.json")
	content := `{
  "schema_version": "v1",
  "servers": [
    {
      "id": "github",
      "enabled": true,
      "transport": {"type": "streamable_http", "endpoint": "https://example.com/mcp"},
      "http": {
        "headers": {"Authorization": "Bearer ${AGENT_RUNTIME_TEST_TOKEN}"},
        "timeout_seconds": 15
      },
      "refresh_seconds": 45,
      "policy": {
        "default_tool_class": "knowledge",
        "default_requires_approval": false,
        "tool_overrides": {
          "danger": {"tool_class": "sensitive", "requires_approval": true}
        }
      }
    }
  ]
}`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	catalog, err := LoadCatalog(path, 120, 30)
	if err != nil {
		t.Fatalf("load catalog: %v", err)
	}
	if len(catalog.Servers) != 1 {
		t.Fatalf("expected 1 server, got %d", len(catalog.Servers))
	}
	server := catalog.Servers[0]
	if server.HTTP.Headers["Authorization"] != "Bearer secret" {
		t.Fatalf("unexpected auth header: %q", server.HTTP.Headers["Authorization"])
	}
	if server.Policy.DefaultToolClass != "knowledge" {
		t.Fatalf("unexpected default tool class: %s", server.Policy.DefaultToolClass)
	}
	if !server.Policy.ToolOverrides["danger"].RequiresApproval {
		t.Fatal("expected tool override requires approval")
	}
}

func TestLoadCatalogRejectsUnknownFields(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "servers.json")
	content := `{"schema_version":"v1","unknown":true,"servers":[]}`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	if _, err := LoadCatalog(path, 120, 30); err == nil {
		t.Fatal("expected unknown field error")
	}
}

func TestLoadCatalogRejectsMissingEnv(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "servers.json")
	content := `{
  "schema_version": "v1",
  "servers": [
    {
      "id": "github",
      "transport": {"type": "streamable_http", "endpoint": "https://example.com/mcp"},
      "http": {"headers": {"Authorization": "Bearer ${AGENT_RUNTIME_MISSING_TOKEN}"}}
    }
  ]
}`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	if _, err := LoadCatalog(path, 120, 30); err == nil {
		t.Fatal("expected missing env error")
	}
}

func TestLoadWorkspaceCatalog(t *testing.T) {
	t.Setenv("AGENT_RUNTIME_WORKSPACE_TOKEN", "workspace-secret")
	root := t.TempDir()
	path := filepath.Join(root, "workspace.json")
	content := `{
  "schema_version": "v1",
  "servers": [
    {
      "id": "github",
      "enabled": false,
      "http": {"headers": {"Authorization": "Bearer ${AGENT_RUNTIME_WORKSPACE_TOKEN}"}},
      "policy": {
        "default_tool_class": "knowledge",
        "tool_overrides": {
          "danger": {"tool_class": "sensitive", "requires_approval": true}
        }
      }
    }
  ]
}`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	catalog, err := LoadWorkspaceCatalog(path)
	if err != nil {
		t.Fatalf("load workspace: %v", err)
	}
	if len(catalog.Servers) != 1 {
		t.Fatalf("expected 1 server override, got %d", len(catalog.Servers))
	}
	item := catalog.Servers[0]
	if item.Enabled == nil || *item.Enabled {
		t.Fatal("expected explicit enabled=false")
	}
	if item.HTTP == nil || item.HTTP.Headers["Authorization"] != "Bearer workspace-secret" {
		t.Fatalf("unexpected workspace auth header: %#v", item.HTTP)
	}
}
