package gateway

import (
	"context"

	mcpclient "github.com/dwizi/agent-runtime/internal/mcp"
)

type MCPRuntime interface {
	Summary() mcpclient.Summary
	ListServerStatus() []mcpclient.ServerStatus
	CallTool(ctx context.Context, input mcpclient.CallToolInput) (mcpclient.ToolCallResult, error)
	ListResources(ctx context.Context, workspaceID, serverID string) ([]mcpclient.ResourceInfo, error)
	ListResourceTemplates(ctx context.Context, workspaceID, serverID string) ([]mcpclient.ResourceTemplateInfo, error)
	ReadResource(ctx context.Context, workspaceID, serverID, uri string) (mcpclient.ReadResourceResult, error)
	ListPrompts(ctx context.Context, workspaceID, serverID string) ([]mcpclient.PromptInfo, error)
	GetPrompt(ctx context.Context, workspaceID, serverID, name string, args map[string]string) (mcpclient.PromptResult, error)
}
