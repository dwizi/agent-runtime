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
	LookupTask(ctx context.Context, id string) (store.TaskRecord, error)
	UpdateTaskRouting(ctx context.Context, input store.UpdateTaskRoutingInput) (store.TaskRecord, error)
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

type RoutingNotifier interface {
	NotifyRoutingDecision(ctx context.Context, decision RouteDecision)
}

type Service struct {
	store          Store
	engine         Engine
	retriever      Retriever
	actionExecutor ActionExecutor
	triageEnabled  bool
	routingNotify  RoutingNotifier
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

const latestPendingActionAlias = "__latest_pending__"

func New(store Store, engine Engine, retriever Retriever, actionExecutor ActionExecutor) *Service {
	return &Service{
		store:          store,
		engine:         engine,
		retriever:      retriever,
		actionExecutor: actionExecutor,
		triageEnabled:  true,
	}
}

func (s *Service) SetTriageEnabled(enabled bool) {
	s.triageEnabled = enabled
}

func (s *Service) SetRoutingNotifier(notifier RoutingNotifier) {
	s.routingNotify = notifier
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
	case "route":
		return s.handleRouteOverride(ctx, input, arg)
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
		if actionArg, ok := parseApproveCommandAsActionArg(arg); ok {
			return s.handleApproveAction(ctx, input, actionArg)
		}
		return s.handleApprove(ctx, input, arg)
	case "deny":
		if actionArg, ok := parseDenyCommandAsActionArg(arg); ok {
			return s.handleDenyAction(ctx, input, actionArg)
		}
		return s.handleDeny(ctx, input, arg)
	case "pending-actions":
		return s.handlePendingActions(ctx, input)
	case "approve-action":
		return s.handleApproveAction(ctx, input, arg)
	case "deny-action":
		return s.handleDenyAction(ctx, input, arg)
	default:
		if nlCommand, nlArg, ok := parseNaturalLanguageCommand(text); ok {
			switch nlCommand {
			case "task":
				return s.handleTask(ctx, input, nlArg)
			case "search":
				return s.handleSearch(ctx, input, nlArg)
			case "open":
				return s.handleOpen(ctx, input, nlArg)
			case "status":
				return s.handleStatus(ctx, input)
			case "admin-channel":
				return s.handleAdminChannel(ctx, input, nlArg)
			case "prompt":
				return s.handlePrompt(ctx, input, nlArg)
			case "approve":
				return s.handleApprove(ctx, input, nlArg)
			case "deny":
				return s.handleDeny(ctx, input, nlArg)
			case "pending-actions":
				return s.handlePendingActions(ctx, input)
			case "approve-action":
				return s.handleApproveAction(ctx, input, nlArg)
			case "deny-action":
				return s.handleDenyAction(ctx, input, nlArg)
			}
		}
		if err := s.handleAutoTriage(ctx, input, text); err != nil {
			return MessageOutput{}, err
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
	resolveLatest := strings.EqualFold(actionID, latestPendingActionAlias)
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
	if resolveLatest {
		resolved, reply := s.resolveSinglePendingActionID(ctx, input)
		if strings.TrimSpace(reply) != "" {
			return MessageOutput{Handled: true, Reply: reply}, nil
		}
		actionID = resolved
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
	resolveLatest := strings.EqualFold(actionID, latestPendingActionAlias)
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
	if resolveLatest {
		resolved, reply := s.resolveSinglePendingActionID(ctx, input)
		if strings.TrimSpace(reply) != "" {
			return MessageOutput{Handled: true, Reply: reply}, nil
		}
		actionID = resolved
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
	task, err := s.enqueueAndPersistTask(ctx, store.CreateTaskInput{
		WorkspaceID:      contextRecord.WorkspaceID,
		ContextID:        contextRecord.ID,
		Kind:             string(orchestrator.TaskKindGeneral),
		Title:            title,
		Prompt:           prompt,
		Status:           "queued",
		RouteClass:       string(TriageTask),
		Priority:         string(TriagePriorityP2),
		DueAt:            time.Now().UTC().Add(24 * time.Hour),
		AssignedLane:     "operations",
		SourceConnector:  strings.ToLower(strings.TrimSpace(input.Connector)),
		SourceExternalID: strings.TrimSpace(input.ExternalID),
		SourceUserID:     strings.TrimSpace(input.FromUserID),
		SourceText:       prompt,
	})
	if err != nil {
		return MessageOutput{}, err
	}
	return MessageOutput{
		Handled: true,
		Reply:   fmt.Sprintf("Task queued: `%s`", task.ID),
	}, nil
}

func (s *Service) handleAutoTriage(ctx context.Context, input MessageInput, text string) error {
	if !s.triageEnabled {
		return nil
	}
	trimmed := strings.TrimSpace(text)
	if trimmed == "" || strings.HasPrefix(trimmed, "/") {
		return nil
	}
	if s.store == nil || s.engine == nil {
		return nil
	}
	contextRecord, err := s.store.EnsureContextForExternalChannel(ctx, input.Connector, input.ExternalID, input.DisplayName)
	if err != nil {
		return err
	}
	decision := deriveRouteDecision(input, contextRecord.WorkspaceID, contextRecord.ID, trimmed)
	if decision.Class == TriageNoise {
		return nil
	}
	taskTitle := buildRoutedTaskTitle(decision.Class, decision.SourceText)
	taskPrompt := buildRoutedTaskPrompt(decision)
	task, err := s.enqueueAndPersistTask(ctx, store.CreateTaskInput{
		WorkspaceID:      decision.WorkspaceID,
		ContextID:        decision.ContextID,
		Kind:             string(orchestrator.TaskKindGeneral),
		Title:            taskTitle,
		Prompt:           taskPrompt,
		Status:           "queued",
		RouteClass:       string(decision.Class),
		Priority:         string(decision.Priority),
		DueAt:            decision.DueAt,
		AssignedLane:     decision.AssignedLane,
		SourceConnector:  decision.SourceConnector,
		SourceExternalID: decision.SourceExternalID,
		SourceUserID:     decision.SourceUserID,
		SourceText:       decision.SourceText,
	})
	if err != nil {
		return err
	}
	decision.TaskID = task.ID
	if s.routingNotify != nil {
		s.routingNotify.NotifyRoutingDecision(ctx, decision)
	}
	return nil
}

func (s *Service) handleRouteOverride(ctx context.Context, input MessageInput, arg string) (MessageOutput, error) {
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
	policy, err := s.store.LookupContextPolicyByExternal(ctx, input.Connector, input.ExternalID)
	if err != nil {
		return MessageOutput{}, err
	}
	if !policy.IsAdmin {
		return MessageOutput{Handled: true, Reply: "Access denied: route overrides are only allowed in admin channels."}, nil
	}

	fields := strings.Fields(strings.TrimSpace(arg))
	if len(fields) < 2 {
		return MessageOutput{Handled: true, Reply: "Usage: /route <task-id> <question|issue|task|moderation|noise> [p1|p2|p3] [due-window like 2h or 1d]"}, nil
	}
	taskID := strings.TrimSpace(fields[0])
	taskRecord, err := s.store.LookupTask(ctx, taskID)
	if err != nil {
		if errors.Is(err, store.ErrTaskNotFound) {
			return MessageOutput{Handled: true, Reply: "Task not found."}, nil
		}
		return MessageOutput{}, err
	}
	if strings.TrimSpace(taskRecord.WorkspaceID) != "" && strings.TrimSpace(policy.WorkspaceID) != "" &&
		!strings.EqualFold(strings.TrimSpace(taskRecord.WorkspaceID), strings.TrimSpace(policy.WorkspaceID)) {
		return MessageOutput{Handled: true, Reply: "Access denied: task belongs to a different workspace."}, nil
	}
	class, ok := normalizeTriageClass(fields[1])
	if !ok {
		return MessageOutput{Handled: true, Reply: "Invalid route class. Use: question, issue, task, moderation, noise."}, nil
	}
	priority, dueWindow, lane := routingDefaults(class)
	if len(fields) >= 3 {
		overridePriority, priorityOK := normalizeTriagePriority(fields[2])
		if priorityOK {
			priority = overridePriority
		} else {
			parsedWindow, dueErr := parseDueWindow(fields[2])
			if dueErr == nil {
				dueWindow = parsedWindow
			}
		}
	}
	if len(fields) >= 4 {
		parsedWindow, dueErr := parseDueWindow(fields[3])
		if dueErr != nil {
			return MessageOutput{Handled: true, Reply: "Invalid due window. Examples: `2h`, `8h`, `1d`, `2d`."}, nil
		}
		dueWindow = parsedWindow
	}
	if class == TriageNoise {
		priority = TriagePriorityP3
		dueWindow = 0
		lane = "backlog"
	}
	dueAt := time.Time{}
	if dueWindow > 0 {
		dueAt = time.Now().UTC().Add(dueWindow)
	}
	updated, err := s.store.UpdateTaskRouting(ctx, store.UpdateTaskRoutingInput{
		ID:           taskID,
		RouteClass:   string(class),
		Priority:     string(priority),
		DueAt:        dueAt,
		AssignedLane: lane,
	})
	if err != nil {
		if errors.Is(err, store.ErrTaskNotFound) {
			return MessageOutput{Handled: true, Reply: "Task not found."}, nil
		}
		return MessageOutput{}, err
	}
	reply := fmt.Sprintf("Routing updated for `%s`:\n- class: `%s`\n- priority: `%s`\n- lane: `%s`", updated.ID, class, priority, lane)
	if !dueAt.IsZero() {
		reply += fmt.Sprintf("\n- due: `%s`", dueAt.UTC().Format(time.RFC3339))
	} else {
		reply += "\n- due: `(none)`"
	}
	return MessageOutput{Handled: true, Reply: reply}, nil
}

func (s *Service) enqueueAndPersistTask(ctx context.Context, input store.CreateTaskInput) (orchestrator.Task, error) {
	task, err := s.engine.Enqueue(orchestrator.Task{
		WorkspaceID: strings.TrimSpace(input.WorkspaceID),
		ContextID:   strings.TrimSpace(input.ContextID),
		Kind:        orchestrator.TaskKind(strings.TrimSpace(input.Kind)),
		Title:       strings.TrimSpace(input.Title),
		Prompt:      strings.TrimSpace(input.Prompt),
	})
	if err != nil {
		return orchestrator.Task{}, err
	}
	input.ID = task.ID
	input.WorkspaceID = task.WorkspaceID
	input.ContextID = task.ContextID
	input.Kind = string(task.Kind)
	input.Title = task.Title
	input.Prompt = task.Prompt
	if strings.TrimSpace(input.Status) == "" {
		input.Status = "queued"
	}
	if err := s.store.CreateTask(ctx, input); err != nil {
		return orchestrator.Task{}, err
	}
	return task, nil
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
	if trimmed == "" {
		return "", false
	}
	lower := strings.ToLower(trimmed)
	phrases := []string{
		"task ",
		"create task ",
		"create a task ",
		"create a task to ",
		"please create a task ",
		"please create a task to ",
		"add task ",
		"add a task ",
		"queue task ",
		"queue a task ",
	}
	for _, phrase := range phrases {
		if !strings.HasPrefix(lower, phrase) {
			continue
		}
		value := strings.TrimSpace(trimmed[len(phrase):])
		if value == "" {
			return "", false
		}
		return value, true
	}
	return "", false
}

func parseApproveCommandAsActionArg(arg string) (string, bool) {
	trimmed := strings.TrimSpace(arg)
	if trimmed == "" {
		return "", false
	}
	lower := strings.ToLower(trimmed)
	if actionID, ok := findActionID(trimmed); ok {
		return actionID, true
	}
	if lower == "it" || lower == "this" || lower == "that" || lower == "action" {
		return latestPendingActionAlias, true
	}
	if strings.Contains(lower, "approve action") || strings.Contains(lower, "the action") || strings.Contains(lower, "approved action") {
		return latestPendingActionAlias, true
	}
	return "", false
}

func parseDenyCommandAsActionArg(arg string) (string, bool) {
	trimmed := strings.TrimSpace(arg)
	if trimmed == "" {
		return "", false
	}
	lower := strings.ToLower(trimmed)
	if actionID, _, end, ok := findActionIDWithBounds(trimmed); ok {
		reason := strings.TrimSpace(trimmed[end:])
		reason = normalizeDenyReason(reason)
		if reason == "" {
			return actionID, true
		}
		return actionID + " " + reason, true
	}
	if lower == "it" || lower == "this" || lower == "that" || strings.HasPrefix(lower, "it ") ||
		strings.HasPrefix(lower, "this ") || strings.HasPrefix(lower, "that ") || strings.Contains(lower, "action") {
		reason := trimmed
		reasonLower := lower
		for _, marker := range []string{"it ", "this ", "that ", "because ", "reason ", "for "} {
			index := strings.Index(reasonLower, marker)
			if index < 0 {
				continue
			}
			value := strings.TrimSpace(reason[index+len(marker):])
			value = normalizeDenyReason(value)
			if value == "" {
				continue
			}
			return latestPendingActionAlias + " " + value, true
		}
		return latestPendingActionAlias, true
	}
	return "", false
}

func parseNaturalLanguageCommand(text string) (command, arg string, ok bool) {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return "", "", false
	}
	lower := strings.ToLower(trimmed)

	if actionID, found := parseIntentApproveAction(trimmed); found {
		return "approve-action", actionID, true
	}
	if actionArg, found := parseIntentDenyAction(trimmed); found {
		return "deny-action", actionArg, true
	}
	if isImplicitApproveActionIntent(lower) {
		return "approve-action", latestPendingActionAlias, true
	}
	if denyReason, found := parseImplicitDenyActionReason(trimmed, lower); found {
		if denyReason == "" {
			return "deny-action", latestPendingActionAlias, true
		}
		return "deny-action", latestPendingActionAlias + " " + denyReason, true
	}
	if token, found := parseIntentApprovePairing(trimmed); found {
		return "approve", token, true
	}
	if denyArg, found := parseIntentDenyPairing(trimmed); found {
		return "deny", denyArg, true
	}
	if isPendingActionsIntent(lower) {
		return "pending-actions", "", true
	}
	if strings.Contains(lower, "admin channel") && strings.Contains(lower, "enable") {
		return "admin-channel", "enable", true
	}
	if promptArg, found := parsePromptIntent(trimmed, lower); found {
		return "prompt", promptArg, true
	}
	if query, found := parseSearchIntent(trimmed, lower); found {
		return "search", query, true
	}
	if target, found := parseOpenIntent(trimmed, lower); found {
		return "open", target, true
	}
	if isStatusIntent(lower) {
		return "status", "", true
	}
	if prompt, found := parseIntentTask(trimmed); found {
		return "task", prompt, true
	}
	return "", "", false
}

func isImplicitApproveActionIntent(lower string) bool {
	if !(strings.Contains(lower, "approve") || strings.Contains(lower, "approved")) {
		return false
	}
	if strings.Contains(lower, "pair") || strings.Contains(lower, "token") {
		return false
	}
	if strings.Contains(lower, "approve action") || strings.Contains(lower, "approve the action") {
		return true
	}
	return strings.Contains(lower, "approve it") ||
		strings.Contains(lower, "approve this") ||
		strings.Contains(lower, "approve that") ||
		strings.Contains(lower, "yes i approve")
}

func parseImplicitDenyActionReason(trimmed, lower string) (string, bool) {
	hasDeny := strings.Contains(lower, "deny") || strings.Contains(lower, "reject") || strings.Contains(lower, "decline")
	if !hasDeny {
		return "", false
	}
	if strings.Contains(lower, "pair") || strings.Contains(lower, "token") {
		return "", false
	}
	if !(strings.Contains(lower, "deny action") ||
		strings.Contains(lower, "reject action") ||
		strings.Contains(lower, "deny it") ||
		strings.Contains(lower, "reject it") ||
		strings.Contains(lower, "decline it") ||
		strings.Contains(lower, "deny this") ||
		strings.Contains(lower, "reject this") ||
		strings.Contains(lower, "decline this")) {
		return "", false
	}
	reason := trimmed
	reasonLower := lower
	for _, marker := range []string{"because ", "reason ", "for "} {
		index := strings.Index(reasonLower, marker)
		if index < 0 {
			continue
		}
		value := strings.TrimSpace(reason[index+len(marker):])
		value = normalizeDenyReason(value)
		return value, true
	}
	return "", true
}

func isPendingActionsIntent(lower string) bool {
	return strings.Contains(lower, "pending action") || strings.Contains(lower, "pending approval")
}

func parsePromptIntent(trimmed, lower string) (string, bool) {
	if strings.Contains(lower, "show prompt") || strings.Contains(lower, "prompt show") {
		return "show", true
	}
	if strings.Contains(lower, "clear prompt") || strings.Contains(lower, "prompt clear") {
		return "clear", true
	}
	for _, phrase := range []string{"set prompt", "update prompt"} {
		index := strings.Index(lower, phrase)
		if index < 0 {
			continue
		}
		value := strings.TrimSpace(trimmed[index+len(phrase):])
		if strings.HasPrefix(strings.ToLower(value), "to ") {
			value = strings.TrimSpace(value[len("to "):])
		}
		if value == "" {
			return "", false
		}
		return "set " + value, true
	}
	return "", false
}

func parseSearchIntent(trimmed, lower string) (string, bool) {
	for _, phrase := range []string{"search for ", "search docs for ", "find in docs ", "find docs for "} {
		if strings.HasPrefix(lower, phrase) {
			value := strings.TrimSpace(trimmed[len(phrase):])
			if value == "" {
				return "", false
			}
			return value, true
		}
	}
	if strings.HasPrefix(lower, "search ") {
		value := strings.TrimSpace(trimmed[len("search "):])
		if value == "" || value == "status" {
			return "", false
		}
		return value, true
	}
	return "", false
}

func parseOpenIntent(trimmed, lower string) (string, bool) {
	for _, phrase := range []string{"open file ", "open doc ", "open markdown ", "show file "} {
		if strings.HasPrefix(lower, phrase) {
			target := sanitizeOpenTarget(trimmed[len(phrase):])
			if target == "" {
				return "", false
			}
			return target, true
		}
	}
	if strings.HasPrefix(lower, "open ") {
		target := sanitizeOpenTarget(trimmed[len("open "):])
		if target == "" {
			return "", false
		}
		return target, true
	}
	return "", false
}

func sanitizeOpenTarget(value string) string {
	trimmed := strings.TrimSpace(value)
	trimmed = strings.Trim(trimmed, "`\"'")
	trimmed = strings.Trim(trimmed, " .,:;!?")
	return trimmed
}

func isStatusIntent(lower string) bool {
	if lower == "status" {
		return true
	}
	return strings.Contains(lower, "qmd status") ||
		strings.Contains(lower, "index status") ||
		strings.Contains(lower, "search index status")
}

func parseIntentApproveAction(text string) (string, bool) {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return "", false
	}
	lower := strings.ToLower(trimmed)
	if !strings.Contains(lower, "approve") {
		return "", false
	}
	if strings.Contains(lower, "deny") || strings.Contains(lower, "reject") || strings.Contains(lower, "decline") {
		return "", false
	}
	actionID, ok := findActionID(trimmed)
	if !ok {
		return "", false
	}
	return actionID, true
}

func parseIntentDenyAction(text string) (string, bool) {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return "", false
	}
	lower := strings.ToLower(trimmed)
	hasDenyVerb := strings.Contains(lower, "deny") || strings.Contains(lower, "reject") || strings.Contains(lower, "decline")
	if !hasDenyVerb {
		return "", false
	}
	actionID, _, end, ok := findActionIDWithBounds(trimmed)
	if !ok {
		return "", false
	}
	reason := strings.TrimSpace(trimmed[end:])
	reason = normalizeDenyReason(reason)
	if reason == "" {
		return actionID, true
	}
	return actionID + " " + reason, true
}

func parseIntentApprovePairing(text string) (string, bool) {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return "", false
	}
	lower := strings.ToLower(trimmed)
	if !strings.Contains(lower, "approve") {
		return "", false
	}
	if strings.Contains(lower, "deny") || strings.Contains(lower, "reject") || strings.Contains(lower, "decline") {
		return "", false
	}
	token, _, _, ok := findPairingTokenWithBounds(trimmed)
	if !ok {
		return "", false
	}
	return token, true
}

func parseIntentDenyPairing(text string) (string, bool) {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return "", false
	}
	lower := strings.ToLower(trimmed)
	if !(strings.Contains(lower, "deny") || strings.Contains(lower, "reject") || strings.Contains(lower, "decline")) {
		return "", false
	}
	token, _, end, ok := findPairingTokenWithBounds(trimmed)
	if !ok {
		return "", false
	}
	reason := strings.TrimSpace(trimmed[end:])
	reason = normalizeDenyReason(reason)
	if reason == "" {
		return token, true
	}
	return token + " " + reason, true
}

func normalizeDenyReason(value string) string {
	reason := strings.TrimSpace(value)
	reason = strings.Trim(reason, " .,:;!?-")
	reasonLower := strings.ToLower(reason)
	switch {
	case strings.HasPrefix(reasonLower, "because "):
		reason = strings.TrimSpace(reason[len("because "):])
	case strings.HasPrefix(reasonLower, "reason "):
		reason = strings.TrimSpace(reason[len("reason "):])
	case strings.HasPrefix(reasonLower, "for "):
		reason = strings.TrimSpace(reason[len("for "):])
	}
	return strings.TrimSpace(strings.Trim(reason, " .,:;!?-"))
}

func (s *Service) resolveSinglePendingActionID(ctx context.Context, input MessageInput) (string, string) {
	items, err := s.store.ListPendingActionApprovals(ctx, input.Connector, input.ExternalID, 2)
	if err != nil {
		return "", "Unable to load pending actions right now."
	}
	if len(items) == 0 {
		return "", "No pending actions in this context."
	}
	if len(items) > 1 {
		return "", "Multiple pending actions found. Use `/pending-actions` and approve by id."
	}
	return strings.TrimSpace(items[0].ID), ""
}

func findActionID(text string) (string, bool) {
	actionID, _, _, ok := findActionIDWithBounds(text)
	return actionID, ok
}

func findActionIDWithBounds(text string) (actionID string, start int, end int, ok bool) {
	lower := strings.ToLower(text)
	const prefix = "act_"
	search := 0
	for {
		offset := strings.Index(lower[search:], prefix)
		if offset < 0 {
			return "", 0, 0, false
		}
		start = search + offset
		end = start + len(prefix)
		for end < len(lower) {
			ch := lower[end]
			if (ch >= 'a' && ch <= 'z') || (ch >= '0' && ch <= '9') || ch == '-' || ch == '_' {
				end++
				continue
			}
			break
		}
		if end-start < len(prefix)+4 {
			search = start + len(prefix)
			continue
		}
		return strings.Trim(lower[start:end], "`"), start, end, true
	}
}

func findPairingTokenWithBounds(text string) (token string, start int, end int, ok bool) {
	lower := strings.ToLower(text)
	contextHint := strings.Contains(lower, "pair") || strings.Contains(lower, "token")
	search := 0
	for {
		start = nextAlphaNumericStart(text, search)
		if start < 0 {
			return "", 0, 0, false
		}
		end = start
		for end < len(text) && isASCIIAlphaNumeric(text[end]) {
			end++
		}
		candidate := text[start:end]
		lowerCandidate := strings.ToLower(candidate)
		if isLikelyPairingToken(candidate, lowerCandidate, contextHint) {
			return strings.ToUpper(candidate), start, end, true
		}
		search = end + 1
	}
}

func nextAlphaNumericStart(text string, from int) int {
	for index := from; index < len(text); index++ {
		if isASCIIAlphaNumeric(text[index]) {
			return index
		}
	}
	return -1
}

func isLikelyPairingToken(candidate, lowerCandidate string, contextHint bool) bool {
	if len(candidate) < 8 || len(candidate) > 64 {
		return false
	}
	if strings.HasPrefix(lowerCandidate, "act_") {
		return false
	}
	switch lowerCandidate {
	case "approve", "approved", "approval", "action", "pair", "pairing", "token", "please", "deny", "denied", "reject", "rejected", "decline", "because", "reason":
		return false
	}
	if contextHint {
		return true
	}
	return hasASCIIDigit(candidate) || candidate == strings.ToUpper(candidate)
}

func hasASCIIDigit(value string) bool {
	for index := 0; index < len(value); index++ {
		if value[index] >= '0' && value[index] <= '9' {
			return true
		}
	}
	return false
}

func isASCIIAlphaNumeric(value byte) bool {
	return (value >= 'a' && value <= 'z') ||
		(value >= 'A' && value <= 'Z') ||
		(value >= '0' && value <= '9')
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
