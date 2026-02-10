package agent

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/carlos/spinner/internal/agent/tools"
	"github.com/carlos/spinner/internal/llm"
)

type mockResponder struct {
	replyFunc func(input llm.MessageInput) (string, error)
}

func (m *mockResponder) Reply(ctx context.Context, input llm.MessageInput) (string, error) {
	return m.replyFunc(input)
}

type mockTool struct {
	name string
	exec func(json.RawMessage) (string, error)
}

func (m *mockTool) Name() string             { return m.name }
func (m *mockTool) Description() string      { return "mock" }
func (m *mockTool) ParametersSchema() string { return "{}" }
func (m *mockTool) Execute(ctx context.Context, input json.RawMessage) (string, error) {
	return m.exec(input)
}

func TestAgent_Execute_DirectReply(t *testing.T) {
	reg := tools.NewRegistry()
	responder := &mockResponder{
		replyFunc: func(input llm.MessageInput) (string, error) {
			return "Hello world", nil
		},
	}

	a := New(responder, reg, "")
	res := a.Execute(context.Background(), llm.MessageInput{Text: "hi"})

	if res.ActionTaken {
		t.Error("expected no action")
	}
	if res.Reply != "Hello world" {
		t.Errorf("expected 'Hello world', got '%s'", res.Reply)
	}
}

func TestAgent_Execute_ToolCall(t *testing.T) {
	reg := tools.NewRegistry()
	reg.Register(&mockTool{
		name: "test_tool",
		exec: func(input json.RawMessage) (string, error) {
			return "success", nil
		},
	})

	callCount := 0
	responder := &mockResponder{
		replyFunc: func(input llm.MessageInput) (string, error) {
			callCount++
			if callCount == 1 {
				return `{"tool": "test_tool", "args": {}}`, nil
			}
			return `{"final": "Done after tool call", "confidence": 0.91}`, nil
		},
	}

	a := New(responder, reg, "")
	res := a.Execute(context.Background(), llm.MessageInput{Text: "do it"})

	if !res.ActionTaken {
		t.Error("expected action taken")
	}
	if res.ToolName != "test_tool" {
		t.Errorf("expected tool 'test_tool', got '%s'", res.ToolName)
	}
	if res.ToolOutput != "success" {
		t.Errorf("expected output 'success', got '%s'", res.ToolOutput)
	}
	if !strings.Contains(strings.ToLower(res.Reply), "done after tool call") {
		t.Fatalf("expected final reply after tool reflection, got %q", res.Reply)
	}
	if res.Steps != 2 {
		t.Fatalf("expected 2 loop steps, got %d", res.Steps)
	}
}

func TestAgent_Execute_ToolCall_Markdown(t *testing.T) {
	reg := tools.NewRegistry()
	reg.Register(&mockTool{
		name: "test_tool",
		exec: func(input json.RawMessage) (string, error) {
			return "ok", nil
		},
	})

	callCount := 0
	responder := &mockResponder{
		replyFunc: func(input llm.MessageInput) (string, error) {
			callCount++
			if callCount == 1 {
				// Markdown wrapped JSON
				return `
` + "```" + `json
{"tool": "test_tool", "args": {}}
` + "```", nil
			}
			return "done", nil
		},
	}

	a := New(responder, reg, "")
	res := a.Execute(context.Background(), llm.MessageInput{Text: "do it"})

	if !res.ActionTaken {
		t.Error("expected action taken")
	}
	if res.ToolName != "test_tool" {
		t.Errorf("expected tool 'test_tool', got '%s'", res.ToolName)
	}
}

func TestAgent_Execute_BlocksDisallowedTool(t *testing.T) {
	reg := tools.NewRegistry()
	reg.Register(&mockTool{
		name: "test_tool",
		exec: func(input json.RawMessage) (string, error) {
			return "ok", nil
		},
	})
	responder := &mockResponder{
		replyFunc: func(input llm.MessageInput) (string, error) {
			return `{"tool": "test_tool", "args": {}}`, nil
		},
	}

	a := New(responder, reg, "")
	a.SetPolicyResolver(func(ctx context.Context, input llm.MessageInput) Policy {
		return Policy{AllowedTools: []string{"search_only"}}
	})

	res := a.Execute(context.Background(), llm.MessageInput{Text: "do it"})
	if !res.Blocked {
		t.Fatal("expected policy block for disallowed tool")
	}
	if !strings.Contains(strings.ToLower(res.BlockReason), "not allowed") {
		t.Fatalf("expected disallowed block reason, got %q", res.BlockReason)
	}
	if res.ActionTaken {
		t.Fatal("expected blocked run to skip action execution")
	}
}

func TestAgent_Execute_BlocksOversizedInput(t *testing.T) {
	called := false
	responder := &mockResponder{
		replyFunc: func(input llm.MessageInput) (string, error) {
			called = true
			return "should not run", nil
		},
	}

	a := New(responder, tools.NewRegistry(), "")
	a.SetDefaultPolicy(Policy{MaxInputChars: 4})

	res := a.Execute(context.Background(), llm.MessageInput{Text: "this is too long"})
	if !res.Blocked {
		t.Fatal("expected oversized input to be blocked")
	}
	if called {
		t.Fatal("expected llm responder to be skipped when input is oversized")
	}
}

func TestAgent_Execute_EnforcesTaskQuota(t *testing.T) {
	reg := tools.NewRegistry()
	reg.Register(&mockTool{
		name: "create_task",
		exec: func(input json.RawMessage) (string, error) {
			return "created", nil
		},
	})
	callCount := 0
	responder := &mockResponder{
		replyFunc: func(input llm.MessageInput) (string, error) {
			callCount++
			if callCount%2 == 1 {
				return `{"tool": "create_task", "args": {"title":"x"}}`, nil
			}
			return `{"final":"queued","confidence":0.8}`, nil
		},
	}

	a := New(responder, reg, "")
	a.SetDefaultPolicy(Policy{
		MaxLoopSteps:              2,
		MaxAutonomousTasksPerHour: 1,
		MaxAutonomousTasksPerDay:  10,
		MaxToolCallsPerTurn:       1,
		MinFinalConfidence:        0,
	})
	input := llm.MessageInput{
		Connector:   "discord",
		WorkspaceID: "ws-1",
		ContextID:   "ctx-1",
		Text:        "file a task",
	}

	first := a.Execute(context.Background(), input)
	if first.Blocked {
		t.Fatalf("expected first task creation to be allowed, got block: %s", first.BlockReason)
	}
	if !first.ActionTaken {
		t.Fatal("expected first run to execute tool")
	}

	second := a.Execute(context.Background(), input)
	if !second.Blocked {
		t.Fatal("expected second run to be blocked by hourly task quota")
	}
	if !strings.Contains(strings.ToLower(second.BlockReason), "per hour") {
		t.Fatalf("expected hourly quota reason, got %q", second.BlockReason)
	}
}

func TestAgent_Execute_BlocksLowConfidenceFinal(t *testing.T) {
	responder := &mockResponder{
		replyFunc: func(input llm.MessageInput) (string, error) {
			return `{"final":"I am not sure","confidence":0.2}`, nil
		},
	}

	a := New(responder, tools.NewRegistry(), "")
	a.SetDefaultPolicy(Policy{MinFinalConfidence: 0.35})

	res := a.Execute(context.Background(), llm.MessageInput{Text: "risky"})
	if !res.Blocked {
		t.Fatal("expected low-confidence answer to be blocked")
	}
	if !strings.Contains(strings.ToLower(res.BlockReason), "confidence") {
		t.Fatalf("expected confidence block reason, got %q", res.BlockReason)
	}
	if strings.TrimSpace(res.Reply) == "" {
		t.Fatal("expected escalation reply")
	}
}

func TestAgent_Execute_BlocksAfterMaxLoopSteps(t *testing.T) {
	reg := tools.NewRegistry()
	reg.Register(&mockTool{
		name: "test_tool",
		exec: func(input json.RawMessage) (string, error) {
			return "ok", nil
		},
	})
	responder := &mockResponder{
		replyFunc: func(input llm.MessageInput) (string, error) {
			return `{"tool":"test_tool","args":{}}`, nil
		},
	}

	a := New(responder, reg, "")
	a.SetDefaultPolicy(Policy{MaxLoopSteps: 2, MaxToolCallsPerTurn: 2, MinFinalConfidence: 0})

	res := a.Execute(context.Background(), llm.MessageInput{Text: "loop"})
	if !res.Blocked {
		t.Fatal("expected max-step loop to block")
	}
	if !strings.Contains(strings.ToLower(res.BlockReason), "max loop steps") {
		t.Fatalf("expected max loop steps block reason, got %q", res.BlockReason)
	}
}

func TestAgent_Execute_CapturesTrace(t *testing.T) {
	responder := &mockResponder{
		replyFunc: func(input llm.MessageInput) (string, error) {
			return "hello", nil
		},
	}

	a := New(responder, tools.NewRegistry(), "")
	res := a.Execute(context.Background(), llm.MessageInput{Text: "hi"})
	if len(res.Trace) == 0 {
		t.Fatal("expected trace events to be captured")
	}
}
