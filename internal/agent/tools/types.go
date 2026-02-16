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

// ArgumentValidator is an optional interface for strict argument validation.
// If a tool implements this, the registry validates arguments before Execute.
type ArgumentValidator interface {
	ValidateArgs(input json.RawMessage) error
}

type ToolClass string

const (
	ToolClassGeneral    ToolClass = "general"
	ToolClassKnowledge  ToolClass = "knowledge"
	ToolClassTasking    ToolClass = "tasking"
	ToolClassModeration ToolClass = "moderation"
	ToolClassObjective  ToolClass = "objective"
	ToolClassDrafting   ToolClass = "drafting"
	ToolClassSensitive  ToolClass = "sensitive"
)

// MetadataProvider is an optional interface for policy/risk metadata.
type MetadataProvider interface {
	ToolClass() ToolClass
	RequiresApproval() bool
}
