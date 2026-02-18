package gateway

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/dwizi/agent-runtime/internal/agent/tools"
)

type MCPListServersTool struct {
	runtimeProvider func() MCPRuntime
}

func NewMCPListServersTool(provider func() MCPRuntime) *MCPListServersTool {
	return &MCPListServersTool{runtimeProvider: provider}
}

func (t *MCPListServersTool) Name() string { return "mcp_list_servers" }
func (t *MCPListServersTool) Description() string {
	return "List configured MCP servers and their health state."
}
func (t *MCPListServersTool) ParametersSchema() string   { return `{}` }
func (t *MCPListServersTool) ToolClass() tools.ToolClass { return tools.ToolClassKnowledge }
func (t *MCPListServersTool) RequiresApproval() bool     { return false }
func (t *MCPListServersTool) ValidateArgs(rawArgs json.RawMessage) error {
	if len(rawArgs) == 0 {
		return nil
	}
	var payload map[string]any
	if err := strictDecodeArgs(rawArgs, &payload); err != nil {
		return err
	}
	if len(payload) != 0 {
		return fmt.Errorf("mcp_list_servers takes no arguments")
	}
	return nil
}

func (t *MCPListServersTool) Execute(ctx context.Context, rawArgs json.RawMessage) (string, error) {
	if err := t.ValidateArgs(rawArgs); err != nil {
		return "", err
	}
	runtime := t.runtimeProvider()
	if runtime == nil {
		return "MCP runtime is not configured.", nil
	}
	_ = ctx
	statuses := runtime.ListServerStatus()
	if len(statuses) == 0 {
		return "No MCP servers configured.", nil
	}
	summary := runtime.Summary()
	lines := []string{
		fmt.Sprintf("MCP servers: enabled=%d healthy=%d degraded=%d", summary.EnabledServers, summary.HealthyServers, summary.DegradedServers),
	}
	for _, item := range statuses {
		status := "degraded"
		if item.Healthy {
			status = "healthy"
		}
		if !item.Enabled {
			status = "disabled"
		}
		line := fmt.Sprintf("- %s: %s (tools=%d resources=%d templates=%d prompts=%d)", item.ID, status, item.ToolCount, item.ResourceCount, item.TemplateCount, item.PromptCount)
		if strings.TrimSpace(item.LastError) != "" {
			line += " error=" + strings.TrimSpace(item.LastError)
		}
		lines = append(lines, line)
	}
	return strings.Join(lines, "\n"), nil
}
