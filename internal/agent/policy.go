package agent

import (
	"context"
	"strings"
	"time"

	"github.com/dwizi/agent-runtime/internal/llm"
)

// Policy controls autonomous behavior limits for a single agent turn.
type Policy struct {
	// MaxLoopSteps caps LLM-think/tool-act iterations in one turn.
	MaxLoopSteps int
	// MaxTurnDuration bounds the total execution time of an agent turn.
	MaxTurnDuration time.Duration
	// MaxInputChars blocks overly large user input payloads.
	MaxInputChars int
	// MaxToolCallsPerTurn caps tool executions in a single turn.
	MaxToolCallsPerTurn int
	// AllowedTools restricts which tools can be executed. Empty means all registered tools.
	AllowedTools []string
	// AllowedToolClasses restricts tool classes that can be executed. Empty means all classes.
	AllowedToolClasses []string
	// MaxAutonomousTasksPerHour limits create_task tool invocations per context key per hour.
	MaxAutonomousTasksPerHour int
	// MaxAutonomousTasksPerDay limits create_task tool invocations per context key per day.
	MaxAutonomousTasksPerDay int
	// MinFinalConfidence optionally enforces minimum confidence for model-provided final answers.
	// Set to 0 to disable confidence gating.
	MinFinalConfidence float64
}

// PolicyResolver can return per-context policy overrides.
type PolicyResolver func(ctx context.Context, input llm.MessageInput) Policy

func defaultPolicy() Policy {
	return Policy{
		MaxLoopSteps:              6,
		MaxTurnDuration:           120 * time.Second,
		MaxInputChars:             12000,
		MaxToolCallsPerTurn:       6,
		MaxAutonomousTasksPerHour: 5,
		MaxAutonomousTasksPerDay:  25,
		MinFinalConfidence:        0.35,
	}
}

func mergePolicy(base, override Policy) Policy {
	policy := base
	if override.MaxTurnDuration > 0 {
		policy.MaxTurnDuration = override.MaxTurnDuration
	}
	if override.MaxLoopSteps > 0 {
		policy.MaxLoopSteps = override.MaxLoopSteps
	}
	if override.MaxInputChars > 0 {
		policy.MaxInputChars = override.MaxInputChars
	}
	if override.MaxToolCallsPerTurn > 0 {
		policy.MaxToolCallsPerTurn = override.MaxToolCallsPerTurn
	}
	if len(override.AllowedTools) > 0 {
		policy.AllowedTools = cleanToolList(override.AllowedTools)
	}
	if len(override.AllowedToolClasses) > 0 {
		policy.AllowedToolClasses = cleanToolList(override.AllowedToolClasses)
	}
	if override.MaxAutonomousTasksPerHour > 0 {
		policy.MaxAutonomousTasksPerHour = override.MaxAutonomousTasksPerHour
	}
	if override.MaxAutonomousTasksPerDay > 0 {
		policy.MaxAutonomousTasksPerDay = override.MaxAutonomousTasksPerDay
	}
	if override.MinFinalConfidence > 0 {
		policy.MinFinalConfidence = override.MinFinalConfidence
	}
	return policy
}

func cleanToolList(items []string) []string {
	if len(items) == 0 {
		return nil
	}
	cleaned := make([]string, 0, len(items))
	seen := map[string]struct{}{}
	for _, item := range items {
		name := strings.TrimSpace(item)
		if name == "" {
			continue
		}
		key := strings.ToLower(name)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		cleaned = append(cleaned, name)
	}
	return cleaned
}
