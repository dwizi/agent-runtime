package mcp

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestManagerBootstrapAndCallToolStreamable(t *testing.T) {
	server := newStreamableTestServer(t)
	t.Cleanup(server.Close)

	root := t.TempDir()
	configPath := filepath.Join(root, "servers.json")
	writeServerConfig(t, configPath, `{
  "schema_version":"v1",
  "servers":[
    {
      "id":"test",
      "enabled":true,
      "transport":{"type":"streamable_http","endpoint":"`+server.URL+`"}
    }
  ]
}`)

	manager, err := NewManager(ManagerConfig{ConfigPath: configPath, WorkspaceRoot: root}, nil)
	if err != nil {
		t.Fatalf("new manager: %v", err)
	}
	t.Cleanup(func() { _ = manager.Close() })

	updates := 0
	manager.SetToolUpdateHandler(func(update ToolUpdate) {
		if update.ServerID == "test" {
			updates++
		}
	})
	manager.Bootstrap(context.Background())
	if updates == 0 {
		t.Fatal("expected at least one tool update callback")
	}

	summary := manager.Summary()
	if summary.EnabledServers != 1 {
		t.Fatalf("expected 1 enabled server, got %d", summary.EnabledServers)
	}
	if summary.HealthyServers != 1 {
		t.Fatalf("expected 1 healthy server, got %d", summary.HealthyServers)
	}

	tools := manager.DiscoveredTools()
	if len(tools) == 0 {
		t.Fatal("expected discovered tools")
	}
	tool := tools[0]
	if tool.RegisteredName == "" || !strings.HasPrefix(tool.RegisteredName, "mcp_test__") {
		t.Fatalf("unexpected registered tool name: %s", tool.RegisteredName)
	}

	call, err := manager.CallTool(context.Background(), CallToolInput{
		WorkspaceID: "ws1",
		ServerID:    "test",
		ToolName:    "echo",
		Args:        json.RawMessage(`{"text":"hello"}`),
	})
	if err != nil {
		t.Fatalf("call tool: %v", err)
	}
	if !strings.Contains(call.Message, "hello") {
		t.Fatalf("unexpected call result: %s", call.Message)
	}

	resources, err := manager.ListResources(context.Background(), "ws1", "test")
	if err != nil {
		t.Fatalf("list resources: %v", err)
	}
	if len(resources) == 0 {
		t.Fatal("expected resources")
	}

	readResult, err := manager.ReadResource(context.Background(), "ws1", "test", "test://about")
	if err != nil {
		t.Fatalf("read resource: %v", err)
	}
	if !strings.Contains(readResult.Message, "about") {
		t.Fatalf("unexpected read result: %s", readResult.Message)
	}

	templates, err := manager.ListResourceTemplates(context.Background(), "ws1", "test")
	if err != nil {
		t.Fatalf("list templates: %v", err)
	}
	if len(templates) == 0 {
		t.Fatal("expected resource templates")
	}

	prompts, err := manager.ListPrompts(context.Background(), "ws1", "test")
	if err != nil {
		t.Fatalf("list prompts: %v", err)
	}
	if len(prompts) == 0 {
		t.Fatal("expected prompts")
	}

	prompt, err := manager.GetPrompt(context.Background(), "ws1", "test", "hello_prompt", map[string]string{"name": "carlos"})
	if err != nil {
		t.Fatalf("get prompt: %v", err)
	}
	if len(prompt.Messages) == 0 || !strings.Contains(prompt.Messages[0].Content, "carlos") {
		t.Fatalf("unexpected prompt output: %#v", prompt)
	}
}

func TestManagerWorkspaceOverrideDisable(t *testing.T) {
	server := newStreamableTestServer(t)
	t.Cleanup(server.Close)

	root := t.TempDir()
	configPath := filepath.Join(root, "servers.json")
	writeServerConfig(t, configPath, `{
  "schema_version":"v1",
  "servers":[
    {
      "id":"test",
      "enabled":true,
      "transport":{"type":"streamable_http","endpoint":"`+server.URL+`"}
    }
  ]
}`)

	overrideDir := filepath.Join(root, "ws1", "context", "mcp")
	if err := os.MkdirAll(overrideDir, 0o755); err != nil {
		t.Fatalf("mkdir override dir: %v", err)
	}
	writeServerConfig(t, filepath.Join(overrideDir, "servers.json"), `{
  "schema_version":"v1",
  "servers":[
    {"id":"test","enabled":false}
  ]
}`)

	manager, err := NewManager(ManagerConfig{ConfigPath: configPath, WorkspaceRoot: root, WorkspaceConfigRelPath: "context/mcp/servers.json"}, nil)
	if err != nil {
		t.Fatalf("new manager: %v", err)
	}
	t.Cleanup(func() { _ = manager.Close() })
	manager.Bootstrap(context.Background())

	_, err = manager.CallTool(context.Background(), CallToolInput{
		WorkspaceID: "ws1",
		ServerID:    "test",
		ToolName:    "echo",
		Args:        json.RawMessage(`{"text":"hello"}`),
	})
	if err == nil || !strings.Contains(strings.ToLower(err.Error()), "disabled") {
		t.Fatalf("expected disabled error, got %v", err)
	}
}

func TestManagerBootstrapFailureIsNonFatal(t *testing.T) {
	root := t.TempDir()
	configPath := filepath.Join(root, "servers.json")
	writeServerConfig(t, configPath, `{
  "schema_version":"v1",
  "servers":[
    {
      "id":"bad",
      "enabled":true,
      "transport":{"type":"streamable_http","endpoint":"http://127.0.0.1:1"}
    }
  ]
}`)

	manager, err := NewManager(ManagerConfig{ConfigPath: configPath, WorkspaceRoot: root}, nil)
	if err != nil {
		t.Fatalf("new manager: %v", err)
	}
	t.Cleanup(func() { _ = manager.Close() })
	manager.Bootstrap(context.Background())

	summary := manager.Summary()
	if summary.EnabledServers != 1 {
		t.Fatalf("expected 1 enabled server, got %d", summary.EnabledServers)
	}
	if summary.HealthyServers != 0 {
		t.Fatalf("expected 0 healthy servers, got %d", summary.HealthyServers)
	}
	if summary.DegradedServers != 1 {
		t.Fatalf("expected 1 degraded server, got %d", summary.DegradedServers)
	}
}

func TestManagerSSETransport(t *testing.T) {
	t.Skip("SSE transport handshake is flaky in local httptest; covered in live acceptance")
	server := newSSETestServer(t)
	t.Cleanup(server.Close)

	root := t.TempDir()
	configPath := filepath.Join(root, "servers.json")
	writeServerConfig(t, configPath, `{
  "schema_version":"v1",
  "servers":[
    {
      "id":"sse_server",
      "enabled":true,
      "transport":{"type":"sse","endpoint":"`+server.URL+`"}
    }
  ]
}`)

	manager, err := NewManager(ManagerConfig{ConfigPath: configPath, WorkspaceRoot: root}, nil)
	if err != nil {
		t.Fatalf("new manager: %v", err)
	}
	t.Cleanup(func() { _ = manager.Close() })
	manager.Bootstrap(context.Background())

	call, err := manager.CallTool(context.Background(), CallToolInput{
		WorkspaceID: "ws1",
		ServerID:    "sse_server",
		ToolName:    "echo",
		Args:        json.RawMessage(`{"text":"hello"}`),
	})
	if err != nil {
		t.Fatalf("call tool over sse: %v", err)
	}
	if !strings.Contains(call.Message, "hello") {
		t.Fatalf("unexpected call result: %s", call.Message)
	}
}

func TestManagerBootstrapIgnoresOptionalListMethodNotFound(t *testing.T) {
	server := newPartialStreamableServer(t, map[string]bool{
		"resources/list":           true,
		"resources/templates/list": true,
		"prompts/list":             true,
	})
	t.Cleanup(server.Close)

	root := t.TempDir()
	configPath := filepath.Join(root, "servers.json")
	writeServerConfig(t, configPath, `{
  "schema_version":"v1",
  "servers":[
    {
      "id":"partial",
      "enabled":true,
      "transport":{"type":"streamable_http","endpoint":"`+server.URL+`"}
    }
  ]
}`)

	manager, err := NewManager(ManagerConfig{ConfigPath: configPath, WorkspaceRoot: root}, nil)
	if err != nil {
		t.Fatalf("new manager: %v", err)
	}
	t.Cleanup(func() { _ = manager.Close() })
	manager.Bootstrap(context.Background())

	summary := manager.Summary()
	if summary.EnabledServers != 1 {
		t.Fatalf("expected 1 enabled server, got %d", summary.EnabledServers)
	}
	if summary.HealthyServers != 1 {
		t.Fatalf("expected 1 healthy server, got %d", summary.HealthyServers)
	}
	if summary.DegradedServers != 0 {
		t.Fatalf("expected 0 degraded servers, got %d", summary.DegradedServers)
	}

	tools := manager.DiscoveredTools()
	if len(tools) != 1 || tools[0].ToolName != "echo" {
		t.Fatalf("expected discovered echo tool, got %#v", tools)
	}

	resources, err := manager.ListResources(context.Background(), "ws1", "partial")
	if err != nil {
		t.Fatalf("list resources: %v", err)
	}
	if len(resources) != 0 {
		t.Fatalf("expected no resources, got %d", len(resources))
	}

	templates, err := manager.ListResourceTemplates(context.Background(), "ws1", "partial")
	if err != nil {
		t.Fatalf("list resource templates: %v", err)
	}
	if len(templates) != 0 {
		t.Fatalf("expected no resource templates, got %d", len(templates))
	}

	prompts, err := manager.ListPrompts(context.Background(), "ws1", "partial")
	if err != nil {
		t.Fatalf("list prompts: %v", err)
	}
	if len(prompts) != 0 {
		t.Fatalf("expected no prompts, got %d", len(prompts))
	}
}

func writeServerConfig(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func newStreamableTestServer(t *testing.T) *httptest.Server {
	t.Helper()
	server := newTestMCPServer(t)
	handler := sdkmcp.NewStreamableHTTPHandler(func(*http.Request) *sdkmcp.Server { return server }, nil)
	return httptest.NewServer(handler)
}

func newSSETestServer(t *testing.T) *httptest.Server {
	t.Helper()
	server := newTestMCPServer(t)
	handler := sdkmcp.NewSSEHandler(func(req *http.Request) *sdkmcp.Server {
		_ = req
		return server
	}, nil)
	return httptest.NewServer(handler)
}

func newPartialStreamableServer(t *testing.T, unsupportedMethods map[string]bool) *httptest.Server {
	t.Helper()
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}

		type request struct {
			JSONRPC string          `json:"jsonrpc"`
			ID      any             `json:"id,omitempty"`
			Method  string          `json:"method"`
			Params  json.RawMessage `json:"params,omitempty"`
		}
		type response struct {
			JSONRPC string `json:"jsonrpc"`
			ID      any    `json:"id,omitempty"`
			Result  any    `json:"result,omitempty"`
			Error   any    `json:"error,omitempty"`
		}

		body, err := io.ReadAll(r.Body)
		if err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		var req request
		if err := json.Unmarshal(body, &req); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		writeResponse := func(res response) {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(res)
		}

		if unsupportedMethods[strings.TrimSpace(req.Method)] {
			writeResponse(response{
				JSONRPC: "2.0",
				ID:      req.ID,
				Error: map[string]any{
					"code":    -32601,
					"message": "method not found",
				},
			})
			return
		}

		switch strings.TrimSpace(req.Method) {
		case "initialize":
			writeResponse(response{
				JSONRPC: "2.0",
				ID:      req.ID,
				Result: map[string]any{
					"protocolVersion": "2025-06-18",
					"serverInfo": map[string]any{
						"name":    "partial-server",
						"version": "1.0.0",
					},
					"capabilities": map[string]any{
						"tools":     map[string]any{},
						"resources": map[string]any{},
						"prompts":   map[string]any{},
					},
				},
			})
		case "notifications/initialized":
			w.WriteHeader(http.StatusAccepted)
		case "tools/list":
			writeResponse(response{
				JSONRPC: "2.0",
				ID:      req.ID,
				Result: map[string]any{
					"tools": []map[string]any{
						{
							"name":        "echo",
							"description": "Echo input",
							"inputSchema": map[string]any{
								"type": "object",
							},
						},
					},
				},
			})
		case "resources/list":
			writeResponse(response{
				JSONRPC: "2.0",
				ID:      req.ID,
				Result: map[string]any{
					"resources": []any{},
				},
			})
		case "resources/templates/list":
			writeResponse(response{
				JSONRPC: "2.0",
				ID:      req.ID,
				Result: map[string]any{
					"resourceTemplates": []any{},
				},
			})
		case "prompts/list":
			writeResponse(response{
				JSONRPC: "2.0",
				ID:      req.ID,
				Result: map[string]any{
					"prompts": []any{},
				},
			})
		default:
			writeResponse(response{
				JSONRPC: "2.0",
				ID:      req.ID,
				Error: map[string]any{
					"code":    -32601,
					"message": "method not found",
				},
			})
		}
	})
	return httptest.NewServer(handler)
}

func newTestMCPServer(t *testing.T) *sdkmcp.Server {
	t.Helper()
	server := sdkmcp.NewServer(&sdkmcp.Implementation{Name: "test-server", Version: "1.0.0"}, nil)
	server.AddTool(&sdkmcp.Tool{
		Name:        "echo",
		Description: "Echo input",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"text": map[string]any{"type": "string"},
			},
		},
	}, func(ctx context.Context, req *sdkmcp.CallToolRequest) (*sdkmcp.CallToolResult, error) {
		var args struct {
			Text string `json:"text"`
		}
		if req != nil && req.Params != nil && len(req.Params.Arguments) > 0 {
			if err := json.Unmarshal(req.Params.Arguments, &args); err != nil {
				return nil, err
			}
		}
		return &sdkmcp.CallToolResult{Content: []sdkmcp.Content{&sdkmcp.TextContent{Text: "echo: " + args.Text}}}, nil
	})
	server.AddResource(&sdkmcp.Resource{Name: "about", URI: "test://about", MIMEType: "text/plain", Description: "about"},
		func(ctx context.Context, req *sdkmcp.ReadResourceRequest) (*sdkmcp.ReadResourceResult, error) {
			return &sdkmcp.ReadResourceResult{Contents: []*sdkmcp.ResourceContents{{URI: "test://about", MIMEType: "text/plain", Text: "about resource"}}}, nil
		})
	server.AddResourceTemplate(&sdkmcp.ResourceTemplate{Name: "dynamic", URITemplate: "test://item/{id}", MIMEType: "text/plain"},
		func(ctx context.Context, req *sdkmcp.ReadResourceRequest) (*sdkmcp.ReadResourceResult, error) {
			uri := "test://item/unknown"
			if req != nil && req.Params != nil {
				uri = req.Params.URI
			}
			return &sdkmcp.ReadResourceResult{Contents: []*sdkmcp.ResourceContents{{URI: uri, MIMEType: "text/plain", Text: "template resource"}}}, nil
		})
	server.AddPrompt(&sdkmcp.Prompt{Name: "hello_prompt", Description: "says hello", Arguments: []*sdkmcp.PromptArgument{{Name: "name", Required: true}}},
		func(ctx context.Context, req *sdkmcp.GetPromptRequest) (*sdkmcp.GetPromptResult, error) {
			name := "friend"
			if req != nil && req.Params != nil {
				if value, ok := req.Params.Arguments["name"]; ok && strings.TrimSpace(value) != "" {
					name = value
				}
			}
			return &sdkmcp.GetPromptResult{Description: "hello prompt", Messages: []*sdkmcp.PromptMessage{{Role: "user", Content: &sdkmcp.TextContent{Text: "hello " + name}}}}, nil
		})
	return server
}
