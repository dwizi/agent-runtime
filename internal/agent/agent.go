package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/dwizi/agent-runtime/internal/agent/tools"
	"github.com/dwizi/agent-runtime/internal/agenterr"
	"github.com/dwizi/agent-runtime/internal/llm"
)

// Agent coordinates the "Think-Act" loop.
type Agent struct {
	logger          *slog.Logger
	llm             llm.Responder
	registry        *tools.Registry
	prompt          string // Base system prompt
	defaultPolicy   Policy
	policyResolver  PolicyResolver
	groundFirstStep bool
	groundEveryStep bool

	quotaMu    sync.Mutex
	taskEvents map[string][]time.Time
}

type contextKey string

const sensitiveToolApprovalKey contextKey = "agent_sensitive_tool_approval"

// New creates a new Agent.
func New(logger *slog.Logger, responder llm.Responder, registry *tools.Registry, systemPrompt string) *Agent {
	if systemPrompt == "" {
		systemPrompt = "You are a helpful AI agent."
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &Agent{
		logger:          logger,
		llm:             responder,
		registry:        registry,
		prompt:          systemPrompt,
		defaultPolicy:   defaultPolicy(),
		groundFirstStep: true,
		taskEvents:      map[string][]time.Time{},
	}
}

// Result represents the outcome of an agent turn.
type Result struct {
	Reply       string // Text to show the user
	ActionTaken bool   // Whether a tool was executed
	ToolName    string
	ToolOutput  string
	ToolCalls   []ToolCall
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

// ToolCall captures a tool invocation attempted by the agent loop.
type ToolCall struct {
	ToolName   string
	ToolArgs   string
	Status     string
	ToolOutput string
	Error      string
}

// WithSensitiveToolApproval marks the context as approved for sensitive tool execution.
func WithSensitiveToolApproval(ctx context.Context) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, sensitiveToolApprovalKey, true)
}

// HasSensitiveToolApproval reports whether the context contains a sensitive-tool approval token.
func HasSensitiveToolApproval(ctx context.Context) bool {
	return hasSensitiveToolApproval(ctx)
}

func (a *Agent) SetDefaultPolicy(policy Policy) {
	a.defaultPolicy = mergePolicy(defaultPolicy(), policy)
}

func (a *Agent) SetPolicyResolver(resolver PolicyResolver) {
	a.policyResolver = resolver
}

// SetGroundingPolicy controls when LLM loop calls include grounding augmentation.
func (a *Agent) SetGroundingPolicy(firstStep, everyStep bool) {
	a.groundFirstStep = firstStep
	a.groundEveryStep = everyStep
}

type loopToolStep struct {
	ToolName   string
	ToolArgs   string
	ToolStatus string
	ToolOutput string
	ToolError  string
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
		a.logger.Info("agent_trace", "stage", stage, "message", message)
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

	// We assume a.prompt contains instructions and a placeholder for tools.
	// If it doesn't have placeholders, we just append them.
	fullPrompt := a.prompt
	now := time.Now().UTC().Format(time.RFC1123)
	fullPrompt = fmt.Sprintf("CURRENT TIME (UTC): %s\n\n%s", now, fullPrompt)

	if strings.Contains(fullPrompt, "%s") {
		fullPrompt = fmt.Sprintf(fullPrompt, toolDesc)
	} else {
		fullPrompt = fmt.Sprintf("%s\n\nAVAILABLE TOOLS:\n%s", fullPrompt, toolDesc)
	}
	appendTrace("prompt.ready", "prepared prompt with tool catalog")

	maxSteps := policy.MaxLoopSteps
	if maxSteps < 1 {
		maxSteps = 1
	}

	toolCalls := 0
	toolSteps := make([]loopToolStep, 0, maxSteps)
	failedSignatures := map[string]int{}
	for step := 1; step <= maxSteps; step++ {
		result.Steps = step
		llmInput := input
		llmInput.SystemPrompt = fullPrompt
		shouldGround := false
		if !input.SkipGrounding {
			if a.groundEveryStep {
				shouldGround = true
			} else if a.groundFirstStep && step == 1 && len(toolSteps) == 0 {
				shouldGround = true
			}
		}
		llmInput.SkipGrounding = !shouldGround
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
		toolSig := loopToolSignature(toolName, toolArgs)
		toolCallIndex := len(result.ToolCalls)
		result.ToolCalls = append(result.ToolCalls, ToolCall{
			ToolName: strings.TrimSpace(toolName),
			ToolArgs: compactLoopText(string(toolArgs), 800),
			Status:   "selected",
		})

		if policy.MaxToolCallsPerTurn > 0 && toolCalls+1 > policy.MaxToolCallsPerTurn {
			result.Blocked = true
			result.BlockReason = "tool call exceeds per-turn policy"
			result.Reply = "I cannot run more tools for this request under current policy."
			result.ToolCalls[toolCallIndex].Status = "blocked"
			result.ToolCalls[toolCallIndex].Error = result.BlockReason
			appendTrace("policy.blocked", result.BlockReason)
			return result
		}

		if !isToolAllowed(policy, toolName) {
			result.Blocked = true
			result.BlockReason = fmt.Sprintf("tool %s is not allowed by policy", toolName)
			result.Reply = "I cannot run that tool in this context."
			result.ToolCalls[toolCallIndex].Status = "blocked"
			result.ToolCalls[toolCallIndex].Error = result.BlockReason
			appendTrace("policy.blocked", result.BlockReason)
			return result
		}

		if a.registry == nil {
			result.ActionTaken = true
			result.ToolName = toolName
			result.Error = fmt.Errorf("tool execution failed: tool registry is not configured")
			result.Reply = fmt.Sprintf("I tried to use `%s` but no tool registry is configured.", toolName)
			result.ToolCalls[toolCallIndex].Status = "failed"
			result.ToolCalls[toolCallIndex].Error = compactLoopText(result.Error.Error(), 800)
			appendTrace("tool.error", "tool registry is nil")
			return result
		}
		toolDef, exists := a.registry.Get(toolName)
		if !exists {
			result.ActionTaken = true
			result.ToolName = toolName
			result.Error = fmt.Errorf("tool execution failed: tool not found: %s", toolName)
			result.Reply = fmt.Sprintf("I tried to use `%s` but it is not registered.", toolName)
			result.ToolCalls[toolCallIndex].Status = "failed"
			result.ToolCalls[toolCallIndex].Error = compactLoopText(result.Error.Error(), 800)
			appendTrace("tool.error", fmt.Sprintf("tool %s not found", toolName))
			return result
		}
		toolClass, requiresApproval := toolPolicyMetadata(toolDef)
		appendTrace("policy.class", fmt.Sprintf("tool %s class=%s approval_required=%t", toolName, toolClass, requiresApproval))

		if !isToolClassAllowed(policy, toolClass) {
			result.Blocked = true
			result.BlockReason = fmt.Sprintf("tool class %s is not allowed by policy", toolClass)
			result.Reply = "I cannot run that action type in this context."
			result.ToolCalls[toolCallIndex].Status = "blocked"
			result.ToolCalls[toolCallIndex].Error = result.BlockReason
			appendTrace("audit.class_policy_block", fmt.Sprintf("blocked tool=%s class=%s connector=%s workspace=%s context=%s external=%s user=%s", toolName, toolClass, strings.TrimSpace(input.Connector), strings.TrimSpace(input.WorkspaceID), strings.TrimSpace(input.ContextID), strings.TrimSpace(input.ExternalID), strings.TrimSpace(input.FromUserID)))
			appendTrace("policy.blocked", result.BlockReason)
			return result
		}
		if requiresApproval && !hasSensitiveToolApproval(ctx) {
			result.Blocked = true
			result.BlockReason = fmt.Sprintf("tool %s requires approval", toolName)
			result.Reply = "I need explicit approval before running that sensitive action."
			result.ToolCalls[toolCallIndex].Status = "blocked"
			result.ToolCalls[toolCallIndex].Error = result.BlockReason
			appendTrace("audit.approval_required", fmt.Sprintf("blocked tool=%s class=%s connector=%s workspace=%s context=%s external=%s user=%s", toolName, toolClass, strings.TrimSpace(input.Connector), strings.TrimSpace(input.WorkspaceID), strings.TrimSpace(input.ContextID), strings.TrimSpace(input.ExternalID), strings.TrimSpace(input.FromUserID)))
			appendTrace("policy.blocked", result.BlockReason)
			return result
		}

		if strings.EqualFold(strings.TrimSpace(toolName), "create_task") {
			allowed, reason := a.allowAutonomousTask(input, policy, time.Now().UTC())
			if !allowed {
				result.Blocked = true
				result.BlockReason = reason
				result.Reply = "I am at the autonomous task limit right now. Please try again shortly."
				result.ToolCalls[toolCallIndex].Status = "blocked"
				result.ToolCalls[toolCallIndex].Error = result.BlockReason
				appendTrace("policy.blocked", result.BlockReason)
				return result
			}
			appendTrace("policy.quota", "autonomous task quota accepted")
		}

		if failedSignatures[toolSig] > 0 {
			reason := "repeated failed tool call with unchanged args; choose a different approach"
			appendTrace("policy.retry_blocked", reason)
			result.ToolCalls[toolCallIndex].Status = "blocked"
			result.ToolCalls[toolCallIndex].Error = reason
			toolSteps = append(toolSteps, loopToolStep{
				ToolName:   toolName,
				ToolArgs:   compactLoopText(string(toolArgs), 500),
				ToolStatus: "blocked",
				ToolError:  reason,
			})
			continue
		}

		output, err := a.registry.ExecuteTool(ctx, toolName, toolArgs)
		toolCalls++
		result.ActionTaken = true
		result.ToolName = toolName
		if err != nil {
			appendTrace("tool.error", err.Error())
			errText := compactLoopText(err.Error(), 1000)
			if isApprovalRequiredToolError(err) {
				result.Blocked = true
				result.BlockReason = compactLoopText(err.Error(), 240)
				result.Reply = "I need explicit approval before running that sensitive action."
				result.ToolCalls[toolCallIndex].Status = "blocked"
				result.ToolCalls[toolCallIndex].Error = compactLoopText(err.Error(), 800)
				appendTrace("audit.approval_required", fmt.Sprintf("blocked tool=%s class=%s connector=%s workspace=%s context=%s external=%s user=%s", toolName, toolClass, strings.TrimSpace(input.Connector), strings.TrimSpace(input.WorkspaceID), strings.TrimSpace(input.ContextID), strings.TrimSpace(input.ExternalID), strings.TrimSpace(input.FromUserID)))
				appendTrace("policy.blocked", result.BlockReason)
				return result
			}
			result.ToolCalls[toolCallIndex].Status = "failed"
			result.ToolCalls[toolCallIndex].Error = compactLoopText(err.Error(), 800)
			toolSteps = append(toolSteps, loopToolStep{
				ToolName:   toolName,
				ToolArgs:   compactLoopText(string(toolArgs), 500),
				ToolStatus: "failed",
				ToolError:  errText,
			})
			failedSignatures[toolSig]++
			continue
		}

		result.ToolOutput = output
		result.ToolCalls[toolCallIndex].Status = "succeeded"
		result.ToolCalls[toolCallIndex].ToolOutput = compactLoopText(output, 1200)
		appendTrace("tool.ok", fmt.Sprintf("tool %s executed successfully", toolName))

		toolSteps = append(toolSteps, loopToolStep{
			ToolName:   toolName,
			ToolArgs:   compactLoopText(string(toolArgs), 500),
			ToolStatus: "succeeded",
			ToolOutput: compactLoopText(output, 1000),
		})
		delete(failedSignatures, toolSig)
	}

	result.Blocked = true
	result.BlockReason = "max loop steps reached"
	if len(toolSteps) > 0 {
		result.Reply = "I ran several checks but could not finalize in time. Ask me to continue and I will keep iterating from here."
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
			status := strings.TrimSpace(record.ToolStatus)
			if status == "" {
				status = "unknown"
			}
			builder.WriteString(fmt.Sprintf("%d. tool=%s status=%s args=%s\n", idx+1, record.ToolName, status, record.ToolArgs))
			if strings.TrimSpace(record.ToolError) != "" {
				builder.WriteString(fmt.Sprintf("   error=%s\n", record.ToolError))
			}
			if strings.TrimSpace(record.ToolOutput) != "" {
				builder.WriteString(fmt.Sprintf("   result=%s\n", record.ToolOutput))
			}
		}
		builder.WriteString("\n")
		builder.WriteString("If a tool failed, diagnose the error and try a different concrete approach. Avoid repeating the same failed call unchanged.\n\n")
	}
	builder.WriteString(fmt.Sprintf("STEP %d OF %d.\n", step, maxSteps))
	builder.WriteString("Decide the best next action: call one tool, or return the final answer.")
	return builder.String()
}

func (a *Agent) parseDecision(response string) parsedDecision {
	// 1. Try to find a JSON object in the response
	jsonStr := findFirstJSON(response)

	// 2. If no JSON found, treat entire response as reply
	if jsonStr == "" {
		return parsedDecision{FinalReply: strings.TrimSpace(response)}
	}

	var envelope map[string]json.RawMessage
	if err := json.Unmarshal([]byte(jsonStr), &envelope); err != nil {
		// If valid JSON extraction failed (should be rare with findFirstJSON), treat as text
		return parsedDecision{FinalReply: strings.TrimSpace(response)}
	}

	// 3. Check for Tool Call
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

	// 3b. Compatibility: convert legacy action payload JSON into a run_action tool call.
	if normalizedArgs, ok := normalizeRunActionArgs(envelope); ok {
		return parsedDecision{
			IsTool:   true,
			ToolName: "run_action",
			ToolArgs: normalizedArgs,
		}
	}

	// 4. Check for Final Answer with Confidence
	reply := firstStringField(envelope, "final", "reply", "answer")
	confidence, hasConfidence := parseConfidence(envelope["confidence"])

	// If it was valid JSON but had no 'tool' or 'final' field, it might be a weird hallucination.
	// But if 'reply' is empty, we fall back to the raw response just in case.
	if strings.TrimSpace(reply) == "" {
		reply = strings.TrimSpace(response)
	}

	return parsedDecision{
		FinalReply:    reply,
		HasConfidence: hasConfidence,
		Confidence:    confidence,
	}
}

func normalizeRunActionArgs(envelope map[string]json.RawMessage) (json.RawMessage, bool) {
	actionType := firstStringField(envelope, "type")
	if strings.TrimSpace(actionType) == "" {
		return nil, false
	}
	target := firstStringField(envelope, "target")
	summary := firstStringField(envelope, "summary")

	payload := map[string]any{}
	if rawPayload, ok := envelope["payload"]; ok && len(strings.TrimSpace(string(rawPayload))) > 0 {
		var decodedPayload map[string]any
		if err := json.Unmarshal(rawPayload, &decodedPayload); err == nil {
			for key, value := range decodedPayload {
				payload[key] = value
			}
		}
	}

	skippedKeys := map[string]struct{}{
		"type":    {},
		"target":  {},
		"summary": {},
		"payload": {},
	}
	keys := make([]string, 0, len(envelope))
	for key := range envelope {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		if _, skip := skippedKeys[strings.ToLower(strings.TrimSpace(key))]; skip {
			continue
		}
		var decoded any
		if err := json.Unmarshal(envelope[key], &decoded); err != nil {
			continue
		}
		payload[key] = decoded
	}

	normalized := map[string]any{
		"type":    strings.TrimSpace(actionType),
		"target":  strings.TrimSpace(target),
		"summary": strings.TrimSpace(summary),
		"payload": payload,
	}
	raw, err := json.Marshal(normalized)
	if err != nil {
		return nil, false
	}
	return json.RawMessage(raw), true
}

// findFirstJSON attempts to locate the first outer-most JSON object {...} in the text.
func findFirstJSON(input string) string {
	start := strings.Index(input, "{")
	if start == -1 {
		return ""
	}

	depth := 0
	inString := false
	escaped := false

	for i := start; i < len(input); i++ {
		char := input[i]

		if inString {
			if escaped {
				escaped = false
			} else if char == '\\' {
				escaped = true
			} else if char == '"' {
				inString = false
			}
			continue
		}

		switch char {
		case '"':
			inString = true
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				// Found the closing brace
				candidate := input[start : i+1]
				if json.Valid([]byte(candidate)) {
					return candidate
				}
				// If invalid, keep searching? For now, we return empty if validation fails
				// because we might have captured a partial block or non-JSON.
				return ""
			}
		}
	}
	return ""
}

// Deprecated: use findFirstJSON
func sanitizeModelPayload(response string) string {
	return response
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

func isApprovalRequiredToolError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, agenterr.ErrApprovalRequired) {
		return true
	}
	lower := strings.ToLower(strings.TrimSpace(err.Error()))
	if lower == "" {
		return false
	}
	return strings.Contains(lower, "approval required") ||
		strings.Contains(lower, "admin role required") ||
		strings.Contains(lower, "explicit approval")
}

func loopToolSignature(name string, args json.RawMessage) string {
	toolName := strings.ToLower(strings.TrimSpace(name))
	normalizedArgs := compactLoopText(string(args), 2000)
	return toolName + "|" + normalizedArgs
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

func isToolClassAllowed(policy Policy, className string) bool {
	if len(policy.AllowedToolClasses) == 0 {
		return true
	}
	normalizedClass := strings.ToLower(strings.TrimSpace(className))
	for _, allowed := range policy.AllowedToolClasses {
		if strings.ToLower(strings.TrimSpace(allowed)) == normalizedClass {
			return true
		}
	}
	return false
}

func toolPolicyMetadata(tool tools.Tool) (string, bool) {
	className := string(tools.ToolClassGeneral)
	requiresApproval := false
	metadata, ok := tool.(tools.MetadataProvider)
	if !ok {
		return className, requiresApproval
	}
	normalized := strings.ToLower(strings.TrimSpace(string(metadata.ToolClass())))
	if normalized != "" {
		className = normalized
	}
	return className, metadata.RequiresApproval()
}

func hasSensitiveToolApproval(ctx context.Context) bool {
	if ctx == nil {
		return false
	}
	granted, ok := ctx.Value(sensitiveToolApprovalKey).(bool)
	return ok && granted
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
