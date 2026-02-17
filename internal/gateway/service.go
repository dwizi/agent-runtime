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
	"github.com/dwizi/agent-runtime/internal/memorylog"
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

				// 1. Get History
				history := agent.GetRecentHistory(s.workspaceRoot, contextRecord.WorkspaceID, input.Connector, input.ExternalID, 15)
				if history != "" {
					agentPrompt = fmt.Sprintf("CONVERSATION HISTORY:\n%s\n\n%s", history, agentPrompt)
				}

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

			// 1. Get History
			history := agent.GetRecentHistory(s.workspaceRoot, contextRecord.WorkspaceID, input.Connector, input.ExternalID, 15)
			if history != "" {
				agentPrompt = fmt.Sprintf("CONVERSATION HISTORY:\n%s\n\n%s", history, agentPrompt)
			}

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
	_, err = s.store.CreateObjective(ctx, store.CreateObjectiveInput{
		WorkspaceID: contextRecord.WorkspaceID,
		ContextID:   contextRecord.ID,
		Title:       title,
		Prompt:      objectivePrompt,
		TriggerType: store.ObjectiveTriggerSchedule,
		CronExpr:    defaultObjectiveCronExpr,
		Active:      true,
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

	// 1. Get Conversation Context (Memory)
	history := agent.GetRecentHistory(s.workspaceRoot, contextRecord.WorkspaceID, input.Connector, input.ExternalID, 15)
	agentInputText := strings.TrimSpace(text)
	if history != "" {
		agentInputText = fmt.Sprintf("CONVERSATION HISTORY:\n%s\n\nNEW MESSAGE:\n%s", history, agentInputText)
	}

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

func (s *Service) appendAgentToolCallLogs(contextRecord store.ContextRecord, input MessageInput, result agent.Result) {
	if s == nil || len(result.ToolCalls) == 0 {
		return
	}
	workspaceRoot := strings.TrimSpace(s.workspaceRoot)
	workspaceID := strings.TrimSpace(contextRecord.WorkspaceID)
	connector := strings.ToLower(strings.TrimSpace(input.Connector))
	externalID := strings.TrimSpace(input.ExternalID)
	if workspaceRoot == "" || workspaceID == "" || connector == "" || externalID == "" {
		return
	}
	displayName := strings.TrimSpace(input.DisplayName)
	if displayName == "" {
		displayName = externalID
	}
	for _, call := range result.ToolCalls {
		logText := formatToolCallLog(call)
		if logText == "" {
			continue
		}
		if err := memorylog.Append(memorylog.Entry{
			WorkspaceRoot: workspaceRoot,
			WorkspaceID:   workspaceID,
			Connector:     connector,
			ExternalID:    externalID,
			Direction:     "tool",
			ActorID:       "agent-runtime",
			DisplayName:   displayName,
			Text:          logText,
			Timestamp:     time.Now().UTC(),
		}); err != nil {
			s.logger.Error("tool call log append failed", "error", err, "connector", connector, "external_id", externalID)
		}
	}
}

func formatToolCallLog(call agent.ToolCall) string {
	toolName := strings.TrimSpace(call.ToolName)
	if toolName == "" {
		return ""
	}
	status := strings.TrimSpace(call.Status)
	if status == "" {
		status = "unknown"
	}
	lines := []string{
		"Tool call",
		fmt.Sprintf("- tool: `%s`", toolName),
		fmt.Sprintf("- status: `%s`", status),
	}
	args := strings.TrimSpace(call.ToolArgs)
	if args != "" {
		lines = append(lines, fmt.Sprintf("- args: `%s`", truncateToolLogField(args, 500)))
	}
	if errText := strings.TrimSpace(call.Error); errText != "" {
		lines = append(lines, fmt.Sprintf("- error: %s", truncateToolLogField(errText, 500)))
	}
	if output := strings.TrimSpace(call.ToolOutput); output != "" {
		lines = append(lines, fmt.Sprintf("- output: %s", truncateToolLogField(output, 700)))
	}
	return strings.Join(lines, "\n")
}

func truncateToolLogField(input string, maxLen int) string {
	value := strings.Join(strings.Fields(strings.TrimSpace(input)), " ")
	if value == "" {
		return ""
	}
	if maxLen < 1 || len(value) <= maxLen {
		return value
	}
	return strings.TrimSpace(value[:maxLen]) + "..."
}

func (s *Service) NarrateTaskResult(ctx context.Context, connector, externalID string, task orchestrator.Task, result orchestrator.TaskResult) (string, error) {
	if s.agent == nil {
		return "", fmt.Errorf("agent not configured")
	}

	// 1. Ensure context
	contextRecord, err := s.store.EnsureContextForExternalChannel(ctx, connector, externalID, "")
	if err != nil {
		return "", err
	}

	// 2. Build synthetic input
	narrativePrompt := fmt.Sprintf(
		"BACKGROUND TASK FINISHED\nTask: %s\nResult: %s\n\nExplain this result to the user naturally and decide if any follow-up actions are needed.",
		task.Title, result.Summary,
	)

	// 3. Get history for context
	history := agent.GetRecentHistory(s.workspaceRoot, contextRecord.WorkspaceID, connector, externalID, 10)
	if history != "" {
		narrativePrompt = fmt.Sprintf("CONVERSATION HISTORY:\n%s\n\n%s", history, narrativePrompt)
	}

	// 4. Execute Agent turn
	agentCtx := context.WithValue(ctx, ContextKeyRecord, contextRecord)
	agentCtx = context.WithValue(agentCtx, ContextKeyInput, MessageInput{
		Connector:  connector,
		ExternalID: externalID,
	})

	agentRes := s.agent.Execute(agentCtx, llm.MessageInput{
		Connector:   connector,
		WorkspaceID: contextRecord.WorkspaceID,
		ContextID:   contextRecord.ID,
		ExternalID:  externalID,
		Text:        narrativePrompt,
	})

	if agentRes.Error != nil {
		return "", agentRes.Error
	}

	return agentRes.Reply, nil
}

func (s *Service) grantSensitiveToolApproval(input MessageInput, now time.Time) {
	if s == nil {
		return
	}
	key := sensitiveApprovalKey(input)
	if key == "" {
		return
	}
	s.approvalMu.Lock()
	defer s.approvalMu.Unlock()
	cutoff := now.UTC()
	for existingKey, expiry := range s.sensitiveApprovals {
		if !expiry.After(cutoff) {
			delete(s.sensitiveApprovals, existingKey)
		}
	}
	ttl := s.sensitiveApprovalTTL
	if ttl <= 0 {
		ttl = 10 * time.Minute
	}
	s.sensitiveApprovals[key] = cutoff.Add(ttl)
}

func (s *Service) persistAgentAuditTraces(ctx context.Context, contextRecord store.ContextRecord, input MessageInput, result agent.Result) {
	if s == nil || s.store == nil || len(result.Trace) == 0 {
		return
	}
	workspaceID := strings.TrimSpace(contextRecord.WorkspaceID)
	contextID := strings.TrimSpace(contextRecord.ID)
	connector := strings.TrimSpace(input.Connector)
	externalID := strings.TrimSpace(input.ExternalID)
	sourceUserID := strings.TrimSpace(input.FromUserID)
	if workspaceID == "" || contextID == "" || connector == "" || externalID == "" {
		return
	}
	for _, entry := range result.Trace {
		stage := strings.TrimSpace(entry.Stage)
		if !strings.HasPrefix(strings.ToLower(stage), "audit.") {
			continue
		}
		eventType := strings.TrimSpace(strings.TrimPrefix(strings.ToLower(stage), "audit."))
		if eventType == "" {
			continue
		}
		meta := parseAuditMetadata(entry.Message)
		toolName := strings.TrimSpace(meta["tool"])
		if toolName == "" {
			toolName = strings.TrimSpace(result.ToolName)
		}
		toolClass := strings.TrimSpace(meta["class"])
		_, _ = s.store.CreateAgentAuditEvent(ctx, store.CreateAgentAuditEventInput{
			WorkspaceID:  workspaceID,
			ContextID:    contextID,
			Connector:    connector,
			ExternalID:   externalID,
			SourceUserID: sourceUserID,
			EventType:    eventType,
			Stage:        stage,
			ToolName:     toolName,
			ToolClass:    toolClass,
			Blocked:      result.Blocked,
			BlockReason:  strings.TrimSpace(result.BlockReason),
			Message:      strings.TrimSpace(entry.Message),
		})
	}
}

func parseAuditMetadata(message string) map[string]string {
	fields := strings.Fields(strings.TrimSpace(message))
	parsed := map[string]string{}
	for _, item := range fields {
		parts := strings.SplitN(item, "=", 2)
		if len(parts) != 2 {
			continue
		}
		key := strings.ToLower(strings.TrimSpace(parts[0]))
		value := strings.TrimSpace(parts[1])
		if key == "" || value == "" {
			continue
		}
		parsed[key] = value
	}
	return parsed
}

func (s *Service) consumeSensitiveToolApproval(input MessageInput, now time.Time) bool {
	if s == nil {
		return false
	}
	key := sensitiveApprovalKey(input)
	if key == "" {
		return false
	}
	s.approvalMu.Lock()
	defer s.approvalMu.Unlock()
	expiry, ok := s.sensitiveApprovals[key]
	if !ok {
		return false
	}
	delete(s.sensitiveApprovals, key)
	return expiry.After(now.UTC())
}

func sensitiveApprovalKey(input MessageInput) string {
	connector := strings.ToLower(strings.TrimSpace(input.Connector))
	externalID := strings.TrimSpace(input.ExternalID)
	fromUser := strings.TrimSpace(input.FromUserID)
	if connector == "" || externalID == "" || fromUser == "" {
		return ""
	}
	return connector + "|" + externalID + "|" + fromUser
}

func (s *Service) handleLegacyAutoTriage(ctx context.Context, input MessageInput, text string) (MessageOutput, error) {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" || strings.HasPrefix(trimmed, "/") {
		return MessageOutput{}, nil
	}
	if s.store == nil || s.engine == nil {
		return MessageOutput{}, nil
	}
	contextRecord, err := s.store.EnsureContextForExternalChannel(ctx, input.Connector, input.ExternalID, input.DisplayName)
	if err != nil {
		return MessageOutput{}, err
	}
	decision := deriveRouteDecision(input, contextRecord.WorkspaceID, contextRecord.ID, trimmed)
	if decision.Class == TriageNoise {
		return MessageOutput{}, nil
	}
	if !shouldAutoRouteDecision(decision) {
		return MessageOutput{}, nil
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
		return MessageOutput{}, err
	}
	decision.TaskID = task.ID
	if s.routingNotify != nil {
		s.routingNotify.NotifyRoutingDecision(ctx, decision)
	}
	return MessageOutput{
		Handled: true,
		Reply:   s.buildAutoTriageAck(ctx, input, contextRecord, decision),
	}, nil
}

func (s *Service) buildAutoTriageAck(ctx context.Context, input MessageInput, contextRecord store.ContextRecord, decision RouteDecision) string {
	fallback := fallbackAutoTriageAck(decision.Class)
	if s.triageAcknowledger == nil {
		return fallback
	}
	sourceText := strings.TrimSpace(decision.SourceText)
	if len(sourceText) > 300 {
		sourceText = sourceText[:300]
	}
	ackPrompt := strings.Join([]string{
		"Write one short natural acknowledgement for a chat message.",
		"Constraints:",
		"- one sentence",
		"- 8 to 20 words",
		"- confirm you are taking action now",
		"- do not include markdown, task IDs, or internal metadata",
		fmt.Sprintf("Route class: %s", strings.TrimSpace(string(decision.Class))),
		"User message:",
		sourceText,
	}, "\n")
	reply, err := s.triageAcknowledger.Reply(ctx, llm.MessageInput{
		Connector:     strings.TrimSpace(input.Connector),
		WorkspaceID:   strings.TrimSpace(contextRecord.WorkspaceID),
		ContextID:     strings.TrimSpace(contextRecord.ID),
		ExternalID:    strings.TrimSpace(input.ExternalID),
		DisplayName:   strings.TrimSpace(input.DisplayName),
		FromUserID:    strings.TrimSpace(input.FromUserID),
		Text:          ackPrompt,
		IsDM:          false,
		SkipGrounding: true,
	})
	if err != nil {
		return fallback
	}
	clean := sanitizeAutoTriageAck(reply)
	if clean == "" {
		return fallback
	}
	return clean
}

func fallbackAutoTriageAck(class TriageClass) string {
	switch class {
	case TriageIssue:
		return "Thanks for flagging this. I’m investigating now and I’ll report back with findings."
	case TriageModeration:
		return "Received. I’m reviewing this now and I’ll follow up with what I find."
	case TriageQuestion:
		return "Yes, I’m on it. I’ll investigate and come back with an answer."
	default:
		return "Understood. I’m handling this now and I’ll share results shortly."
	}
}

func sanitizeAutoTriageAck(input string) string {
	trimmed := strings.TrimSpace(input)
	if trimmed == "" {
		return ""
	}
	trimmed = strings.ReplaceAll(trimmed, "```", "")
	trimmed = strings.Join(strings.Fields(trimmed), " ")
	trimmed = strings.Trim(trimmed, "`\"'")
	if trimmed == "" {
		return ""
	}
	if len(trimmed) > 220 {
		trimmed = strings.TrimSpace(trimmed[:220]) + "..."
	}
	return trimmed
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
	command = NormalizeCommandName(command)

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
		return latestPendingActionAlias, true
	}
	lower := strings.ToLower(trimmed)
	if strings.Contains(lower, "most recent") || strings.Contains(lower, "latest pending") || lower == "latest" || lower == "newest" {
		return mostRecentPendingActionAlias, true
	}
	if lower == "all" || lower == "everything" {
		return allPendingActionsAlias, true
	}
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
		return latestPendingActionAlias, true
	}
	lower := strings.ToLower(trimmed)
	if lower == "all" || lower == "everything" {
		return allPendingActionsAlias, true
	}
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

	if actionArg, found := parseIntentApproveMostRecentPendingAction(trimmed, lower); found {
		return "approve-action", actionArg, true
	}
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
	if goal, found := parseMonitorIntent(trimmed, lower); found {
		return "monitor", goal, true
	}
	if taskPrompt, found := parseTaskCreationIntent(trimmed, lower); found {
		return "task", taskPrompt, true
	}
	if prompt, found := parseIntentTask(trimmed); found {
		return "task", prompt, true
	}
	return "", "", false
}

func parseIntentApproveMostRecentPendingAction(trimmed, lower string) (string, bool) {
	if !strings.Contains(lower, "approve") {
		return "", false
	}
	if strings.Contains(lower, "pair") || strings.Contains(lower, "token") {
		return "", false
	}
	if !(strings.Contains(lower, "most recent") ||
		strings.Contains(lower, "latest") ||
		strings.Contains(lower, "newest") ||
		strings.Contains(lower, "last pending")) {
		return "", false
	}
	if !(strings.Contains(lower, "pending action") || strings.Contains(lower, "pending approval")) {
		return "", false
	}
	_ = trimmed
	return mostRecentPendingActionAlias, true
}

func isImplicitApproveActionIntent(lower string) bool {
	if lower == "approve" || lower == "yes" {
		return true
	}
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

func parseMonitorIntent(trimmed, lower string) (string, bool) {
	prefixes := []string{
		"monitor ",
		"track ",
		"keep monitoring ",
		"set an alert for ",
		"set an alert to monitor ",
		"create a monitoring objective for ",
		"create monitoring objective for ",
		"create a monitor objective for ",
		"set up a monitoring objective for ",
		"setup a monitoring objective for ",
		"create an objective to monitor ",
		"create a monitoring objective to monitor ",
		"set up monitoring for ",
		"setup monitoring for ",
	}
	for _, prefix := range prefixes {
		if !strings.HasPrefix(lower, prefix) {
			continue
		}
		value := cleanMonitorGoal(trimmed[len(prefix):])
		if value == "" {
			return "", false
		}
		return value, true
	}
	for _, phrase := range []string{
		"set an alert and monitor ",
		"create an alert and monitor ",
	} {
		index := strings.Index(lower, phrase)
		if index < 0 {
			continue
		}
		value := cleanMonitorGoal(trimmed[index+len(phrase):])
		if value != "" {
			return value, true
		}
	}
	if strings.Contains(lower, "monitoring objective") || strings.Contains(lower, "monitor objective") {
		for _, marker := range []string{" for ", " to monitor "} {
			index := strings.Index(lower, marker)
			if index < 0 {
				continue
			}
			value := cleanMonitorGoal(trimmed[index+len(marker):])
			if value != "" {
				return value, true
			}
		}
	}
	return "", false
}

func parseTaskCreationIntent(trimmed, lower string) (string, bool) {
	prefixes := []string{
		"turn that into an actionable task",
		"turn this into an actionable task",
		"turn that into a task",
		"turn this into a task",
		"create one actionable task",
		"please create one actionable task",
		"create an actionable task",
		"make this a task",
		"create a task from this",
	}
	for _, prefix := range prefixes {
		index := strings.Index(lower, prefix)
		if index < 0 {
			continue
		}
		after := strings.TrimSpace(trimmed[index+len(prefix):])
		afterLower := strings.ToLower(after)
		for _, marker := range []string{
			" and tell me the task id",
			", and tell me the task id",
			" and return only the task id",
			", return only the task id",
		} {
			markerIndex := strings.Index(afterLower, marker)
			if markerIndex < 0 {
				continue
			}
			after = strings.TrimSpace(after[:markerIndex])
			afterLower = strings.ToLower(after)
		}
		after = strings.TrimSpace(strings.Trim(after, " .,:;!?"))
		if strings.HasPrefix(strings.ToLower(after), "in this workspace") {
			after = strings.TrimSpace(after[len("in this workspace"):])
		}
		after = strings.TrimSpace(strings.Trim(after, " .,:;!?"))
		if after != "" {
			return "Create one actionable task: " + after, true
		}
		if strings.Contains(lower, "rollout plan") {
			return "Create one actionable task from the rollout plan discussed in this conversation.", true
		}
		return "Create one actionable task from the latest plan discussed in this conversation.", true
	}
	return "", false
}

func cleanMonitorGoal(value string) string {
	goal := strings.TrimSpace(value)
	if goal == "" {
		return ""
	}
	lower := strings.ToLower(goal)
	for _, marker := range []string{
		" and tell me",
		", and tell me",
		" and then tell me",
		", then tell me",
		" and show me",
		", and show me",
		" and report",
		", and report",
	} {
		index := strings.Index(lower, marker)
		if index < 0 {
			continue
		}
		goal = strings.TrimSpace(goal[:index])
		lower = strings.ToLower(goal)
	}
	goal = strings.TrimSpace(strings.Trim(goal, " .,:;!?"))
	if strings.HasPrefix(strings.ToLower(goal), "to ") {
		goal = strings.TrimSpace(goal[len("to "):])
	}
	return goal
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
	if len(items) == 1 {
		return strings.TrimSpace(items[0].ID), ""
	}
	if len(items) > 1 {
		return "", "Multiple pending actions found. Use `/pending-actions` and approve by id."
	}
	items, err = s.store.ListPendingActionApprovalsGlobal(ctx, 2)
	if err != nil {
		return "", "Unable to load pending actions right now."
	}
	if len(items) == 0 {
		return "", "No pending actions."
	}
	if len(items) > 1 {
		return "", "Multiple pending actions found across contexts. Use `/pending-actions` and approve by id."
	}
	return strings.TrimSpace(items[0].ID), ""
}

func (s *Service) resolveMostRecentPendingActionID(ctx context.Context, input MessageInput) (string, string) {
	items, err := s.store.ListPendingActionApprovals(ctx, input.Connector, input.ExternalID, 50)
	if err != nil {
		return "", "Unable to load pending actions right now."
	}
	if len(items) == 0 {
		items, err = s.store.ListPendingActionApprovalsGlobal(ctx, 50)
		if err != nil {
			return "", "Unable to load pending actions right now."
		}
	}
	if len(items) == 0 {
		return "", "No pending actions."
	}
	latest := items[len(items)-1]
	actionID := strings.TrimSpace(latest.ID)
	if actionID == "" {
		return "", "Unable to determine the latest pending action id."
	}
	return actionID, ""
}

func normalizeActionCommandID(value string) string {
	trimmed := strings.TrimSpace(value)
	trimmed = strings.Trim(trimmed, "`\"'")
	trimmed = strings.Trim(trimmed, "[](){}<>,.;:!?")
	if trimmed == "" {
		return ""
	}
	if actionID, ok := findActionID(trimmed); ok {
		return actionID
	}
	return trimmed
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

func formatActionExecutionReply(record store.ActionApproval) string {
	actionID := strings.TrimSpace(record.ID)
	if actionID == "" {
		actionID = "(unknown-action)"
	}
	switch strings.ToLower(strings.TrimSpace(record.ExecutionStatus)) {
	case "skipped":
		reason := humanizeExecutionMessage(record.ExecutionMessage)
		if reason == "" {
			reason = "No executor is configured for this workspace."
		}
		return fmt.Sprintf("I approved action `%s`, but it was not run. Outcome: %s", actionID, reason)
	case "failed":
		detail := humanizeExecutionFailure(record.ExecutionMessage)
		if detail == "" {
			detail = "Execution failed without additional details."
		}
		return fmt.Sprintf("I approved action `%s`, but execution failed. Outcome: %s", actionID, detail)
	default:
		plugin := fallbackPluginLabel(record.ExecutorPlugin)
		outcome := humanizeExecutionMessage(record.ExecutionMessage)
		if outcome == "" {
			outcome = "Completed successfully."
		}
		return fmt.Sprintf("I approved action `%s` and ran it with `%s`. Outcome: %s", actionID, plugin, outcome)
	}
}

func humanizeExecutionMessage(message string) string {
	text := strings.TrimSpace(message)
	if text == "" {
		return ""
	}
	text = trimCaseInsensitivePrefix(text, "command succeeded:")
	text = trimCaseInsensitivePrefix(text, "command completed:")
	text = trimCaseInsensitivePrefix(text, "webhook request completed with status")
	text = strings.TrimSpace(text)
	if text == "" {
		return ""
	}
	if strings.HasPrefix(strings.ToLower(strings.TrimSpace(message)), "webhook request completed with status") {
		return "Webhook request completed with status " + text
	}
	return compactSnippet(text)
}

func humanizeExecutionFailure(message string) string {
	text := strings.TrimSpace(message)
	if text == "" {
		return ""
	}
	text = trimCaseInsensitivePrefix(text, "command failed:")
	parts := strings.SplitN(text, "; output=", 2)
	switch len(parts) {
	case 2:
		cause := compactSnippet(parts[0])
		output := compactSnippet(parts[1])
		if output == "" {
			return cause
		}
		if cause == "" {
			return "Output: " + output
		}
		return cause + ". Output: " + output
	default:
		return compactSnippet(text)
	}
}

func trimCaseInsensitivePrefix(value, prefix string) string {
	trimmedValue := strings.TrimSpace(value)
	trimmedPrefix := strings.TrimSpace(prefix)
	if trimmedValue == "" || trimmedPrefix == "" {
		return trimmedValue
	}
	if strings.HasPrefix(strings.ToLower(trimmedValue), strings.ToLower(trimmedPrefix)) {
		return strings.TrimSpace(trimmedValue[len(trimmedPrefix):])
	}
	return trimmedValue
}

func isAdminRole(role string) bool {
	switch strings.ToLower(strings.TrimSpace(role)) {
	case "overlord", "admin":
		return true
	default:
		return false
	}
}
