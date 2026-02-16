package gateway

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/dwizi/agent-runtime/internal/agent"
	"github.com/dwizi/agent-runtime/internal/agent/tools"
	"github.com/dwizi/agent-runtime/internal/llm"
	"github.com/dwizi/agent-runtime/internal/orchestrator"
	"github.com/dwizi/agent-runtime/internal/store"
)

// MockResponder allows simulating LLM responses for testing agent flows.
type MockResponder struct {
	ReplyFunc func(ctx context.Context, input llm.MessageInput) (string, error)
}

func (m *MockResponder) Reply(ctx context.Context, input llm.MessageInput) (string, error) {
	if m.ReplyFunc != nil {
		return m.ReplyFunc(ctx, input)
	}
	return "mock reply", nil
}

// MockStore just needs EnsureContext for this test
type ProactiveMockStore struct {
	Store
}

func (m *ProactiveMockStore) EnsureContextForExternalChannel(ctx context.Context, connector, externalID, displayName string) (store.ContextRecord, error) {
	return store.ContextRecord{
		ID:          "ctx-123",
		WorkspaceID: "ws-test",
	}, nil
}

func TestNarrateTaskResult_UsesAgent(t *testing.T) {
	mockStore := &ProactiveMockStore{}
	mockResponder := &MockResponder{
		ReplyFunc: func(ctx context.Context, input llm.MessageInput) (string, error) {
			// Verify prompt content
			if input.Text == "" {
				t.Error("expected non-empty prompt for narration")
			}
			// Simulate the Agent deciding to report the result
			return "I have completed the task successfully. The result is 42.", nil
		},
	}

	// Create service with mock dependencies
	svc := New(mockStore, nil, nil, nil, "/tmp/workspace", nil)
	svc.SetTriageAcknowledger(mockResponder) // This initializes the Agent

	// Setup task and result
	task := orchestrator.Task{
		ID:    "task-1",
		Title: "Calculate Meaning of Life",
	}
	result := orchestrator.TaskResult{
		Summary: "The answer is 42.",
	}

	ctx := context.Background()
	narration, err := svc.NarrateTaskResult(ctx, "discord", "chan-1", task, result)
	
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	expected := "I have completed the task successfully. The result is 42."
	if narration != expected {
		t.Errorf("expected narration '%s', got '%s'", expected, narration)
	}
}

func TestNarrateTaskResult_AgentToolUse(t *testing.T) {
	// Test that the agent can even use tools during narration phase (proactive follow-up)
	mockStore := &ProactiveMockStore{}
	
	// Create a registry with a mock tool
	registry := tools.NewRegistry()
	toolCalled := false
	registry.Register(&tools.MockTool{
		NameVal: "follow_up_tool",
		ExecFunc: func(ctx context.Context, args json.RawMessage) (string, error) {
			toolCalled = true
			return "Follow-up done.", nil
		},
	})

	svc := New(mockStore, nil, nil, nil, "/tmp/workspace", nil)
	svc.toolRegistry = registry // Manually inject registry for test
	
	// Mock responder handles the multi-turn loop
	turn := 0
	mockResponder := &MockResponder{
		ReplyFunc: func(ctx context.Context, input llm.MessageInput) (string, error) {
			turn++
			if turn == 1 {
				// First turn: decide to use a tool
				return `{"tool": "follow_up_tool", "args": {}}`, nil
			}
			// Second turn: final answer
			return "I finished the task and also ran a follow-up.", nil
		},
	}
	
	// Re-init agent with our registry and responder
	svc.agent = agent.New(nil, mockResponder, registry, "")

	task := orchestrator.Task{ID: "task-2", Title: "Complex Job"}
	result := orchestrator.TaskResult{Summary: "Part 1 done."}

	narration, err := svc.NarrateTaskResult(context.Background(), "discord", "chan-1", task, result)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !toolCalled {
		t.Error("expected agent to call 'follow_up_tool' during narration phase")
	}
	
	expected := "I finished the task and also ran a follow-up."
	if narration != expected {
		t.Errorf("expected '%s', got '%s'", expected, narration)
	}
}
