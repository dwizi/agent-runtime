package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/dwizi/agent-runtime/internal/agent"
	"github.com/dwizi/agent-runtime/internal/agent/tools"
	"github.com/dwizi/agent-runtime/internal/agenterr"
	"github.com/dwizi/agent-runtime/internal/orchestrator"
	"github.com/dwizi/agent-runtime/internal/qmd"
	"github.com/dwizi/agent-runtime/internal/store"
)

// Ensure implementation
var _ tools.Tool = (*SearchTool)(nil)
var _ tools.Tool = (*OpenKnowledgeDocumentTool)(nil)
var _ tools.Tool = (*CreateTaskTool)(nil)
var _ tools.Tool = (*ModerationTriageTool)(nil)
var _ tools.Tool = (*DraftEscalationTool)(nil)
var _ tools.Tool = (*DraftFAQAnswerTool)(nil)
var _ tools.Tool = (*CreateObjectiveTool)(nil)
var _ tools.Tool = (*UpdateObjectiveTool)(nil)
var _ tools.Tool = (*UpdateTaskTool)(nil)
var _ tools.MetadataProvider = (*SearchTool)(nil)
var _ tools.MetadataProvider = (*OpenKnowledgeDocumentTool)(nil)
var _ tools.MetadataProvider = (*CreateTaskTool)(nil)
var _ tools.MetadataProvider = (*ModerationTriageTool)(nil)
var _ tools.MetadataProvider = (*DraftEscalationTool)(nil)
var _ tools.MetadataProvider = (*DraftFAQAnswerTool)(nil)
var _ tools.MetadataProvider = (*CreateObjectiveTool)(nil)
var _ tools.MetadataProvider = (*UpdateObjectiveTool)(nil)
var _ tools.MetadataProvider = (*UpdateTaskTool)(nil)

var _ tools.ArgumentValidator = (*SearchTool)(nil)
var _ tools.ArgumentValidator = (*OpenKnowledgeDocumentTool)(nil)
var _ tools.ArgumentValidator = (*CreateTaskTool)(nil)
var _ tools.ArgumentValidator = (*ModerationTriageTool)(nil)
var _ tools.ArgumentValidator = (*DraftEscalationTool)(nil)
var _ tools.ArgumentValidator = (*DraftFAQAnswerTool)(nil)
var _ tools.ArgumentValidator = (*CreateObjectiveTool)(nil)
var _ tools.ArgumentValidator = (*UpdateObjectiveTool)(nil)
var _ tools.ArgumentValidator = (*UpdateTaskTool)(nil)
var _ tools.ArgumentValidator = (*RunActionTool)(nil)

type contextKey string

const (
	ContextKeyRecord contextKey = "context_record"
	ContextKeyInput  contextKey = "message_input"
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

// CreateTaskTool implements tools.Tool for creating tasks.
type CreateTaskTool struct {
	engine Engine
	store  Store
}

func NewCreateTaskTool(store Store, engine Engine) *CreateTaskTool {
	return &CreateTaskTool{store: store, engine: engine}
}

func (t *CreateTaskTool) Name() string { return "create_task" }
func (t *CreateTaskTool) ToolClass() tools.ToolClass {
	return tools.ToolClassTasking
}
func (t *CreateTaskTool) RequiresApproval() bool { return false }

func (t *CreateTaskTool) Description() string {
	return "Create a background task for complex jobs, investigations, or system changes."
}

func (t *CreateTaskTool) ParametersSchema() string {
	return `{"title": "string", "description": "string", "priority": "p1|p2|p3"}`
}

func (t *CreateTaskTool) ValidateArgs(rawArgs json.RawMessage) error {
	var args struct {
		Title       string `json:"title"`
		Description string `json:"description"`
		Priority    string `json:"priority"`
	}
	if err := strictDecodeArgs(rawArgs, &args); err != nil {
		return err
	}
	args.Title = strings.TrimSpace(args.Title)
	args.Description = strings.TrimSpace(args.Description)
	if args.Title == "" {
		return fmt.Errorf("title is required")
	}
	if len(args.Title) > 120 {
		return fmt.Errorf("title is too long")
	}
	if args.Description == "" {
		return fmt.Errorf("description is required")
	}
	if len(args.Description) > 4000 {
		return fmt.Errorf("description is too long")
	}
	if strings.TrimSpace(args.Priority) != "" {
		if _, ok := normalizeTriagePriority(args.Priority); !ok {
			return fmt.Errorf("priority must be p1, p2, or p3")
		}
	}
	return nil
}

func (t *CreateTaskTool) Execute(ctx context.Context, rawArgs json.RawMessage) (string, error) {
	var args struct {
		Title       string `json:"title"`
		Description string `json:"description"`
		Priority    string `json:"priority"`
	}
	if err := strictDecodeArgs(rawArgs, &args); err != nil {
		return "", fmt.Errorf("invalid arguments: %w", err)
	}

	record, input, err := readToolContext(ctx)
	if err != nil {
		return "", err
	}

	// Check approval if not system/admin
	// CreateTaskTool previously had RequiresApproval() = false,
	// but the Agent loop was blocking sensitive tools if they required approval.
	// Now we moved approval logic inside Execute for other tools.
	// But CreateTaskTool was always false.
	// The complaint is likely about the SUB-TASKS created by the agent or subsequent actions?
	// OR the user means: "I ask agent to do something, it creates 5 subtasks, and I have to approve each one?"
	// If create_task does not require approval (it doesn't), then the user doesn't approve CREATION.
	// But maybe the ACTIONS inside those tasks require approval?
	// If the user is Admin, we already enabled auto-approval for fetch/search etc. in the WORKER.
	// But maybe the user is talking about the CHAT session where the agent proposes actions?
	// The user said: "it needs to approve multiple ones for just one prompt".
	// This usually means the agent proposes multiple `run_action` calls in sequence.
	// And `RunActionTool` (legacy) requires approval.
	// I should update `RunActionTool` to also support auto-approval for Admin!
	// This is the missing piece. I updated specialized tools, but RunActionTool (the generic one used by legacy/chat agent) still blocks.

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

// LearnSkillTool implements tools.Tool for persisting knowledge.
type LearnSkillTool struct {
	workspaceRoot string
}

func NewLearnSkillTool(workspaceRoot string) *LearnSkillTool {
	return &LearnSkillTool{workspaceRoot: workspaceRoot}
}

func (t *LearnSkillTool) Name() string { return "learn_skill" }

func (t *LearnSkillTool) Description() string {
	return "Save a new fact, behavior, or operational procedure to your long-term knowledge base (skills)."
}

func (t *LearnSkillTool) ParametersSchema() string {
	return `{"name": "string (snake_case)", "content": "string (markdown)"}`
}

func (t *LearnSkillTool) Execute(ctx context.Context, rawArgs json.RawMessage) (string, error) {
	var args struct {
		Name    string `json:"name"`
		Content string `json:"content"`
	}
	if err := json.Unmarshal(rawArgs, &args); err != nil {
		return "", fmt.Errorf("invalid arguments: %w", err)
	}

	record, ok := ctx.Value(ContextKeyRecord).(store.ContextRecord)
	if !ok {
		return "", fmt.Errorf("internal error: context record missing from context")
	}

	// We'll put it in context/skills/common for now, or a workspace-specific one
	skillDir := filepath.Join(t.workspaceRoot, record.WorkspaceID, "context", "skills", "common")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		return "", err
	}

	path := filepath.Join(skillDir, args.Name+".md")
	if err := os.WriteFile(path, []byte(args.Content), 0o644); err != nil {
		return "", err
	}

	return fmt.Sprintf("I've learned a new skill: %s", args.Name), nil
}

// RunActionTool implements tools.Tool for executing system actions.
type RunActionTool struct {
	executor ActionExecutor
	store    Store
}

func NewRunActionTool(store Store, executor ActionExecutor) *RunActionTool {
	return &RunActionTool{store: store, executor: executor}
}

func (t *RunActionTool) Name() string { return "run_action" }

func (t *RunActionTool) Description() string {
	return "Execute a system action like 'run_command' (curl, etc.), 'send_email', or 'webhook'. Use this for external integration."
}

func (t *RunActionTool) ParametersSchema() string {
	return `{"type": "run_command|send_email|webhook", "target": "string", "summary": "brief summary", "payload": {}}`
}

func (t *RunActionTool) ValidateArgs(rawArgs json.RawMessage) error {
	var args struct {
		Type    string         `json:"type"`
		Target  string         `json:"target"`
		Summary string         `json:"summary"`
		Payload map[string]any `json:"payload"`
	}
	if err := strictDecodeArgs(rawArgs, &args); err != nil {
		return err
	}

	actionType := strings.ToLower(strings.TrimSpace(args.Type))
	switch actionType {
	case "run_command", "send_email", "webhook":
	default:
		return fmt.Errorf("%w: type must be run_command, send_email, or webhook", agenterr.ErrToolInvalidArgs)
	}

	if actionType == "run_command" {
		if err := validateRunCommandPreflight(args.Target, args.Payload); err != nil {
			return err
		}
	}

	if actionType == "webhook" && strings.TrimSpace(args.Target) == "" {
		return fmt.Errorf("%w: target is required for webhook", agenterr.ErrToolInvalidArgs)
	}
	return nil
}

func (t *RunActionTool) Execute(ctx context.Context, rawArgs json.RawMessage) (string, error) {
	var args struct {
		Type    string         `json:"type"`
		Target  string         `json:"target"`
		Summary string         `json:"summary"`
		Payload map[string]any `json:"payload"`
	}
	if err := strictDecodeArgs(rawArgs, &args); err != nil {
		return "", fmt.Errorf("invalid arguments: %w", err)
	}
	if err := t.ValidateArgs(rawArgs); err != nil {
		return "", err
	}

	record, ok := ctx.Value(ContextKeyRecord).(store.ContextRecord)
	if !ok {
		return "", fmt.Errorf("internal error: context record missing from context")
	}
	input, ok := ctx.Value(ContextKeyInput).(MessageInput)
	if !ok {
		return "", fmt.Errorf("internal error: message input missing from context")
	}

	// 1. Create the approval record (even if it might be auto-approved in future,
	// for now we follow the system's human-in-the-loop design).
	approval, err := t.store.CreateActionApproval(ctx, store.CreateActionApprovalInput{
		WorkspaceID:     record.WorkspaceID,
		ContextID:       record.ID,
		Connector:       input.Connector,
		ExternalID:      input.ExternalID,
		RequesterUserID: input.FromUserID,
		ActionType:      args.Type,
		ActionTarget:    args.Target,
		ActionSummary:   args.Summary,
		Payload:         args.Payload,
	})
	if err != nil {
		return "", err
	}

	// 2. Check if we can auto-approve
	// We reuse checkAutoApproval logic but don't return error, just bool
	canAutoApprove := false
	if input.FromUserID == "system:task-worker" {
		canAutoApprove = true
	} else if identity, err := t.store.LookupUserIdentity(ctx, input.Connector, input.FromUserID); err == nil {
		if identity.Role == "admin" || identity.Role == "overlord" {
			canAutoApprove = true
		}
	}

	if !canAutoApprove {
		return fmt.Sprintf("Action request created: %s. I need an admin to approve this before I can continue.", approval.ID), nil
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
	result, err := t.executor.Execute(ctx, approved)

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

type ModerationTriageTool struct{}

func NewModerationTriageTool() *ModerationTriageTool {
	return &ModerationTriageTool{}
}

func (t *ModerationTriageTool) Name() string { return "moderation_triage" }
func (t *ModerationTriageTool) ToolClass() tools.ToolClass {
	return tools.ToolClassModeration
}
func (t *ModerationTriageTool) RequiresApproval() bool { return false }

func (t *ModerationTriageTool) Description() string {
	return "Classify moderation risk and suggest safe next steps for community messages."
}

func (t *ModerationTriageTool) ParametersSchema() string {
	return `{"message":"string","reporter_user_id":"string(optional)","channel":"string(optional)"}`
}

func (t *ModerationTriageTool) ValidateArgs(rawArgs json.RawMessage) error {
	var args struct {
		Message        string `json:"message"`
		ReporterUserID string `json:"reporter_user_id"`
		Channel        string `json:"channel"`
	}
	if err := strictDecodeArgs(rawArgs, &args); err != nil {
		return err
	}
	if strings.TrimSpace(args.Message) == "" {
		return fmt.Errorf("message is required")
	}
	if len(strings.TrimSpace(args.Message)) > 5000 {
		return fmt.Errorf("message is too long")
	}
	return nil
}

func (t *ModerationTriageTool) Execute(ctx context.Context, rawArgs json.RawMessage) (string, error) {
	var args struct {
		Message        string `json:"message"`
		ReporterUserID string `json:"reporter_user_id"`
		Channel        string `json:"channel"`
	}
	if err := strictDecodeArgs(rawArgs, &args); err != nil {
		return "", fmt.Errorf("invalid arguments: %w", err)
	}
	text := strings.ToLower(strings.TrimSpace(args.Message))
	labels := []string{}
	severity := "low"
	action := "monitor and keep conversation civil."

	if containsAnyKeyword(text, "kill", "dox", "swat", "suicide", "self-harm", "bomb", "shoot") {
		severity = "critical"
		labels = append(labels, "threat")
		action = "escalate to moderators immediately, preserve evidence, and consider emergency protocol."
	} else if containsAnyKeyword(text, "hate", "slur", "harass", "stalk", "abuse") {
		severity = "high"
		labels = append(labels, "harassment")
		action = "escalate quickly, remove harmful content, and warn or mute according to policy."
	} else if containsAnyKeyword(text, "spam", "airdrops", "dm me", "crypto signal", "free nitro") {
		severity = "medium"
		labels = append(labels, "spam")
		action = "remove spam, apply anti-spam controls, and monitor repeat behavior."
	} else {
		labels = append(labels, "general")
	}

	lines := []string{
		fmt.Sprintf("Severity: %s", severity),
		fmt.Sprintf("Labels: %s", strings.Join(labels, ", ")),
		fmt.Sprintf("Suggested action: %s", action),
	}
	if userID := strings.TrimSpace(args.ReporterUserID); userID != "" {
		lines = append(lines, fmt.Sprintf("Reporter: %s", userID))
	}
	if channel := strings.TrimSpace(args.Channel); channel != "" {
		lines = append(lines, fmt.Sprintf("Channel: %s", channel))
	}
	return strings.Join(lines, "\n"), nil
}

type DraftEscalationTool struct{}

func NewDraftEscalationTool() *DraftEscalationTool {
	return &DraftEscalationTool{}
}

func (t *DraftEscalationTool) Name() string { return "draft_escalation" }
func (t *DraftEscalationTool) ToolClass() tools.ToolClass {
	return tools.ToolClassDrafting
}
func (t *DraftEscalationTool) RequiresApproval() bool { return false }

func (t *DraftEscalationTool) Description() string {
	return "Draft an escalation note for moderators/admins from incident details."
}

func (t *DraftEscalationTool) ParametersSchema() string {
	return `{"topic":"string","summary":"string","urgency":"low|medium|high|critical","evidence":["string"],"next_step":"string(optional)"}`
}

func (t *DraftEscalationTool) ValidateArgs(rawArgs json.RawMessage) error {
	var args struct {
		Topic    string   `json:"topic"`
		Summary  string   `json:"summary"`
		Urgency  string   `json:"urgency"`
		Evidence []string `json:"evidence"`
		NextStep string   `json:"next_step"`
	}
	if err := strictDecodeArgs(rawArgs, &args); err != nil {
		return err
	}
	if strings.TrimSpace(args.Topic) == "" {
		return fmt.Errorf("topic is required")
	}
	if strings.TrimSpace(args.Summary) == "" {
		return fmt.Errorf("summary is required")
	}
	switch strings.ToLower(strings.TrimSpace(args.Urgency)) {
	case "low", "medium", "high", "critical":
	default:
		return fmt.Errorf("urgency must be low, medium, high, or critical")
	}
	return nil
}

func (t *DraftEscalationTool) Execute(ctx context.Context, rawArgs json.RawMessage) (string, error) {
	var args struct {
		Topic    string   `json:"topic"`
		Summary  string   `json:"summary"`
		Urgency  string   `json:"urgency"`
		Evidence []string `json:"evidence"`
		NextStep string   `json:"next_step"`
	}
	if err := strictDecodeArgs(rawArgs, &args); err != nil {
		return "", fmt.Errorf("invalid arguments: %w", err)
	}

	urgency := strings.ToUpper(strings.TrimSpace(args.Urgency))
	lines := []string{
		fmt.Sprintf("Escalation: %s", strings.TrimSpace(args.Topic)),
		fmt.Sprintf("Urgency: %s", urgency),
		fmt.Sprintf("Summary: %s", strings.TrimSpace(args.Summary)),
	}
	if len(args.Evidence) > 0 {
		lines = append(lines, "Evidence:")
		for _, item := range args.Evidence {
			clean := strings.TrimSpace(item)
			if clean == "" {
				continue
			}
			lines = append(lines, "- "+clean)
		}
	}
	nextStep := strings.TrimSpace(args.NextStep)
	if nextStep == "" {
		nextStep = "Assign an on-call moderator and post an update in the admin channel."
	}
	lines = append(lines, "Recommended next step: "+nextStep)
	return strings.Join(lines, "\n"), nil
}

type DraftFAQAnswerTool struct{}

func NewDraftFAQAnswerTool() *DraftFAQAnswerTool {
	return &DraftFAQAnswerTool{}
}

func (t *DraftFAQAnswerTool) Name() string { return "draft_faq_answer" }
func (t *DraftFAQAnswerTool) ToolClass() tools.ToolClass {
	return tools.ToolClassDrafting
}
func (t *DraftFAQAnswerTool) RequiresApproval() bool { return false }

func (t *DraftFAQAnswerTool) Description() string {
	return "Draft a concise FAQ-style community answer from key points."
}

func (t *DraftFAQAnswerTool) ParametersSchema() string {
	return `{"question":"string","key_points":["string"],"tone":"neutral|friendly|strict(optional)","include_follow_up":"boolean(optional)"}`
}

func (t *DraftFAQAnswerTool) ValidateArgs(rawArgs json.RawMessage) error {
	var args struct {
		Question        string   `json:"question"`
		KeyPoints       []string `json:"key_points"`
		Tone            string   `json:"tone"`
		IncludeFollowUp bool     `json:"include_follow_up"`
	}
	if err := strictDecodeArgs(rawArgs, &args); err != nil {
		return err
	}
	if strings.TrimSpace(args.Question) == "" {
		return fmt.Errorf("question is required")
	}
	if len(args.KeyPoints) == 0 {
		return fmt.Errorf("key_points must contain at least one item")
	}
	if tone := strings.ToLower(strings.TrimSpace(args.Tone)); tone != "" {
		switch tone {
		case "neutral", "friendly", "strict":
		default:
			return fmt.Errorf("tone must be neutral, friendly, or strict")
		}
	}
	return nil
}

func (t *DraftFAQAnswerTool) Execute(ctx context.Context, rawArgs json.RawMessage) (string, error) {
	var args struct {
		Question        string   `json:"question"`
		KeyPoints       []string `json:"key_points"`
		Tone            string   `json:"tone"`
		IncludeFollowUp bool     `json:"include_follow_up"`
	}
	if err := strictDecodeArgs(rawArgs, &args); err != nil {
		return "", fmt.Errorf("invalid arguments: %w", err)
	}

	tonePrefix := ""
	switch strings.ToLower(strings.TrimSpace(args.Tone)) {
	case "friendly":
		tonePrefix = "Thanks for asking. "
	case "strict":
		tonePrefix = "Please follow the policy: "
	default:
		tonePrefix = ""
	}

	points := make([]string, 0, len(args.KeyPoints))
	for _, item := range args.KeyPoints {
		clean := strings.TrimSpace(item)
		if clean == "" {
			continue
		}
		points = append(points, clean)
	}
	answer := tonePrefix + strings.Join(points, " ")
	if args.IncludeFollowUp {
		answer += " If you need more detail, share your exact case and I can help further."
	}
	return strings.TrimSpace(answer), nil
}

type CreateObjectiveTool struct {
	store Store
}

func NewCreateObjectiveTool(store Store) *CreateObjectiveTool {
	return &CreateObjectiveTool{store: store}
}

func (t *CreateObjectiveTool) Name() string { return "create_objective" }
func (t *CreateObjectiveTool) ToolClass() tools.ToolClass {
	return tools.ToolClassObjective
}
func (t *CreateObjectiveTool) RequiresApproval() bool { return false }

func (t *CreateObjectiveTool) Description() string {
	return "Create a proactive monitoring objective for recurring community checks."
}

func (t *CreateObjectiveTool) ParametersSchema() string {
	return `{"title":"string","prompt":"string","interval_seconds":"number(optional >=60)","active":"boolean(optional)"}`
}

func (t *CreateObjectiveTool) ValidateArgs(rawArgs json.RawMessage) error {
	var args struct {
		Title           string `json:"title"`
		Prompt          string `json:"prompt"`
		IntervalSeconds int    `json:"interval_seconds"`
		Active          *bool  `json:"active"`
	}
	if err := strictDecodeArgs(rawArgs, &args); err != nil {
		return err
	}
	if strings.TrimSpace(args.Title) == "" {
		return fmt.Errorf("title is required")
	}
	if strings.TrimSpace(args.Prompt) == "" {
		return fmt.Errorf("prompt is required")
	}
	if args.IntervalSeconds != 0 && args.IntervalSeconds < 60 {
		return fmt.Errorf("interval_seconds must be >= 60")
	}
	return nil
}

func (t *CreateObjectiveTool) Execute(ctx context.Context, rawArgs json.RawMessage) (string, error) {
	var args struct {
		Title           string `json:"title"`
		Prompt          string `json:"prompt"`
		IntervalSeconds int    `json:"interval_seconds"`
		Active          *bool  `json:"active"`
	}
	if err := strictDecodeArgs(rawArgs, &args); err != nil {
		return "", fmt.Errorf("invalid arguments: %w", err)
	}
	record, _, err := readToolContext(ctx)
	if err != nil {
		return "", err
	}

	// Check approval if not system/admin
	if err := checkAutoApproval(ctx, t.store); err != nil {
		// For objective creation, we don't have a specific ActionApproval flow wired up for 'create_objective' tool class yet?
		// Wait, 'CreateObjectiveTool' is ToolClassObjective.
		// If RequiresApproval is false, the Agent calls it.
		// If we want to gate it, we must implement internal gating or rely on Agent policy.
		// Agent policy 'RequiresApproval' was true. Now false.
		// So we must gate internally if we want to restrict non-admins.
		// But 'checkAutoApproval' will return error if not admin.
		return "", fmt.Errorf("approval required: %w", err)
	}

	intervalSeconds := args.IntervalSeconds
	if intervalSeconds < 1 {
		intervalSeconds = int((6 * time.Hour).Seconds())
	}
	active := true
	if args.Active != nil {
		active = *args.Active
	}
	obj, err := t.store.CreateObjective(ctx, store.CreateObjectiveInput{
		WorkspaceID:     record.WorkspaceID,
		ContextID:       record.ID,
		Title:           strings.TrimSpace(args.Title),
		Prompt:          strings.TrimSpace(args.Prompt),
		TriggerType:     store.ObjectiveTriggerSchedule,
		IntervalSeconds: intervalSeconds,
		Active:          active,
	})
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("Objective created successfully (ID: %s).", obj.ID), nil
}

type UpdateObjectiveTool struct {
	store Store
}

func NewUpdateObjectiveTool(store Store) *UpdateObjectiveTool {
	return &UpdateObjectiveTool{store: store}
}

func (t *UpdateObjectiveTool) Name() string { return "update_objective" }
func (t *UpdateObjectiveTool) ToolClass() tools.ToolClass {
	return tools.ToolClassObjective
}
func (t *UpdateObjectiveTool) RequiresApproval() bool { return false }

func (t *UpdateObjectiveTool) Description() string {
	return "Update objective settings such as title, prompt, schedule, trigger type, or active state."
}

func (t *UpdateObjectiveTool) ParametersSchema() string {
	return `{"objective_id":"string","title":"string(optional)","prompt":"string(optional)","trigger_type":"schedule|event(optional)","event_key":"string(optional)","interval_seconds":"number(optional >=60)","active":"boolean(optional)"}`
}

func (t *UpdateObjectiveTool) ValidateArgs(rawArgs json.RawMessage) error {
	var args struct {
		ObjectiveID     string `json:"objective_id"`
		Title           string `json:"title"`
		Prompt          string `json:"prompt"`
		TriggerType     string `json:"trigger_type"`
		EventKey        string `json:"event_key"`
		IntervalSeconds int    `json:"interval_seconds"`
		Active          *bool  `json:"active"`
	}
	if err := strictDecodeArgs(rawArgs, &args); err != nil {
		return err
	}
	if strings.TrimSpace(args.ObjectiveID) == "" {
		return fmt.Errorf("objective_id is required")
	}
	if args.IntervalSeconds != 0 && args.IntervalSeconds < 60 {
		return fmt.Errorf("interval_seconds must be >= 60")
	}
	if trigger := strings.ToLower(strings.TrimSpace(args.TriggerType)); trigger != "" {
		if trigger != string(store.ObjectiveTriggerSchedule) && trigger != string(store.ObjectiveTriggerEvent) {
			return fmt.Errorf("trigger_type must be schedule or event")
		}
		if trigger == string(store.ObjectiveTriggerEvent) && strings.TrimSpace(args.EventKey) == "" {
			return fmt.Errorf("event_key is required when trigger_type is event")
		}
	}
	if strings.TrimSpace(args.Title) == "" &&
		strings.TrimSpace(args.Prompt) == "" &&
		strings.TrimSpace(args.TriggerType) == "" &&
		strings.TrimSpace(args.EventKey) == "" &&
		args.IntervalSeconds == 0 &&
		args.Active == nil {
		return fmt.Errorf("at least one field must be provided")
	}
	return nil
}

func (t *UpdateObjectiveTool) Execute(ctx context.Context, rawArgs json.RawMessage) (string, error) {
	var args struct {
		ObjectiveID     string `json:"objective_id"`
		Title           string `json:"title"`
		Prompt          string `json:"prompt"`
		TriggerType     string `json:"trigger_type"`
		EventKey        string `json:"event_key"`
		IntervalSeconds int    `json:"interval_seconds"`
		Active          *bool  `json:"active"`
	}
	if err := strictDecodeArgs(rawArgs, &args); err != nil {
		return "", fmt.Errorf("invalid arguments: %w", err)
	}

	if err := checkAutoApproval(ctx, t.store); err != nil {
		return "", fmt.Errorf("approval required: %w", err)
	}

	update := store.UpdateObjectiveInput{
		ID: strings.TrimSpace(args.ObjectiveID),
	}
	if title := strings.TrimSpace(args.Title); title != "" {
		update.Title = &title
	}
	if prompt := strings.TrimSpace(args.Prompt); prompt != "" {
		update.Prompt = &prompt
	}
	if trigger := strings.ToLower(strings.TrimSpace(args.TriggerType)); trigger != "" {
		triggerType := store.ObjectiveTriggerType(trigger)
		update.TriggerType = &triggerType
	}
	if eventKey := strings.TrimSpace(args.EventKey); eventKey != "" {
		update.EventKey = &eventKey
	}
	if args.IntervalSeconds > 0 {
		interval := args.IntervalSeconds
		update.IntervalSeconds = &interval
	}
	if args.Active != nil {
		update.Active = args.Active
	}
	obj, err := t.store.UpdateObjective(ctx, update)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("Objective updated successfully (ID: %s, active=%t).", obj.ID, obj.Active), nil
}

type UpdateTaskTool struct {
	store Store
}

func NewUpdateTaskTool(store Store) *UpdateTaskTool {
	return &UpdateTaskTool{store: store}
}

func (t *UpdateTaskTool) Name() string { return "update_task" }
func (t *UpdateTaskTool) ToolClass() tools.ToolClass {
	return tools.ToolClassTasking
}
func (t *UpdateTaskTool) RequiresApproval() bool { return false }

func (t *UpdateTaskTool) Description() string {
	return "Update task routing metadata or close a task with a completion summary."
}

func (t *UpdateTaskTool) ParametersSchema() string {
	return `{"task_id":"string","status":"open|closed(optional)","route_class":"question|issue|task|moderation|noise(optional)","priority":"p1|p2|p3(optional)","lane":"string(optional)","due_in":"duration like 2h or 1d(optional)","summary":"string(optional)"}`
}

func (t *UpdateTaskTool) ValidateArgs(rawArgs json.RawMessage) error {
	var args struct {
		TaskID     string `json:"task_id"`
		Status     string `json:"status"`
		RouteClass string `json:"route_class"`
		Priority   string `json:"priority"`
		Lane       string `json:"lane"`
		DueIn      string `json:"due_in"`
		Summary    string `json:"summary"`
	}
	if err := strictDecodeArgs(rawArgs, &args); err != nil {
		return err
	}
	if strings.TrimSpace(args.TaskID) == "" {
		return fmt.Errorf("task_id is required")
	}
	if status := strings.ToLower(strings.TrimSpace(args.Status)); status != "" {
		if status != "open" && status != "closed" {
			return fmt.Errorf("status must be open or closed")
		}
	}
	if cls := strings.TrimSpace(args.RouteClass); cls != "" {
		if _, ok := normalizeTriageClass(cls); !ok {
			return fmt.Errorf("invalid route_class")
		}
	}
	if pr := strings.TrimSpace(args.Priority); pr != "" {
		if _, ok := normalizeTriagePriority(pr); !ok {
			return fmt.Errorf("invalid priority")
		}
	}
	if due := strings.TrimSpace(args.DueIn); due != "" {
		if _, err := parseDueWindow(due); err != nil {
			return fmt.Errorf("invalid due_in: %w", err)
		}
	}
	if strings.TrimSpace(args.Status) == "" &&
		strings.TrimSpace(args.RouteClass) == "" &&
		strings.TrimSpace(args.Priority) == "" &&
		strings.TrimSpace(args.Lane) == "" &&
		strings.TrimSpace(args.DueIn) == "" &&
		strings.TrimSpace(args.Summary) == "" {
		return fmt.Errorf("at least one update field must be provided")
	}
	return nil
}

func (t *UpdateTaskTool) Execute(ctx context.Context, rawArgs json.RawMessage) (string, error) {
	var args struct {
		TaskID     string `json:"task_id"`
		Status     string `json:"status"`
		RouteClass string `json:"route_class"`
		Priority   string `json:"priority"`
		Lane       string `json:"lane"`
		DueIn      string `json:"due_in"`
		Summary    string `json:"summary"`
	}
	if err := strictDecodeArgs(rawArgs, &args); err != nil {
		return "", fmt.Errorf("invalid arguments: %w", err)
	}

	if err := checkAutoApproval(ctx, t.store); err != nil {
		return "", fmt.Errorf("approval required: %w", err)
	}

	taskID := strings.TrimSpace(args.TaskID)
	status := strings.ToLower(strings.TrimSpace(args.Status))
	if status == "closed" {
		summary := strings.TrimSpace(args.Summary)
		if summary == "" {
			summary = "Closed by assistant."
		}
		if err := t.store.MarkTaskCompleted(ctx, taskID, time.Now().UTC(), summary, ""); err != nil {
			return "", err
		}
		return fmt.Sprintf("Task closed successfully (ID: %s).", taskID), nil
	}

	record, err := t.store.LookupTask(ctx, taskID)
	if err != nil {
		return "", err
	}
	class := TriageTask
	if cls := strings.TrimSpace(record.RouteClass); cls != "" {
		if normalized, ok := normalizeTriageClass(cls); ok {
			class = normalized
		}
	}
	if cls := strings.TrimSpace(args.RouteClass); cls != "" {
		normalized, _ := normalizeTriageClass(cls)
		class = normalized
	}

	priority := TriagePriorityP3
	if p := strings.TrimSpace(record.Priority); p != "" {
		if normalized, ok := normalizeTriagePriority(p); ok {
			priority = normalized
		}
	}
	if p := strings.TrimSpace(args.Priority); p != "" {
		normalized, _ := normalizeTriagePriority(p)
		priority = normalized
	}

	dueAt := record.DueAt
	if dueAt.IsZero() {
		dueAt = time.Now().UTC().Add(24 * time.Hour)
	}
	if due := strings.TrimSpace(args.DueIn); due != "" {
		window, err := parseDueWindow(due)
		if err != nil {
			return "", err
		}
		dueAt = time.Now().UTC().Add(window)
	}

	lane := strings.TrimSpace(record.AssignedLane)
	if lane == "" {
		_, _, lane = routingDefaults(class)
	}
	if updatedLane := strings.TrimSpace(args.Lane); updatedLane != "" {
		lane = updatedLane
	}
	if status == "open" && strings.TrimSpace(args.Summary) != "" {
		// Summary is accepted for compatibility but open status does not mutate completion fields.
	}

	if _, err := t.store.UpdateTaskRouting(ctx, store.UpdateTaskRoutingInput{
		ID:           taskID,
		RouteClass:   string(class),
		Priority:     string(priority),
		DueAt:        dueAt,
		AssignedLane: lane,
	}); err != nil {
		return "", err
	}
	return fmt.Sprintf("Task updated successfully (ID: %s).", taskID), nil
}

func readToolContext(ctx context.Context) (store.ContextRecord, MessageInput, error) {
	record, ok := ctx.Value(ContextKeyRecord).(store.ContextRecord)
	if !ok {
		return store.ContextRecord{}, MessageInput{}, fmt.Errorf("internal error: context record missing from context")
	}
	input, ok := ctx.Value(ContextKeyInput).(MessageInput)
	if !ok {
		return store.ContextRecord{}, MessageInput{}, fmt.Errorf("internal error: message input missing from context")
	}
	return record, input, nil
}

func strictDecodeArgs(raw json.RawMessage, target any) error {
	payload := bytes.TrimSpace(raw)
	if len(payload) == 0 {
		payload = []byte(`{}`)
	}
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return fmt.Errorf("unexpected trailing json")
	}
	return nil
}

func containsAnyKeyword(haystack string, needles ...string) bool {
	for _, needle := range needles {
		if strings.Contains(haystack, needle) {
			return true
		}
	}
	return false
}

func checkAutoApproval(ctx context.Context, store Store) error {
	_ = store
	input, ok := ctx.Value(ContextKeyInput).(MessageInput)
	if !ok {
		return fmt.Errorf("%w: input context missing", agenterr.ErrAccessDenied)
	}
	if input.FromUserID == "system:task-worker" {
		return nil
	}
	if agent.HasSensitiveToolApproval(ctx) {
		return nil
	}
	return fmt.Errorf("%w: %w", agenterr.ErrApprovalRequired, agenterr.ErrAdminRole)
}

func looksLikePlaceholderValue(value string) bool {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return false
	}
	normalized := strings.ToUpper(strings.Trim(trimmed, `"'`))

	// Generic template markers such as <path>, <file>, ${VAR}, etc.
	if (strings.Contains(normalized, "<") && strings.Contains(normalized, ">")) ||
		(strings.Contains(normalized, "${") && strings.Contains(normalized, "}")) {
		return true
	}
	for _, keyword := range []string{"PLACEHOLDER", "REPLACE_ME", "TO_BE_FILLED", "FILL_ME", "EXAMPLE_VALUE"} {
		if strings.Contains(normalized, keyword) {
			return true
		}
	}

	// All-caps symbolic tokens like FILE_PATH, TARGET_URL, INPUT_FILE, etc.
	if isLikelySymbolicToken(normalized) && containsAnyKeyword(normalized,
		"PATH", "FILE", "URL", "URI", "HOST", "ENDPOINT", "TARGET", "INPUT", "OUTPUT", "DIR", "FOLDER", "ROUTE", "RUTA",
	) {
		return true
	}
	return false
}

func isLikelySymbolicToken(value string) bool {
	if len(value) < 5 {
		return false
	}
	if strings.ContainsAny(value, "/\\.:") {
		return false
	}
	hasLetter := false
	hasUnderscore := false
	for _, ch := range value {
		switch {
		case ch >= 'A' && ch <= 'Z':
			hasLetter = true
		case ch >= '0' && ch <= '9':
		case ch == '_':
			hasUnderscore = true
		default:
			return false
		}
	}
	return hasLetter && hasUnderscore
}

func runActionPayloadString(payload map[string]any, key string) string {
	value, ok := runActionPayloadValue(payload, key)
	if !ok || value == nil {
		return ""
	}
	switch casted := value.(type) {
	case string:
		return strings.TrimSpace(casted)
	default:
		return strings.TrimSpace(fmt.Sprintf("%v", value))
	}
}

func runActionPayloadValue(payload map[string]any, key string) (any, bool) {
	if payload == nil {
		return nil, false
	}
	if value, ok := payload[key]; ok {
		return value, true
	}
	nestedRaw, ok := payload["payload"]
	if !ok || nestedRaw == nil {
		return nil, false
	}
	nested, ok := nestedRaw.(map[string]any)
	if !ok {
		return nil, false
	}
	value, ok := nested[key]
	return value, ok
}

func runActionParseArgs(value any) ([]string, error) {
	switch casted := value.(type) {
	case []string:
		return casted, nil
	case []any:
		args := make([]string, 0, len(casted))
		for _, raw := range casted {
			args = append(args, strings.TrimSpace(fmt.Sprintf("%v", raw)))
		}
		return args, nil
	case string:
		trimmed := strings.TrimSpace(casted)
		if trimmed == "" {
			return nil, nil
		}
		return strings.Fields(trimmed), nil
	default:
		return nil, fmt.Errorf("%w: unsupported args payload", agenterr.ErrToolInvalidArgs)
	}
}

func validateRunCommandPreflight(target string, payload map[string]any) error {
	target = strings.TrimSpace(target)
	if target == "" {
		return fmt.Errorf("%w: target is required for run_command", agenterr.ErrToolInvalidArgs)
	}
	if looksLikePlaceholderValue(target) {
		return fmt.Errorf("%w: target contains a placeholder value; use a concrete command", agenterr.ErrToolPreflight)
	}
	if strings.Contains(target, "/") || strings.Contains(target, "\\") || strings.ContainsAny(target, " \t\r\n") {
		return fmt.Errorf("%w: target must be a bare executable name", agenterr.ErrToolPreflight)
	}

	if command := strings.TrimSpace(runActionPayloadString(payload, "command")); command != "" {
		if looksLikePlaceholderValue(command) {
			return fmt.Errorf("%w: payload.command contains a placeholder value; use a concrete command", agenterr.ErrToolPreflight)
		}
		commandParts := strings.Fields(command)
		if len(commandParts) > 0 {
			commandExec := strings.TrimSpace(commandParts[0])
			if strings.Contains(commandExec, "/") || strings.Contains(commandExec, "\\") {
				return fmt.Errorf("%w: payload.command must use a bare executable name", agenterr.ErrToolPreflight)
			}
			if !strings.EqualFold(commandExec, target) {
				return fmt.Errorf("%w: payload.command executable must match target", agenterr.ErrToolPreflight)
			}
		}
	}

	if rawArgs, ok := runActionPayloadValue(payload, "args"); ok {
		parsedArgs, err := runActionParseArgs(rawArgs)
		if err != nil {
			return fmt.Errorf("%w: payload.args is invalid: %w", agenterr.ErrToolInvalidArgs, err)
		}
		if len(parsedArgs) > 32 {
			return fmt.Errorf("%w: too many command args", agenterr.ErrToolPreflight)
		}
		for _, value := range parsedArgs {
			if looksLikePlaceholderValue(value) {
				return fmt.Errorf("%w: payload.args contains placeholder value %q; use concrete args", agenterr.ErrToolPreflight, value)
			}
			if len(value) > 512 {
				return fmt.Errorf("%w: command arg exceeds size limit", agenterr.ErrToolPreflight)
			}
		}
	}

	if cwd := strings.TrimSpace(runActionPayloadString(payload, "cwd")); cwd != "" {
		if looksLikePlaceholderValue(cwd) {
			return fmt.Errorf("%w: payload.cwd contains placeholder value; use a concrete path", agenterr.ErrToolPreflight)
		}
	}
	return nil
}
