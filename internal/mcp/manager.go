package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"sync"
	"time"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"
)

type serverState struct {
	cfg         ServerConfig
	healthy     bool
	lastError   string
	lastRefresh time.Time
	nextRefresh time.Time

	tools             []DiscoveredTool
	resources         []ResourceInfo
	resourceTemplates []ResourceTemplateInfo
	prompts           []PromptInfo
}

type Manager struct {
	logger                 *slog.Logger
	workspaceRoot          string
	workspaceConfigRelPath string
	defaultRefresh         int
	defaultHTTPTimeout     int

	mu            sync.RWMutex
	servers       map[string]*serverState
	toolIndex     map[string]DiscoveredTool
	sessions      map[string]*sdkmcp.ClientSession
	updateHandler ToolUpdateHandler
	closed        bool

	warnedUnknownOverrides map[string]struct{}
}

func NewManager(cfg ManagerConfig, logger *slog.Logger) (*Manager, error) {
	if logger == nil {
		logger = slog.Default()
	}
	refreshSeconds := cfg.DefaultRefreshSeconds
	if refreshSeconds < 1 {
		refreshSeconds = DefaultRefreshSeconds
	}
	httpTimeout := cfg.DefaultHTTPTimeoutSec
	if httpTimeout < 1 {
		httpTimeout = DefaultHTTPTimeoutSeconds
	}
	configPath := strings.TrimSpace(cfg.ConfigPath)
	if configPath == "" {
		configPath = DefaultConfigPath
	}
	catalog, err := LoadCatalog(configPath, refreshSeconds, httpTimeout)
	if err != nil {
		return nil, err
	}
	manager := &Manager{
		logger:                 logger.With("component", "mcp"),
		workspaceRoot:          strings.TrimSpace(cfg.WorkspaceRoot),
		workspaceConfigRelPath: strings.TrimSpace(cfg.WorkspaceConfigRelPath),
		defaultRefresh:         refreshSeconds,
		defaultHTTPTimeout:     httpTimeout,
		servers:                map[string]*serverState{},
		toolIndex:              map[string]DiscoveredTool{},
		sessions:               map[string]*sdkmcp.ClientSession{},
		warnedUnknownOverrides: map[string]struct{}{},
	}
	if manager.workspaceConfigRelPath == "" {
		manager.workspaceConfigRelPath = DefaultWorkspaceConfigRelPath
	}
	now := time.Now().UTC()
	for _, server := range catalog.Servers {
		manager.servers[server.ID] = &serverState{
			cfg:         server,
			nextRefresh: now,
		}
	}
	return manager, nil
}

func (m *Manager) SetToolUpdateHandler(handler ToolUpdateHandler) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.updateHandler = handler
}

func (m *Manager) Bootstrap(ctx context.Context) {
	for _, id := range m.serverIDs() {
		_ = m.refreshServer(ctx, id)
	}
}

func (m *Manager) Start(ctx context.Context) error {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			for _, id := range m.serverIDs() {
				if m.shouldRefresh(id) {
					_ = m.refreshServer(ctx, id)
				}
			}
		}
	}
}

func (m *Manager) Close() error {
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return nil
	}
	m.closed = true
	sessions := make([]*sdkmcp.ClientSession, 0, len(m.sessions))
	for _, session := range m.sessions {
		sessions = append(sessions, session)
	}
	m.sessions = map[string]*sdkmcp.ClientSession{}
	m.mu.Unlock()

	for _, session := range sessions {
		_ = session.Close()
	}
	return nil
}

func (m *Manager) serverIDs() []string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	ids := make([]string, 0, len(m.servers))
	for id := range m.servers {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

func (m *Manager) shouldRefresh(serverID string) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	state, ok := m.servers[serverID]
	if !ok {
		return false
	}
	if !state.cfg.Enabled {
		return false
	}
	return !state.nextRefresh.After(time.Now().UTC())
}

func (m *Manager) refreshServer(ctx context.Context, serverID string) error {
	m.mu.RLock()
	state, ok := m.servers[serverID]
	if !ok {
		m.mu.RUnlock()
		return fmt.Errorf("mcp server %s not configured", serverID)
	}
	cfg := state.cfg
	m.mu.RUnlock()

	if !cfg.Enabled {
		m.updateServerFailure(serverID, "server disabled")
		return nil
	}
	session, err := m.ensureSession(ctx, cfg)
	if err != nil {
		m.updateServerFailure(serverID, err.Error())
		m.logger.Warn("mcp discovery failed", "server_id", serverID, "error", err)
		return err
	}
	tools, resources, templates, prompts, discoverWarnings, discoverErr := discoverCapabilities(ctx, cfg, session)
	if discoverErr != nil {
		m.updateServerFailure(serverID, discoverErr.Error())
		m.logger.Warn("mcp discovery failed", "server_id", serverID, "error", discoverErr)
		return discoverErr
	}
	m.updateServerSuccess(serverID, tools, resources, templates, prompts)
	for _, warning := range discoverWarnings {
		m.logger.Warn("mcp optional capability unavailable", "server_id", serverID, "warning", warning)
	}
	m.logger.Info("mcp discovery succeeded",
		"server_id", serverID,
		"tools", len(tools),
		"resources", len(resources),
		"resource_templates", len(templates),
		"prompts", len(prompts),
	)
	return nil
}

func (m *Manager) updateServerFailure(serverID, message string) {
	now := time.Now().UTC()
	m.mu.Lock()
	defer m.mu.Unlock()
	state := m.servers[serverID]
	if state == nil {
		return
	}
	state.healthy = false
	state.lastError = strings.TrimSpace(message)
	state.lastRefresh = now
	state.nextRefresh = now.Add(time.Duration(state.cfg.RefreshSeconds) * time.Second)
}

func (m *Manager) updateServerSuccess(serverID string, tools []DiscoveredTool, resources []ResourceInfo, templates []ResourceTemplateInfo, prompts []PromptInfo) {
	now := time.Now().UTC()
	var update ToolUpdate
	m.mu.Lock()
	state := m.servers[serverID]
	if state != nil {
		state.healthy = true
		state.lastError = ""
		state.lastRefresh = now
		state.nextRefresh = now.Add(time.Duration(state.cfg.RefreshSeconds) * time.Second)
		state.tools = tools
		state.resources = resources
		state.resourceTemplates = templates
		state.prompts = prompts
	}
	m.rebuildToolIndexLocked()
	update = ToolUpdate{ServerID: serverID, Tools: append([]DiscoveredTool{}, tools...)}
	handler := m.updateHandler
	m.mu.Unlock()

	if handler != nil {
		handler(update)
	}
}

func (m *Manager) rebuildToolIndexLocked() {
	next := map[string]DiscoveredTool{}
	for _, state := range m.servers {
		for _, tool := range state.tools {
			next[tool.RegisteredName] = tool
		}
	}
	m.toolIndex = next
}

func (m *Manager) ListServerStatus() []ServerStatus {
	m.mu.RLock()
	defer m.mu.RUnlock()
	ids := make([]string, 0, len(m.servers))
	for id := range m.servers {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	result := make([]ServerStatus, 0, len(ids))
	for _, id := range ids {
		state := m.servers[id]
		if state == nil {
			continue
		}
		result = append(result, ServerStatus{
			ID:              id,
			Enabled:         state.cfg.Enabled,
			Healthy:         state.healthy,
			LastError:       state.lastError,
			ToolCount:       len(state.tools),
			ResourceCount:   len(state.resources),
			TemplateCount:   len(state.resourceTemplates),
			PromptCount:     len(state.prompts),
			LastRefreshUnix: state.lastRefresh.Unix(),
		})
	}
	return result
}

func (m *Manager) Summary() Summary {
	statuses := m.ListServerStatus()
	summary := Summary{}
	var lastRefresh int64
	for _, item := range statuses {
		if item.Enabled {
			summary.EnabledServers++
			if item.Healthy {
				summary.HealthyServers++
			}
		}
		if item.LastRefreshUnix > lastRefresh {
			lastRefresh = item.LastRefreshUnix
		}
	}
	summary.DegradedServers = summary.EnabledServers - summary.HealthyServers
	summary.LastRefreshUnix = lastRefresh
	return summary
}

func (m *Manager) DiscoveredTools() []DiscoveredTool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	list := make([]DiscoveredTool, 0, len(m.toolIndex))
	for _, tool := range m.toolIndex {
		list = append(list, tool)
	}
	sort.Slice(list, func(i, j int) bool {
		if list[i].ServerID == list[j].ServerID {
			return list[i].RegisteredName < list[j].RegisteredName
		}
		return list[i].ServerID < list[j].ServerID
	})
	return list
}

func (m *Manager) ResolveToolByRegisteredName(name string) (DiscoveredTool, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	tool, ok := m.toolIndex[strings.TrimSpace(name)]
	return tool, ok
}

func (m *Manager) CallTool(ctx context.Context, input CallToolInput) (ToolCallResult, error) {
	cfg, err := m.effectiveServerConfig(input.WorkspaceID, input.ServerID)
	if err != nil {
		return ToolCallResult{}, err
	}
	session, err := m.ensureSession(ctx, cfg)
	if err != nil {
		return ToolCallResult{}, err
	}
	var args any
	if len(input.Args) > 0 {
		if err := json.Unmarshal(input.Args, &args); err != nil {
			return ToolCallResult{}, fmt.Errorf("invalid mcp arguments: %w", err)
		}
	}
	result, err := session.CallTool(ctx, &sdkmcp.CallToolParams{Name: input.ToolName, Arguments: args})
	if err != nil {
		return ToolCallResult{}, err
	}
	message := flattenCallResult(result)
	return ToolCallResult{Message: message, IsError: result.IsError}, nil
}

func (m *Manager) ListResources(ctx context.Context, workspaceID, serverID string) ([]ResourceInfo, error) {
	cfg, err := m.effectiveServerConfig(workspaceID, serverID)
	if err != nil {
		return nil, err
	}
	session, err := m.ensureSession(ctx, cfg)
	if err != nil {
		return nil, err
	}
	result := []ResourceInfo{}
	for item, iterErr := range session.Resources(ctx, nil) {
		if iterErr != nil {
			if isMethodNotFoundError(iterErr) {
				return result, nil
			}
			return nil, iterErr
		}
		if item == nil {
			continue
		}
		result = append(result, ResourceInfo{
			ServerID:    serverID,
			Name:        item.Name,
			Title:       item.Title,
			URI:         item.URI,
			Description: item.Description,
			MIMEType:    item.MIMEType,
			Size:        item.Size,
		})
	}
	return result, nil
}

func (m *Manager) ListResourceTemplates(ctx context.Context, workspaceID, serverID string) ([]ResourceTemplateInfo, error) {
	cfg, err := m.effectiveServerConfig(workspaceID, serverID)
	if err != nil {
		return nil, err
	}
	session, err := m.ensureSession(ctx, cfg)
	if err != nil {
		return nil, err
	}
	result := []ResourceTemplateInfo{}
	for item, iterErr := range session.ResourceTemplates(ctx, nil) {
		if iterErr != nil {
			if isMethodNotFoundError(iterErr) {
				return result, nil
			}
			return nil, iterErr
		}
		if item == nil {
			continue
		}
		result = append(result, ResourceTemplateInfo{
			ServerID:    serverID,
			Name:        item.Name,
			Title:       item.Title,
			URITemplate: item.URITemplate,
			Description: item.Description,
			MIMEType:    item.MIMEType,
		})
	}
	return result, nil
}

func (m *Manager) ReadResource(ctx context.Context, workspaceID, serverID, uri string) (ReadResourceResult, error) {
	cfg, err := m.effectiveServerConfig(workspaceID, serverID)
	if err != nil {
		return ReadResourceResult{}, err
	}
	session, err := m.ensureSession(ctx, cfg)
	if err != nil {
		return ReadResourceResult{}, err
	}
	result, err := session.ReadResource(ctx, &sdkmcp.ReadResourceParams{URI: strings.TrimSpace(uri)})
	if err != nil {
		return ReadResourceResult{}, err
	}
	return ReadResourceResult{Message: flattenResourceResult(result)}, nil
}

func (m *Manager) ListPrompts(ctx context.Context, workspaceID, serverID string) ([]PromptInfo, error) {
	cfg, err := m.effectiveServerConfig(workspaceID, serverID)
	if err != nil {
		return nil, err
	}
	session, err := m.ensureSession(ctx, cfg)
	if err != nil {
		return nil, err
	}
	result := []PromptInfo{}
	for item, iterErr := range session.Prompts(ctx, nil) {
		if iterErr != nil {
			if isMethodNotFoundError(iterErr) {
				return result, nil
			}
			return nil, iterErr
		}
		if item == nil {
			continue
		}
		result = append(result, PromptInfo{
			ServerID:    serverID,
			Name:        item.Name,
			Title:       item.Title,
			Description: item.Description,
		})
	}
	return result, nil
}

func (m *Manager) GetPrompt(ctx context.Context, workspaceID, serverID, name string, args map[string]string) (PromptResult, error) {
	cfg, err := m.effectiveServerConfig(workspaceID, serverID)
	if err != nil {
		return PromptResult{}, err
	}
	session, err := m.ensureSession(ctx, cfg)
	if err != nil {
		return PromptResult{}, err
	}
	result, err := session.GetPrompt(ctx, &sdkmcp.GetPromptParams{Name: strings.TrimSpace(name), Arguments: args})
	if err != nil {
		return PromptResult{}, err
	}
	messages := make([]PromptMessage, 0, len(result.Messages))
	for _, message := range result.Messages {
		if message == nil {
			continue
		}
		messages = append(messages, PromptMessage{
			Role:    strings.TrimSpace(string(message.Role)),
			Content: flattenPromptMessage(message),
		})
	}
	return PromptResult{Description: strings.TrimSpace(result.Description), Messages: messages}, nil
}

func (m *Manager) effectiveServerConfig(workspaceID, serverID string) (ServerConfig, error) {
	serverID = strings.ToLower(strings.TrimSpace(serverID))
	if serverID == "" {
		return ServerConfig{}, fmt.Errorf("server_id is required")
	}
	m.mu.RLock()
	state, ok := m.servers[serverID]
	m.mu.RUnlock()
	if !ok || state == nil {
		return ServerConfig{}, fmt.Errorf("mcp server %q is not configured", serverID)
	}
	cfg := state.cfg
	if strings.TrimSpace(workspaceID) == "" {
		if !cfg.Enabled {
			return ServerConfig{}, fmt.Errorf("mcp server %q is disabled", serverID)
		}
		return cfg, nil
	}
	path := WorkspaceConfigPath(m.workspaceRoot, workspaceID, m.workspaceConfigRelPath)
	if path == "" {
		if !cfg.Enabled {
			return ServerConfig{}, fmt.Errorf("mcp server %q is disabled", serverID)
		}
		return cfg, nil
	}
	overrides, err := LoadWorkspaceCatalog(path)
	if err != nil {
		return ServerConfig{}, err
	}
	m.warnUnknownOverrides(workspaceID, overrides)
	if override, ok := FindOverride(overrides, serverID); ok {
		cfg = MergeServer(cfg, override)
	}
	if !cfg.Enabled {
		return ServerConfig{}, fmt.Errorf("mcp server %q is disabled for workspace %s", serverID, workspaceID)
	}
	return cfg, nil
}

func (m *Manager) warnUnknownOverrides(workspaceID string, catalog WorkspaceCatalog) {
	if len(catalog.Servers) == 0 {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, item := range catalog.Servers {
		id := strings.ToLower(strings.TrimSpace(item.ID))
		if id == "" {
			continue
		}
		if _, exists := m.servers[id]; exists {
			continue
		}
		key := strings.TrimSpace(workspaceID) + "|" + id
		if _, warned := m.warnedUnknownOverrides[key]; warned {
			continue
		}
		m.warnedUnknownOverrides[key] = struct{}{}
		m.logger.Warn("ignoring unknown workspace mcp override", "workspace_id", workspaceID, "server_id", id)
	}
}

func (m *Manager) ensureSession(ctx context.Context, cfg ServerConfig) (*sdkmcp.ClientSession, error) {
	key := sessionKey(cfg)
	m.mu.RLock()
	session, exists := m.sessions[key]
	m.mu.RUnlock()
	if exists && session != nil {
		return session, nil
	}
	connectCtx, cancel := context.WithTimeout(ctx, time.Duration(cfg.HTTP.TimeoutSeconds)*time.Second)
	defer cancel()
	created, err := connectSession(connectCtx, cfg)
	if err != nil {
		return nil, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if current, ok := m.sessions[key]; ok && current != nil {
		_ = created.Close()
		return current, nil
	}
	m.sessions[key] = created
	return created, nil
}

func sessionKey(cfg ServerConfig) string {
	headers := make([]string, 0, len(cfg.HTTP.Headers))
	for key, value := range cfg.HTTP.Headers {
		headers = append(headers, strings.ToLower(strings.TrimSpace(key))+"="+strings.TrimSpace(value))
	}
	sort.Strings(headers)
	return strings.Join([]string{
		cfg.ID,
		cfg.Transport.Type,
		cfg.Transport.Endpoint,
		fmt.Sprintf("timeout=%d", cfg.HTTP.TimeoutSeconds),
		strings.Join(headers, "|"),
	}, "::")
}

func discoverCapabilities(ctx context.Context, cfg ServerConfig, session *sdkmcp.ClientSession) ([]DiscoveredTool, []ResourceInfo, []ResourceTemplateInfo, []PromptInfo, []string, error) {
	warnings := []string{}
	rawTools := []*sdkmcp.Tool{}
	for item, iterErr := range session.Tools(ctx, nil) {
		if iterErr != nil {
			return nil, nil, nil, nil, nil, iterErr
		}
		if item == nil {
			continue
		}
		rawTools = append(rawTools, item)
	}
	toolNames := make([]string, 0, len(rawTools))
	for _, item := range rawTools {
		toolNames = append(toolNames, item.Name)
	}
	nameMap := EnsureUniqueRegisteredNames(cfg.ID, toolNames)
	tools := make([]DiscoveredTool, 0, len(rawTools))
	for _, item := range rawTools {
		toolClass := cfg.Policy.DefaultToolClass
		requiresApproval := cfg.Policy.DefaultRequiresApproval
		if override, exists := cfg.Policy.ToolOverrides[item.Name]; exists {
			if strings.TrimSpace(override.ToolClass) != "" {
				toolClass = override.ToolClass
			}
			requiresApproval = override.RequiresApproval
		}
		schema := "{}"
		if item.InputSchema != nil {
			schemaBytes, err := json.Marshal(item.InputSchema)
			if err == nil {
				schema = string(schemaBytes)
			}
		}
		tools = append(tools, DiscoveredTool{
			ServerID:         cfg.ID,
			ToolName:         item.Name,
			RegisteredName:   nameMap[item.Name],
			Description:      strings.TrimSpace(item.Description),
			InputSchemaJSON:  schema,
			ToolClass:        toolClass,
			RequiresApproval: requiresApproval,
		})
	}
	resources := []ResourceInfo{}
	for item, iterErr := range session.Resources(ctx, nil) {
		if iterErr != nil {
			if isMethodNotFoundError(iterErr) {
				warnings = append(warnings, fmt.Sprintf("resources/list unavailable: %v", iterErr))
				break
			}
			return nil, nil, nil, nil, nil, iterErr
		}
		if item == nil {
			continue
		}
		resources = append(resources, ResourceInfo{
			ServerID:    cfg.ID,
			Name:        item.Name,
			Title:       item.Title,
			URI:         item.URI,
			Description: item.Description,
			MIMEType:    item.MIMEType,
			Size:        item.Size,
		})
	}
	templates := []ResourceTemplateInfo{}
	for item, iterErr := range session.ResourceTemplates(ctx, nil) {
		if iterErr != nil {
			if isMethodNotFoundError(iterErr) {
				warnings = append(warnings, fmt.Sprintf("resources/templates/list unavailable: %v", iterErr))
				break
			}
			return nil, nil, nil, nil, nil, iterErr
		}
		if item == nil {
			continue
		}
		templates = append(templates, ResourceTemplateInfo{
			ServerID:    cfg.ID,
			Name:        item.Name,
			Title:       item.Title,
			URITemplate: item.URITemplate,
			Description: item.Description,
			MIMEType:    item.MIMEType,
		})
	}
	prompts := []PromptInfo{}
	for item, iterErr := range session.Prompts(ctx, nil) {
		if iterErr != nil {
			if isMethodNotFoundError(iterErr) {
				warnings = append(warnings, fmt.Sprintf("prompts/list unavailable: %v", iterErr))
				break
			}
			return nil, nil, nil, nil, nil, iterErr
		}
		if item == nil {
			continue
		}
		prompts = append(prompts, PromptInfo{
			ServerID:    cfg.ID,
			Name:        item.Name,
			Title:       item.Title,
			Description: item.Description,
		})
	}
	return tools, resources, templates, prompts, warnings, nil
}

func isMethodNotFoundError(err error) bool {
	if err == nil {
		return false
	}
	return strings.Contains(strings.ToLower(strings.TrimSpace(err.Error())), "method not found")
}

func flattenCallResult(result *sdkmcp.CallToolResult) string {
	if result == nil {
		return "(empty result)"
	}
	parts := []string{}
	for _, content := range result.Content {
		parts = append(parts, flattenContent(content))
	}
	if len(parts) == 0 && result.StructuredContent != nil {
		if raw, err := json.Marshal(result.StructuredContent); err == nil {
			parts = append(parts, string(raw))
		}
	}
	text := strings.TrimSpace(strings.Join(parts, "\n"))
	if text == "" {
		text = "(empty result)"
	}
	return truncateText(text, 8000)
}

func flattenResourceResult(result *sdkmcp.ReadResourceResult) string {
	if result == nil || len(result.Contents) == 0 {
		return "(empty resource)"
	}
	parts := []string{}
	for _, item := range result.Contents {
		if item == nil {
			continue
		}
		if strings.TrimSpace(item.Text) != "" {
			parts = append(parts, item.Text)
			continue
		}
		if len(item.Blob) > 0 {
			parts = append(parts, fmt.Sprintf("[blob uri=%s mime=%s bytes=%d]", item.URI, item.MIMEType, len(item.Blob)))
			continue
		}
		parts = append(parts, fmt.Sprintf("[resource uri=%s mime=%s]", item.URI, item.MIMEType))
	}
	text := strings.TrimSpace(strings.Join(parts, "\n"))
	if text == "" {
		text = "(empty resource)"
	}
	return truncateText(text, 8000)
}

func flattenPromptMessage(message *sdkmcp.PromptMessage) string {
	if message == nil {
		return ""
	}
	return truncateText(flattenContent(message.Content), 4000)
}

func flattenContent(content sdkmcp.Content) string {
	switch value := content.(type) {
	case *sdkmcp.TextContent:
		return strings.TrimSpace(value.Text)
	case *sdkmcp.ImageContent:
		return fmt.Sprintf("[image mime=%s bytes=%d]", value.MIMEType, len(value.Data))
	case *sdkmcp.AudioContent:
		return fmt.Sprintf("[audio mime=%s bytes=%d]", value.MIMEType, len(value.Data))
	case *sdkmcp.ResourceLink:
		return fmt.Sprintf("[resource_link %s %s]", strings.TrimSpace(value.Name), strings.TrimSpace(value.URI))
	case *sdkmcp.EmbeddedResource:
		if value.Resource == nil {
			return "[embedded_resource]"
		}
		if strings.TrimSpace(value.Resource.Text) != "" {
			return value.Resource.Text
		}
		if len(value.Resource.Blob) > 0 {
			return fmt.Sprintf("[embedded_blob uri=%s mime=%s bytes=%d]", value.Resource.URI, value.Resource.MIMEType, len(value.Resource.Blob))
		}
		return fmt.Sprintf("[embedded_resource uri=%s mime=%s]", value.Resource.URI, value.Resource.MIMEType)
	default:
		if raw, err := json.Marshal(content); err == nil {
			return string(raw)
		}
		return "[content]"
	}
}

func truncateText(value string, max int) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return value
	}
	if max < 1 || len(value) <= max {
		return value
	}
	return value[:max] + "..."
}
