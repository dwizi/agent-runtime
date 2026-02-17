package gateway

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/dwizi/agent-runtime/internal/actions"
	"github.com/dwizi/agent-runtime/internal/agent/tools"
	"github.com/dwizi/agent-runtime/internal/store"
)

// FetchUrlTool fetches a URL and converts the content to Markdown.
type FetchUrlTool struct {
	store          Store
	actionExecutor ActionExecutor
}

func NewFetchUrlTool(store Store, executor ActionExecutor) *FetchUrlTool {
	return &FetchUrlTool{store: store, actionExecutor: executor}
}

func (t *FetchUrlTool) Name() string { return "fetch_url" }
func (t *FetchUrlTool) ToolClass() tools.ToolClass {
	return tools.ToolClassGeneral
}
func (t *FetchUrlTool) RequiresApproval() bool { return false }

func (t *FetchUrlTool) Description() string {
	return "Fetch a web page and convert it to readable Markdown. Supports 'curl' (fast, static) or 'chromium' (slow, js-rendered)."
}

func (t *FetchUrlTool) ParametersSchema() string {
	return `{"url": "string", "renderer": "curl|chromium(optional)"}`
}

func (t *FetchUrlTool) Execute(ctx context.Context, rawArgs json.RawMessage) (string, error) {
	var args struct {
		URL      string `json:"url"`
		Renderer string `json:"renderer"`
	}
	if err := json.Unmarshal(rawArgs, &args); err != nil {
		return "", fmt.Errorf("invalid arguments: %w", err)
	}
	if strings.TrimSpace(args.URL) == "" {
		return "", fmt.Errorf("url is required")
	}
	
	renderer := strings.ToLower(strings.TrimSpace(args.Renderer))
	if renderer == "" {
		renderer = "curl"
	}
	if renderer != "curl" && renderer != "chromium" {
		return "", fmt.Errorf("invalid renderer: must be curl or chromium")
	}

	record, ok := ctx.Value(ContextKeyRecord).(store.ContextRecord)
	if !ok {
		return "", fmt.Errorf("internal error: context record missing from context")
	}
	input, ok := ctx.Value(ContextKeyInput).(MessageInput)
	if !ok {
		return "", fmt.Errorf("internal error: message input missing from context")
	}

	var actionType, actionTarget, actionSummary string
	var payload map[string]any

	if renderer == "chromium" {
		actionType = "run_command"
		actionTarget = "chromium"
		actionSummary = fmt.Sprintf("fetch %s (headless)", args.URL)
		payload = map[string]any{
			"command": "chromium",
			"args":    []string{"--headless", "--dump-dom", "--disable-gpu", "--no-sandbox", "--timeout=20000", args.URL},
		}
	} else {
		actionType = "run_command"
		actionTarget = "curl"
		actionSummary = fmt.Sprintf("fetch %s", args.URL)
		payload = map[string]any{
			"command": "curl",
			"args":    []string{"-sSL", args.URL},
		}
	}

	// 1. Create approval
	approval, err := t.store.CreateActionApproval(ctx, store.CreateActionApprovalInput{
		WorkspaceID:     record.WorkspaceID,
		ContextID:       record.ID,
		Connector:       input.Connector,
		ExternalID:      input.ExternalID,
		RequesterUserID: input.FromUserID,
		ActionType:      actionType,
		ActionTarget:    actionTarget,
		ActionSummary:   actionSummary,
		Payload:         payload,
	})
	if err != nil {
		return "", err
	}

	// 2. Check if we can auto-approve
	if t.canAutoApprove(ctx, input) {
		// Auto-approve
		approved, err := t.store.ApproveActionApproval(ctx, store.ApproveActionApprovalInput{
			ID:             approval.ID,
			ApproverUserID: "system:agent",
		})
		if err != nil {
			return "", fmt.Errorf("auto-approve failed: %w", err)
		}

		// Execute
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

		// Convert to Markdown
		markdown := convertHTMLToMarkdown(result.Message)
		if len(markdown) > 20000 {
			markdown = markdown[:20000] + "\n\n[Truncated]"
		}
		return markdown, nil
	}

	return actions.FormatApprovalRequestNotice(approval.ID), nil
}

func (t *FetchUrlTool) canAutoApprove(ctx context.Context, input MessageInput) bool {
	if input.FromUserID == "system:task-worker" {
		return true
	}
	identity, err := t.store.LookupUserIdentity(ctx, input.Connector, input.FromUserID)
	if err != nil {
		return false
	}
	return identity.Role == "admin" || identity.Role == "overlord"
}

// InspectFileTool executes safe inspection commands (head, tail, grep, wc, jq).
type InspectFileTool struct {
	store          Store
	actionExecutor ActionExecutor
	workspaceRoot  string
}

func NewInspectFileTool(store Store, executor ActionExecutor, workspaceRoot string) *InspectFileTool {
	return &InspectFileTool{store: store, actionExecutor: executor, workspaceRoot: workspaceRoot}
}

func (t *InspectFileTool) Name() string { return "inspect_file" }
func (t *InspectFileTool) ToolClass() tools.ToolClass {
	return tools.ToolClassGeneral
}
func (t *InspectFileTool) RequiresApproval() bool { return false }

func (t *InspectFileTool) Description() string {
	return "Inspect files using shell tools (head, tail, grep, wc, jq). Use this for large files."
}

func (t *InspectFileTool) ParametersSchema() string {
	return `{"command": "head|tail|grep|wc|jq", "file": "string (relative path)", "args": ["string"]}`
}

func (t *InspectFileTool) Execute(ctx context.Context, rawArgs json.RawMessage) (string, error) {
	var args struct {
		Command string   `json:"command"`
		File    string   `json:"file"`
		Args    []string `json:"args"`
	}
	if err := json.Unmarshal(rawArgs, &args); err != nil {
		return "", fmt.Errorf("invalid arguments: %w", err)
	}
	
	cmd := strings.ToLower(strings.TrimSpace(args.Command))
	allowed := map[string]bool{"head": true, "tail": true, "grep": true, "wc": true, "jq": true}
	if !allowed[cmd] {
		return "", fmt.Errorf("command not allowed: %s", cmd)
	}
	
	if strings.Contains(args.File, "..") || strings.HasPrefix(args.File, "/") {
		return "", fmt.Errorf("invalid file path")
	}

	// Construct full args: [native_flags..., file]
	// Note: We rely on the sandbox to run this in the workspace root.
	// So we can just pass the relative path.
	fullArgs := append([]string{}, args.Args...)
	fullArgs = append(fullArgs, args.File)

	record, ok := ctx.Value(ContextKeyRecord).(store.ContextRecord)
	if !ok {
		return "", fmt.Errorf("internal error: context record missing from context")
	}
	input, ok := ctx.Value(ContextKeyInput).(MessageInput)
	if !ok {
		return "", fmt.Errorf("internal error: message input missing from context")
	}

	approval, err := t.store.CreateActionApproval(ctx, store.CreateActionApprovalInput{
		WorkspaceID:     record.WorkspaceID,
		ContextID:       record.ID,
		Connector:       input.Connector,
		ExternalID:      input.ExternalID,
		RequesterUserID: input.FromUserID,
		ActionType:      "run_command",
		ActionTarget:    cmd,
		ActionSummary:   fmt.Sprintf("inspect %s %s", cmd, args.File),
		Payload: map[string]any{
			"command": cmd,
			"args":    fullArgs,
			"cwd":     record.WorkspaceID, // Execute inside the workspace
		},
	})
	if err != nil {
		return "", err
	}

	// Check if we can auto-approve
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

	approved, err := t.store.ApproveActionApproval(ctx, store.ApproveActionApprovalInput{
		ID:             approval.ID,
		ApproverUserID: "system:agent",
	})
	if err != nil {
		return "", fmt.Errorf("auto-approve failed: %w", err)
	}

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
	return result.Message, nil
}

// Simple heuristic converter
func convertHTMLToMarkdown(html string) string {
	// Remove scripts and styles
	reScript := regexp.MustCompile(`(?si)<script.*?>.*?</script>`)
	text := reScript.ReplaceAllString(html, "")
	reStyle := regexp.MustCompile(`(?si)<style.*?>.*?</style>`)
	text = reStyle.ReplaceAllString(text, "")

	// Headers
	reH1 := regexp.MustCompile(`(?i)<h1.*?>(.*?)</h1>`)
	text = reH1.ReplaceAllString(text, "\n# $1\n")
	reH2 := regexp.MustCompile(`(?i)<h2.*?>(.*?)</h2>`)
	text = reH2.ReplaceAllString(text, "\n## $1\n")
	reH3 := regexp.MustCompile(`(?i)<h3.*?>(.*?)</h3>`)
	text = reH3.ReplaceAllString(text, "\n### $1\n")

	// Paragraphs
	reP := regexp.MustCompile(`(?i)<p.*?>(.*?)</p>`)
	text = reP.ReplaceAllString(text, "\n$1\n")

	// Links
	reLink := regexp.MustCompile(`(?i)<a[^>]*href=["']([^"']*)["'][^>]*>(.*?)</a>`)
	text = reLink.ReplaceAllString(text, "[$2]($1)")

	// Lists
	reLi := regexp.MustCompile(`(?i)<li.*?>(.*?)</li>`)
	text = reLi.ReplaceAllString(text, "- $1\n")
	
	// Breaks
	reBr := regexp.MustCompile(`(?i)<br\s*/?>`)
	text = reBr.ReplaceAllString(text, "\n")

	// Strip remaining tags
	reTag := regexp.MustCompile(`<[^>]*>`)
	text = reTag.ReplaceAllString(text, "")

	// Decode common entities
	text = strings.ReplaceAll(text, "&nbsp;", " ")
	text = strings.ReplaceAll(text, "&amp;", "&")
	text = strings.ReplaceAll(text, "&lt;", "<")
	text = strings.ReplaceAll(text, "&gt;", ">")
	text = strings.ReplaceAll(text, "&quot;", "\"")

	// Collapse whitespace
	reSpace := regexp.MustCompile(`\n\s+\n`)
	text = reSpace.ReplaceAllString(text, "\n\n")
	
	return strings.TrimSpace(text)
}
