package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/dwizi/agent-runtime/internal/agent/tools"
	"github.com/dwizi/agent-runtime/internal/llm"
)

type mockResponder struct {
	replyFunc func(input llm.MessageInput) (string, error)
}

func (m *mockResponder) Reply(ctx context.Context, input llm.MessageInput) (string, error) {
	return m.replyFunc(input)
}

type mockTool struct {
	name             string
	toolClass        tools.ToolClass
	requiresApproval bool
	exec             func(json.RawMessage) (string, error)
}

func (m *mockTool) Name() string             { return m.name }
func (m *mockTool) Description() string      { return "mock" }
func (m *mockTool) ParametersSchema() string { return "{}" }
func (m *mockTool) ToolClass() tools.ToolClass {
	if m.toolClass == "" {
		return tools.ToolClassGeneral
	}
	return m.toolClass
}
func (m *mockTool) RequiresApproval() bool { return m.requiresApproval }
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

	a := New(nil, responder, reg, "")
	res := a.Execute(context.Background(), llm.MessageInput{Text: "hi"})

	if res.ActionTaken {
		t.Error("expected no action")
	}
	if res.Reply != "Hello world" {
		t.Errorf("expected 'Hello world', got '%s'", res.Reply)
	}
}

func TestAgent_Execute_UsesGroundingOnFirstStepByDefault(t *testing.T) {
	reg := tools.NewRegistry()
	calls := 0
	responder := &mockResponder{
		replyFunc: func(input llm.MessageInput) (string, error) {
			calls++
			if calls != 1 {
				t.Fatalf("expected exactly one call, got %d", calls)
			}
			if input.SkipGrounding {
				t.Fatal("expected first-step agent call to allow grounding")
			}
			return "ok", nil
		},
	}

	a := New(nil, responder, reg, "")
	res := a.Execute(context.Background(), llm.MessageInput{Text: "how do we do this?", WorkspaceID: "ws-1"})
	if res.Error != nil {
		t.Fatalf("unexpected error: %v", res.Error)
	}
}

func TestAgent_Execute_GroundingCanBeForcedEveryStep(t *testing.T) {
	reg := tools.NewRegistry()
	reg.Register(&mockTool{
		name: "test_tool",
		exec: func(input json.RawMessage) (string, error) {
			return "done", nil
		},
	})
	calls := 0
	responder := &mockResponder{
		replyFunc: func(input llm.MessageInput) (string, error) {
			calls++
			if input.SkipGrounding {
				t.Fatalf("expected grounding enabled for call %d", calls)
			}
			if calls == 1 {
				return `{"tool":"test_tool","args":{}}`, nil
			}
			return `{"final":"ok","confidence":0.9}`, nil
		},
	}

	a := New(nil, responder, reg, "")
	a.SetGroundingPolicy(true, true)
	res := a.Execute(context.Background(), llm.MessageInput{Text: "use tools", WorkspaceID: "ws-1"})
	if res.Error != nil {
		t.Fatalf("unexpected error: %v", res.Error)
	}
	if calls != 2 {
		t.Fatalf("expected 2 calls, got %d", calls)
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

	a := New(nil, responder, reg, "")
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
	if len(res.ToolCalls) != 1 {
		t.Fatalf("expected exactly one tool call record, got %d", len(res.ToolCalls))
	}
	if res.ToolCalls[0].Status != "succeeded" {
		t.Fatalf("expected tool call status succeeded, got %q", res.ToolCalls[0].Status)
	}
	if !strings.Contains(res.ToolCalls[0].ToolArgs, "{}") {
		t.Fatalf("expected tool args in call record, got %q", res.ToolCalls[0].ToolArgs)
	}
	if !strings.Contains(strings.ToLower(res.ToolCalls[0].ToolOutput), "success") {
		t.Fatalf("expected tool output in call record, got %q", res.ToolCalls[0].ToolOutput)
	}
}

func TestAgent_Execute_ContinuesAfterToolFailure(t *testing.T) {
	reg := tools.NewRegistry()
	reg.Register(&mockTool{
		name: "fail_tool",
		exec: func(input json.RawMessage) (string, error) {
			return "", fmt.Errorf("boom")
		},
	})
	reg.Register(&mockTool{
		name: "ok_tool",
		exec: func(input json.RawMessage) (string, error) {
			return "recovered", nil
		},
	})

	callCount := 0
	secondCallInput := ""
	responder := &mockResponder{
		replyFunc: func(input llm.MessageInput) (string, error) {
			callCount++
			switch callCount {
			case 1:
				return `{"tool":"fail_tool","args":{}}`, nil
			case 2:
				secondCallInput = input.Text
				return `{"tool":"ok_tool","args":{}}`, nil
			default:
				return `{"final":"Recovered after retry","confidence":0.9}`, nil
			}
		},
	}

	a := New(nil, responder, reg, "")
	res := a.Execute(context.Background(), llm.MessageInput{Text: "solve it"})

	if res.Error != nil {
		t.Fatalf("expected agent to recover from tool failure, got %v", res.Error)
	}
	if res.Blocked {
		t.Fatalf("expected run to stay unblocked, got %s", res.BlockReason)
	}
	if !strings.Contains(strings.ToLower(res.Reply), "recovered") {
		t.Fatalf("expected recovery final reply, got %q", res.Reply)
	}
	if len(res.ToolCalls) != 2 {
		t.Fatalf("expected 2 tool calls, got %d", len(res.ToolCalls))
	}
	if res.ToolCalls[0].Status != "failed" {
		t.Fatalf("expected first tool call to fail, got %s", res.ToolCalls[0].Status)
	}
	if res.ToolCalls[1].Status != "succeeded" {
		t.Fatalf("expected second tool call to succeed, got %s", res.ToolCalls[1].Status)
	}
	if !strings.Contains(secondCallInput, "error=boom") {
		t.Fatalf("expected failure to be reflected in next loop input, got %q", secondCallInput)
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

	a := New(nil, responder, reg, "")
	res := a.Execute(context.Background(), llm.MessageInput{Text: "do it"})

	if !res.ActionTaken {
		t.Error("expected action taken")
	}
	if res.ToolName != "test_tool" {
		t.Errorf("expected tool 'test_tool', got '%s'", res.ToolName)
	}
}

func TestAgent_Execute_ActionBlockPayloadExecutesRunAction(t *testing.T) {
	reg := tools.NewRegistry()
	var captured struct {
		Type    string         `json:"type"`
		Target  string         `json:"target"`
		Summary string         `json:"summary"`
		Payload map[string]any `json:"payload"`
	}
	reg.Register(&mockTool{
		name: "run_action",
		exec: func(input json.RawMessage) (string, error) {
			if err := json.Unmarshal(input, &captured); err != nil {
				t.Fatalf("failed to decode run_action args: %v", err)
			}
			return "queued", nil
		},
	})

	callCount := 0
	responder := &mockResponder{
		replyFunc: func(input llm.MessageInput) (string, error) {
			callCount++
			if callCount == 1 {
				return "I will fetch this now.\n```action\n{\"type\":\"run_command\",\"target\":\"curl\",\"summary\":\"Fetch SWAPI character\",\"payload\":{\"args\":[\"-sS\",\"https://swapi.dev/api/people/3/\"]}}\n```", nil
			}
			return `{"final":"Fetch queued and pending approval.","confidence":0.94}`, nil
		},
	}

	a := New(nil, responder, reg, "")
	res := a.Execute(context.Background(), llm.MessageInput{Text: "fetch swapi people 3"})

	if !res.ActionTaken {
		t.Fatal("expected action taken")
	}
	if res.ToolName != "run_action" {
		t.Fatalf("expected tool run_action, got %q", res.ToolName)
	}
	if captured.Type != "run_command" {
		t.Fatalf("expected action type run_command, got %q", captured.Type)
	}
	if captured.Target != "curl" {
		t.Fatalf("expected target curl, got %q", captured.Target)
	}
	if captured.Payload == nil {
		t.Fatal("expected payload to be present")
	}
	args, ok := captured.Payload["args"].([]any)
	if !ok || len(args) != 2 {
		t.Fatalf("expected payload.args with two elements, got %#v", captured.Payload["args"])
	}
}

func TestAgent_Execute_ActionJSONTopLevelArgsExecutesRunAction(t *testing.T) {
	reg := tools.NewRegistry()
	var captured struct {
		Type    string         `json:"type"`
		Payload map[string]any `json:"payload"`
	}
	reg.Register(&mockTool{
		name: "run_action",
		exec: func(input json.RawMessage) (string, error) {
			if err := json.Unmarshal(input, &captured); err != nil {
				t.Fatalf("failed to decode run_action args: %v", err)
			}
			return "queued", nil
		},
	})

	callCount := 0
	responder := &mockResponder{
		replyFunc: func(input llm.MessageInput) (string, error) {
			callCount++
			if callCount == 1 {
				return "{\"type\":\"run_command\",\"target\":\"curl\",\"summary\":\"Fetch SWAPI character\",\"args\":[\"-sS\",\"https://swapi.dev/api/people/3/\"]}", nil
			}
			return `{"final":"ok","confidence":0.9}`, nil
		},
	}

	a := New(nil, responder, reg, "")
	res := a.Execute(context.Background(), llm.MessageInput{Text: "fetch swapi people 3"})

	if !res.ActionTaken {
		t.Fatal("expected action taken")
	}
	if res.ToolName != "run_action" {
		t.Fatalf("expected tool run_action, got %q", res.ToolName)
	}
	if captured.Type != "run_command" {
		t.Fatalf("expected action type run_command, got %q", captured.Type)
	}
	args, ok := captured.Payload["args"].([]any)
	if !ok || len(args) != 2 {
		t.Fatalf("expected payload.args with two elements, got %#v", captured.Payload["args"])
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

	a := New(nil, responder, reg, "")
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

	a := New(nil, responder, tools.NewRegistry(), "")
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

	a := New(nil, responder, reg, "")
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

	a := New(nil, responder, tools.NewRegistry(), "")
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

	a := New(nil, responder, reg, "")
	a.SetDefaultPolicy(Policy{MaxLoopSteps: 2, MaxToolCallsPerTurn: 2, MinFinalConfidence: 0})

	res := a.Execute(context.Background(), llm.MessageInput{Text: "loop"})
	if !res.Blocked {
		t.Fatal("expected max-step loop to block")
	}
	if !strings.Contains(strings.ToLower(res.BlockReason), "max loop steps") {
		t.Fatalf("expected max loop steps block reason, got %q", res.BlockReason)
	}
}

func TestAgent_Execute_BlocksDisallowedToolClass(t *testing.T) {
	reg := tools.NewRegistry()
	reg.Register(&mockTool{
		name:      "mod_tool",
		toolClass: tools.ToolClassModeration,
		exec: func(input json.RawMessage) (string, error) {
			return "ok", nil
		},
	})
	responder := &mockResponder{
		replyFunc: func(input llm.MessageInput) (string, error) {
			return `{"tool":"mod_tool","args":{}}`, nil
		},
	}
	a := New(nil, responder, reg, "")
	a.SetPolicyResolver(func(ctx context.Context, input llm.MessageInput) Policy {
		return Policy{AllowedToolClasses: []string{"knowledge"}}
	})

	res := a.Execute(context.Background(), llm.MessageInput{Text: "moderate this"})
	if !res.Blocked {
		t.Fatal("expected class policy block")
	}
	if !strings.Contains(strings.ToLower(res.BlockReason), "class") {
		t.Fatalf("expected class block reason, got %q", res.BlockReason)
	}
}

func TestAgent_Execute_BlocksSensitiveToolWithoutApproval(t *testing.T) {
	reg := tools.NewRegistry()
	reg.Register(&mockTool{
		name:             "sensitive_tool",
		toolClass:        tools.ToolClassSensitive,
		requiresApproval: true,
		exec: func(input json.RawMessage) (string, error) {
			return "ok", nil
		},
	})
	responder := &mockResponder{
		replyFunc: func(input llm.MessageInput) (string, error) {
			return `{"tool":"sensitive_tool","args":{}}`, nil
		},
	}

	a := New(nil, responder, reg, "")
	res := a.Execute(context.Background(), llm.MessageInput{Text: "run a risky action"})
	if !res.Blocked {
		t.Fatal("expected approval gate block")
	}
	if !strings.Contains(strings.ToLower(res.BlockReason), "requires approval") {
		t.Fatalf("expected approval block reason, got %q", res.BlockReason)
	}
	foundAudit := false
	for _, entry := range res.Trace {
		if strings.EqualFold(strings.TrimSpace(entry.Stage), "audit.approval_required") {
			foundAudit = true
			break
		}
	}
	if !foundAudit {
		t.Fatal("expected audit.approval_required trace event")
	}
}

func TestAgent_Execute_AllowsSensitiveToolWithApproval(t *testing.T) {
	reg := tools.NewRegistry()
	reg.Register(&mockTool{
		name:             "sensitive_tool",
		toolClass:        tools.ToolClassSensitive,
		requiresApproval: true,
		exec: func(input json.RawMessage) (string, error) {
			return "ok", nil
		},
	})
	callCount := 0
	responder := &mockResponder{
		replyFunc: func(input llm.MessageInput) (string, error) {
			callCount++
			if callCount == 1 {
				return `{"tool":"sensitive_tool","args":{}}`, nil
			}
			return `{"final":"approved run complete","confidence":0.9}`, nil
		},
	}

	a := New(nil, responder, reg, "")
	ctx := WithSensitiveToolApproval(context.Background())
	res := a.Execute(ctx, llm.MessageInput{Text: "run an approved risky action"})
	if res.Blocked {
		t.Fatalf("expected approved sensitive action to run, got block: %s", res.BlockReason)
	}
	if !res.ActionTaken {
		t.Fatal("expected sensitive tool execution")
	}
	if res.ToolName != "sensitive_tool" {
		t.Fatalf("expected sensitive_tool, got %s", res.ToolName)
	}
}

func TestAgent_Execute_CapturesTrace(t *testing.T) {
	responder := &mockResponder{
		replyFunc: func(input llm.MessageInput) (string, error) {
			return "hello", nil
		},
	}

	a := New(nil, responder, tools.NewRegistry(), "")
	res := a.Execute(context.Background(), llm.MessageInput{Text: "hi"})
	if len(res.Trace) == 0 {
		t.Fatal("expected trace events to be captured")
	}
}
