package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/carlos/spinner/internal/agent/tools"
	"github.com/carlos/spinner/internal/orchestrator"
	"github.com/carlos/spinner/internal/qmd"
	"github.com/carlos/spinner/internal/store"
)

// Ensure implementation
var _ tools.Tool = (*SearchTool)(nil)
var _ tools.Tool = (*CreateTaskTool)(nil)

type contextKey string

const (
	contextKeyRecord contextKey = "context_record"
	contextKeyInput  contextKey = "message_input"
)

// SearchTool implements tools.Tool for QMD search.
type SearchTool struct {
	retriever Retriever
}

func NewSearchTool(retriever Retriever) *SearchTool {
	return &SearchTool{retriever: retriever}
}

func (t *SearchTool) Name() string { return "search_knowledge_base" }

func (t *SearchTool) Description() string {
	return "Search the documentation and knowledge base for answers."
}

func (t *SearchTool) ParametersSchema() string {
	return `{"query": "string"}`
}

func (t *SearchTool) Execute(ctx context.Context, rawArgs json.RawMessage) (string, error) {
	var args struct {
		Query string `json:"query"`
	}
	if err := json.Unmarshal(rawArgs, &args); err != nil {
		return "", fmt.Errorf("invalid arguments: %w", err)
	}
	if strings.TrimSpace(args.Query) == "" {
		return "Error: query cannot be empty", nil
	}

	record, ok := ctx.Value(contextKeyRecord).(store.ContextRecord)
	if !ok {
		return "", fmt.Errorf("internal error: context record missing from context")
	}

	results, err := t.retriever.Search(ctx, record.WorkspaceID, args.Query, 5)
	if err != nil {
		if errors.Is(err, qmd.ErrUnavailable) {
			return "Search is currently unavailable.", nil
		}
		return "", err
	}
	if len(results) == 0 {
		return "No results found.", nil
	}

	lines := []string{fmt.Sprintf("Found %d results:", len(results))}
	for i, result := range results {
		lines = append(lines, fmt.Sprintf("%d. %s\n   %s", i+1, result.Path, compactSnippet(result.Snippet)))
	}
	return strings.Join(lines, "\n"), nil
}

// CreateTaskTool implements tools.Tool for creating tasks.
type CreateTaskTool struct {
	engine Engine
	store  Store
}

func NewCreateTaskTool(store Store, engine Engine) *CreateTaskTool {
	return &CreateTaskTool{store: store, engine: engine}
}

func (t *CreateTaskTool) Name() string { return "create_task" }

func (t *CreateTaskTool) Description() string {
	return "Create a background task for complex jobs, investigations, or system changes."
}

func (t *CreateTaskTool) ParametersSchema() string {
	return `{"title": "string", "description": "string", "priority": "p1|p2|p3"}`
}

func (t *CreateTaskTool) Execute(ctx context.Context, rawArgs json.RawMessage) (string, error) {
	var args struct {
		Title       string `json:"title"`
		Description string `json:"description"`
		Priority    string `json:"priority"`
	}
	if err := json.Unmarshal(rawArgs, &args); err != nil {
		return "", fmt.Errorf("invalid arguments: %w", err)
	}

	record, ok := ctx.Value(contextKeyRecord).(store.ContextRecord)
	if !ok {
		return "", fmt.Errorf("internal error: context record missing from context")
	}
	input, ok := ctx.Value(contextKeyInput).(MessageInput)
	if !ok {
		return "", fmt.Errorf("internal error: message input missing from context")
	}

	priority := "p3"
	if p, ok := normalizeTriagePriority(args.Priority); ok {
		priority = string(p)
	}

	task, err := t.engine.Enqueue(orchestrator.Task{
		WorkspaceID: record.WorkspaceID,
		ContextID:   record.ID,
		Kind:        orchestrator.TaskKindGeneral,
		Title:       args.Title,
		Prompt:      args.Description,
	})
	if err != nil {
		return "", err
	}

	persistErr := t.store.CreateTask(ctx, store.CreateTaskInput{
		ID:               task.ID,
		WorkspaceID:      task.WorkspaceID,
		ContextID:        task.ContextID,
		Kind:             string(task.Kind),
		Title:            task.Title,
		Prompt:           task.Prompt,
		Status:           "queued",
		RouteClass:       string(TriageTask),
		Priority:         priority,
		DueAt:            time.Now().UTC().Add(24 * time.Hour),
		AssignedLane:     "operations",
		SourceConnector:  strings.ToLower(strings.TrimSpace(input.Connector)),
		SourceExternalID: strings.TrimSpace(input.ExternalID),
		SourceUserID:     strings.TrimSpace(input.FromUserID),
		SourceText:       input.Text,
	})
	if persistErr != nil {
		return "", fmt.Errorf("task queued but failed to persist: %w", persistErr)
	}

	return fmt.Sprintf("Task created successfully (ID: %s).", task.ID), nil
}
