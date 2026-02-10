package tools

import (
	"context"
	"encoding/json"
)

// Tool represents an executable capability for the agent.
type Tool interface {
	// Name returns the unique identifier for the tool (e.g., "search_knowledge_base").
	Name() string

	// Description returns a human/LLM-readable explanation of what the tool does.
	Description() string

	// ParametersSchema returns a JSON schema or description of expected input.
	ParametersSchema() string

	// Execute runs the tool with the given input (usually JSON) and returns a result string.
	Execute(ctx context.Context, input json.RawMessage) (string, error)
}
