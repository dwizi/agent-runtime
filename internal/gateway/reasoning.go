package gateway

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/carlos/spinner/internal/llm"
)

type AgentIntention string

const (
	IntentionTask   AgentIntention = "task"
	IntentionSearch AgentIntention = "search"
	IntentionAnswer AgentIntention = "answer"
	IntentionNone   AgentIntention = "none"
)

type AgentDecision struct {
	Intention AgentIntention `json:"intention"`
	Title     string         `json:"title"`
	Reasoning string         `json:"reasoning"`
	Priority  string         `json:"priority"`
	Query     string         `json:"query,omitempty"` // For search
}

type ReasoningEngine struct {
	llm            llm.Responder
	promptTemplate string
}

func NewReasoningEngine(responder llm.Responder, promptTemplate string) *ReasoningEngine {
	if promptTemplate == "" {
		promptTemplate = "You are the Brain of an autonomous operations agent.\nContext Summary:\n%s"
	}
	return &ReasoningEngine{
		llm:            responder,
		promptTemplate: promptTemplate,
	}
}

func (r *ReasoningEngine) Decide(ctx context.Context, input MessageInput, contextSummary string) (AgentDecision, error) {
	prompt := fmt.Sprintf(r.promptTemplate, contextSummary)

	// We wrap the user's message to force structured output
	userMessage := fmt.Sprintf("User Input: %s\n\nReturn ONLY the JSON decision object.", input.Text)

	response, err := r.llm.Reply(ctx, llm.MessageInput{
		Connector:     input.Connector,
		WorkspaceID:   "", // filled by caller if needed, or left empty for routing
		ContextID:     "",
		ExternalID:    input.ExternalID,
		DisplayName:   input.DisplayName,
		FromUserID:    input.FromUserID,
		Text:          userMessage,
		SystemPrompt:  prompt,
		SkipGrounding: true,
	})
	if err != nil {
		return AgentDecision{Intention: IntentionNone}, err
	}

	return r.parseDecision(response)
}

func (r *ReasoningEngine) buildSystemPrompt(contextSummary string) string {
	// Deprecated: use promptTemplate field
	return fmt.Sprintf(r.promptTemplate, contextSummary)
}

func (r *ReasoningEngine) parseDecision(raw string) (AgentDecision, error) {
	// Attempt to clean markdown if the model disregarded instructions
	cleaned := strings.TrimSpace(raw)
	cleaned = strings.TrimPrefix(cleaned, "```json")
	cleaned = strings.TrimPrefix(cleaned, "```")
	cleaned = strings.TrimSuffix(cleaned, "```")

	var decision AgentDecision
	err := json.Unmarshal([]byte(cleaned), &decision)
	if err != nil {
		// Fallback: simpler text analysis if JSON fails
		return AgentDecision{
			Intention: IntentionAnswer,
			Reasoning: "Failed to parse model decision, defaulting to answer. Raw: " + compactSnippet(raw),
		}, nil
	}
	return decision, nil
}