package gateway

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/dwizi/agent-runtime/internal/actions"
	"github.com/dwizi/agent-runtime/internal/agent/tools"
	"github.com/dwizi/agent-runtime/internal/store"
)

// WebSearchTool performs a web search (currently via DuckDuckGo HTML scraping).
type WebSearchTool struct {
	store          Store
	actionExecutor ActionExecutor
}

func NewWebSearchTool(store Store, executor ActionExecutor) *WebSearchTool {
	return &WebSearchTool{store: store, actionExecutor: executor}
}

func (t *WebSearchTool) Name() string { return "web_search" }
func (t *WebSearchTool) ToolClass() tools.ToolClass {
	return tools.ToolClassGeneral
}
func (t *WebSearchTool) RequiresApproval() bool { return false }

func (t *WebSearchTool) Description() string {
	return "Search the web for information. Currently uses DuckDuckGo."
}

func (t *WebSearchTool) ParametersSchema() string {
	return `{"query": "string"}`
}

func (t *WebSearchTool) Execute(ctx context.Context, rawArgs json.RawMessage) (string, error) {
	var args struct {
		Query string `json:"query"`
	}
	if err := json.Unmarshal(rawArgs, &args); err != nil {
		return "", fmt.Errorf("invalid arguments: %w", err)
	}
	if strings.TrimSpace(args.Query) == "" {
		return "", fmt.Errorf("query is required")
	}

	// Construct DuckDuckGo HTML URL
	searchURL := fmt.Sprintf("https://duckduckgo.com/html/?q=%s", url.QueryEscape(args.Query))

	record, ok := ctx.Value(ContextKeyRecord).(store.ContextRecord)
	if !ok {
		return "", fmt.Errorf("internal error: context record missing from context")
	}
	input, ok := ctx.Value(ContextKeyInput).(MessageInput)
	if !ok {
		return "", fmt.Errorf("internal error: message input missing from context")
	}

	// 1. Create approval for curl
	approval, err := t.store.CreateActionApproval(ctx, store.CreateActionApprovalInput{
		WorkspaceID:     record.WorkspaceID,
		ContextID:       record.ID,
		Connector:       input.Connector,
		ExternalID:      input.ExternalID,
		RequesterUserID: input.FromUserID,
		ActionType:      "run_command",
		ActionTarget:    "curl",
		ActionSummary:   fmt.Sprintf("search web for '%s'", args.Query),
		Payload: map[string]any{
			"command": "curl",
			"args":    []string{"-sSL", "-A", "Mozilla/5.0", searchURL}, // User-Agent to avoid some blocks
		},
	})
	if err != nil {
		return "", err
	}

	// 2. Check if we can auto-approve
	canAutoApprove := false
	if input.FromUserID == "system:task-worker" {
		canAutoApprove = true
	} else {
		if identity, err := t.store.LookupUserIdentity(ctx, input.Connector, input.FromUserID); err == nil {
			if identity.Role == "admin" || identity.Role == "overlord" {
				canAutoApprove = true
			}
		}
	}
	
	if !canAutoApprove {
		return actions.FormatApprovalRequestNotice(approval.ID), nil
	}

	// 3. Auto-approve
	approved, err := t.store.ApproveActionApproval(ctx, store.ApproveActionApprovalInput{
		ID:             approval.ID,
		ApproverUserID: "system:agent",
	})
	if err != nil {
		return "", fmt.Errorf("auto-approve failed: %w", err)
	}

	// 4. Execute
	result, err := t.actionExecutor.Execute(ctx, approved)
	
	status := "succeeded"
	msg := result.Message
	if err != nil {
		status = "failed"
		msg = err.Error()
	}
	_, _ = t.store.UpdateActionExecution(ctx, store.UpdateActionExecutionInput{
		ID:               approved.ID,
		ExecutionStatus:  status,
		ExecutionMessage: msg,
		ExecutorPlugin:   result.Plugin,
		ExecutedAt:       time.Now().UTC(),
	})

	if err != nil {
		return "", err
	}

	// 5. Convert to Markdown
	markdown := convertHTMLToMarkdown(result.Message)
	if len(markdown) > 10000 {
		markdown = markdown[:10000] + "\n\n[Truncated]"
	}
	return markdown, nil
}
