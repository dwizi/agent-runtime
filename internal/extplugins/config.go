package extplugins

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

const DefaultConfigPath = "ext/plugins/plugins.json"

type Config struct {
	Tinyfish        TinyfishConfig      `json:"tinyfish"`
	ExternalPlugins []ExternalPluginRef `json:"external_plugins"`
}

type TinyfishConfig struct {
	Enabled        bool   `json:"enabled"`
	BaseURL        string `json:"base_url"`
	APIKey         string `json:"api_key"`
	APIKeyEnv      string `json:"api_key_env"`
	TimeoutSeconds int    `json:"timeout_seconds"`
}

type ExternalPluginRef struct {
	ID       string `json:"id"`
	Enabled  bool   `json:"enabled"`
	Manifest string `json:"manifest"`
}

type PluginManifest struct {
	SchemaVersion string        `json:"schema_version"`
	Name          string        `json:"name"`
	Description   string        `json:"description"`
	PluginKey     string        `json:"plugin_key"`
	ActionTypes   []string      `json:"action_types"`
	Runtime       PluginRuntime `json:"runtime"`
}

type PluginRuntime struct {
	Command        string            `json:"command"`
	Args           []string          `json:"args"`
	Env            map[string]string `json:"env"`
	TimeoutSeconds int               `json:"timeout_seconds"`
}

type ResolvedExternalPlugin struct {
	ID           string
	ManifestPath string
	BaseDir      string
	Manifest     PluginManifest
}

func LoadConfig(path string) (Config, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		path = DefaultConfigPath
	}

	var cfg Config
	if err := decodeJSONFile(path, &cfg); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return Config{}, nil
		}
		return Config{}, err
	}
	return cfg, nil
}

func LoadManifest(path string) (PluginManifest, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return PluginManifest{}, fmt.Errorf("external plugin manifest path is required")
	}
	var manifest PluginManifest
	if err := decodeJSONFile(path, &manifest); err != nil {
		return PluginManifest{}, err
	}
	if strings.TrimSpace(manifest.Runtime.Command) == "" {
		return PluginManifest{}, fmt.Errorf("external plugin manifest %s requires runtime.command", path)
	}
	actionTypes := make([]string, 0, len(manifest.ActionTypes))
	seen := map[string]struct{}{}
	for _, actionType := range manifest.ActionTypes {
		normalized := strings.ToLower(strings.TrimSpace(actionType))
		if normalized == "" {
			continue
		}
		if _, exists := seen[normalized]; exists {
			continue
		}
		seen[normalized] = struct{}{}
		actionTypes = append(actionTypes, normalized)
	}
	if len(actionTypes) == 0 {
		return PluginManifest{}, fmt.Errorf("external plugin manifest %s requires action_types", path)
	}
	manifest.ActionTypes = actionTypes
	return manifest, nil
}

func ResolveExternalPlugins(configPath string, refs []ExternalPluginRef) ([]ResolvedExternalPlugin, error) {
	if len(refs) == 0 {
		return nil, nil
	}
	configPath = strings.TrimSpace(configPath)
	if configPath == "" {
		configPath = DefaultConfigPath
	}
	baseDir := filepath.Dir(configPath)
	results := make([]ResolvedExternalPlugin, 0, len(refs))
	for index, ref := range refs {
		if !ref.Enabled {
			continue
		}
		manifestRawPath := strings.TrimSpace(ref.Manifest)
		if manifestRawPath == "" {
			return nil, fmt.Errorf("external_plugins[%d] missing manifest path", index)
		}
		manifestPath := manifestRawPath
		if !filepath.IsAbs(manifestPath) {
			manifestPath = filepath.Join(baseDir, manifestPath)
		}
		manifestPath = filepath.Clean(manifestPath)
		manifest, err := LoadManifest(manifestPath)
		if err != nil {
			return nil, err
		}
		pluginID := strings.TrimSpace(ref.ID)
		if pluginID == "" {
			pluginID = strings.TrimSpace(manifest.PluginKey)
		}
		if pluginID == "" {
			pluginID = strings.TrimSpace(manifest.Name)
		}
		if pluginID == "" {
			pluginID = filepath.Base(filepath.Dir(manifestPath))
		}
		results = append(results, ResolvedExternalPlugin{
			ID:           pluginID,
			ManifestPath: manifestPath,
			BaseDir:      filepath.Dir(manifestPath),
			Manifest:     manifest,
		})
	}
	return results, nil
}

func decodeJSONFile(path string, output any) error {
	content, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read json file %s: %w", path, err)
	}

	decoder := json.NewDecoder(bytes.NewReader(content))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(output); err != nil {
		return fmt.Errorf("decode json file %s: %w", path, err)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return fmt.Errorf("decode json file %s: trailing content", path)
	}
	return nil
}
