package gateway

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/dwizi/agent-runtime/internal/actions/executor"
	"github.com/dwizi/agent-runtime/internal/agent"
	"github.com/dwizi/agent-runtime/internal/agent/tools"
	"github.com/dwizi/agent-runtime/internal/llm"
	"github.com/dwizi/agent-runtime/internal/orchestrator"
	"github.com/dwizi/agent-runtime/internal/qmd"
	"github.com/dwizi/agent-runtime/internal/store"
)

type Store interface {
	EnsureContextForExternalChannel(ctx context.Context, connector, externalID, displayName string) (store.ContextRecord, error)
	SetContextAdminByExternal(ctx context.Context, connector, externalID string, enabled bool) (store.ContextRecord, error)
	LookupContextPolicyByExternal(ctx context.Context, connector, externalID string) (store.ContextPolicy, error)
	SetContextSystemPromptByExternal(ctx context.Context, connector, externalID, prompt string) (store.ContextPolicy, error)
	LookupUserIdentity(ctx context.Context, connector, connectorUserID string) (store.UserIdentity, error)
	CreateTask(ctx context.Context, input store.CreateTaskInput) error
	LookupTask(ctx context.Context, id string) (store.TaskRecord, error)
	MarkTaskCompleted(ctx context.Context, id string, finishedAt time.Time, summary, resultPath string) error
	UpdateTaskRouting(ctx context.Context, input store.UpdateTaskRoutingInput) (store.TaskRecord, error)
	ApprovePairing(ctx context.Context, input store.ApprovePairingInput) (store.ApprovePairingResult, error)
	DenyPairing(ctx context.Context, input store.DenyPairingInput) (store.PairingRequest, error)
	CreateActionApproval(ctx context.Context, input store.CreateActionApprovalInput) (store.ActionApproval, error)
	ListPendingActionApprovals(ctx context.Context, connector, externalID string, limit int) ([]store.ActionApproval, error)
	ListPendingActionApprovalsGlobal(ctx context.Context, limit int) ([]store.ActionApproval, error)
	ApproveActionApproval(ctx context.Context, input store.ApproveActionApprovalInput) (store.ActionApproval, error)
	DenyActionApproval(ctx context.Context, input store.DenyActionApprovalInput) (store.ActionApproval, error)
	UpdateActionExecution(ctx context.Context, input store.UpdateActionExecutionInput) (store.ActionApproval, error)
	CreateObjective(ctx context.Context, input store.CreateObjectiveInput) (store.Objective, error)
	UpdateObjective(ctx context.Context, input store.UpdateObjectiveInput) (store.Objective, error)
	CreateAgentAuditEvent(ctx context.Context, input store.CreateAgentAuditEventInput) (store.AgentAuditEvent, error)
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
	store                   Store
	engine                  Engine
	retriever               Retriever
	actionExecutor          ActionExecutor
	agent                   *agent.Agent
	toolRegistry            *tools.Registry
	reasoningPromptTemplate string
	workspaceRoot           string
	agentMaxTurnDuration    time.Duration
	agentGroundingFirstStep bool
	agentGroundingEveryStep bool
	triageAcknowledger      llm.Responder
	triageEnabled           bool
	routingNotify           RoutingNotifier
	approvalMu              sync.Mutex
	sensitiveApprovals      map[string]time.Time
	sensitiveApprovalTTL    time.Duration
	logger                  *slog.Logger
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
const mostRecentPendingActionAlias = "__most_recent_pending__"
const allPendingActionsAlias = "__all_pending__"

func New(store Store, engine Engine, retriever Retriever, actionExecutor ActionExecutor, workspaceRoot string, logger *slog.Logger) *Service {
	if logger == nil {
		logger = slog.Default()
	}
	registry := tools.NewRegistry()
	registry.Register(NewSearchTool(retriever))
	registry.Register(NewOpenKnowledgeDocumentTool(retriever))
	registry.Register(NewCreateTaskTool(store, engine))
	registry.Register(NewModerationTriageTool())
	registry.Register(NewDraftEscalationTool())
	registry.Register(NewDraftFAQAnswerTool())
	registry.Register(NewCreateObjectiveTool(store))
	registry.Register(NewUpdateObjectiveTool(store))
	registry.Register(NewUpdateTaskTool(store))
	registry.Register(NewLearnSkillTool(workspaceRoot))
	registry.Register(NewRunActionTool(store, actionExecutor))
	registry.Register(NewWriteFileTool(workspaceRoot))
	registry.Register(NewReadFileTool(workspaceRoot))
	registry.Register(NewListFilesTool(workspaceRoot))
	registry.Register(NewCurlTool(store, actionExecutor))
	registry.Register(NewFetchUrlTool(store, actionExecutor))
	registry.Register(NewInspectFileTool(store, actionExecutor, workspaceRoot))
	registry.Register(NewLookupTaskTool(store))
	registry.Register(NewWebSearchTool(store, actionExecutor))
	registry.Register(NewPythonCodeTool(store, actionExecutor, workspaceRoot))

	return &Service{
		store:                   store,
		engine:                  engine,
		retriever:               retriever,
		actionExecutor:          actionExecutor,
		toolRegistry:            registry,
		workspaceRoot:           workspaceRoot,
		agentGroundingFirstStep: true,
		triageEnabled:           true,
		sensitiveApprovals:      map[string]time.Time{},
		sensitiveApprovalTTL:    10 * time.Minute,
		logger:                  logger,
	}
}

func (s *Service) Registry() *tools.Registry {
	return s.toolRegistry
}

func (s *Service) SetTriageEnabled(enabled bool) {
	s.triageEnabled = enabled
}

func (s *Service) SetSensitiveApprovalTTL(ttl time.Duration) {
	if ttl <= 0 {
		return
	}
	s.approvalMu.Lock()
	defer s.approvalMu.Unlock()
	s.sensitiveApprovalTTL = ttl
}

func (s *Service) SetAgentMaxTurnDuration(duration time.Duration) {
	s.agentMaxTurnDuration = duration
	s.applyAgentConfig()
}

func (s *Service) SetAgentGroundingPolicy(firstStep, everyStep bool) {
	s.agentGroundingFirstStep = firstStep
	s.agentGroundingEveryStep = everyStep
	s.applyAgentConfig()
}

func (s *Service) SetReasoningPromptTemplate(template string) {
	s.reasoningPromptTemplate = template
	if s.triageAcknowledger != nil {
		s.agent = agent.New(s.logger.With("component", "agent"), s.triageAcknowledger, s.toolRegistry, s.reasoningPromptTemplate)
		s.applyAgentConfig()
	}
}

func (s *Service) SetTriageAcknowledger(responder llm.Responder) {
	s.triageAcknowledger = responder
	if responder != nil {
		s.agent = agent.New(s.logger.With("component", "agent"), responder, s.toolRegistry, s.reasoningPromptTemplate)
		s.applyAgentConfig()
	}
}

func (s *Service) applyAgentConfig() {
	if s == nil || s.agent == nil {
		return
	}
	if s.agentMaxTurnDuration > 0 {
		s.agent.SetDefaultPolicy(agent.Policy{MaxTurnDuration: s.agentMaxTurnDuration})
	}
	s.agent.SetGroundingPolicy(s.agentGroundingFirstStep, s.agentGroundingEveryStep)
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
	case "monitor":
		return s.handleMonitorObjective(ctx, input, arg)
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
		if output, handled, err := s.handleCommandGuidance(ctx, input, text); handled || err != nil {
			return output, err
		}
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
			case "monitor":
				return s.handleMonitorObjective(ctx, input, nlArg)
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
		triageOutput, err := s.handleAutoTriage(ctx, input, text)
		if err != nil {
			return MessageOutput{}, err
		}
		return triageOutput, nil
	}
}

func (s *Service) handleCommandGuidance(ctx context.Context, input MessageInput, text string) (MessageOutput, bool, error) {
	lower := strings.ToLower(strings.TrimSpace(text))
	if reply, ok := pendingApprovalGuidanceReply(lower); ok {
		return MessageOutput{
			Handled: true,
			Reply:   reply,
		}, true, nil
	}
	if !looksLikeCommandGuidanceQuestion(lower) {
		return MessageOutput{}, false, nil
	}

	if strings.Contains(lower, "pending action") || strings.Contains(lower, "pending approval") {
		return MessageOutput{
			Handled: true,
			Reply:   "Use `/pending-actions` to list pending approvals.",
		}, true, nil
	}

	if strings.Contains(lower, "approval") || strings.Contains(lower, "approve") {
		reply, err := s.buildApprovalNextCommandGuidance(ctx, input)
		if err != nil {
			return MessageOutput{}, false, err
		}
		if strings.TrimSpace(reply) != "" {
			return MessageOutput{
				Handled: true,
				Reply:   reply,
			}, true, nil
		}
	}
	return MessageOutput{}, false, nil
}

func looksLikeCommandGuidanceQuestion(lower string) bool {
	if lower == "" || strings.HasPrefix(lower, "/") {
		return false
	}
	guidancePhrases := []string{
		"what command",
		"which command",
		"exact command",
		"exact next command",
		"what should i run",
		"what do i run",
		"tell me the command",
		"next command i should run",
	}
	for _, phrase := range guidancePhrases {
		if strings.Contains(lower, phrase) {
			return true
		}
	}
	return strings.Contains(lower, "if approval is needed")
}

func pendingApprovalGuidanceReply(lower string) (string, bool) {
	if lower == "" {
		return "", false
	}
	mentionsPendingApprovals := strings.Contains(lower, "pending approval") || strings.Contains(lower, "pending action")
	if !mentionsPendingApprovals {
		return "", false
	}
	if strings.Contains(lower, "without using slash") ||
		strings.Contains(lower, "plain language") ||
		strings.Contains(lower, "how can i ask") ||
		strings.Contains(lower, "how do i ask") ||
		strings.HasPrefix(lower, "how ") {
		return "Use plain language like: \"show me pending approvals\" or \"what approvals are waiting right now?\". If you need to approve one, say: \"approve action <id>\" or \"approve the most recent pending action\".", true
	}
	if strings.Contains(lower, "many pending") ||
		(strings.Contains(lower, "what should i do first") && mentionsPendingApprovals) {
		return "First, list pending approvals and prioritize by risk and urgency: security-impacting actions first, then oldest blocked user requests. Approve one action at a time, confirm outcome, then move to the next.", true
	}
	return "", false
}

func (s *Service) buildApprovalNextCommandGuidance(ctx context.Context, input MessageInput) (string, error) {
	items, err := s.store.ListPendingActionApprovals(ctx, input.Connector, input.ExternalID, 5)
	if err != nil {
		return "", err
	}
	scope := "this context"
	if len(items) == 0 {
		items, err = s.store.ListPendingActionApprovalsGlobal(ctx, 5)
		if err != nil {
			return "", err
		}
		scope = "all contexts"
	}

	if len(items) == 0 {
		return "No pending action approvals right now. After an action is queued, run `/pending-actions` and then `/approve-action <action-id>`.", nil
	}

	if len(items) > 1 {
		return fmt.Sprintf("Multiple pending actions found in %s. Run `/pending-actions`, then execute `/approve-action <action-id>` for the one you want.", scope), nil
	}

	actionID := strings.TrimSpace(items[0].ID)
	if actionID == "" {
		return "Run `/pending-actions`, then execute `/approve-action <action-id>`.", nil
	}

	identity, err := s.store.LookupUserIdentity(ctx, input.Connector, input.FromUserID)
	if err != nil {
		if errors.Is(err, store.ErrIdentityNotFound) {
			return fmt.Sprintf("Pending action found: `%s`.\nNext:\n1) Link your admin identity by sending `pair` and completing approval.\n2) Run `/approve-action %s`.\nUse `/pending-actions` to verify.", actionID, actionID), nil
		}
		return "", err
	}
	if !isAdminRole(identity.Role) {
		return fmt.Sprintf("Pending action found: `%s`.\nYou do not have admin approval rights in this context. Ask an admin to run `/approve-action %s`.\nUse `/pending-actions` to verify.", actionID, actionID), nil
	}
	return fmt.Sprintf("Run `/approve-action %s`.\nUse `/pending-actions` if you want to review all pending approvals first.", actionID), nil
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
	showAllContexts := false
	if len(items) == 0 {
		items, err = s.store.ListPendingActionApprovalsGlobal(ctx, 10)
		if err != nil {
			return MessageOutput{}, err
		}
		showAllContexts = true
	}
	if len(items) == 0 {
		return MessageOutput{Handled: true, Reply: "No pending actions."}, nil
	}
	header := "Pending actions:"
	if showAllContexts {
		header = "Pending actions (all contexts):"
	}
	lines := []string{header}
	for _, item := range items {
		summary := strings.TrimSpace(item.ActionSummary)
		if summary == "" {
			summary = item.ActionType
		}
		line := fmt.Sprintf("- `%s` %s (%s)", item.ID, summary, item.ActionType)
		if showAllContexts {
			connector := strings.TrimSpace(item.Connector)
			externalID := strings.TrimSpace(item.ExternalID)
			if connector == "" {
				connector = "unknown"
			}
			if externalID == "" {
				externalID = "unknown"
			}
			line = fmt.Sprintf("%s [%s/%s]", line, connector, externalID)
		}
		lines = append(lines, line)
	}
	return MessageOutput{Handled: true, Reply: strings.Join(lines, "\n")}, nil
}

func (s *Service) handleApproveAction(ctx context.Context, input MessageInput, arg string) (MessageOutput, error) {
	actionID := normalizeActionCommandID(arg)
	resolveLatest := strings.EqualFold(actionID, latestPendingActionAlias)
	resolveMostRecent := strings.EqualFold(actionID, mostRecentPendingActionAlias)
	resolveAll := strings.EqualFold(actionID, allPendingActionsAlias)

	if actionID == "" {
		return MessageOutput{Handled: true, Reply: "Usage: /approve-action <action-id> or 'approve all'"}, nil
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

	if resolveAll {
		// List all pending actions for this context
		items, err := s.store.ListPendingActionApprovals(ctx, input.Connector, input.ExternalID, 50)
		if err != nil {
			return MessageOutput{}, err
		}
		if len(items) == 0 {
			// Fallback to global if empty? Or just say none.
			// Let's check global too if context is empty, similar to pending-actions command.
			items, err = s.store.ListPendingActionApprovalsGlobal(ctx, 50)
			if err != nil {
				return MessageOutput{}, err
			}
		}
		if len(items) == 0 {
			return MessageOutput{Handled: true, Reply: "No pending actions to approve."}, nil
		}

		successCount := 0
		failures := []string{}
		results := []string{}

		for _, item := range items {
			res, _, err := s.approveAndExecuteAction(ctx, input, item.ID, identity.UserID)
			if err != nil {
				failures = append(failures, fmt.Sprintf("%s: %v", item.ID, err))
			} else {
				successCount++
				if res != nil {
					results = append(results, fmt.Sprintf("Action `%s` output:\n%s", item.ID, res.Message))
				}
			}
		}

		if successCount > 0 && s.agent != nil {
			contextRecord, err := s.store.EnsureContextForExternalChannel(ctx, input.Connector, input.ExternalID, input.DisplayName)
			if err == nil {
				agentPrompt := fmt.Sprintf("APPROVED ACTIONS EXECUTED.\n\n%s\n\nInterpret these results for the user.", strings.Join(results, "\n\n"))

				agentCtx := context.WithValue(ctx, ContextKeyRecord, contextRecord)
				agentCtx = context.WithValue(agentCtx, ContextKeyInput, input)
				// Grant sensitive approval for follow-up actions (if any)
				s.grantSensitiveToolApproval(input, time.Now().UTC())
				agentCtx = agent.WithSensitiveToolApproval(agentCtx)

				agentRes := s.agent.Execute(agentCtx, llm.MessageInput{
					Connector:   input.Connector,
					WorkspaceID: contextRecord.WorkspaceID,
					ContextID:   contextRecord.ID,
					ExternalID:  input.ExternalID,
					DisplayName: input.DisplayName,
					FromUserID:  input.FromUserID,
					Text:        agentPrompt,
				})

				if agentRes.Error == nil && strings.TrimSpace(agentRes.Reply) != "" {
					return MessageOutput{Handled: true, Reply: agentRes.Reply}, nil
				}
			}
		}

		reply := fmt.Sprintf("Approved %d actions.", successCount)
		if len(failures) > 0 {
			reply += fmt.Sprintf("\nFailed: %d\n%s", len(failures), strings.Join(failures, "\n"))
		}
		return MessageOutput{Handled: true, Reply: reply}, nil
	}

	if resolveLatest {
		resolved, reply := s.resolveSinglePendingActionID(ctx, input)
		if strings.TrimSpace(reply) != "" {
			return MessageOutput{Handled: true, Reply: reply}, nil
		}
		actionID = resolved
	}
	if resolveMostRecent {
		resolved, reply := s.resolveMostRecentPendingActionID(ctx, input)
		if strings.TrimSpace(reply) != "" {
			return MessageOutput{Handled: true, Reply: reply}, nil
		}
		actionID = resolved
	}

	res, reply, err := s.approveAndExecuteAction(ctx, input, actionID, identity.UserID)
	if err != nil {
		if errors.Is(err, store.ErrActionApprovalNotFound) {
			return MessageOutput{Handled: true, Reply: "Action approval not found."}, nil
		}
		if errors.Is(err, store.ErrActionApprovalNotReady) {
			return MessageOutput{Handled: true, Reply: "Action approval is not pending."}, nil
		}
		return MessageOutput{}, err
	}

	if res != nil && s.agent != nil {
		contextRecord, err := s.store.EnsureContextForExternalChannel(ctx, input.Connector, input.ExternalID, input.DisplayName)
		if err == nil {
			agentPrompt := fmt.Sprintf("APPROVED ACTION EXECUTED.\nAction: %s\nResult: %s\n\nInterpret this result for the user.", actionID, res.Message)

			agentCtx := context.WithValue(ctx, ContextKeyRecord, contextRecord)
			agentCtx = context.WithValue(agentCtx, ContextKeyInput, input)
			// Grant sensitive approval for follow-up actions (if any)
			s.grantSensitiveToolApproval(input, time.Now().UTC())
			agentCtx = agent.WithSensitiveToolApproval(agentCtx)

			agentRes := s.agent.Execute(agentCtx, llm.MessageInput{
				Connector:   input.Connector,
				WorkspaceID: contextRecord.WorkspaceID,
				ContextID:   contextRecord.ID,
				ExternalID:  input.ExternalID,
				DisplayName: input.DisplayName,
				FromUserID:  input.FromUserID,
				Text:        agentPrompt,
			})

			if agentRes.Error == nil && strings.TrimSpace(agentRes.Reply) != "" {
				return MessageOutput{Handled: true, Reply: agentRes.Reply}, nil
			}
		}
	}

	return MessageOutput{Handled: true, Reply: reply}, nil
}

func (s *Service) approveAndExecuteAction(ctx context.Context, input MessageInput, actionID, approverID string) (*executor.Result, string, error) {
	record, err := s.store.ApproveActionApproval(ctx, store.ApproveActionApprovalInput{
		ID:             actionID,
		ApproverUserID: approverID,
	})
	if err != nil {
		return nil, "", err
	}
	s.grantSensitiveToolApproval(input, time.Now().UTC())

	if s.actionExecutor == nil {
		record, err = s.store.UpdateActionExecution(ctx, store.UpdateActionExecutionInput{
			ID:               record.ID,
			ExecutionStatus:  "skipped",
			ExecutionMessage: "approved but no action executor is configured",
			ExecutorPlugin:   "",
			ExecutedAt:       time.Now().UTC(),
		})
		if err != nil {
			return nil, "", err
		}
		return nil, formatActionExecutionReply(record), nil
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
			return nil, "", err
		}
		return &executionResult, formatActionExecutionReply(record), nil
	}

	record, err = s.store.UpdateActionExecution(ctx, store.UpdateActionExecutionInput{
		ID:               record.ID,
		ExecutionStatus:  "succeeded",
		ExecutionMessage: executionResult.Message,
		ExecutorPlugin:   executionResult.Plugin,
		ExecutedAt:       time.Now().UTC(),
	})
	if err != nil {
		return nil, "", err
	}
	if strings.TrimSpace(record.ExecutionMessage) == "" {
		record.ExecutionMessage = "executed successfully"
	}
	return &executionResult, formatActionExecutionReply(record), nil
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
	actionID := normalizeActionCommandID(parts[0])
	if actionID == "" {
		return MessageOutput{Handled: true, Reply: "Usage: /deny-action <action-id> [reason]"}, nil
	}
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

func (s *Service) handleMonitorObjective(ctx context.Context, input MessageInput, arg string) (MessageOutput, error) {
	goal := strings.TrimSpace(arg)
	if goal == "" {
		return MessageOutput{Handled: true, Reply: "Usage: /monitor <what to track>"}, nil
	}
	if s.store == nil {
		return MessageOutput{Handled: true, Reply: "Monitoring objectives are unavailable in this runtime."}, nil
	}
	contextRecord, err := s.store.EnsureContextForExternalChannel(ctx, input.Connector, input.ExternalID, input.DisplayName)
	if err != nil {
		return MessageOutput{}, err
	}
	title := "Monitor: " + compactSnippet(goal)
	if len(title) > 72 {
		title = title[:72]
	}
	objectivePrompt := strings.TrimSpace("Monitor this target for updates and report only concrete changes:\n" + goal)
	active := true
	_, err = s.store.CreateObjective(ctx, store.CreateObjectiveInput{
		WorkspaceID: contextRecord.WorkspaceID,
		ContextID:   contextRecord.ID,
		Title:       title,
		Prompt:      objectivePrompt,
		TriggerType: store.ObjectiveTriggerSchedule,
		CronExpr:    defaultObjectiveCronExpr,
		Active:      &active,
	})
	if err != nil {
		return MessageOutput{}, err
	}
	return MessageOutput{
		Handled: true,
		Reply:   "Monitoring objective created. I’ll keep checking and report updates until you pause or delete it.",
	}, nil
}

func (s *Service) handleAutoTriage(ctx context.Context, input MessageInput, text string) (MessageOutput, error) {
	if !s.triageEnabled {
		return MessageOutput{}, nil
	}
	if s.agent != nil {
		return s.handleAgentAutoTriage(ctx, input, text), nil
	}
	return s.handleLegacyAutoTriage(ctx, input, text)
}

func (s *Service) handleAgentAutoTriage(ctx context.Context, input MessageInput, text string) MessageOutput {
	if s.agent == nil {
		return MessageOutput{}
	}
	contextRecord, err := s.store.EnsureContextForExternalChannel(ctx, input.Connector, input.ExternalID, input.DisplayName)
	if err != nil {
		return MessageOutput{
			Handled: true,
			Reply:   "I started work on that, but I hit an internal routing issue. Please try again in a moment.",
		}
	}

	agentInputText := strings.TrimSpace(text)

	agentCtx := context.WithValue(ctx, ContextKeyRecord, contextRecord)
	agentCtx = context.WithValue(agentCtx, ContextKeyInput, input)
	if s.consumeSensitiveToolApproval(input, time.Now().UTC()) {
		agentCtx = agent.WithSensitiveToolApproval(agentCtx)
	}
	result := s.agent.Execute(agentCtx, llm.MessageInput{
		Connector:   strings.TrimSpace(input.Connector),
		WorkspaceID: strings.TrimSpace(contextRecord.WorkspaceID),
		ContextID:   strings.TrimSpace(contextRecord.ID),
		ExternalID:  strings.TrimSpace(input.ExternalID),
		DisplayName: strings.TrimSpace(input.DisplayName),
		FromUserID:  strings.TrimSpace(input.FromUserID),
		Text:        agentInputText,
	})
	s.persistAgentAuditTraces(ctx, contextRecord, input, result)
	s.appendAgentToolCallLogs(contextRecord, input, result)
	reply := strings.TrimSpace(result.Reply)
	if result.Error != nil {
		if reply != "" {
			return MessageOutput{
				Handled: true,
				Reply:   reply,
			}
		}
		return MessageOutput{
			Handled: true,
			Reply:   "I started work on that but ran into an internal error. Please try again in a moment.",
		}
	}
	if reply == "" {
		return MessageOutput{
			Handled: true,
			Reply:   "I started work on that and I am still processing. Share more detail if you want me to keep digging now.",
		}
	}
	return MessageOutput{
		Handled: true,
		Reply:   reply,
	}
}
