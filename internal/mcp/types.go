package mcp

import (
	"encoding/json"
	"time"
)

const (
	DefaultConfigPath             = "ext/mcp/servers.json"
	DefaultWorkspaceConfigRelPath = "context/mcp/servers.json"
	DefaultRefreshSeconds         = 120
	DefaultHTTPTimeoutSeconds     = 30
)

const (
	TransportStreamableHTTP = "streamable_http"
	TransportSSE            = "sse"
)

type Catalog struct {
	SchemaVersion string         `json:"schema_version"`
	Servers       []ServerConfig `json:"servers"`
}

type ServerConfig struct {
	ID             string
	Enabled        bool
	Transport      TransportConfig
	HTTP           HTTPConfig
	RefreshSeconds int
	Policy         PolicyConfig
}

type TransportConfig struct {
	Type     string
	Endpoint string
}

type HTTPConfig struct {
	Headers        map[string]string
	TimeoutSeconds int
}

type PolicyConfig struct {
	DefaultToolClass        string
	DefaultRequiresApproval bool
	ToolOverrides           map[string]ToolPolicy
}

type ToolPolicy struct {
	ToolClass        string
	RequiresApproval bool
}

type WorkspaceCatalog struct {
	SchemaVersion string           `json:"schema_version"`
	Servers       []ServerOverride `json:"servers"`
}

type ServerOverride struct {
	ID             string           `json:"id"`
	Enabled        *bool            `json:"enabled,omitempty"`
	Transport      *TransportConfig `json:"transport,omitempty"`
	HTTP           *HTTPConfig      `json:"http,omitempty"`
	RefreshSeconds *int             `json:"refresh_seconds,omitempty"`
	Policy         *PolicyOverride  `json:"policy,omitempty"`
}

type PolicyOverride struct {
	DefaultToolClass        *string                 `json:"default_tool_class,omitempty"`
	DefaultRequiresApproval *bool                   `json:"default_requires_approval,omitempty"`
	ToolOverrides           map[string]ToolOverride `json:"tool_overrides,omitempty"`
}

type ToolOverride struct {
	ToolClass        *string `json:"tool_class,omitempty"`
	RequiresApproval *bool   `json:"requires_approval,omitempty"`
}

type DiscoveredTool struct {
	ServerID         string
	ToolName         string
	RegisteredName   string
	Description      string
	InputSchemaJSON  string
	ToolClass        string
	RequiresApproval bool
}

type ResourceInfo struct {
	ServerID    string
	Name        string
	Title       string
	URI         string
	Description string
	MIMEType    string
	Size        int64
	Annotations map[string]any
}

type ResourceTemplateInfo struct {
	ServerID    string
	Name        string
	Title       string
	URITemplate string
	Description string
	MIMEType    string
}

type PromptInfo struct {
	ServerID    string
	Name        string
	Title       string
	Description string
}

type PromptMessage struct {
	Role    string
	Content string
}

type PromptResult struct {
	Description string
	Messages    []PromptMessage
}

type ToolCallResult struct {
	Message string
	IsError bool
}

type ReadResourceResult struct {
	Message string
}

type ServerStatus struct {
	ID              string `json:"id"`
	Enabled         bool   `json:"enabled"`
	Healthy         bool   `json:"healthy"`
	LastError       string `json:"last_error,omitempty"`
	ToolCount       int    `json:"tool_count"`
	ResourceCount   int    `json:"resource_count"`
	TemplateCount   int    `json:"template_count"`
	PromptCount     int    `json:"prompt_count"`
	LastRefreshUnix int64  `json:"last_refresh_unix,omitempty"`
}

type Summary struct {
	EnabledServers  int   `json:"enabled_servers"`
	HealthyServers  int   `json:"healthy_servers"`
	DegradedServers int   `json:"degraded_servers"`
	LastRefreshUnix int64 `json:"last_refresh_unix,omitempty"`
}

type ToolUpdate struct {
	ServerID string
	Tools    []DiscoveredTool
}

type ToolUpdateHandler func(update ToolUpdate)

type CallToolInput struct {
	WorkspaceID string
	ServerID    string
	ToolName    string
	Args        json.RawMessage
}

type ManagerConfig struct {
	ConfigPath             string
	WorkspaceRoot          string
	WorkspaceConfigRelPath string
	DefaultRefreshSeconds  int
	DefaultHTTPTimeoutSec  int
}

type noopClock struct{}

func (noopClock) Now() time.Time { return time.Now().UTC() }
