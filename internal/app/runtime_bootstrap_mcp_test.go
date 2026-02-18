package app

import (
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"github.com/dwizi/agent-runtime/internal/config"
)

func TestNewRuntimeMCPBootstrapFailureIsNonFatal(t *testing.T) {
	root := t.TempDir()
	mcpConfigPath := filepath.Join(root, "mcp.json")
	mcpConfig := `{
  "schema_version":"v1",
  "servers":[
    {
      "id":"broken",
      "enabled":true,
      "transport":{"type":"streamable_http","endpoint":"http://127.0.0.1:1"}
    }
  ]
}`
	if err := os.WriteFile(mcpConfigPath, []byte(mcpConfig), 0o644); err != nil {
		t.Fatalf("write mcp config: %v", err)
	}

	cfg := config.FromEnv()
	cfg.DataDir = root
	cfg.DBPath = filepath.Join(root, "meta.sqlite")
	cfg.WorkspaceRoot = filepath.Join(root, "workspaces")
	cfg.ExtPluginsConfigPath = filepath.Join(root, "plugins.json")
	cfg.ExtPluginCacheDir = filepath.Join(root, "ext-plugin-cache")
	cfg.MCPConfigPath = mcpConfigPath

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	runtime, err := New(cfg, logger)
	if err != nil {
		t.Fatalf("new runtime: %v", err)
	}
	t.Cleanup(func() { _ = runtime.Close() })

	if runtime.mcp == nil {
		t.Fatal("expected mcp manager to be configured")
	}
	summary := runtime.mcp.Summary()
	if summary.EnabledServers != 1 {
		t.Fatalf("expected enabled servers 1, got %d", summary.EnabledServers)
	}
	if summary.HealthyServers != 0 {
		t.Fatalf("expected healthy servers 0, got %d", summary.HealthyServers)
	}
}

func TestNewRuntimeMCPConfigMissingIsNoop(t *testing.T) {
	root := t.TempDir()
	cfg := config.FromEnv()
	cfg.DataDir = root
	cfg.DBPath = filepath.Join(root, "meta.sqlite")
	cfg.WorkspaceRoot = filepath.Join(root, "workspaces")
	cfg.ExtPluginsConfigPath = filepath.Join(root, "plugins.json")
	cfg.ExtPluginCacheDir = filepath.Join(root, "ext-plugin-cache")
	cfg.MCPConfigPath = filepath.Join(root, "missing-mcp.json")

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	runtime, err := New(cfg, logger)
	if err != nil {
		t.Fatalf("new runtime: %v", err)
	}
	t.Cleanup(func() { _ = runtime.Close() })

	if runtime.mcp == nil {
		t.Fatal("expected mcp manager to be initialized")
	}
	summary := runtime.mcp.Summary()
	if summary.EnabledServers != 0 {
		t.Fatalf("expected zero enabled servers, got %d", summary.EnabledServers)
	}
}
