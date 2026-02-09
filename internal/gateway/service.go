package gateway

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/carlos/spinner/internal/actions/executor"
	"github.com/carlos/spinner/internal/orchestrator"
	"github.com/carlos/spinner/internal/qmd"
	"github.com/carlos/spinner/internal/store"
)

type Store interface {
	EnsureContextForExternalChannel(ctx context.Context, connector, externalID, displayName string) (store.ContextRecord, error)
	SetContextAdminByExternal(ctx context.Context, connector, externalID string, enabled bool) (store.ContextRecord, error)
	LookupContextPolicyByExternal(ctx context.Context, connector, externalID string) (store.ContextPolicy, error)
	SetContextSystemPromptByExternal(ctx context.Context, connector, externalID, prompt string) (store.ContextPolicy, error)
	LookupUserIdentity(ctx context.Context, connector, connectorUserID string) (store.UserIdentity, error)
	CreateTask(ctx context.Context, input store.CreateTaskInput) error
	ApprovePairing(ctx context.Context, input store.ApprovePairingInput) (store.ApprovePairingResult, error)
	DenyPairing(ctx context.Context, input store.DenyPairingInput) (store.PairingRequest, error)
	CreateActionApproval(ctx context.Context, input store.CreateActionApprovalInput) (store.ActionApproval, error)
	ListPendingActionApprovals(ctx context.Context, connector, externalID string, limit int) ([]store.ActionApproval, error)
	ApproveActionApproval(ctx context.Context, input store.ApproveActionApprovalInput) (store.ActionApproval, error)
	DenyActionApproval(ctx context.Context, input store.DenyActionApprovalInput) (store.ActionApproval, error)
	UpdateActionExecution(ctx context.Context, input store.UpdateActionExecutionInput) (store.ActionApproval, error)
}

type Engine interface {
	Enqueue(task orchestrator.Task) (orchestrator.Task, error)
}

type Retriever interface {
	Search(ctx context.Context, workspaceID, query string, limit int) ([]qmd.SearchResult, error)
	OpenMarkdown(ctx context.Context, workspaceID, target string) (qmd.OpenResult, error)
	Status(ctx context.Context, workspaceID string) (qmd.Status, error)
}

type ActionExecutor interface {
	Execute(ctx context.Context, approval store.ActionApproval) (executor.Result, error)
}

type Service struct {
	store          Store
	engine         Engine
	retriever      Retriever
	actionExecutor ActionExecutor
}

type MessageInput struct {
	Connector   string
	ExternalID  string
	DisplayName string
	FromUserID  string
	Text        string
}

type MessageOutput struct {
	Handled bool
	Reply   string
}

func New(store Store, engine Engine, retriever Retriever, actionExecutor ActionExecutor) *Service {
	return &Service{
		store:          store,
		engine:         engine,
		retriever:      retriever,
		actionExecutor: actionExecutor,
	}
}

func (s *Service) HandleMessage(ctx context.Context, input MessageInput) (MessageOutput, error) {
	text := strings.TrimSpace(input.Text)
	if text == "" {
		return MessageOutput{}, nil
	}

	command, arg := splitCommand(text)
	switch command {
	case "task":
		return s.handleTask(ctx, input, arg)
	case "search":
		return s.handleSearch(ctx, input, arg)
	case "open":
		return s.handleOpen(ctx, input, arg)
	case "status":
		return s.handleStatus(ctx, input)
	case "admin-channel":
		return s.handleAdminChannel(ctx, input, arg)
	case "prompt":
		return s.handlePrompt(ctx, input, arg)
	case "approve":
		return s.handleApprove(ctx, input, arg)
	case "deny":
		return s.handleDeny(ctx, input, arg)
	case "pending-actions":
		return s.handlePendingActions(ctx, input)
	case "approve-action":
		return s.handleApproveAction(ctx, input, arg)
	case "deny-action":
		return s.handleDenyAction(ctx, input, arg)
	default:
		if prompt, ok := parseIntentTask(text); ok {
			return s.handleTask(ctx, input, prompt)
		}
		return MessageOutput{}, nil
	}
}

func (s *Service) handlePendingActions(ctx context.Context, input MessageInput) (MessageOutput, error) {
	identity, err := s.store.LookupUserIdentity(ctx, input.Connector, input.FromUserID)
	if err != nil {
		if errors.Is(err, store.ErrIdentityNotFound) {
			return MessageOutput{Handled: true, Reply: "Access denied: link your admin identity first."}, nil
		}
		return MessageOutput{}, err
	}
	if !isAdminRole(identity.Role) {
		return MessageOutput{Handled: true, Reply: "Access denied: admin role required."}, nil
	}
	items, err := s.store.ListPendingActionApprovals(ctx, input.Connector, input.ExternalID, 10)
	if err != nil {
		return MessageOutput{}, err
	}
	if len(items) == 0 {
		return MessageOutput{Handled: true, Reply: "No pending actions in this context."}, nil
	}
	lines := []string{"Pending actions:"}
	for _, item := range items {
		summary := strings.TrimSpace(item.ActionSummary)
		if summary == "" {
			summary = item.ActionType
		}
		lines = append(lines, fmt.Sprintf("- `%s` %s (%s)", item.ID, summary, item.ActionType))
	}
	return MessageOutput{Handled: true, Reply: strings.Join(lines, "\n")}, nil
}

func (s *Service) handleApproveAction(ctx context.Context, input MessageInput, arg string) (MessageOutput, error) {
	actionID := strings.TrimSpace(arg)
	if actionID == "" {
		return MessageOutput{Handled: true, Reply: "Usage: /approve-action <action-id>"}, nil
	}
	identity, err := s.store.LookupUserIdentity(ctx, input.Connector, input.FromUserID)
	if err != nil {
		if errors.Is(err, store.ErrIdentityNotFound) {
			return MessageOutput{Handled: true, Reply: "Access denied: link your admin identity first."}, nil
		}
		return MessageOutput{}, err
	}
	if !isAdminRole(identity.Role) {
		return MessageOutput{Handled: true, Reply: "Access denied: admin role required."}, nil
	}
	record, err := s.store.ApproveActionApproval(ctx, store.ApproveActionApprovalInput{
		ID:             actionID,
		ApproverUserID: identity.UserID,
	})
	if err != nil {
		if errors.Is(err, store.ErrActionApprovalNotFound) {
			return MessageOutput{Handled: true, Reply: "Action approval not found."}, nil
		}
		if errors.Is(err, store.ErrActionApprovalNotReady) {
			return MessageOutput{Handled: true, Reply: "Action approval is not pending."}, nil
		}
		return MessageOutput{}, err
	}

	if s.actionExecutor == nil {
		record, err = s.store.UpdateActionExecution(ctx, store.UpdateActionExecutionInput{
			ID:               record.ID,
			ExecutionStatus:  "skipped",
			ExecutionMessage: "approved but no action executor is configured",
			ExecutorPlugin:   "",
			ExecutedAt:       time.Now().UTC(),
		})
		if err != nil {
			return MessageOutput{}, err
		}
		return MessageOutput{
			Handled: true,
			Reply:   fmt.Sprintf("Action `%s` approved, execution skipped (no executor configured).", record.ID),
		}, nil
	}

	executionResult, execErr := s.actionExecutor.Execute(ctx, record)
	if execErr != nil {
		record, err = s.store.UpdateActionExecution(ctx, store.UpdateActionExecutionInput{
			ID:               record.ID,
			ExecutionStatus:  "failed",
			ExecutionMessage: execErr.Error(),
			ExecutorPlugin:   executionResult.Plugin,
			ExecutedAt:       time.Now().UTC(),
		})
		if err != nil {
			return MessageOutput{}, err
		}
		return MessageOutput{
			Handled: true,
			Reply:   fmt.Sprintf("Action `%s` approved but execution failed: %s", record.ID, compactSnippet(record.ExecutionMessage)),
		}, nil
	}

	record, err = s.store.UpdateActionExecution(ctx, store.UpdateActionExecutionInput{
		ID:               record.ID,
		ExecutionStatus:  "succeeded",
		ExecutionMessage: executionResult.Message,
		ExecutorPlugin:   executionResult.Plugin,
		ExecutedAt:       time.Now().UTC(),
	})
	if err != nil {
		return MessageOutput{}, err
	}
	if strings.TrimSpace(record.ExecutionMessage) == "" {
		record.ExecutionMessage = "executed successfully"
	}
	return MessageOutput{
		Handled: true,
		Reply:   fmt.Sprintf("Action `%s` approved and executed via `%s`: %s", record.ID, fallbackPluginLabel(record.ExecutorPlugin), record.ExecutionMessage),
	}, nil
}

func fallbackPluginLabel(value string) string {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return "executor"
	}
	return trimmed
}

func (s *Service) handleDenyAction(ctx context.Context, input MessageInput, arg string) (MessageOutput, error) {
	trimmed := strings.TrimSpace(arg)
	if trimmed == "" {
		return MessageOutput{Handled: true, Reply: "Usage: /deny-action <action-id> [reason]"}, nil
	}
	parts := strings.Fields(trimmed)
	actionID := parts[0]
	reason := "denied by admin"
	if len(parts) > 1 {
		reason = strings.Join(parts[1:], " ")
	}
	identity, err := s.store.LookupUserIdentity(ctx, input.Connector, input.FromUserID)
	if err != nil {
		if errors.Is(err, store.ErrIdentityNotFound) {
			return MessageOutput{Handled: true, Reply: "Access denied: link your admin identity first."}, nil
		}
		return MessageOutput{}, err
	}
	if !isAdminRole(identity.Role) {
		return MessageOutput{Handled: true, Reply: "Access denied: admin role required."}, nil
	}
	record, err := s.store.DenyActionApproval(ctx, store.DenyActionApprovalInput{
		ID:             actionID,
		ApproverUserID: identity.UserID,
		Reason:         reason,
	})
	if err != nil {
		if errors.Is(err, store.ErrActionApprovalNotFound) {
			return MessageOutput{Handled: true, Reply: "Action approval not found."}, nil
		}
		if errors.Is(err, store.ErrActionApprovalNotReady) {
			return MessageOutput{Handled: true, Reply: "Action approval is not pending."}, nil
		}
		return MessageOutput{}, err
	}
	return MessageOutput{
		Handled: true,
		Reply:   fmt.Sprintf("Action `%s` denied.", record.ID),
	}, nil
}

func (s *Service) handlePrompt(ctx context.Context, input MessageInput, arg string) (MessageOutput, error) {
	identity, err := s.store.LookupUserIdentity(ctx, input.Connector, input.FromUserID)
	if err != nil {
		if errors.Is(err, store.ErrIdentityNotFound) {
			return MessageOutput{Handled: true, Reply: "Access denied: link your admin identity first."}, nil
		}
		return MessageOutput{}, err
	}
	if !isAdminRole(identity.Role) {
		return MessageOutput{Handled: true, Reply: "Access denied: admin role required."}, nil
	}

	trimmed := strings.TrimSpace(arg)
	if trimmed == "" {
		return MessageOutput{Handled: true, Reply: "Usage: /prompt show | /prompt set <text> | /prompt clear"}, nil
	}
	lower := strings.ToLower(trimmed)
	switch {
	case lower == "show":
		policy, err := s.store.LookupContextPolicyByExternal(ctx, input.Connector, input.ExternalID)
		if err != nil {
			return MessageOutput{}, err
		}
		prompt := strings.TrimSpace(policy.SystemPrompt)
		if prompt == "" {
			prompt = "(empty)"
		}
		return MessageOutput{
			Handled: true,
			Reply:   "Current context prompt:\n" + prompt,
		}, nil
	case lower == "clear":
		_, err := s.store.SetContextSystemPromptByExternal(ctx, input.Connector, input.ExternalID, "")
		if err != nil {
			return MessageOutput{}, err
		}
		return MessageOutput{
			Handled: true,
			Reply:   "Context prompt cleared.",
		}, nil
	case strings.HasPrefix(lower, "set "):
		value := strings.TrimSpace(trimmed[len("set "):])
		if value == "" {
			return MessageOutput{Handled: true, Reply: "Usage: /prompt set <text>"}, nil
		}
		policy, err := s.store.SetContextSystemPromptByExternal(ctx, input.Connector, input.ExternalID, value)
		if err != nil {
			return MessageOutput{}, err
		}
		return MessageOutput{
			Handled: true,
			Reply:   fmt.Sprintf("Context prompt updated for `%s`.", policy.ContextID),
		}, nil
	default:
		return MessageOutput{Handled: true, Reply: "Usage: /prompt show | /prompt set <text> | /prompt clear"}, nil
	}
}

func (s *Service) handleStatus(ctx context.Context, input MessageInput) (MessageOutput, error) {
	if s.retriever == nil {
		return MessageOutput{Handled: true, Reply: "Status is not configured on this runtime."}, nil
	}
	contextRecord, err := s.store.EnsureContextForExternalChannel(ctx, input.Connector, input.ExternalID, input.DisplayName)
	if err != nil {
		return MessageOutput{}, err
	}
	status, err := s.retriever.Status(ctx, contextRecord.WorkspaceID)
	if err != nil {
		if errors.Is(err, qmd.ErrUnavailable) {
			return MessageOutput{
				Handled: true,
				Reply:   "Status is unavailable: install `qmd` and ensure it is available in PATH.",
			}, nil
		}
		return MessageOutput{}, err
	}

	lines := []string{
		fmt.Sprintf("Workspace `%s` qmd status:", status.WorkspaceID),
	}
	if !status.WorkspaceExist {
		lines = append(lines, "- workspace directory not created yet")
		return MessageOutput{Handled: true, Reply: strings.Join(lines, "\n")}, nil
	}
	if status.Indexed {
		lines = append(lines, "- indexed: yes")
	} else {
		lines = append(lines, "- indexed: no (will build on first search/change)")
	}
	if status.Pending {
		lines = append(lines, "- pending reindex: yes")
	} else {
		lines = append(lines, "- pending reindex: no")
	}
	if status.IndexExists {
		lines = append(lines, "- index file: present")
	} else {
		lines = append(lines, "- index file: not found")
	}
	if !status.LastIndexedAt.IsZero() {
		lines = append(lines, "- last indexed: "+status.LastIndexedAt.Format(time.RFC3339))
	}
	if strings.TrimSpace(status.Summary) != "" {
		lines = append(lines, "- qmd: "+compactSnippet(status.Summary))
	}

	return MessageOutput{
		Handled: true,
		Reply:   strings.Join(lines, "\n"),
	}, nil
}

func (s *Service) handleSearch(ctx context.Context, input MessageInput, query string) (MessageOutput, error) {
	query = strings.TrimSpace(query)
	if query == "" {
		return MessageOutput{Handled: true, Reply: "Usage: /search <query>"}, nil
	}
	if s.retriever == nil {
		return MessageOutput{Handled: true, Reply: "Search is not configured on this runtime."}, nil
	}

	contextRecord, err := s.store.EnsureContextForExternalChannel(ctx, input.Connector, input.ExternalID, input.DisplayName)
	if err != nil {
		return MessageOutput{}, err
	}
	results, err := s.retriever.Search(ctx, contextRecord.WorkspaceID, query, 5)
	if err != nil {
		if errors.Is(err, qmd.ErrUnavailable) {
			return MessageOutput{
				Handled: true,
				Reply:   "Search is unavailable: install `qmd` and ensure it is available in PATH.",
			}, nil
		}
		return MessageOutput{}, err
	}
	if len(results) == 0 {
		return MessageOutput{Handled: true, Reply: "No markdown matches found."}, nil
	}

	lines := make([]string, 0, len(results)+1)
	lines = append(lines, fmt.Sprintf("Top %d result(s):", len(results)))
	for index, result := range results {
		location := strings.TrimSpace(result.Path)
		if location == "" {
			location = strings.TrimSpace(result.DocID)
		}
		score := int(result.Score * 100)
		snippet := compactSnippet(result.Snippet)
		if score > 0 {
			lines = append(lines, fmt.Sprintf("%d. `%s` (%d%%) %s", index+1, location, score, snippet))
			continue
		}
		lines = append(lines, fmt.Sprintf("%d. `%s` %s", index+1, location, snippet))
	}
	return MessageOutput{
		Handled: true,
		Reply:   strings.Join(lines, "\n"),
	}, nil
}

func (s *Service) handleOpen(ctx context.Context, input MessageInput, target string) (MessageOutput, error) {
	target = strings.TrimSpace(target)
	if target == "" {
		return MessageOutput{Handled: true, Reply: "Usage: /open <path-or-docid>"}, nil
	}
	if s.retriever == nil {
		return MessageOutput{Handled: true, Reply: "Open is not configured on this runtime."}, nil
	}

	contextRecord, err := s.store.EnsureContextForExternalChannel(ctx, input.Connector, input.ExternalID, input.DisplayName)
	if err != nil {
		return MessageOutput{}, err
	}

	result, err := s.retriever.OpenMarkdown(ctx, contextRecord.WorkspaceID, target)
	if err != nil {
		switch {
		case errors.Is(err, qmd.ErrUnavailable):
			return MessageOutput{
				Handled: true,
				Reply:   "Open is unavailable: install `qmd` and ensure it is available in PATH.",
			}, nil
		case errors.Is(err, qmd.ErrNotFound):
			return MessageOutput{Handled: true, Reply: "Markdown file not found in this workspace."}, nil
		case errors.Is(err, qmd.ErrInvalidTarget):
			return MessageOutput{Handled: true, Reply: "Invalid target. Use a relative `.md` path or a qmd docid (`#abc123`)."}, nil
		default:
			return MessageOutput{}, err
		}
	}

	content := strings.TrimSpace(result.Content)
	if content == "" {
		content = "(empty file)"
	}
	reply := fmt.Sprintf("`%s`\n%s", result.Path, content)
	if result.Truncated {
		reply += "\n\n(Truncated to safe output size.)"
	}
	return MessageOutput{
		Handled: true,
		Reply:   reply,
	}, nil
}

func (s *Service) handleTask(ctx context.Context, input MessageInput, prompt string) (MessageOutput, error) {
	prompt = strings.TrimSpace(prompt)
	if prompt == "" {
		return MessageOutput{Handled: true, Reply: "Usage: /task <what should be done>"}, nil
	}

	contextRecord, err := s.store.EnsureContextForExternalChannel(ctx, input.Connector, input.ExternalID, input.DisplayName)
	if err != nil {
		return MessageOutput{}, err
	}

	title := prompt
	if len(title) > 72 {
		title = title[:72]
	}
	task, err := s.engine.Enqueue(orchestrator.Task{
		WorkspaceID: contextRecord.WorkspaceID,
		ContextID:   contextRecord.ID,
		Kind:        orchestrator.TaskKindGeneral,
		Title:       title,
		Prompt:      prompt,
	})
	if err != nil {
		return MessageOutput{}, err
	}
	if err := s.store.CreateTask(ctx, store.CreateTaskInput{
		ID:          task.ID,
		WorkspaceID: task.WorkspaceID,
		ContextID:   task.ContextID,
		Kind:        string(task.Kind),
		Title:       task.Title,
		Prompt:      task.Prompt,
		Status:      "queued",
	}); err != nil {
		return MessageOutput{}, err
	}
	return MessageOutput{
		Handled: true,
		Reply:   fmt.Sprintf("Task queued: `%s`", task.ID),
	}, nil
}

func (s *Service) handleAdminChannel(ctx context.Context, input MessageInput, arg string) (MessageOutput, error) {
	if strings.ToLower(strings.TrimSpace(arg)) != "enable" {
		return MessageOutput{Handled: true, Reply: "Usage: /admin-channel enable"}, nil
	}

	identity, err := s.store.LookupUserIdentity(ctx, input.Connector, input.FromUserID)
	if err != nil {
		if errors.Is(err, store.ErrIdentityNotFound) {
			return MessageOutput{Handled: true, Reply: "Access denied: link your admin identity first."}, nil
		}
		return MessageOutput{}, err
	}
	if !isAdminRole(identity.Role) {
		return MessageOutput{Handled: true, Reply: "Access denied: admin role required."}, nil
	}

	contextRecord, err := s.store.SetContextAdminByExternal(ctx, input.Connector, input.ExternalID, true)
	if err != nil {
		return MessageOutput{}, err
	}
	return MessageOutput{
		Handled: true,
		Reply:   fmt.Sprintf("Admin channel enabled for context `%s`.", contextRecord.ID),
	}, nil
}

func (s *Service) handleApprove(ctx context.Context, input MessageInput, arg string) (MessageOutput, error) {
	token := strings.TrimSpace(arg)
	if token == "" {
		return MessageOutput{Handled: true, Reply: "Usage: /approve <pairing-token>"}, nil
	}
	identity, err := s.store.LookupUserIdentity(ctx, input.Connector, input.FromUserID)
	if err != nil {
		if errors.Is(err, store.ErrIdentityNotFound) {
			return MessageOutput{Handled: true, Reply: "Access denied: link your admin identity first."}, nil
		}
		return MessageOutput{}, err
	}
	if !isAdminRole(identity.Role) {
		return MessageOutput{Handled: true, Reply: "Access denied: admin role required."}, nil
	}

	result, err := s.store.ApprovePairing(ctx, store.ApprovePairingInput{
		Token:          token,
		ApproverUserID: identity.UserID,
		Role:           identity.Role,
	})
	if err != nil {
		if errors.Is(err, store.ErrPairingNotFound) {
			return MessageOutput{Handled: true, Reply: "Pairing token not found."}, nil
		}
		return MessageOutput{}, err
	}
	return MessageOutput{
		Handled: true,
		Reply:   fmt.Sprintf("Pairing approved for `%s` (%s).", result.PairingRequest.DisplayName, result.UserID),
	}, nil
}

func (s *Service) handleDeny(ctx context.Context, input MessageInput, arg string) (MessageOutput, error) {
	token := strings.TrimSpace(arg)
	if token == "" {
		return MessageOutput{Handled: true, Reply: "Usage: /deny <pairing-token> [reason]"}, nil
	}

	parts := strings.Fields(token)
	reason := "denied by admin"
	actualToken := parts[0]
	if len(parts) > 1 {
		reason = strings.Join(parts[1:], " ")
	}

	identity, err := s.store.LookupUserIdentity(ctx, input.Connector, input.FromUserID)
	if err != nil {
		if errors.Is(err, store.ErrIdentityNotFound) {
			return MessageOutput{Handled: true, Reply: "Access denied: link your admin identity first."}, nil
		}
		return MessageOutput{}, err
	}
	if !isAdminRole(identity.Role) {
		return MessageOutput{Handled: true, Reply: "Access denied: admin role required."}, nil
	}

	request, err := s.store.DenyPairing(ctx, store.DenyPairingInput{
		Token:          actualToken,
		ApproverUserID: identity.UserID,
		Reason:         reason,
	})
	if err != nil {
		if errors.Is(err, store.ErrPairingNotFound) {
			return MessageOutput{Handled: true, Reply: "Pairing token not found."}, nil
		}
		return MessageOutput{}, err
	}
	return MessageOutput{
		Handled: true,
		Reply:   fmt.Sprintf("Pairing denied for `%s`.", request.DisplayName),
	}, nil
}

func splitCommand(text string) (string, string) {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return "", ""
	}
	if strings.HasPrefix(trimmed, "/") {
		trimmed = strings.TrimPrefix(trimmed, "/")
	}
	fields := strings.Fields(trimmed)
	if len(fields) == 0 {
		return "", ""
	}
	command := strings.ToLower(fields[0])
	if idx := strings.Index(command, "@"); idx >= 0 {
		command = command[:idx]
	}

	if len(fields) == 1 {
		return command, ""
	}
	argStart := strings.Index(trimmed, " ")
	if argStart < 0 {
		return command, ""
	}
	return command, strings.TrimSpace(trimmed[argStart+1:])
}

func parseIntentTask(text string) (string, bool) {
	trimmed := strings.TrimSpace(text)
	lower := strings.ToLower(trimmed)
	switch {
	case strings.HasPrefix(lower, "task "):
		return strings.TrimSpace(trimmed[len("task "):]), true
	case strings.HasPrefix(lower, "create task "):
		return strings.TrimSpace(trimmed[len("create task "):]), true
	default:
		return "", false
	}
}

func compactSnippet(input string) string {
	text := strings.TrimSpace(input)
	if text == "" {
		return ""
	}
	text = strings.Join(strings.Fields(text), " ")
	if len(text) <= 120 {
		return text
	}
	return text[:120] + "..."
}

func isAdminRole(role string) bool {
	switch strings.ToLower(strings.TrimSpace(role)) {
	case "overlord", "admin":
		return true
	default:
		return false
	}
}
