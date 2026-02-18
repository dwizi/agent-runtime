package gateway

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/dwizi/agent-runtime/internal/agent/tools"
	mcpclient "github.com/dwizi/agent-runtime/internal/mcp"
	"github.com/dwizi/agent-runtime/internal/store"
)

type MCPDynamicTool struct {
	runtimeProvider  func() MCPRuntime
	serverID         string
	toolName         string
	registeredName   string
	description      string
	schema           string
	toolClass        tools.ToolClass
	requiresApproval bool
}

func NewMCPDynamicTool(provider func() MCPRuntime, definition mcpclient.DiscoveredTool) *MCPDynamicTool {
	toolClass := tools.ToolClassGeneral
	switch strings.ToLower(strings.TrimSpace(definition.ToolClass)) {
	case string(tools.ToolClassKnowledge):
		toolClass = tools.ToolClassKnowledge
	case string(tools.ToolClassTasking):
		toolClass = tools.ToolClassTasking
	case string(tools.ToolClassModeration):
		toolClass = tools.ToolClassModeration
	case string(tools.ToolClassObjective):
		toolClass = tools.ToolClassObjective
	case string(tools.ToolClassDrafting):
		toolClass = tools.ToolClassDrafting
	case string(tools.ToolClassSensitive):
		toolClass = tools.ToolClassSensitive
	}
	schema := strings.TrimSpace(definition.InputSchemaJSON)
	if schema == "" {
		schema = `{}`
	}
	description := strings.TrimSpace(definition.Description)
	if description == "" {
		description = fmt.Sprintf("MCP tool %s on server %s", definition.ToolName, definition.ServerID)
	}
	return &MCPDynamicTool{
		runtimeProvider:  provider,
		serverID:         definition.ServerID,
		toolName:         definition.ToolName,
		registeredName:   definition.RegisteredName,
		description:      description,
		schema:           schema,
		toolClass:        toolClass,
		requiresApproval: definition.RequiresApproval,
	}
}

func (t *MCPDynamicTool) Name() string { return t.registeredName }

func (t *MCPDynamicTool) Description() string { return t.description }

func (t *MCPDynamicTool) ParametersSchema() string { return t.schema }

func (t *MCPDynamicTool) ToolClass() tools.ToolClass { return t.toolClass }

func (t *MCPDynamicTool) RequiresApproval() bool { return t.requiresApproval }

func (t *MCPDynamicTool) ValidateArgs(rawArgs json.RawMessage) error {
	if len(rawArgs) == 0 {
		return nil
	}
	var payload any
	if err := json.Unmarshal(rawArgs, &payload); err != nil {
		return fmt.Errorf("invalid mcp tool arguments: %w", err)
	}
	return nil
}

func (t *MCPDynamicTool) Execute(ctx context.Context, rawArgs json.RawMessage) (string, error) {
	runtime := t.runtimeProvider()
	if runtime == nil {
		return "", fmt.Errorf("mcp runtime is not configured")
	}
	record, ok := ctx.Value(ContextKeyRecord).(store.ContextRecord)
	if !ok {
		return "", fmt.Errorf("internal error: context record missing from context")
	}
	result, err := runtime.CallTool(ctx, mcpclient.CallToolInput{
		WorkspaceID: strings.TrimSpace(record.WorkspaceID),
		ServerID:    t.serverID,
		ToolName:    t.toolName,
		Args:        rawArgs,
	})
	if err != nil {
		return "", err
	}
	if result.IsError {
		return fmt.Sprintf("MCP tool `%s` returned an error: %s", t.Name(), strings.TrimSpace(result.Message)), nil
	}
	return strings.TrimSpace(result.Message), nil
}

func BuildMCPDynamicTools(provider func() MCPRuntime, definitions []mcpclient.DiscoveredTool) []tools.Tool {
	result := make([]tools.Tool, 0, len(definitions))
	for _, definition := range definitions {
		result = append(result, NewMCPDynamicTool(provider, definition))
	}
	return result
}
