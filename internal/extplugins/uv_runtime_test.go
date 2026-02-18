package extplugins

import (
	"os"
	"path/filepath"
	"testing"
)

func TestBuildUVRuntimeDefaults(t *testing.T) {
	root := t.TempDir()
	pluginDir := filepath.Join(root, "plugin")
	if err := os.MkdirAll(pluginDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(pluginDir, "pyproject.toml"), []byte("[project]\nname='p'\nversion='0.1.0'\n"), 0o644); err != nil {
		t.Fatalf("write pyproject: %v", err)
	}
	if err := os.WriteFile(filepath.Join(pluginDir, "uv.lock"), []byte("version = 1\n"), 0o644); err != nil {
		t.Fatalf("write lock: %v", err)
	}

	runtime, err := BuildUVRuntime("My Plugin", pluginDir, filepath.Join(root, "cache"), PluginIsolation{
		Mode: "uv",
	}, true)
	if err != nil {
		t.Fatalf("build runtime: %v", err)
	}
	if !runtime.Enabled {
		t.Fatal("expected uv runtime enabled")
	}
	if runtime.ProjectDir != pluginDir {
		t.Fatalf("unexpected project dir: %s", runtime.ProjectDir)
	}
	if !runtime.WarmOnBootstrap {
		t.Fatal("expected warm_on_bootstrap default true")
	}
	if !runtime.Locked {
		t.Fatal("expected locked default true")
	}
	if filepath.Base(runtime.VenvDir) == "" {
		t.Fatal("expected venv path")
	}
}

func TestBuildUVRuntimeRequiresLockWhenLocked(t *testing.T) {
	root := t.TempDir()
	pluginDir := filepath.Join(root, "plugin")
	if err := os.MkdirAll(pluginDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(pluginDir, "pyproject.toml"), []byte("[project]\nname='p'\nversion='0.1.0'\n"), 0o644); err != nil {
		t.Fatalf("write pyproject: %v", err)
	}

	_, err := BuildUVRuntime("plugin", pluginDir, filepath.Join(root, "cache"), PluginIsolation{
		Mode: "uv",
	}, true)
	if err == nil {
		t.Fatal("expected lockfile error")
	}
}

func TestBuildUVRuntimeAllowsUnlockedWithoutLock(t *testing.T) {
	root := t.TempDir()
	pluginDir := filepath.Join(root, "plugin")
	if err := os.MkdirAll(pluginDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(pluginDir, "pyproject.toml"), []byte("[project]\nname='p'\nversion='0.1.0'\n"), 0o644); err != nil {
		t.Fatalf("write pyproject: %v", err)
	}
	locked := false
	warm := false
	runtime, err := BuildUVRuntime("plugin", pluginDir, filepath.Join(root, "cache"), PluginIsolation{
		Mode:            "uv",
		Locked:          &locked,
		WarmOnBootstrap: &warm,
	}, true)
	if err != nil {
		t.Fatalf("build runtime: %v", err)
	}
	if runtime.Locked {
		t.Fatal("expected locked false")
	}
	if runtime.WarmOnBootstrap {
		t.Fatal("expected warm_on_bootstrap false")
	}
}
