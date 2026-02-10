package tools

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

// MockTool is a simple tool for testing purposes.
type MockTool struct {
	name        string
	description string
	schema      string
	executeFunc func(input json.RawMessage) (string, error)
}

func (m *MockTool) Name() string             { return m.name }
func (m *MockTool) Description() string      { return m.description }
func (m *MockTool) ParametersSchema() string { return m.schema }
func (m *MockTool) Execute(ctx context.Context, input json.RawMessage) (string, error) {
	if m.executeFunc != nil {
		return m.executeFunc(input)
	}
	return "mock executed", nil
}

func TestRegistry_RegisterAndGet(t *testing.T) {
	reg := NewRegistry()
	mock := &MockTool{name: "test_tool"}

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
	reg.Register(&MockTool{name: "b_tool"})
	reg.Register(&MockTool{name: "a_tool"})

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
		name: "echo",
		executeFunc: func(input json.RawMessage) (string, error) {
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
		name:        "search",
		description: "searches docs",
		schema:      `{"query": "string"}`,
	})
	
	desc := reg.DescribeAll()
	if !strings.Contains(desc, "search: searches docs") {
		t.Errorf("description missing tool details: %s", desc)
	}
	if !strings.Contains(desc, "Schema: {\"query\": \"string\"}") {
		t.Errorf("description missing schema: %s", desc)
	}
}
