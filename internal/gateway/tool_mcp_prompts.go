package gateway

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/dwizi/agent-runtime/internal/agent/tools"
	"github.com/dwizi/agent-runtime/internal/store"
)

type MCPListPromptsTool struct{ runtimeProvider func() MCPRuntime }

type MCPGetPromptTool struct{ runtimeProvider func() MCPRuntime }

func NewMCPListPromptsTool(provider func() MCPRuntime) *MCPListPromptsTool {
	return &MCPListPromptsTool{runtimeProvider: provider}
}

func NewMCPGetPromptTool(provider func() MCPRuntime) *MCPGetPromptTool {
	return &MCPGetPromptTool{runtimeProvider: provider}
}

func (t *MCPListPromptsTool) Name() string { return "mcp_list_prompts" }
func (t *MCPListPromptsTool) Description() string {
	return "List prompts exposed by a specific MCP server."
}
func (t *MCPListPromptsTool) ParametersSchema() string   { return `{"server_id":"string"}` }
func (t *MCPListPromptsTool) ToolClass() tools.ToolClass { return tools.ToolClassKnowledge }
func (t *MCPListPromptsTool) RequiresApproval() bool     { return false }
func (t *MCPListPromptsTool) ValidateArgs(rawArgs json.RawMessage) error {
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

func (t *MCPListPromptsTool) Execute(ctx context.Context, rawArgs json.RawMessage) (string, error) {
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
	items, err := runtime.ListPrompts(ctx, strings.TrimSpace(record.WorkspaceID), strings.TrimSpace(args.ServerID))
	if err != nil {
		return "", err
	}
	if len(items) == 0 {
		return "No prompts returned by the MCP server.", nil
	}
	lines := make([]string, 0, len(items)+1)
	lines = append(lines, fmt.Sprintf("Prompts on server `%s`:", strings.TrimSpace(args.ServerID)))
	for _, item := range items {
		display := item.Title
		if strings.TrimSpace(display) == "" {
			display = item.Name
		}
		line := "- " + strings.TrimSpace(display)
		if strings.TrimSpace(item.Description) != "" {
			line += ": " + strings.TrimSpace(item.Description)
		}
		lines = append(lines, line)
	}
	return strings.Join(lines, "\n"), nil
}

func (t *MCPGetPromptTool) Name() string { return "mcp_get_prompt" }
func (t *MCPGetPromptTool) Description() string {
	return "Resolve one prompt from an MCP server with optional prompt arguments."
}
func (t *MCPGetPromptTool) ParametersSchema() string {
	return `{"server_id":"string","name":"string","arguments":{"key":"value"}}`
}
func (t *MCPGetPromptTool) ToolClass() tools.ToolClass { return tools.ToolClassKnowledge }
func (t *MCPGetPromptTool) RequiresApproval() bool     { return false }
func (t *MCPGetPromptTool) ValidateArgs(rawArgs json.RawMessage) error {
	var args struct {
		ServerID  string            `json:"server_id"`
		Name      string            `json:"name"`
		Arguments map[string]string `json:"arguments"`
	}
	if err := strictDecodeArgs(rawArgs, &args); err != nil {
		return err
	}
	if strings.TrimSpace(args.ServerID) == "" {
		return fmt.Errorf("server_id is required")
	}
	if strings.TrimSpace(args.Name) == "" {
		return fmt.Errorf("name is required")
	}
	return nil
}

func (t *MCPGetPromptTool) Execute(ctx context.Context, rawArgs json.RawMessage) (string, error) {
	var args struct {
		ServerID  string            `json:"server_id"`
		Name      string            `json:"name"`
		Arguments map[string]string `json:"arguments"`
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
	result, err := runtime.GetPrompt(ctx, strings.TrimSpace(record.WorkspaceID), strings.TrimSpace(args.ServerID), strings.TrimSpace(args.Name), args.Arguments)
	if err != nil {
		return "", err
	}
	lines := []string{}
	if strings.TrimSpace(result.Description) != "" {
		lines = append(lines, result.Description)
	}
	for _, message := range result.Messages {
		role := strings.TrimSpace(message.Role)
		if role == "" {
			role = "message"
		}
		lines = append(lines, fmt.Sprintf("[%s] %s", role, strings.TrimSpace(message.Content)))
	}
	if len(lines) == 0 {
		return "Prompt returned no messages.", nil
	}
	return strings.Join(lines, "\n"), nil
}
