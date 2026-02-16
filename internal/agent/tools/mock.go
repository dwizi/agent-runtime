package tools

import (
	"context"
	"encoding/json"
)

// MockTool is a helper for testing that implements the Tool interface.
// It is exported so other packages (like gateway tests) can use it.
type MockTool struct {
	NameVal     string
	DescVal     string
	SchemaVal   string
	ExecFunc    func(ctx context.Context, args json.RawMessage) (string, error)
}

func (m *MockTool) Name() string {
	if m.NameVal == "" {
		return "mock_tool"
	}
	return m.NameVal
}

func (m *MockTool) Description() string {
	return m.DescVal
}

func (m *MockTool) ParametersSchema() string {
	if m.SchemaVal == "" {
		return "{}"
	}
	return m.SchemaVal
}

func (m *MockTool) Execute(ctx context.Context, args json.RawMessage) (string, error) {
	if m.ExecFunc != nil {
		return m.ExecFunc(ctx, args)
	}
	return "mock result", nil
}
