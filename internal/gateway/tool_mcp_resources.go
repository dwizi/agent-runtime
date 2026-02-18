package gateway

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/dwizi/agent-runtime/internal/agent/tools"
	"github.com/dwizi/agent-runtime/internal/store"
)

type MCPListResourcesTool struct{ runtimeProvider func() MCPRuntime }

type MCPReadResourceTool struct{ runtimeProvider func() MCPRuntime }

type MCPListResourceTemplatesTool struct{ runtimeProvider func() MCPRuntime }

func NewMCPListResourcesTool(provider func() MCPRuntime) *MCPListResourcesTool {
	return &MCPListResourcesTool{runtimeProvider: provider}
}

func NewMCPReadResourceTool(provider func() MCPRuntime) *MCPReadResourceTool {
	return &MCPReadResourceTool{runtimeProvider: provider}
}

func NewMCPListResourceTemplatesTool(provider func() MCPRuntime) *MCPListResourceTemplatesTool {
	return &MCPListResourceTemplatesTool{runtimeProvider: provider}
}

func (t *MCPListResourcesTool) Name() string { return "mcp_list_resources" }
func (t *MCPListResourcesTool) Description() string {
	return "List resources available from a specific MCP server."
}
func (t *MCPListResourcesTool) ParametersSchema() string   { return `{"server_id":"string"}` }
func (t *MCPListResourcesTool) ToolClass() tools.ToolClass { return tools.ToolClassKnowledge }
func (t *MCPListResourcesTool) RequiresApproval() bool     { return false }

func (t *MCPListResourcesTool) ValidateArgs(rawArgs json.RawMessage) error {
	var args struct {
		ServerID string `json:"server_id"`
	}
	if err := strictDecodeArgs(rawArgs, &args); err != nil {
		return err
	}
	if strings.TrimSpace(args.ServerID) == "" {
		return fmt.Errorf("server_id is required")
	}
	return nil
}

func (t *MCPListResourcesTool) Execute(ctx context.Context, rawArgs json.RawMessage) (string, error) {
	var args struct {
		ServerID string `json:"server_id"`
	}
	if err := strictDecodeArgs(rawArgs, &args); err != nil {
		return "", err
	}
	runtime := t.runtimeProvider()
	if runtime == nil {
		return "MCP runtime is not configured.", nil
	}
	record, ok := ctx.Value(ContextKeyRecord).(store.ContextRecord)
	if !ok {
		return "", fmt.Errorf("internal error: context record missing from context")
	}
	items, err := runtime.ListResources(ctx, strings.TrimSpace(record.WorkspaceID), strings.TrimSpace(args.ServerID))
	if err != nil {
		return "", err
	}
	if len(items) == 0 {
		return "No resources returned by the MCP server.", nil
	}
	lines := make([]string, 0, len(items)+1)
	lines = append(lines, fmt.Sprintf("Resources on server `%s`:", strings.TrimSpace(args.ServerID)))
	for _, item := range items {
		display := item.Title
		if strings.TrimSpace(display) == "" {
			display = item.Name
		}
		lines = append(lines, fmt.Sprintf("- %s (%s)", strings.TrimSpace(display), strings.TrimSpace(item.URI)))
	}
	return strings.Join(lines, "\n"), nil
}

func (t *MCPReadResourceTool) Name() string { return "mcp_read_resource" }
func (t *MCPReadResourceTool) Description() string {
	return "Read one resource from an MCP server by URI."
}
func (t *MCPReadResourceTool) ParametersSchema() string {
	return `{"server_id":"string","uri":"string"}`
}
func (t *MCPReadResourceTool) ToolClass() tools.ToolClass { return tools.ToolClassKnowledge }
func (t *MCPReadResourceTool) RequiresApproval() bool     { return false }

func (t *MCPReadResourceTool) ValidateArgs(rawArgs json.RawMessage) error {
	var args struct {
		ServerID string `json:"server_id"`
		URI      string `json:"uri"`
	}
	if err := strictDecodeArgs(rawArgs, &args); err != nil {
		return err
	}
	if strings.TrimSpace(args.ServerID) == "" {
		return fmt.Errorf("server_id is required")
	}
	if strings.TrimSpace(args.URI) == "" {
		return fmt.Errorf("uri is required")
	}
	return nil
}

func (t *MCPReadResourceTool) Execute(ctx context.Context, rawArgs json.RawMessage) (string, error) {
	var args struct {
		ServerID string `json:"server_id"`
		URI      string `json:"uri"`
	}
	if err := strictDecodeArgs(rawArgs, &args); err != nil {
		return "", err
	}
	runtime := t.runtimeProvider()
	if runtime == nil {
		return "MCP runtime is not configured.", nil
	}
	record, ok := ctx.Value(ContextKeyRecord).(store.ContextRecord)
	if !ok {
		return "", fmt.Errorf("internal error: context record missing from context")
	}
	result, err := runtime.ReadResource(ctx, strings.TrimSpace(record.WorkspaceID), strings.TrimSpace(args.ServerID), strings.TrimSpace(args.URI))
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(result.Message), nil
}

func (t *MCPListResourceTemplatesTool) Name() string { return "mcp_list_resource_templates" }
func (t *MCPListResourceTemplatesTool) Description() string {
	return "List resource templates available from a specific MCP server."
}
func (t *MCPListResourceTemplatesTool) ParametersSchema() string   { return `{"server_id":"string"}` }
func (t *MCPListResourceTemplatesTool) ToolClass() tools.ToolClass { return tools.ToolClassKnowledge }
func (t *MCPListResourceTemplatesTool) RequiresApproval() bool     { return false }

func (t *MCPListResourceTemplatesTool) ValidateArgs(rawArgs json.RawMessage) error {
	var args struct {
		ServerID string `json:"server_id"`
	}
	if err := strictDecodeArgs(rawArgs, &args); err != nil {
		return err
	}
	if strings.TrimSpace(args.ServerID) == "" {
		return fmt.Errorf("server_id is required")
	}
	return nil
}

func (t *MCPListResourceTemplatesTool) Execute(ctx context.Context, rawArgs json.RawMessage) (string, error) {
	var args struct {
		ServerID string `json:"server_id"`
	}
	if err := strictDecodeArgs(rawArgs, &args); err != nil {
		return "", err
	}
	runtime := t.runtimeProvider()
	if runtime == nil {
		return "MCP runtime is not configured.", nil
	}
	record, ok := ctx.Value(ContextKeyRecord).(store.ContextRecord)
	if !ok {
		return "", fmt.Errorf("internal error: context record missing from context")
	}
	items, err := runtime.ListResourceTemplates(ctx, strings.TrimSpace(record.WorkspaceID), strings.TrimSpace(args.ServerID))
	if err != nil {
		return "", err
	}
	if len(items) == 0 {
		return "No resource templates returned by the MCP server.", nil
	}
	lines := make([]string, 0, len(items)+1)
	lines = append(lines, fmt.Sprintf("Resource templates on server `%s`:", strings.TrimSpace(args.ServerID)))
	for _, item := range items {
		display := item.Title
		if strings.TrimSpace(display) == "" {
			display = item.Name
		}
		lines = append(lines, fmt.Sprintf("- %s (%s)", strings.TrimSpace(display), strings.TrimSpace(item.URITemplate)))
	}
	return strings.Join(lines, "\n"), nil
}
