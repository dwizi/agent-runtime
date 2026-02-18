package tools

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestRegistry_RegisterAndGet(t *testing.T) {
	reg := NewRegistry()
	mock := &MockTool{NameVal: "test_tool"}

	reg.Register(mock)

	retrieved, ok := reg.Get("test_tool")
	if !ok {
		t.Errorf("expected to retrieve tool, got nil")
	}
	if retrieved.Name() != "test_tool" {
		t.Errorf("expected name 'test_tool', got '%s'", retrieved.Name())
	}
}

func TestRegistry_List(t *testing.T) {
	reg := NewRegistry()
	reg.Register(&MockTool{NameVal: "b_tool"})
	reg.Register(&MockTool{NameVal: "a_tool"})

	list := reg.List()
	if len(list) != 2 {
		t.Errorf("expected 2 tools, got %d", len(list))
	}
	if list[0].Name() != "a_tool" {
		t.Errorf("expected sorted order, got %s first", list[0].Name())
	}
}

func TestRegistry_ExecuteTool(t *testing.T) {
	reg := NewRegistry()
	reg.Register(&MockTool{
		NameVal: "echo",
		ExecFunc: func(ctx context.Context, input json.RawMessage) (string, error) {
			return "echo: " + string(input), nil
		},
	})

	ctx := context.Background()
	result, err := reg.ExecuteTool(ctx, "echo", json.RawMessage(`"hello"`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != "echo: \"hello\"" {
		t.Errorf("unexpected result: %s", result)
	}

	_, err = reg.ExecuteTool(ctx, "missing_tool", nil)
	if err == nil {
		t.Error("expected error for missing tool, got nil")
	}
}

func TestRegistry_DescribeAll(t *testing.T) {
	reg := NewRegistry()
	reg.Register(&MockTool{
		NameVal:   "search",
		DescVal:   "searches docs",
		SchemaVal: `{"query": "string"}`,
	})

	desc := reg.DescribeAll()
	if !strings.Contains(desc, "search: searches docs") {
		t.Errorf("description missing tool details: %s", desc)
	}
	if !strings.Contains(desc, "Schema: {\"query\": \"string\"}") {
		t.Errorf("description missing schema: %s", desc)
	}
}

func TestRegistry_ReplaceNamespace(t *testing.T) {
	reg := NewRegistry()
	reg.Register(&MockTool{NameVal: "static_tool"})
	reg.ReplaceNamespace("mcp:test", []Tool{
		&MockTool{NameVal: "mcp_test__one"},
		&MockTool{NameVal: "mcp_test__two"},
	})

	list := reg.List()
	if len(list) != 3 {
		t.Fatalf("expected 3 tools, got %d", len(list))
	}

	reg.ReplaceNamespace("mcp:test", []Tool{
		&MockTool{NameVal: "mcp_test__three"},
	})
	if _, ok := reg.Get("mcp_test__one"); ok {
		t.Fatal("expected old namespaced tool to be removed")
	}
	if _, ok := reg.Get("mcp_test__two"); ok {
		t.Fatal("expected old namespaced tool to be removed")
	}
	if _, ok := reg.Get("mcp_test__three"); !ok {
		t.Fatal("expected replacement namespaced tool")
	}
	if _, ok := reg.Get("static_tool"); !ok {
		t.Fatal("expected static tool to remain")
	}
}

func TestRegistry_RemoveNamespace(t *testing.T) {
	reg := NewRegistry()
	reg.Register(&MockTool{NameVal: "static_tool"})
	reg.ReplaceNamespace("mcp:test", []Tool{
		&MockTool{NameVal: "mcp_test__one"},
	})
	reg.RemoveNamespace("mcp:test")
	if _, ok := reg.Get("mcp_test__one"); ok {
		t.Fatal("expected namespaced tool removed")
	}
	if _, ok := reg.Get("static_tool"); !ok {
		t.Fatal("expected static tool preserved")
	}
}
