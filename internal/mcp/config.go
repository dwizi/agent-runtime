package mcp

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	agenttools "github.com/dwizi/agent-runtime/internal/agent/tools"
)

var envTokenPattern = regexp.MustCompile(`\$\{([A-Za-z_][A-Za-z0-9_]*)\}`)

type rawCatalog struct {
	SchemaVersion string      `json:"schema_version"`
	Servers       []rawServer `json:"servers"`
}

type rawServer struct {
	ID             string       `json:"id"`
	Enabled        *bool        `json:"enabled,omitempty"`
	Transport      rawTransport `json:"transport"`
	HTTP           rawHTTP      `json:"http,omitempty"`
	RefreshSeconds *int         `json:"refresh_seconds,omitempty"`
	Policy         rawPolicy    `json:"policy,omitempty"`
}

type rawTransport struct {
	Type     string `json:"type"`
	Endpoint string `json:"endpoint"`
}

type rawHTTP struct {
	Headers        map[string]string `json:"headers,omitempty"`
	TimeoutSeconds *int              `json:"timeout_seconds,omitempty"`
}

type rawPolicy struct {
	DefaultToolClass        *string                  `json:"default_tool_class,omitempty"`
	DefaultRequiresApproval *bool                    `json:"default_requires_approval,omitempty"`
	ToolOverrides           map[string]rawToolPolicy `json:"tool_overrides,omitempty"`
}

type rawToolPolicy struct {
	ToolClass        *string `json:"tool_class,omitempty"`
	RequiresApproval *bool   `json:"requires_approval,omitempty"`
}

func LoadCatalog(path string, defaultRefreshSeconds, defaultHTTPTimeoutSeconds int) (Catalog, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		path = DefaultConfigPath
	}
	raw := rawCatalog{}
	if err := decodeJSONFile(path, &raw); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return Catalog{SchemaVersion: "v1", Servers: nil}, nil
		}
		return Catalog{}, err
	}
	if strings.TrimSpace(raw.SchemaVersion) == "" {
		raw.SchemaVersion = "v1"
	}
	if raw.SchemaVersion != "v1" {
		return Catalog{}, fmt.Errorf("unsupported mcp schema_version %q", raw.SchemaVersion)
	}
	servers := make([]ServerConfig, 0, len(raw.Servers))
	seen := map[string]struct{}{}
	for i, item := range raw.Servers {
		server, err := normalizeServer(item, defaultRefreshSeconds, defaultHTTPTimeoutSeconds)
		if err != nil {
			return Catalog{}, fmt.Errorf("servers[%d]: %w", i, err)
		}
		if _, exists := seen[server.ID]; exists {
			return Catalog{}, fmt.Errorf("duplicate server id %q", server.ID)
		}
		seen[server.ID] = struct{}{}
		servers = append(servers, server)
	}
	sort.Slice(servers, func(i, j int) bool { return servers[i].ID < servers[j].ID })
	return Catalog{SchemaVersion: raw.SchemaVersion, Servers: servers}, nil
}

func LoadWorkspaceCatalog(path string) (WorkspaceCatalog, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return WorkspaceCatalog{SchemaVersion: "v1"}, nil
	}
	raw := struct {
		SchemaVersion string           `json:"schema_version"`
		Servers       []ServerOverride `json:"servers"`
	}{}
	if err := decodeJSONFile(path, &raw); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return WorkspaceCatalog{SchemaVersion: "v1"}, nil
		}
		return WorkspaceCatalog{}, err
	}
	if strings.TrimSpace(raw.SchemaVersion) == "" {
		raw.SchemaVersion = "v1"
	}
	if raw.SchemaVersion != "v1" {
		return WorkspaceCatalog{}, fmt.Errorf("unsupported workspace mcp schema_version %q", raw.SchemaVersion)
	}
	result := WorkspaceCatalog{SchemaVersion: raw.SchemaVersion}
	seen := map[string]struct{}{}
	for i, item := range raw.Servers {
		id := sanitizeServerID(item.ID)
		if id == "" {
			return WorkspaceCatalog{}, fmt.Errorf("servers[%d]: id is required", i)
		}
		if _, exists := seen[id]; exists {
			return WorkspaceCatalog{}, fmt.Errorf("duplicate workspace server id %q", id)
		}
		seen[id] = struct{}{}
		item.ID = id
		if item.Transport != nil {
			transport := *item.Transport
			transport.Type = normalizeTransportType(transport.Type)
			transport.Endpoint = strings.TrimSpace(transport.Endpoint)
			if transport.Type != "" && transport.Type != TransportStreamableHTTP && transport.Type != TransportSSE {
				return WorkspaceCatalog{}, fmt.Errorf("servers[%d]: unsupported transport.type %q", i, transport.Type)
			}
			if transport.Endpoint != "" {
				expandedEndpoint, err := expandEnvStrict(transport.Endpoint)
				if err != nil {
					return WorkspaceCatalog{}, fmt.Errorf("servers[%d].transport.endpoint: %w", i, err)
				}
				transport.Endpoint = expandedEndpoint
			}
			item.Transport = &transport
		}
		if item.HTTP != nil {
			h := *item.HTTP
			if h.TimeoutSeconds < 0 {
				return WorkspaceCatalog{}, fmt.Errorf("servers[%d].http.timeout_seconds must be positive", i)
			}
			if len(h.Headers) > 0 {
				headers := map[string]string{}
				for key, value := range h.Headers {
					name := strings.TrimSpace(key)
					if name == "" {
						continue
					}
					expanded, err := expandEnvStrict(strings.TrimSpace(value))
					if err != nil {
						return WorkspaceCatalog{}, fmt.Errorf("servers[%d].http.headers[%q]: %w", i, key, err)
					}
					headers[name] = expanded
				}
				h.Headers = headers
			}
			item.HTTP = &h
		}
		if item.Policy != nil {
			policy, err := normalizeOverridePolicy(*item.Policy)
			if err != nil {
				return WorkspaceCatalog{}, fmt.Errorf("servers[%d].policy: %w", i, err)
			}
			item.Policy = &policy
		}
		if item.RefreshSeconds != nil && *item.RefreshSeconds < 1 {
			return WorkspaceCatalog{}, fmt.Errorf("servers[%d].refresh_seconds must be >= 1", i)
		}
		result.Servers = append(result.Servers, item)
	}
	return result, nil
}

func WorkspaceConfigPath(workspaceRoot, workspaceID, relPath string) string {
	workspaceRoot = strings.TrimSpace(workspaceRoot)
	workspaceID = strings.TrimSpace(workspaceID)
	relPath = strings.TrimSpace(relPath)
	if workspaceRoot == "" || workspaceID == "" || relPath == "" {
		return ""
	}
	if filepath.IsAbs(relPath) {
		return ""
	}
	return filepath.Clean(filepath.Join(workspaceRoot, workspaceID, relPath))
}

func normalizeServer(raw rawServer, defaultRefreshSeconds, defaultHTTPTimeoutSeconds int) (ServerConfig, error) {
	id := sanitizeServerID(raw.ID)
	if id == "" {
		return ServerConfig{}, fmt.Errorf("id is required")
	}
	transportType := normalizeTransportType(raw.Transport.Type)
	if transportType == "" {
		transportType = TransportStreamableHTTP
	}
	if transportType != TransportStreamableHTTP && transportType != TransportSSE {
		return ServerConfig{}, fmt.Errorf("unsupported transport.type %q", raw.Transport.Type)
	}
	endpoint := strings.TrimSpace(raw.Transport.Endpoint)
	if endpoint == "" {
		return ServerConfig{}, fmt.Errorf("transport.endpoint is required")
	}
	expandedEndpoint, err := expandEnvStrict(endpoint)
	if err != nil {
		return ServerConfig{}, fmt.Errorf("transport.endpoint: %w", err)
	}

	enabled := true
	if raw.Enabled != nil {
		enabled = *raw.Enabled
	}

	timeout := defaultHTTPTimeoutSeconds
	if timeout < 1 {
		timeout = DefaultHTTPTimeoutSeconds
	}
	if raw.HTTP.TimeoutSeconds != nil {
		if *raw.HTTP.TimeoutSeconds < 1 {
			return ServerConfig{}, fmt.Errorf("http.timeout_seconds must be >= 1")
		}
		timeout = *raw.HTTP.TimeoutSeconds
	}
	headers := map[string]string{}
	for key, value := range raw.HTTP.Headers {
		name := strings.TrimSpace(key)
		if name == "" {
			continue
		}
		expanded, err := expandEnvStrict(strings.TrimSpace(value))
		if err != nil {
			return ServerConfig{}, fmt.Errorf("http.headers[%q]: %w", key, err)
		}
		headers[name] = expanded
	}

	refresh := defaultRefreshSeconds
	if refresh < 1 {
		refresh = DefaultRefreshSeconds
	}
	if raw.RefreshSeconds != nil {
		if *raw.RefreshSeconds < 1 {
			return ServerConfig{}, fmt.Errorf("refresh_seconds must be >= 1")
		}
		refresh = *raw.RefreshSeconds
	}

	policy, err := normalizePolicy(raw.Policy)
	if err != nil {
		return ServerConfig{}, fmt.Errorf("policy: %w", err)
	}

	return ServerConfig{
		ID:      id,
		Enabled: enabled,
		Transport: TransportConfig{
			Type:     transportType,
			Endpoint: expandedEndpoint,
		},
		HTTP: HTTPConfig{
			Headers:        headers,
			TimeoutSeconds: timeout,
		},
		RefreshSeconds: refresh,
		Policy:         policy,
	}, nil
}

func normalizePolicy(raw rawPolicy) (PolicyConfig, error) {
	class := string(agenttools.ToolClassGeneral)
	if raw.DefaultToolClass != nil {
		value, err := normalizeToolClass(*raw.DefaultToolClass)
		if err != nil {
			return PolicyConfig{}, err
		}
		class = value
	}
	requiresApproval := false
	if raw.DefaultRequiresApproval != nil {
		requiresApproval = *raw.DefaultRequiresApproval
	}
	overrides := map[string]ToolPolicy{}
	for rawName, override := range raw.ToolOverrides {
		name := strings.TrimSpace(rawName)
		if name == "" {
			continue
		}
		toolClass := class
		if override.ToolClass != nil {
			value, err := normalizeToolClass(*override.ToolClass)
			if err != nil {
				return PolicyConfig{}, fmt.Errorf("tool_overrides[%q]: %w", rawName, err)
			}
			toolClass = value
		}
		req := requiresApproval
		if override.RequiresApproval != nil {
			req = *override.RequiresApproval
		}
		overrides[name] = ToolPolicy{ToolClass: toolClass, RequiresApproval: req}
	}
	return PolicyConfig{
		DefaultToolClass:        class,
		DefaultRequiresApproval: requiresApproval,
		ToolOverrides:           overrides,
	}, nil
}

func normalizeOverridePolicy(raw PolicyOverride) (PolicyOverride, error) {
	if raw.DefaultToolClass != nil {
		value, err := normalizeToolClass(*raw.DefaultToolClass)
		if err != nil {
			return PolicyOverride{}, err
		}
		raw.DefaultToolClass = &value
	}
	if len(raw.ToolOverrides) > 0 {
		normalized := map[string]ToolOverride{}
		for rawName, override := range raw.ToolOverrides {
			name := strings.TrimSpace(rawName)
			if name == "" {
				continue
			}
			if override.ToolClass != nil {
				value, err := normalizeToolClass(*override.ToolClass)
				if err != nil {
					return PolicyOverride{}, fmt.Errorf("tool_overrides[%q]: %w", rawName, err)
				}
				override.ToolClass = &value
			}
			normalized[name] = override
		}
		raw.ToolOverrides = normalized
	}
	return raw, nil
}

func normalizeToolClass(raw string) (string, error) {
	value := strings.ToLower(strings.TrimSpace(raw))
	switch value {
	case string(agenttools.ToolClassGeneral),
		string(agenttools.ToolClassKnowledge),
		string(agenttools.ToolClassTasking),
		string(agenttools.ToolClassModeration),
		string(agenttools.ToolClassObjective),
		string(agenttools.ToolClassDrafting),
		string(agenttools.ToolClassSensitive):
		return value, nil
	default:
		return "", fmt.Errorf("unsupported tool class %q", raw)
	}
}

func sanitizeServerID(raw string) string {
	value := strings.ToLower(strings.TrimSpace(raw))
	if value == "" {
		return ""
	}
	builder := strings.Builder{}
	for _, r := range value {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '_' || r == '-' {
			builder.WriteRune(r)
			continue
		}
		builder.WriteRune('_')
	}
	result := strings.Trim(builder.String(), "_")
	if result == "" {
		return ""
	}
	return result
}

func normalizeTransportType(raw string) string {
	value := strings.ToLower(strings.TrimSpace(raw))
	return value
}

func expandEnvStrict(value string) (string, error) {
	missing := []string{}
	matches := envTokenPattern.FindAllStringSubmatch(value, -1)
	for _, match := range matches {
		if len(match) < 2 {
			continue
		}
		name := strings.TrimSpace(match[1])
		if name == "" {
			continue
		}
		if _, ok := os.LookupEnv(name); !ok {
			missing = append(missing, name)
		}
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		return "", fmt.Errorf("missing environment variables: %s", strings.Join(missing, ", "))
	}
	return os.ExpandEnv(value), nil
}

func decodeJSONFile(path string, out any) error {
	content, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read mcp config %s: %w", path, err)
	}
	decoder := json.NewDecoder(bytes.NewReader(content))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(out); err != nil {
		return fmt.Errorf("decode mcp config %s: %w", path, err)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return fmt.Errorf("decode mcp config %s: trailing content", path)
	}
	return nil
}
