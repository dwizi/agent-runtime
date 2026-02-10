package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/carlos/spinner/internal/agent/tools"
	"github.com/carlos/spinner/internal/llm"
)

// Agent coordinates the "Think-Act" loop.
type Agent struct {
	llm            llm.Responder
	registry       *tools.Registry
	prompt         string // Base system prompt
	defaultPolicy  Policy
	policyResolver PolicyResolver

	quotaMu    sync.Mutex
	taskEvents map[string][]time.Time
}

// New creates a new Agent.
func New(responder llm.Responder, registry *tools.Registry, systemPrompt string) *Agent {
	if systemPrompt == "" {
		systemPrompt = "You are a helpful AI agent."
	}
	return &Agent{
		llm:           responder,
		registry:      registry,
		prompt:        systemPrompt,
		defaultPolicy: defaultPolicy(),
		taskEvents:    map[string][]time.Time{},
	}
}

// Result represents the outcome of an agent turn.
type Result struct {
	Reply       string // Text to show the user
	ActionTaken bool   // Whether a tool was executed
	ToolName    string
	ToolOutput  string
	Steps       int
	Confidence  float64
	Error       error
	Blocked     bool
	BlockReason string
	Policy      Policy
	Trace       []TraceEvent
}

// TraceEvent captures a notable step for diagnostics and audit.
type TraceEvent struct {
	Time    time.Time
	Stage   string
	Message string
}

func (a *Agent) SetDefaultPolicy(policy Policy) {
	a.defaultPolicy = mergePolicy(defaultPolicy(), policy)
}

func (a *Agent) SetPolicyResolver(resolver PolicyResolver) {
	a.policyResolver = resolver
}

type loopToolStep struct {
	ToolName   string
	ToolArgs   string
	ToolOutput string
}

type parsedDecision struct {
	IsTool        bool
	ToolName      string
	ToolArgs      json.RawMessage
	FinalReply    string
	HasConfidence bool
	Confidence    float64
}

// Execute runs a bounded multi-step turn of the agent loop.
func (a *Agent) Execute(ctx context.Context, input llm.MessageInput) Result {
	result := Result{}
	appendTrace := func(stage, message string) {
		result.Trace = append(result.Trace, TraceEvent{
			Time:    time.Now().UTC(),
			Stage:   strings.TrimSpace(stage),
			Message: strings.TrimSpace(message),
		})
	}

	policy := a.resolvePolicy(ctx, input)
	result.Policy = policy
	appendTrace("start", "agent turn started")

	if policy.MaxTurnDuration > 0 {
		timeoutCtx, cancel := context.WithTimeout(ctx, policy.MaxTurnDuration)
		defer cancel()
		ctx = timeoutCtx
		appendTrace("policy.timeout", fmt.Sprintf("applied max turn duration of %s", policy.MaxTurnDuration))
	}

	if policy.MaxInputChars > 0 && utf8.RuneCountInString(input.Text) > policy.MaxInputChars {
		result.Blocked = true
		result.BlockReason = "input exceeds max size policy"
		result.Reply = "I can help, but that message is too long for one autonomous turn."
		appendTrace("policy.blocked", result.BlockReason)
		return result
	}

	// 1. Construct Prompt with Tools
	toolDesc := "No tools registered."
	if a.registry != nil {
		toolDesc = a.registry.DescribeAll()
	}
	fullPrompt := fmt.Sprintf("%s\n\nAVAILABLE TOOLS:\n%s\n\nINSTRUCTIONS:\n- You may call tools when needed.\n- To call a tool, output ONLY JSON: {\"tool\": \"name\", \"args\": {...}}\n- To finalize, output JSON: {\"final\": \"answer text\", \"confidence\": 0.0-1.0}\n- Plain text is also accepted as a final response.", a.prompt, toolDesc)
	appendTrace("prompt.ready", "prepared prompt with tool catalog")

	maxSteps := policy.MaxLoopSteps
	if maxSteps < 1 {
		maxSteps = 1
	}

	toolCalls := 0
	toolSteps := make([]loopToolStep, 0, maxSteps)
	for step := 1; step <= maxSteps; step++ {
		result.Steps = step
		llmInput := input
		llmInput.SystemPrompt = fullPrompt
		llmInput.SkipGrounding = true
		llmInput.Text = buildLoopInput(input.Text, toolSteps, step, maxSteps)

		response, err := a.llm.Reply(ctx, llmInput)
		if err != nil {
			appendTrace("llm.error", err.Error())
			result.Error = fmt.Errorf("llm error: %w", err)
			return result
		}
		appendTrace("llm.reply", fmt.Sprintf("received model response at step %d", step))

		decision := a.parseDecision(response)
		if !decision.IsTool {
			if decision.HasConfidence {
				result.Confidence = decision.Confidence
				appendTrace("decision.confidence", fmt.Sprintf("model confidence=%.2f", decision.Confidence))
			}
			if policy.MinFinalConfidence > 0 && decision.HasConfidence && decision.Confidence < policy.MinFinalConfidence {
				result.Blocked = true
				result.BlockReason = fmt.Sprintf("model confidence %.2f below threshold %.2f", decision.Confidence, policy.MinFinalConfidence)
				result.Reply = "I need a human review before taking action on this."
				appendTrace("policy.blocked", result.BlockReason)
				return result
			}

			reply := strings.TrimSpace(decision.FinalReply)
			if reply == "" && len(toolSteps) > 0 {
				last := toolSteps[len(toolSteps)-1]
				reply = fmt.Sprintf("Executed `%s`. Result:\n%s", last.ToolName, last.ToolOutput)
			}
			result.Reply = reply
			appendTrace("decision.reply", "model returned final response")
			return result
		}

		toolName := decision.ToolName
		toolArgs := decision.ToolArgs
		appendTrace("decision.tool", fmt.Sprintf("model selected tool %s", toolName))

		if policy.MaxToolCallsPerTurn > 0 && toolCalls+1 > policy.MaxToolCallsPerTurn {
			result.Blocked = true
			result.BlockReason = "tool call exceeds per-turn policy"
			result.Reply = "I cannot run more tools for this request under current policy."
			appendTrace("policy.blocked", result.BlockReason)
			return result
		}

		if !isToolAllowed(policy, toolName) {
			result.Blocked = true
			result.BlockReason = fmt.Sprintf("tool %s is not allowed by policy", toolName)
			result.Reply = "I cannot run that tool in this context."
			appendTrace("policy.blocked", result.BlockReason)
			return result
		}

		if strings.EqualFold(strings.TrimSpace(toolName), "create_task") {
			allowed, reason := a.allowAutonomousTask(input, policy, time.Now().UTC())
			if !allowed {
				result.Blocked = true
				result.BlockReason = reason
				result.Reply = "I am at the autonomous task limit right now. Please try again shortly."
				appendTrace("policy.blocked", result.BlockReason)
				return result
			}
			appendTrace("policy.quota", "autonomous task quota accepted")
		}

		if a.registry == nil {
			result.ActionTaken = true
			result.ToolName = toolName
			result.Error = fmt.Errorf("tool execution failed: tool registry is not configured")
			result.Reply = fmt.Sprintf("I tried to use `%s` but no tool registry is configured.", toolName)
			appendTrace("tool.error", "tool registry is nil")
			return result
		}

		output, err := a.registry.ExecuteTool(ctx, toolName, toolArgs)
		if err != nil {
			appendTrace("tool.error", err.Error())
			result.ActionTaken = true
			result.ToolName = toolName
			result.Error = fmt.Errorf("tool execution failed: %w", err)
			result.Reply = fmt.Sprintf("I tried to use `%s` but it failed: %v", toolName, err)
			return result
		}

		toolCalls++
		result.ActionTaken = true
		result.ToolName = toolName
		result.ToolOutput = output
		appendTrace("tool.ok", fmt.Sprintf("tool %s executed successfully", toolName))

		toolSteps = append(toolSteps, loopToolStep{
			ToolName:   toolName,
			ToolArgs:   compactLoopText(string(toolArgs), 500),
			ToolOutput: compactLoopText(output, 1000),
		})
	}

	result.Blocked = true
	result.BlockReason = "max loop steps reached"
	if len(toolSteps) > 0 {
		result.Reply = "I ran some tools but could not finalize confidently in time. Please review and I can continue."
	} else {
		result.Reply = "I could not complete this safely in one autonomous turn."
	}
	appendTrace("loop.stop", result.BlockReason)
	return result
}

func buildLoopInput(userText string, toolSteps []loopToolStep, step, maxSteps int) string {
	builder := strings.Builder{}
	builder.WriteString("USER REQUEST:\n")
	builder.WriteString(strings.TrimSpace(userText))
	builder.WriteString("\n\n")
	if len(toolSteps) > 0 {
		builder.WriteString("WORK LOG:\n")
		for idx, record := range toolSteps {
			builder.WriteString(fmt.Sprintf("%d. tool=%s args=%s\n", idx+1, record.ToolName, record.ToolArgs))
			builder.WriteString(fmt.Sprintf("   result=%s\n", record.ToolOutput))
		}
		builder.WriteString("\n")
	}
	builder.WriteString(fmt.Sprintf("STEP %d OF %d.\n", step, maxSteps))
	builder.WriteString("Decide the best next action: call one tool, or return the final answer.")
	return builder.String()
}

func (a *Agent) parseDecision(response string) parsedDecision {
	trimmed := sanitizeModelPayload(response)
	if trimmed == "" {
		return parsedDecision{FinalReply: ""}
	}
	if !strings.HasPrefix(trimmed, "{") {
		return parsedDecision{FinalReply: strings.TrimSpace(response)}
	}

	var envelope map[string]json.RawMessage
	if err := json.Unmarshal([]byte(trimmed), &envelope); err != nil {
		return parsedDecision{FinalReply: strings.TrimSpace(response)}
	}

	var toolName string
	if rawTool, ok := envelope["tool"]; ok {
		_ = json.Unmarshal(rawTool, &toolName)
	}
	if strings.TrimSpace(toolName) != "" {
		decision := parsedDecision{
			IsTool:   true,
			ToolName: strings.TrimSpace(toolName),
			ToolArgs: json.RawMessage("{}"),
		}
		if args, ok := envelope["args"]; ok && len(strings.TrimSpace(string(args))) > 0 {
			decision.ToolArgs = args
		}
		return decision
	}

	reply := firstStringField(envelope, "final", "reply", "answer")
	confidence, hasConfidence := parseConfidence(envelope["confidence"])
	if strings.TrimSpace(reply) == "" {
		reply = strings.TrimSpace(response)
	}
	return parsedDecision{
		FinalReply:    reply,
		HasConfidence: hasConfidence,
		Confidence:    confidence,
	}
}

func sanitizeModelPayload(response string) string {
	trimmed := strings.TrimSpace(response)
	trimmed = strings.TrimPrefix(trimmed, "```json")
	trimmed = strings.TrimPrefix(trimmed, "```JSON")
	trimmed = strings.TrimPrefix(trimmed, "```")
	trimmed = strings.TrimSuffix(trimmed, "```")
	return strings.TrimSpace(trimmed)
}

func firstStringField(fields map[string]json.RawMessage, keys ...string) string {
	for _, key := range keys {
		raw, ok := fields[key]
		if !ok {
			continue
		}
		var value string
		if err := json.Unmarshal(raw, &value); err != nil {
			continue
		}
		value = strings.TrimSpace(value)
		if value != "" {
			return value
		}
	}
	return ""
}

func parseConfidence(raw json.RawMessage) (float64, bool) {
	if len(raw) == 0 {
		return 0, false
	}
	var value float64
	if err := json.Unmarshal(raw, &value); err == nil {
		return clamp01(value), true
	}
	var text string
	if err := json.Unmarshal(raw, &text); err != nil {
		return 0, false
	}
	text = strings.TrimSpace(text)
	if text == "" {
		return 0, false
	}
	var parsed float64
	if _, err := fmt.Sscanf(text, "%f", &parsed); err != nil {
		return 0, false
	}
	return clamp01(parsed), true
}

func clamp01(value float64) float64 {
	if value < 0 {
		return 0
	}
	if value > 1 {
		return 1
	}
	return value
}

func compactLoopText(input string, maxLen int) string {
	clean := strings.Join(strings.Fields(strings.TrimSpace(input)), " ")
	if maxLen < 1 || len(clean) <= maxLen {
		return clean
	}
	return strings.TrimSpace(clean[:maxLen]) + "..."
}

func (a *Agent) resolvePolicy(ctx context.Context, input llm.MessageInput) Policy {
	base := a.defaultPolicy
	if a.policyResolver == nil {
		return base
	}
	override := a.policyResolver(ctx, input)
	return mergePolicy(base, override)
}

func isToolAllowed(policy Policy, toolName string) bool {
	if len(policy.AllowedTools) == 0 {
		return true
	}
	for _, allowed := range policy.AllowedTools {
		if strings.EqualFold(strings.TrimSpace(allowed), strings.TrimSpace(toolName)) {
			return true
		}
	}
	return false
}

func (a *Agent) allowAutonomousTask(input llm.MessageInput, policy Policy, now time.Time) (bool, string) {
	if policy.MaxAutonomousTasksPerHour <= 0 && policy.MaxAutonomousTasksPerDay <= 0 {
		return true, ""
	}
	key := contextQuotaKey(input)
	a.quotaMu.Lock()
	defer a.quotaMu.Unlock()

	events := append([]time.Time(nil), a.taskEvents[key]...)
	if len(events) == 0 {
		a.taskEvents[key] = []time.Time{now}
		return true, ""
	}
	dayCutoff := now.Add(-24 * time.Hour)
	pruned := make([]time.Time, 0, len(events)+1)
	for _, ts := range events {
		if ts.After(dayCutoff) {
			pruned = append(pruned, ts)
		}
	}
	if policy.MaxAutonomousTasksPerDay > 0 && len(pruned) >= policy.MaxAutonomousTasksPerDay {
		a.taskEvents[key] = pruned
		return false, "max autonomous tasks per day reached"
	}
	if policy.MaxAutonomousTasksPerHour > 0 {
		hourCutoff := now.Add(-1 * time.Hour)
		hourCount := 0
		for _, ts := range pruned {
			if ts.After(hourCutoff) {
				hourCount++
			}
		}
		if hourCount >= policy.MaxAutonomousTasksPerHour {
			a.taskEvents[key] = pruned
			return false, "max autonomous tasks per hour reached"
		}
	}
	pruned = append(pruned, now)
	a.taskEvents[key] = pruned
	return true, ""
}

func contextQuotaKey(input llm.MessageInput) string {
	parts := []string{
		strings.ToLower(strings.TrimSpace(input.Connector)),
		strings.TrimSpace(input.WorkspaceID),
		strings.TrimSpace(input.ContextID),
		strings.TrimSpace(input.ExternalID),
	}
	hasValue := false
	for _, part := range parts {
		if part != "" {
			hasValue = true
			break
		}
	}
	if !hasValue {
		return "global"
	}
	return strings.Join(parts, "|")
}
