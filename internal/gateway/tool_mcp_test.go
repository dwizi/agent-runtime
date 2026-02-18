package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	mcpclient "github.com/dwizi/agent-runtime/internal/mcp"
	"github.com/dwizi/agent-runtime/internal/store"
)

type fakeMCPRuntime struct {
	statuses   []mcpclient.ServerStatus
	summary    mcpclient.Summary
	callResult mcpclient.ToolCallResult
	callErr    error

	resources []mcpclient.ResourceInfo
	read      mcpclient.ReadResourceResult
	prompts   []mcpclient.PromptInfo
	prompt    mcpclient.PromptResult
	templates []mcpclient.ResourceTemplateInfo

	lastCall mcpclient.CallToolInput
}

func (f *fakeMCPRuntime) Summary() mcpclient.Summary                 { return f.summary }
func (f *fakeMCPRuntime) ListServerStatus() []mcpclient.ServerStatus { return f.statuses }
func (f *fakeMCPRuntime) CallTool(ctx context.Context, input mcpclient.CallToolInput) (mcpclient.ToolCallResult, error) {
	_ = ctx
	f.lastCall = input
	if f.callErr != nil {
		return mcpclient.ToolCallResult{}, f.callErr
	}
	return f.callResult, nil
}
func (f *fakeMCPRuntime) ListResources(ctx context.Context, workspaceID, serverID string) ([]mcpclient.ResourceInfo, error) {
	_ = ctx
	if strings.TrimSpace(workspaceID) == "" || strings.TrimSpace(serverID) == "" {
		return nil, errors.New("missing workspace/server")
	}
	return f.resources, nil
}
func (f *fakeMCPRuntime) ListResourceTemplates(ctx context.Context, workspaceID, serverID string) ([]mcpclient.ResourceTemplateInfo, error) {
	_ = ctx
	if strings.TrimSpace(workspaceID) == "" || strings.TrimSpace(serverID) == "" {
		return nil, errors.New("missing workspace/server")
	}
	return f.templates, nil
}
func (f *fakeMCPRuntime) ReadResource(ctx context.Context, workspaceID, serverID, uri string) (mcpclient.ReadResourceResult, error) {
	_ = ctx
	if strings.TrimSpace(workspaceID) == "" || strings.TrimSpace(serverID) == "" || strings.TrimSpace(uri) == "" {
		return mcpclient.ReadResourceResult{}, errors.New("missing input")
	}
	return f.read, nil
}
func (f *fakeMCPRuntime) ListPrompts(ctx context.Context, workspaceID, serverID string) ([]mcpclient.PromptInfo, error) {
	_ = ctx
	if strings.TrimSpace(workspaceID) == "" || strings.TrimSpace(serverID) == "" {
		return nil, errors.New("missing workspace/server")
	}
	return f.prompts, nil
}
func (f *fakeMCPRuntime) GetPrompt(ctx context.Context, workspaceID, serverID, name string, args map[string]string) (mcpclient.PromptResult, error) {
	_ = ctx
	_ = args
	if strings.TrimSpace(workspaceID) == "" || strings.TrimSpace(serverID) == "" || strings.TrimSpace(name) == "" {
		return mcpclient.PromptResult{}, errors.New("missing input")
	}
	return f.prompt, nil
}

func TestMCPDynamicToolExecute(t *testing.T) {
	runtime := &fakeMCPRuntime{callResult: mcpclient.ToolCallResult{Message: "ok", IsError: false}}
	tool := NewMCPDynamicTool(func() MCPRuntime { return runtime }, mcpclient.DiscoveredTool{
		ServerID:        "github",
		ToolName:        "search",
		RegisteredName:  "mcp_github__search",
		Description:     "search docs",
		InputSchemaJSON: `{"type":"object"}`,
		ToolClass:       "knowledge",
	})

	ctx := context.WithValue(context.Background(), ContextKeyRecord, store.ContextRecord{WorkspaceID: "ws-1"})
	output, err := tool.Execute(ctx, json.RawMessage(`{"q":"hello"}`))
	if err != nil {
		t.Fatalf("execute tool: %v", err)
	}
	if output != "ok" {
		t.Fatalf("unexpected output: %s", output)
	}
	if runtime.lastCall.WorkspaceID != "ws-1" {
		t.Fatalf("expected workspace ws-1, got %s", runtime.lastCall.WorkspaceID)
	}
	if runtime.lastCall.ServerID != "github" || runtime.lastCall.ToolName != "search" {
		t.Fatalf("unexpected call target: %#v", runtime.lastCall)
	}
}

func TestMCPListServersTool(t *testing.T) {
	runtime := &fakeMCPRuntime{
		summary:  mcpclient.Summary{EnabledServers: 1, HealthyServers: 1, DegradedServers: 0},
		statuses: []mcpclient.ServerStatus{{ID: "github", Enabled: true, Healthy: true, ToolCount: 1}},
	}
	tool := NewMCPListServersTool(func() MCPRuntime { return runtime })
	output, err := tool.Execute(context.Background(), json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !strings.Contains(output, "github") {
		t.Fatalf("expected server output, got %s", output)
	}
}

func TestMCPResourceAndPromptTools(t *testing.T) {
	runtime := &fakeMCPRuntime{
		resources: []mcpclient.ResourceInfo{{Name: "about", URI: "test://about"}},
		templates: []mcpclient.ResourceTemplateInfo{{Name: "tmpl", URITemplate: "test://{id}"}},
		read:      mcpclient.ReadResourceResult{Message: "resource body"},
		prompts:   []mcpclient.PromptInfo{{Name: "hello"}},
		prompt:    mcpclient.PromptResult{Description: "prompt desc", Messages: []mcpclient.PromptMessage{{Role: "user", Content: "hello"}}},
	}
	ctx := context.WithValue(context.Background(), ContextKeyRecord, store.ContextRecord{WorkspaceID: "ws-1"})

	listResources := NewMCPListResourcesTool(func() MCPRuntime { return runtime })
	resourcesOutput, err := listResources.Execute(ctx, json.RawMessage(`{"server_id":"github"}`))
	if err != nil {
		t.Fatalf("list resources: %v", err)
	}
	if !strings.Contains(resourcesOutput, "about") {
		t.Fatalf("unexpected resources output: %s", resourcesOutput)
	}

	readResource := NewMCPReadResourceTool(func() MCPRuntime { return runtime })
	readOutput, err := readResource.Execute(ctx, json.RawMessage(`{"server_id":"github","uri":"test://about"}`))
	if err != nil {
		t.Fatalf("read resource: %v", err)
	}
	if !strings.Contains(readOutput, "resource body") {
		t.Fatalf("unexpected read output: %s", readOutput)
	}

	listTemplates := NewMCPListResourceTemplatesTool(func() MCPRuntime { return runtime })
	templatesOutput, err := listTemplates.Execute(ctx, json.RawMessage(`{"server_id":"github"}`))
	if err != nil {
		t.Fatalf("list templates: %v", err)
	}
	if !strings.Contains(templatesOutput, "tmpl") {
		t.Fatalf("unexpected template output: %s", templatesOutput)
	}

	listPrompts := NewMCPListPromptsTool(func() MCPRuntime { return runtime })
	promptsOutput, err := listPrompts.Execute(ctx, json.RawMessage(`{"server_id":"github"}`))
	if err != nil {
		t.Fatalf("list prompts: %v", err)
	}
	if !strings.Contains(promptsOutput, "hello") {
		t.Fatalf("unexpected prompts output: %s", promptsOutput)
	}

	getPrompt := NewMCPGetPromptTool(func() MCPRuntime { return runtime })
	promptOutput, err := getPrompt.Execute(ctx, json.RawMessage(`{"server_id":"github","name":"hello","arguments":{"name":"carlos"}}`))
	if err != nil {
		t.Fatalf("get prompt: %v", err)
	}
	if !strings.Contains(promptOutput, "prompt desc") || !strings.Contains(promptOutput, "hello") {
		t.Fatalf("unexpected prompt output: %s", promptOutput)
	}
}
