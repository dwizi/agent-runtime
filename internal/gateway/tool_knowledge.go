package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/dwizi/agent-runtime/internal/agent/tools"
	"github.com/dwizi/agent-runtime/internal/qmd"
)

// SearchTool implements tools.Tool for QMD search.
type SearchTool struct {
	retriever Retriever
}

func NewSearchTool(retriever Retriever) *SearchTool {
	return &SearchTool{retriever: retriever}
}

func (t *SearchTool) Name() string { return "search_knowledge_base" }
func (t *SearchTool) ToolClass() tools.ToolClass {
	return tools.ToolClassKnowledge
}
func (t *SearchTool) RequiresApproval() bool { return false }

func (t *SearchTool) Description() string {
	return "Search the documentation and knowledge base for answers."
}

func (t *SearchTool) ParametersSchema() string {
	return `{"query":"string","limit":"number(optional 1-10)"}`
}

func (t *SearchTool) ValidateArgs(rawArgs json.RawMessage) error {
	var args struct {
		Query string `json:"query"`
		Limit int    `json:"limit"`
	}
	if err := strictDecodeArgs(rawArgs, &args); err != nil {
		return err
	}
	args.Query = strings.TrimSpace(args.Query)
	if args.Query == "" {
		return fmt.Errorf("query is required")
	}
	if len(args.Query) > 400 {
		return fmt.Errorf("query is too long")
	}
	if args.Limit != 0 && (args.Limit < 1 || args.Limit > 10) {
		return fmt.Errorf("limit must be between 1 and 10")
	}
	return nil
}

func (t *SearchTool) Execute(ctx context.Context, rawArgs json.RawMessage) (string, error) {
	var args struct {
		Query string `json:"query"`
		Limit int    `json:"limit"`
	}
	if err := strictDecodeArgs(rawArgs, &args); err != nil {
		return "", fmt.Errorf("invalid arguments: %w", err)
	}
	if strings.TrimSpace(args.Query) == "" {
		return "Error: query cannot be empty", nil
	}
	limit := args.Limit
	if limit < 1 {
		limit = 5
	}

	record, _, err := readToolContext(ctx)
	if err != nil {
		return "", err
	}
	results, err := t.retriever.Search(ctx, record.WorkspaceID, args.Query, limit)
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
		target := strings.TrimSpace(result.Path)
		if target == "" {
			target = strings.TrimSpace(result.DocID)
		}
		lines = append(lines, fmt.Sprintf("%d. %s\n   %s", i+1, target, compactSnippet(result.Snippet)))
	}
	return strings.Join(lines, "\n"), nil
}

// OpenKnowledgeDocumentTool implements tools.Tool for opening a specific markdown document.
type OpenKnowledgeDocumentTool struct {
	retriever Retriever
}

func NewOpenKnowledgeDocumentTool(retriever Retriever) *OpenKnowledgeDocumentTool {
	return &OpenKnowledgeDocumentTool{retriever: retriever}
}

func (t *OpenKnowledgeDocumentTool) Name() string { return "open_knowledge_document" }
func (t *OpenKnowledgeDocumentTool) ToolClass() tools.ToolClass {
	return tools.ToolClassKnowledge
}
func (t *OpenKnowledgeDocumentTool) RequiresApproval() bool { return false }

func (t *OpenKnowledgeDocumentTool) Description() string {
	return "Open a markdown document from the workspace knowledge base by path or doc id."
}

func (t *OpenKnowledgeDocumentTool) ParametersSchema() string {
	return `{"target":"string (path/doc id from search results)"}`
}

func (t *OpenKnowledgeDocumentTool) ValidateArgs(rawArgs json.RawMessage) error {
	var args struct {
		Target string `json:"target"`
	}
	if err := strictDecodeArgs(rawArgs, &args); err != nil {
		return err
	}
	target := strings.TrimSpace(args.Target)
	if target == "" {
		return fmt.Errorf("target is required")
	}
	if len(target) > 800 {
		return fmt.Errorf("target is too long")
	}
	return nil
}

func (t *OpenKnowledgeDocumentTool) Execute(ctx context.Context, rawArgs json.RawMessage) (string, error) {
	var args struct {
		Target string `json:"target"`
	}
	if err := strictDecodeArgs(rawArgs, &args); err != nil {
		return "", fmt.Errorf("invalid arguments: %w", err)
	}
	if t.retriever == nil {
		return "Knowledge base is currently unavailable.", nil
	}

	record, _, err := readToolContext(ctx)
	if err != nil {
		return "", err
	}
	target := strings.TrimSpace(args.Target)
	openResult, err := t.retriever.OpenMarkdown(ctx, record.WorkspaceID, target)
	if err != nil {
		if errors.Is(err, qmd.ErrNotFound) {
			return "Document not found.", nil
		}
		if errors.Is(err, qmd.ErrInvalidTarget) {
			return "Invalid document target.", nil
		}
		if errors.Is(err, qmd.ErrUnavailable) {
			return "Knowledge base is currently unavailable.", nil
		}
		return "", err
	}
	content := strings.TrimSpace(openResult.Content)
	if content == "" {
		return "Document is empty.", nil
	}
	if openResult.Truncated {
		return fmt.Sprintf("Source: %s\n%s\n\n[truncated]", strings.TrimSpace(openResult.Path), content), nil
	}
	return fmt.Sprintf("Source: %s\n%s", strings.TrimSpace(openResult.Path), content), nil
}
