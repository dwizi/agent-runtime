package extplugins

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type UVRuntime struct {
	Enabled         bool
	ProjectDir      string
	CacheDir        string
	VenvDir         string
	WarmOnBootstrap bool
	Locked          bool
}

func BuildUVRuntime(pluginID, baseDir, cacheRoot string, isolation PluginIsolation, defaultWarm bool) (UVRuntime, error) {
	mode := strings.ToLower(strings.TrimSpace(isolation.Mode))
	if mode == "" || mode == "none" {
		return UVRuntime{}, nil
	}
	if mode != "uv" {
		return UVRuntime{}, fmt.Errorf("unsupported isolation mode %q", mode)
	}

	baseDir = filepath.Clean(strings.TrimSpace(baseDir))
	if baseDir == "" {
		return UVRuntime{}, fmt.Errorf("base directory is required")
	}
	cacheRoot = filepath.Clean(strings.TrimSpace(cacheRoot))
	if cacheRoot == "" {
		return UVRuntime{}, fmt.Errorf("external plugin cache directory is required for uv isolation")
	}

	projectRel := strings.TrimSpace(isolation.Project)
	if projectRel == "" {
		projectRel = "."
	}
	if filepath.IsAbs(projectRel) {
		return UVRuntime{}, fmt.Errorf("uv isolation project path must be relative, got %q", projectRel)
	}
	projectDir := filepath.Clean(filepath.Join(baseDir, projectRel))
	if !isPathWithinBase(projectDir, baseDir) {
		return UVRuntime{}, fmt.Errorf("uv isolation project path %q escapes plugin directory", projectRel)
	}

	pyprojectPath := filepath.Join(projectDir, "pyproject.toml")
	pyprojectBytes, err := os.ReadFile(pyprojectPath)
	if err != nil {
		return UVRuntime{}, fmt.Errorf("read uv project file %s: %w", pyprojectPath, err)
	}

	locked := isolation.LockedValue(true)
	lockPath := filepath.Join(projectDir, "uv.lock")
	lockBytes, lockErr := os.ReadFile(lockPath)
	if lockErr != nil {
		if !os.IsNotExist(lockErr) {
			return UVRuntime{}, fmt.Errorf("read uv lock file %s: %w", lockPath, lockErr)
		}
		if locked {
			return UVRuntime{}, fmt.Errorf("uv isolation requires lock file %s when locked=true", lockPath)
		}
		lockBytes = nil
	}

	hashValue := digestConfig(pyprojectBytes, lockBytes)
	safeID := sanitizePluginID(pluginID)
	cacheDir := filepath.Join(cacheRoot, "uv-cache")
	venvDir := filepath.Join(cacheRoot, "venvs", safeID+"-"+hashValue[:12])

	return UVRuntime{
		Enabled:         true,
		ProjectDir:      projectDir,
		CacheDir:        cacheDir,
		VenvDir:         venvDir,
		WarmOnBootstrap: isolation.WarmOnBootstrapValue(defaultWarm),
		Locked:          locked,
	}, nil
}

func digestConfig(pyproject, lock []byte) string {
	hasher := sha256.New()
	_, _ = hasher.Write([]byte("pyproject:"))
	_, _ = hasher.Write(pyproject)
	_, _ = hasher.Write([]byte("\nuv.lock:"))
	if len(lock) > 0 {
		_, _ = hasher.Write(lock)
	}
	return hex.EncodeToString(hasher.Sum(nil))
}

func sanitizePluginID(raw string) string {
	normalized := strings.ToLower(strings.TrimSpace(raw))
	if normalized == "" {
		return "plugin"
	}
	builder := strings.Builder{}
	for _, r := range normalized {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' || r == '_' {
			builder.WriteRune(r)
			continue
		}
		builder.WriteByte('-')
	}
	result := strings.Trim(builder.String(), "-")
	if result == "" {
		return "plugin"
	}
	return result
}

func isPathWithinBase(path, base string) bool {
	rel, err := filepath.Rel(base, path)
	if err != nil {
		return false
	}
	rel = filepath.Clean(rel)
	return rel == "." || (!strings.HasPrefix(rel, "..") && rel != "..")
}
