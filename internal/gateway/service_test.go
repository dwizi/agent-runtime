package gateway

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/dwizi/agent-runtime/internal/actions/executor"
	"github.com/dwizi/agent-runtime/internal/llm"
	"github.com/dwizi/agent-runtime/internal/orchestrator"
	"github.com/dwizi/agent-runtime/internal/qmd"
	"github.com/dwizi/agent-runtime/internal/store"
)

type fakeStore struct {
	contextRecord          store.ContextRecord
	contextPolicy          store.ContextPolicy
	identity               store.UserIdentity
	identityErr            error
	lastTask               store.CreateTaskInput
	tasks                  map[string]store.TaskRecord
	adminUpdated           bool
	approved               bool
	denied                 bool
	actionApprovals        []store.ActionApproval
	lastExecutionUpdate    store.UpdateActionExecutionInput
	executionUpdateInvoked bool
	lastObjective          store.CreateObjectiveInput
	objectiveInvoked       bool
	auditEvents            []store.CreateAgentAuditEventInput
}

func (f *fakeStore) EnsureContextForExternalChannel(ctx context.Context, connector, externalID, displayName string) (store.ContextRecord, error) {
	if f.contextRecord.ID == "" {
		f.contextRecord = store.ContextRecord{ID: "ctx-1", WorkspaceID: "ws-1", IsAdmin: false}
	}
	return f.contextRecord, nil
}

func (f *fakeStore) SetContextAdminByExternal(ctx context.Context, connector, externalID string, enabled bool) (store.ContextRecord, error) {
	f.adminUpdated = true
	f.contextRecord = store.ContextRecord{ID: "ctx-admin", WorkspaceID: "ws-1", IsAdmin: enabled}
	return f.contextRecord, nil
}

func (f *fakeStore) LookupContextPolicyByExternal(ctx context.Context, connector, externalID string) (store.ContextPolicy, error) {
	if f.contextPolicy.ContextID == "" {
		f.contextPolicy = store.ContextPolicy{
			ContextID:    "ctx-1",
			WorkspaceID:  "ws-1",
			IsAdmin:      false,
			SystemPrompt: "",
		}
	}
	return f.contextPolicy, nil
}

func (f *fakeStore) SetContextSystemPromptByExternal(ctx context.Context, connector, externalID, prompt string) (store.ContextPolicy, error) {
	f.contextPolicy = store.ContextPolicy{
		ContextID:    "ctx-1",
		WorkspaceID:  "ws-1",
		IsAdmin:      false,
		SystemPrompt: strings.TrimSpace(prompt),
	}
	return f.contextPolicy, nil
}

func (f *fakeStore) LookupUserIdentity(ctx context.Context, connector, connectorUserID string) (store.UserIdentity, error) {
	if f.identityErr != nil {
		return store.UserIdentity{}, f.identityErr
	}
	return f.identity, nil
}

func (f *fakeStore) CreateTask(ctx context.Context, input store.CreateTaskInput) error {
	f.lastTask = input
	if f.tasks == nil {
		f.tasks = map[string]store.TaskRecord{}
	}
	f.tasks[input.ID] = store.TaskRecord{
		ID:               input.ID,
		WorkspaceID:      input.WorkspaceID,
		ContextID:        input.ContextID,
		Kind:             input.Kind,
		Title:            input.Title,
		Prompt:           input.Prompt,
		Status:           input.Status,
		RouteClass:       input.RouteClass,
		Priority:         input.Priority,
		DueAt:            input.DueAt,
		AssignedLane:     input.AssignedLane,
		SourceConnector:  input.SourceConnector,
		SourceExternalID: input.SourceExternalID,
		SourceUserID:     input.SourceUserID,
		SourceText:       input.SourceText,
	}
	return nil
}

func (f *fakeStore) LookupTask(ctx context.Context, id string) (store.TaskRecord, error) {
	if f.tasks == nil {
		return store.TaskRecord{}, store.ErrTaskNotFound
	}
	record, ok := f.tasks[id]
	if !ok {
		return store.TaskRecord{}, store.ErrTaskNotFound
	}
	return record, nil
}

func (f *fakeStore) MarkTaskCompleted(ctx context.Context, id string, finishedAt time.Time, summary, resultPath string) error {
	if f.tasks == nil {
		return store.ErrTaskNotFound
	}
	record, ok := f.tasks[id]
	if !ok {
		return store.ErrTaskNotFound
	}
	record.Status = "succeeded"
	record.FinishedAt = finishedAt
	record.ResultSummary = strings.TrimSpace(summary)
	record.ResultPath = strings.TrimSpace(resultPath)
	f.tasks[id] = record
	return nil
}

func (f *fakeStore) UpdateTaskRouting(ctx context.Context, input store.UpdateTaskRoutingInput) (store.TaskRecord, error) {
	if f.tasks == nil {
		return store.TaskRecord{}, store.ErrTaskNotFound
	}
	record, ok := f.tasks[input.ID]
	if !ok {
		return store.TaskRecord{}, store.ErrTaskNotFound
	}
	record.RouteClass = strings.TrimSpace(input.RouteClass)
	record.Priority = strings.TrimSpace(input.Priority)
	record.AssignedLane = strings.TrimSpace(input.AssignedLane)
	record.DueAt = input.DueAt
	f.tasks[input.ID] = record
	return record, nil
}

func (f *fakeStore) ApprovePairing(ctx context.Context, input store.ApprovePairingInput) (store.ApprovePairingResult, error) {
	f.approved = true
	return store.ApprovePairingResult{
		PairingRequest: store.PairingRequest{DisplayName: "Alice"},
		UserID:         "user-1",
		IdentityID:     "identity-1",
	}, nil
}

func (f *fakeStore) DenyPairing(ctx context.Context, input store.DenyPairingInput) (store.PairingRequest, error) {
	f.denied = true
	return store.PairingRequest{DisplayName: "Alice"}, nil
}

func (f *fakeStore) CreateActionApproval(ctx context.Context, input store.CreateActionApprovalInput) (store.ActionApproval, error) {
	record := store.ActionApproval{
		ID:            "act-1",
		WorkspaceID:   input.WorkspaceID,
		ContextID:     input.ContextID,
		Connector:     input.Connector,
		ExternalID:    input.ExternalID,
		ActionType:    input.ActionType,
		ActionTarget:  input.ActionTarget,
		ActionSummary: input.ActionSummary,
		Status:        "pending",
	}
	f.actionApprovals = append(f.actionApprovals, record)
	return record, nil
}

func (f *fakeStore) ListPendingActionApprovals(ctx context.Context, connector, externalID string, limit int) ([]store.ActionApproval, error) {
	if len(f.actionApprovals) == 0 {
		return []store.ActionApproval{}, nil
	}
	results := make([]store.ActionApproval, 0, len(f.actionApprovals))
	for _, item := range f.actionApprovals {
		matchesConnector := strings.TrimSpace(item.Connector) == "" || strings.EqualFold(strings.TrimSpace(item.Connector), strings.TrimSpace(connector))
		matchesExternalID := strings.TrimSpace(item.ExternalID) == "" || strings.TrimSpace(item.ExternalID) == strings.TrimSpace(externalID)
		if item.Status == "pending" && matchesConnector && matchesExternalID {
			results = append(results, item)
		}
	}
	return results, nil
}

func (f *fakeStore) ListPendingActionApprovalsGlobal(ctx context.Context, limit int) ([]store.ActionApproval, error) {
	if len(f.actionApprovals) == 0 {
		return []store.ActionApproval{}, nil
	}
	results := make([]store.ActionApproval, 0, len(f.actionApprovals))
	for _, item := range f.actionApprovals {
		if item.Status == "pending" {
			results = append(results, item)
		}
	}
	return results, nil
}

func (f *fakeStore) ApproveActionApproval(ctx context.Context, input store.ApproveActionApprovalInput) (store.ActionApproval, error) {
	for index := range f.actionApprovals {
		if f.actionApprovals[index].ID == input.ID {
			if f.actionApprovals[index].Status != "pending" {
				return store.ActionApproval{}, store.ErrActionApprovalNotReady
			}
			f.actionApprovals[index].Status = "approved"
			f.actionApprovals[index].ApproverUserID = input.ApproverUserID
			return f.actionApprovals[index], nil
		}
	}
	return store.ActionApproval{}, store.ErrActionApprovalNotFound
}

func (f *fakeStore) DenyActionApproval(ctx context.Context, input store.DenyActionApprovalInput) (store.ActionApproval, error) {
	for index := range f.actionApprovals {
		if f.actionApprovals[index].ID == input.ID {
			if f.actionApprovals[index].Status != "pending" {
				return store.ActionApproval{}, store.ErrActionApprovalNotReady
			}
			f.actionApprovals[index].Status = "denied"
			f.actionApprovals[index].ApproverUserID = input.ApproverUserID
			f.actionApprovals[index].DeniedReason = input.Reason
			return f.actionApprovals[index], nil
		}
	}
	return store.ActionApproval{}, store.ErrActionApprovalNotFound
}

func (f *fakeStore) UpdateActionExecution(ctx context.Context, input store.UpdateActionExecutionInput) (store.ActionApproval, error) {
	f.executionUpdateInvoked = true
	f.lastExecutionUpdate = input
	for index := range f.actionApprovals {
		if f.actionApprovals[index].ID != input.ID {
			continue
		}
		f.actionApprovals[index].ExecutionStatus = input.ExecutionStatus
		f.actionApprovals[index].ExecutionMessage = input.ExecutionMessage
		f.actionApprovals[index].ExecutorPlugin = input.ExecutorPlugin
		f.actionApprovals[index].ExecutedAt = input.ExecutedAt
		f.actionApprovals[index].UpdatedAt = time.Now().UTC()
		return f.actionApprovals[index], nil
	}
	return store.ActionApproval{}, store.ErrActionApprovalNotFound
}

func (f *fakeStore) CreateObjective(ctx context.Context, input store.CreateObjectiveInput) (store.Objective, error) {
	f.objectiveInvoked = true
	f.lastObjective = input
	return store.Objective{
		ID:          "obj-1",
		WorkspaceID: input.WorkspaceID,
		ContextID:   input.ContextID,
		Title:       input.Title,
		Prompt:      input.Prompt,
		TriggerType: input.TriggerType,
		CronExpr:    input.CronExpr,
		Active:      input.Active,
	}, nil
}

func (f *fakeStore) UpdateObjective(ctx context.Context, input store.UpdateObjectiveInput) (store.Objective, error) {
	id := strings.TrimSpace(input.ID)
	if id == "" {
		return store.Objective{}, store.ErrObjectiveInvalid
	}
	title := "objective"
	if input.Title != nil {
		title = strings.TrimSpace(*input.Title)
	}
	prompt := "prompt"
	if input.Prompt != nil {
		prompt = strings.TrimSpace(*input.Prompt)
	}
	active := true
	if input.Active != nil {
		active = *input.Active
	}
	cronExpr := defaultObjectiveCronExpr
	if input.CronExpr != nil {
		cronExpr = strings.TrimSpace(*input.CronExpr)
	}
	trigger := store.ObjectiveTriggerSchedule
	if input.TriggerType != nil {
		trigger = *input.TriggerType
	}
	eventKey := ""
	if input.EventKey != nil {
		eventKey = strings.TrimSpace(*input.EventKey)
	}
	if trigger == store.ObjectiveTriggerEvent {
		cronExpr = ""
	}
	return store.Objective{
		ID:          id,
		WorkspaceID: "ws-1",
		ContextID:   "ctx-1",
		Title:       title,
		Prompt:      prompt,
		TriggerType: trigger,
		EventKey:    eventKey,
		CronExpr:    cronExpr,
		Active:      active,
	}, nil
}

func (f *fakeStore) CreateAgentAuditEvent(ctx context.Context, input store.CreateAgentAuditEventInput) (store.AgentAuditEvent, error) {
	f.auditEvents = append(f.auditEvents, input)
	return store.AgentAuditEvent{
		ID:           "audit-1",
		WorkspaceID:  input.WorkspaceID,
		ContextID:    input.ContextID,
		Connector:    input.Connector,
		ExternalID:   input.ExternalID,
		SourceUserID: input.SourceUserID,
		EventType:    input.EventType,
		Stage:        input.Stage,
		ToolName:     input.ToolName,
		ToolClass:    input.ToolClass,
		Blocked:      input.Blocked,
		BlockReason:  input.BlockReason,
		Message:      input.Message,
		CreatedAt:    time.Now().UTC(),
	}, nil
}

type fakeEngine struct {
	lastTask orchestrator.Task
}

func (f *fakeEngine) Enqueue(task orchestrator.Task) (orchestrator.Task, error) {
	task.ID = "task-123"
	f.lastTask = task
	return task, nil
}

type fakeRetriever struct {
	searchResults []qmd.SearchResult
	searchErr     error
	openResult    qmd.OpenResult
	openErr       error
	statusResult  qmd.Status
	statusErr     error
}

type fakeActionExecutor struct {
	result executor.Result
	err    error
}

type fakeTriageAcknowledger struct {
	reply     string
	replies   []string
	err       error
	callCount int
	lastInput llm.MessageInput
}

type fakeRoutingNotifier struct {
	lastDecision RouteDecision
	invoked      bool
}

func (f *fakeRoutingNotifier) NotifyRoutingDecision(ctx context.Context, decision RouteDecision) {
	f.lastDecision = decision
	f.invoked = true
}

func (f *fakeActionExecutor) Execute(ctx context.Context, approval store.ActionApproval) (executor.Result, error) {
	if f.err != nil {
		return executor.Result{}, f.err
	}
	return f.result, nil
}

func (f *fakeTriageAcknowledger) Reply(ctx context.Context, input llm.MessageInput) (string, error) {
	f.callCount++
	f.lastInput = input
	if f.err != nil {
		return "", f.err
	}
	if len(f.replies) > 0 {
		index := f.callCount - 1
		if index >= 0 && index < len(f.replies) {
			return f.replies[index], nil
		}
		return f.replies[len(f.replies)-1], nil
	}
	return f.reply, nil
}

func (f *fakeRetriever) Search(ctx context.Context, workspaceID, query string, limit int) ([]qmd.SearchResult, error) {
	if f.searchErr != nil {
		return nil, f.searchErr
	}
	return f.searchResults, nil
}

func (f *fakeRetriever) OpenMarkdown(ctx context.Context, workspaceID, target string) (qmd.OpenResult, error) {
	if f.openErr != nil {
		return qmd.OpenResult{}, f.openErr
	}
	return f.openResult, nil
}

func (f *fakeRetriever) Status(ctx context.Context, workspaceID string) (qmd.Status, error) {
	if f.statusErr != nil {
		return qmd.Status{}, f.statusErr
	}
	return f.statusResult, nil
}

func TestHandleTaskCommand(t *testing.T) {
	fStore := &fakeStore{}
	fEngine := &fakeEngine{}
	service := New(fStore, fEngine, nil, nil, "", nil)

	output, err := service.HandleMessage(context.Background(), MessageInput{
		Connector:   "telegram",
		ExternalID:  "42",
		DisplayName: "ops",
		FromUserID:  "user",
		Text:        "/task prepare weekly report",
	})
	if err != nil {
		t.Fatalf("handle message failed: %v", err)
	}
	if !output.Handled {
		t.Fatal("expected command to be handled")
	}
	if fStore.lastTask.ID != "task-123" {
		t.Fatalf("expected persisted task id task-123, got %s", fStore.lastTask.ID)
	}
}

func TestHandleTaskNaturalLanguage(t *testing.T) {
	fStore := &fakeStore{}
	fEngine := &fakeEngine{}
	service := New(fStore, fEngine, nil, nil, "", nil)

	output, err := service.HandleMessage(context.Background(), MessageInput{
		Connector:   "telegram",
		ExternalID:  "42",
		DisplayName: "ops",
		FromUserID:  "user",
		Text:        "please create a task to prepare weekly report",
	})
	if err != nil {
		t.Fatalf("handle message failed: %v", err)
	}
	if !output.Handled {
		t.Fatal("expected nl task to be handled")
	}
	if fStore.lastTask.ID != "task-123" {
		t.Fatalf("expected persisted task id task-123, got %s", fStore.lastTask.ID)
	}
}

func TestHandleAdminChannelEnableRequiresAdmin(t *testing.T) {
	fStore := &fakeStore{
		identityErr: store.ErrIdentityNotFound,
	}
	service := New(fStore, &fakeEngine{}, nil, nil, "", nil)
	output, err := service.HandleMessage(context.Background(), MessageInput{
		Connector:  "telegram",
		ExternalID: "42",
		FromUserID: "123",
		Text:       "/admin-channel enable",
	})
	if err != nil {
		t.Fatalf("handle message failed: %v", err)
	}
	if !output.Handled || output.Reply == "" {
		t.Fatal("expected handled response with denial message")
	}
	if fStore.adminUpdated {
		t.Fatal("admin update should not happen for missing identity")
	}
}

func TestHandleAdminChannelEnable(t *testing.T) {
	fStore := &fakeStore{
		identity: store.UserIdentity{
			UserID: "user-1",
			Role:   "admin",
		},
	}
	service := New(fStore, &fakeEngine{}, nil, nil, "", nil)
	output, err := service.HandleMessage(context.Background(), MessageInput{
		Connector:  "telegram",
		ExternalID: "42",
		FromUserID: "123",
		Text:       "/admin-channel enable",
	})
	if err != nil {
		t.Fatalf("handle message failed: %v", err)
	}
	if !output.Handled {
		t.Fatal("expected command handled")
	}
	if !fStore.adminUpdated {
		t.Fatal("expected admin flag update")
	}
}

func TestHandleApproveCommand(t *testing.T) {
	fStore := &fakeStore{
		identity: store.UserIdentity{
			UserID: "user-1",
			Role:   "overlord",
		},
	}
	service := New(fStore, &fakeEngine{}, nil, nil, "", nil)
	output, err := service.HandleMessage(context.Background(), MessageInput{
		Connector:  "telegram",
		ExternalID: "42",
		FromUserID: "123",
		Text:       "/approve ABC123",
	})
	if err != nil {
		t.Fatalf("handle message failed: %v", err)
	}
	if !output.Handled || !fStore.approved {
		t.Fatal("expected approval to run")
	}
}

func TestHandleApprovePairingNaturalLanguage(t *testing.T) {
	fStore := &fakeStore{
		identity: store.UserIdentity{
			UserID: "user-1",
			Role:   "admin",
		},
	}
	service := New(fStore, &fakeEngine{}, nil, nil, "", nil)
	output, err := service.HandleMessage(context.Background(), MessageInput{
		Connector:  "telegram",
		ExternalID: "42",
		FromUserID: "123",
		Text:       "please approve pairing token ABCDEF123456",
	})
	if err != nil {
		t.Fatalf("handle message failed: %v", err)
	}
	if !output.Handled || !fStore.approved {
		t.Fatal("expected natural-language pairing approval to run")
	}
}

func TestHandleDenyPairingNaturalLanguage(t *testing.T) {
	fStore := &fakeStore{
		identity: store.UserIdentity{
			UserID: "user-1",
			Role:   "admin",
		},
	}
	service := New(fStore, &fakeEngine{}, nil, nil, "", nil)
	output, err := service.HandleMessage(context.Background(), MessageInput{
		Connector:  "discord",
		ExternalID: "42",
		FromUserID: "123",
		Text:       "deny pairing token ZXCVBNM12345 because duplicate identity",
	})
	if err != nil {
		t.Fatalf("handle message failed: %v", err)
	}
	if !output.Handled || !fStore.denied {
		t.Fatal("expected natural-language pairing denial to run")
	}
}

func TestHandleRoutesUnknownMessage(t *testing.T) {
	fStore := &fakeStore{}
	service := New(fStore, &fakeEngine{}, nil, nil, "", nil)
	output, err := service.HandleMessage(context.Background(), MessageInput{
		Connector: "telegram",
		Text:      "hello world",
	})
	if err != nil {
		t.Fatalf("handle message failed: %v", err)
	}
	if output.Handled {
		t.Fatal("expected unknown conversational message to pass through")
	}
	if fStore.lastTask.ID != "" {
		t.Fatal("expected unknown message to skip task routing")
	}
}

func TestHandleAutoTriageCreatesTaskAndNotifies(t *testing.T) {
	fStore := &fakeStore{}
	fEngine := &fakeEngine{}
	service := New(fStore, fEngine, nil, nil, "", nil)
	notifier := &fakeRoutingNotifier{}
	service.SetRoutingNotifier(notifier)

	output, err := service.HandleMessage(context.Background(), MessageInput{
		Connector:   "telegram",
		ExternalID:  "42",
		DisplayName: "ops",
		FromUserID:  "u1",
		Text:        "There is a bug in the onboarding flow and it keeps failing",
	})
	if err != nil {
		t.Fatalf("handle message failed: %v", err)
	}
	if !output.Handled {
		t.Fatal("expected auto triage to acknowledge in origin channel")
	}
	if strings.TrimSpace(output.Reply) == "" {
		t.Fatal("expected acknowledgement reply")
	}
	if fStore.lastTask.ID == "" {
		t.Fatal("expected triaged task to be created")
	}
	if fStore.lastTask.RouteClass != "issue" {
		t.Fatalf("expected issue route class, got %s", fStore.lastTask.RouteClass)
	}
	if fStore.lastTask.Priority != "p2" {
		t.Fatalf("expected p2 priority, got %s", fStore.lastTask.Priority)
	}
	if !notifier.invoked {
		t.Fatal("expected routing notifier invocation")
	}
	if notifier.lastDecision.TaskID == "" {
		t.Fatal("expected notifier decision to include task id")
	}
}

func TestHandleAutoTriageUsesLLMAckWhenAvailable(t *testing.T) {
	fStore := &fakeStore{}
	fEngine := &fakeEngine{}
	service := New(fStore, fEngine, nil, nil, "", nil)
	ack := &fakeTriageAcknowledger{
		reply: "Absolutely - I am digging into this now and will report back shortly.",
	}
	service.SetTriageAcknowledger(ack)

	output, err := service.HandleMessage(context.Background(), MessageInput{
		Connector:   "telegram",
		ExternalID:  "42",
		DisplayName: "ops",
		FromUserID:  "u1",
		Text:        "There is a bug in the onboarding flow and it keeps failing",
	})
	if err != nil {
		t.Fatalf("handle message failed: %v", err)
	}
	if !output.Handled {
		t.Fatal("expected triage path to be handled")
	}
	if !strings.Contains(strings.ToLower(output.Reply), "digging into this") {
		t.Fatalf("expected llm-generated ack, got %q", output.Reply)
	}
	if ack.callCount != 1 {
		t.Fatalf("expected one triage ack call, got %d", ack.callCount)
	}
	if !strings.Contains(ack.lastInput.Text, "USER REQUEST") {
		t.Fatalf("expected agent prompt structure, got %q", ack.lastInput.Text)
	}
	if ack.lastInput.SkipGrounding {
		t.Fatal("expected first-step triage reasoning to allow grounding")
	}
}

func TestHandleAutoTriageQuestionWithoutFollowUpSkipsTask(t *testing.T) {
	fStore := &fakeStore{}
	service := New(fStore, &fakeEngine{}, nil, nil, "", nil)
	output, err := service.HandleMessage(context.Background(), MessageInput{
		Connector: "telegram",
		Text:      "how are you today?",
	})
	if err != nil {
		t.Fatalf("handle message failed: %v", err)
	}
	if output.Handled {
		t.Fatal("expected normal question to pass through to conversational responder")
	}
	if fStore.lastTask.ID != "" {
		t.Fatalf("expected no triaged task, got %s", fStore.lastTask.ID)
	}
}

func TestHandleAutoTriageFallsBackToAgentWhenLegacySkips(t *testing.T) {
	fStore := &fakeStore{}
	service := New(fStore, &fakeEngine{}, nil, nil, "", nil)
	ack := &fakeTriageAcknowledger{
		reply: "I ran some checks and here is the answer.",
	}
	service.SetTriageAcknowledger(ack)

	output, err := service.HandleMessage(context.Background(), MessageInput{
		Connector:   "telegram",
		ExternalID:  "42",
		DisplayName: "ops",
		FromUserID:  "u1",
		Text:        "how are you today?",
	})
	if err != nil {
		t.Fatalf("handle message failed: %v", err)
	}
	if !output.Handled {
		t.Fatal("expected fallback agent response to handle conversational message")
	}
	if !strings.Contains(strings.ToLower(output.Reply), "answer") {
		t.Fatalf("expected fallback reply, got %q", output.Reply)
	}
	if fStore.lastTask.ID != "" {
		t.Fatalf("expected no routed task, got %s", fStore.lastTask.ID)
	}
	if ack.callCount != 1 {
		t.Fatalf("expected one llm call from fallback agent, got %d", ack.callCount)
	}
}

func TestHandleAutoTriageFallbackAgentErrorReturnsStatusReply(t *testing.T) {
	fStore := &fakeStore{}
	service := New(fStore, &fakeEngine{}, nil, nil, "", nil)
	ack := &fakeTriageAcknowledger{
		err: errors.New("llm unavailable"),
	}
	service.SetTriageAcknowledger(ack)

	output, err := service.HandleMessage(context.Background(), MessageInput{
		Connector:   "telegram",
		ExternalID:  "42",
		DisplayName: "ops",
		FromUserID:  "u1",
		Text:        "how are you today?",
	})
	if err != nil {
		t.Fatalf("handle message failed: %v", err)
	}
	if !output.Handled {
		t.Fatal("expected fallback agent error path to remain handled")
	}
	if !strings.Contains(strings.ToLower(output.Reply), "internal error") {
		t.Fatalf("expected user-visible internal error reply, got %q", output.Reply)
	}
}

func TestHandleAutoTriageFallbackAgentToolErrorKeepsSpecificReply(t *testing.T) {
	fStore := &fakeStore{}
	service := New(fStore, &fakeEngine{}, nil, nil, "", nil)
	ack := &fakeTriageAcknowledger{
		reply: `{"tool":"missing_tool","args":{}}`,
	}
	service.SetTriageAcknowledger(ack)

	output, err := service.HandleMessage(context.Background(), MessageInput{
		Connector:   "telegram",
		ExternalID:  "42",
		DisplayName: "ops",
		FromUserID:  "u1",
		Text:        "how are you today?",
	})
	if err != nil {
		t.Fatalf("handle message failed: %v", err)
	}
	if !output.Handled {
		t.Fatal("expected fallback tool error path to remain handled")
	}
	if !strings.Contains(strings.ToLower(output.Reply), "not registered") {
		t.Fatalf("expected tool-specific error reply, got %q", output.Reply)
	}
}

func TestHandleAutoTriageFallbackAgentEmptyReplyReturnsStatusReply(t *testing.T) {
	fStore := &fakeStore{}
	service := New(fStore, &fakeEngine{}, nil, nil, "", nil)
	ack := &fakeTriageAcknowledger{
		reply: "   ",
	}
	service.SetTriageAcknowledger(ack)

	output, err := service.HandleMessage(context.Background(), MessageInput{
		Connector:   "telegram",
		ExternalID:  "42",
		DisplayName: "ops",
		FromUserID:  "u1",
		Text:        "how are you today?",
	})
	if err != nil {
		t.Fatalf("handle message failed: %v", err)
	}
	if !output.Handled {
		t.Fatal("expected fallback empty-reply path to remain handled")
	}
	if !strings.Contains(strings.ToLower(output.Reply), "still processing") {
		t.Fatalf("expected status reply for empty model output, got %q", output.Reply)
	}
}

func TestHandleAutoTriageFallbackAgentCanExecuteTools(t *testing.T) {
	fStore := &fakeStore{}
	fEngine := &fakeEngine{}
	service := New(fStore, fEngine, &fakeRetriever{}, nil, "", nil)
	ack := &fakeTriageAcknowledger{
		replies: []string{
			`{"tool":"create_task","args":{"title":"Investigate report","description":"follow up with logs","priority":"p2"}}`,
			`{"final":"I created a follow-up task and will keep you posted.","confidence":0.9}`,
		},
	}
	service.SetTriageAcknowledger(ack)

	output, err := service.HandleMessage(context.Background(), MessageInput{
		Connector:   "telegram",
		ExternalID:  "42",
		DisplayName: "ops",
		FromUserID:  "u1",
		Text:        "how are you today?",
	})
	if err != nil {
		t.Fatalf("handle message failed: %v", err)
	}
	if !output.Handled {
		t.Fatal("expected fallback agent response to handle message")
	}
	if !strings.Contains(strings.ToLower(output.Reply), "follow-up task") {
		t.Fatalf("expected final fallback reply, got %q", output.Reply)
	}
	if fStore.lastTask.ID == "" {
		t.Fatal("expected create_task tool to persist a task")
	}
	if fStore.lastTask.Title != "Investigate report" {
		t.Fatalf("expected tool task title, got %q", fStore.lastTask.Title)
	}
	if ack.callCount < 2 {
		t.Fatalf("expected looped agent calls, got %d", ack.callCount)
	}
}

func TestHandleAutoTriageFallbackAgentConvertsActionPayloadToRunActionTool(t *testing.T) {
	fStore := &fakeStore{}
	service := New(fStore, &fakeEngine{}, &fakeRetriever{}, nil, "", nil)
	ack := &fakeTriageAcknowledger{
		replies: []string{
			"I'll fetch this now.\n```action\n{\"type\":\"run_command\",\"target\":\"curl\",\"summary\":\"Fetch SWAPI person 3\",\"payload\":{\"args\":[\"-sS\",\"https://swapi.dev/api/people/3/\"]}}\n```",
			`{"final":"Queued for approval. Reply /approve-action act-1 to execute.","confidence":0.92}`,
		},
	}
	service.SetTriageAcknowledger(ack)

	output, err := service.HandleMessage(context.Background(), MessageInput{
		Connector:   "telegram",
		ExternalID:  "42",
		DisplayName: "ops",
		FromUserID:  "u1",
		Text:        "fetch swapi person 3 and summarize",
	})
	if err != nil {
		t.Fatalf("handle message failed: %v", err)
	}
	if !output.Handled {
		t.Fatal("expected fallback agent response to handle message")
	}
	if len(fStore.actionApprovals) != 1 {
		t.Fatalf("expected one action approval, got %d", len(fStore.actionApprovals))
	}
	if fStore.actionApprovals[0].ActionType != "run_command" {
		t.Fatalf("expected run_command action type, got %q", fStore.actionApprovals[0].ActionType)
	}
	if fStore.actionApprovals[0].ActionTarget != "curl" {
		t.Fatalf("expected curl action target, got %q", fStore.actionApprovals[0].ActionTarget)
	}
	if strings.Contains(output.Reply, "```action") {
		t.Fatalf("expected final reply without raw action block, got %q", output.Reply)
	}
}

func TestHandleAutoTriageFallbackAgentAppendsToolCallsToChatLog(t *testing.T) {
	workspaceRoot := t.TempDir()
	fStore := &fakeStore{}
	service := New(fStore, &fakeEngine{}, &fakeRetriever{}, nil, workspaceRoot, nil)
	ack := &fakeTriageAcknowledger{
		replies: []string{
			`{"tool":"create_task","args":{"title":"Investigate report","description":"follow up with logs","priority":"p2"}}`,
			`{"final":"Queued a follow-up task.","confidence":0.93}`,
		},
	}
	service.SetTriageAcknowledger(ack)

	output, err := service.HandleMessage(context.Background(), MessageInput{
		Connector:   "telegram",
		ExternalID:  "42",
		DisplayName: "ops",
		FromUserID:  "u1",
		Text:        "please investigate this",
	})
	if err != nil {
		t.Fatalf("handle message failed: %v", err)
	}
	if !output.Handled {
		t.Fatal("expected message to be handled")
	}

	logPath := filepath.Join(workspaceRoot, "ws-1", "logs", "chats", "telegram", "42.md")
	content, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("read log file: %v", err)
	}
	logText := string(content)
	if !strings.Contains(logText, "`TOOL`") {
		t.Fatalf("expected TOOL log entry, got %s", logText)
	}
	if !strings.Contains(logText, "Tool call") {
		t.Fatalf("expected tool call marker in log entry, got %s", logText)
	}
	if !strings.Contains(logText, "create_task") {
		t.Fatalf("expected tool name in log entry, got %s", logText)
	}
	if !strings.Contains(logText, "succeeded") {
		t.Fatalf("expected tool status in log entry, got %s", logText)
	}
}

func TestHandleAutoTriageSensitiveToolBlockedWithoutApproval(t *testing.T) {
	fStore := &fakeStore{}
	service := New(fStore, &fakeEngine{}, &fakeRetriever{}, nil, "", nil)
	ack := &fakeTriageAcknowledger{
		replies: []string{
			`{"tool":"create_objective","args":{"title":"Watch spam","prompt":"Monitor repeated spam"}}`,
		},
	}
	service.SetTriageAcknowledger(ack)

	output, err := service.HandleMessage(context.Background(), MessageInput{
		Connector:   "telegram",
		ExternalID:  "42",
		DisplayName: "ops",
		FromUserID:  "u1",
		Text:        "how are you today?",
	})
	if err != nil {
		t.Fatalf("handle message failed: %v", err)
	}
	if !output.Handled {
		t.Fatal("expected fallback path to handle the message")
	}
	if !strings.Contains(strings.ToLower(output.Reply), "explicit approval") {
		t.Fatalf("expected approval-required reply, got %q", output.Reply)
	}
	if fStore.objectiveInvoked {
		t.Fatal("expected sensitive objective tool to be blocked before execution")
	}
	if len(fStore.auditEvents) == 0 {
		t.Fatal("expected blocked sensitive attempt to be persisted as audit event")
	}
	lastAudit := fStore.auditEvents[len(fStore.auditEvents)-1]
	if lastAudit.EventType != "approval_required" {
		t.Fatalf("expected approval_required audit event type, got %s", lastAudit.EventType)
	}
	if strings.TrimSpace(lastAudit.ToolName) != "create_objective" {
		t.Fatalf("expected audit tool create_objective, got %s", lastAudit.ToolName)
	}
}

func TestHandleAutoTriageSensitiveApprovalTokenFromApproveAction(t *testing.T) {
	fStore := &fakeStore{
		identity: store.UserIdentity{UserID: "admin-1", Role: "admin"},
		actionApprovals: []store.ActionApproval{
			{ID: "act-1", ActionType: "run_command", Status: "pending"},
		},
	}
	service := New(fStore, &fakeEngine{}, &fakeRetriever{}, nil, "", nil)
	ack := &fakeTriageAcknowledger{
		replies: []string{
			`{"tool":"create_objective","args":{"title":"Watch spam","prompt":"Monitor repeated spam"}}`,
			`{"final":"Objective created and monitoring started.","confidence":0.9}`,
			`{"tool":"create_objective","args":{"title":"Watch spam","prompt":"Monitor repeated spam"}}`,
		},
	}
	service.SetTriageAcknowledger(ack)

	approveOutput, err := service.HandleMessage(context.Background(), MessageInput{
		Connector:  "telegram",
		ExternalID: "42",
		FromUserID: "u1",
		Text:       "/approve-action act-1",
	})
	if err != nil {
		t.Fatalf("approve-action failed: %v", err)
	}
	if !approveOutput.Handled {
		t.Fatal("expected approve-action to be handled")
	}

	firstRun, err := service.HandleMessage(context.Background(), MessageInput{
		Connector:   "telegram",
		ExternalID:  "42",
		DisplayName: "ops",
		FromUserID:  "u1",
		Text:        "how are you today?",
	})
	if err != nil {
		t.Fatalf("first fallback run failed: %v", err)
	}
	if !firstRun.Handled {
		t.Fatal("expected first fallback run to be handled")
	}
	if !strings.Contains(strings.ToLower(firstRun.Reply), "monitoring started") {
		t.Fatalf("expected successful sensitive-tool run after approval, got %q", firstRun.Reply)
	}
	if !fStore.objectiveInvoked {
		t.Fatal("expected create_objective to run with approval token")
	}

	// Token is one-shot; the next sensitive tool attempt should be blocked again.
	fStore.objectiveInvoked = false
	secondRun, err := service.HandleMessage(context.Background(), MessageInput{
		Connector:   "telegram",
		ExternalID:  "42",
		DisplayName: "ops",
		FromUserID:  "u1",
		Text:        "how are you today?",
	})
	if err != nil {
		t.Fatalf("second fallback run failed: %v", err)
	}
	if !secondRun.Handled {
		t.Fatal("expected second fallback run to be handled")
	}
	if !strings.Contains(strings.ToLower(secondRun.Reply), "explicit approval") {
		t.Fatalf("expected approval-required block after token consumption, got %q", secondRun.Reply)
	}
	if fStore.objectiveInvoked {
		t.Fatal("expected sensitive tool to be blocked after approval token is consumed")
	}
}

func TestHandleAutoTriageQuestionWithExternalResearchRoutesTask(t *testing.T) {
	fStore := &fakeStore{}
	service := New(fStore, &fakeEngine{}, nil, nil, "", nil)
	output, err := service.HandleMessage(context.Background(), MessageInput{
		Connector:   "telegram",
		ExternalID:  "42",
		DisplayName: "ops",
		FromUserID:  "u1",
		Text:        "can you run a search in dwizi.com and tell me pricing?",
	})
	if err != nil {
		t.Fatalf("handle message failed: %v", err)
	}
	if !output.Handled {
		t.Fatal("expected external research question to route and acknowledge")
	}
	if fStore.lastTask.ID == "" {
		t.Fatal("expected triaged task for external research question")
	}
	if fStore.lastTask.RouteClass != "question" {
		t.Fatalf("expected question route class, got %s", fStore.lastTask.RouteClass)
	}
}

func TestHandleAutoTriageAgentPrecedesLegacy(t *testing.T) {
	fStore := &fakeStore{}
	service := New(fStore, &fakeEngine{}, nil, nil, "", nil)
	ack := &fakeTriageAcknowledger{
		reply: "I am handling this via agent.",
	}
	service.SetTriageAcknowledger(ack)

	output, err := service.HandleMessage(context.Background(), MessageInput{
		Connector:   "telegram",
		ExternalID:  "42",
		DisplayName: "ops",
		FromUserID:  "u1",
		Text:        "There is a bug in the onboarding flow and it keeps failing",
	})
	if err != nil {
		t.Fatalf("handle message failed: %v", err)
	}
	if !output.Handled {
		t.Fatal("expected agent to handle message")
	}
	if output.Reply != "I am handling this via agent." {
		t.Fatalf("expected agent reply, got %q", output.Reply)
	}
	if fStore.lastTask.ID != "" {
		t.Fatal("expected no legacy task to be created automatically")
	}
	if ack.callCount != 1 {
		t.Fatalf("expected one llm call, got %d", ack.callCount)
	}
}

func TestHandleRouteOverrideCommand(t *testing.T) {
	fStore := &fakeStore{
		contextPolicy: store.ContextPolicy{
			ContextID:   "ctx-admin",
			WorkspaceID: "ws-1",
			IsAdmin:     true,
		},
		identity: store.UserIdentity{
			UserID: "admin-1",
			Role:   "admin",
		},
		tasks: map[string]store.TaskRecord{
			"task-1": {ID: "task-1", WorkspaceID: "ws-1", ContextID: "ctx-1"},
		},
	}
	service := New(fStore, &fakeEngine{}, nil, nil, "", nil)
	output, err := service.HandleMessage(context.Background(), MessageInput{
		Connector:  "telegram",
		ExternalID: "42",
		FromUserID: "admin-user",
		Text:       "/route task-1 moderation p1 2h",
	})
	if err != nil {
		t.Fatalf("handle message failed: %v", err)
	}
	if !output.Handled {
		t.Fatal("expected /route to be handled")
	}
	updated := fStore.tasks["task-1"]
	if updated.RouteClass != "moderation" {
		t.Fatalf("expected moderation class, got %s", updated.RouteClass)
	}
	if updated.Priority != "p1" {
		t.Fatalf("expected p1 priority, got %s", updated.Priority)
	}
	if updated.AssignedLane != "moderation" {
		t.Fatalf("expected moderation lane, got %s", updated.AssignedLane)
	}
	if updated.DueAt.IsZero() {
		t.Fatal("expected due at to be set")
	}
}

func TestHandleRouteOverrideRequiresAdminChannel(t *testing.T) {
	fStore := &fakeStore{
		contextPolicy: store.ContextPolicy{
			ContextID:   "ctx-1",
			WorkspaceID: "ws-1",
			IsAdmin:     false,
		},
		identity: store.UserIdentity{
			UserID: "admin-1",
			Role:   "admin",
		},
		tasks: map[string]store.TaskRecord{
			"task-1": {ID: "task-1", WorkspaceID: "ws-1", ContextID: "ctx-1"},
		},
	}
	service := New(fStore, &fakeEngine{}, nil, nil, "", nil)
	output, err := service.HandleMessage(context.Background(), MessageInput{
		Connector:  "telegram",
		ExternalID: "42",
		FromUserID: "admin-user",
		Text:       "/route task-1 moderation p1 2h",
	})
	if err != nil {
		t.Fatalf("handle message failed: %v", err)
	}
	if !output.Handled {
		t.Fatal("expected /route to be handled")
	}
	if !strings.Contains(strings.ToLower(output.Reply), "admin channels") {
		t.Fatalf("expected admin channel denial, got %q", output.Reply)
	}
}

func TestHandleRouteOverrideRejectsCrossWorkspaceTask(t *testing.T) {
	fStore := &fakeStore{
		contextPolicy: store.ContextPolicy{
			ContextID:   "ctx-admin",
			WorkspaceID: "ws-1",
			IsAdmin:     true,
		},
		identity: store.UserIdentity{
			UserID: "admin-1",
			Role:   "admin",
		},
		tasks: map[string]store.TaskRecord{
			"task-1": {ID: "task-1", WorkspaceID: "ws-2", ContextID: "ctx-other"},
		},
	}
	service := New(fStore, &fakeEngine{}, nil, nil, "", nil)
	output, err := service.HandleMessage(context.Background(), MessageInput{
		Connector:  "telegram",
		ExternalID: "42",
		FromUserID: "admin-user",
		Text:       "/route task-1 moderation p1 2h",
	})
	if err != nil {
		t.Fatalf("handle message failed: %v", err)
	}
	if !output.Handled {
		t.Fatal("expected /route to be handled")
	}
	if !strings.Contains(strings.ToLower(output.Reply), "different workspace") {
		t.Fatalf("expected workspace denial, got %q", output.Reply)
	}
}

func TestHandleAutoTriageSkipsNoise(t *testing.T) {
	fStore := &fakeStore{}
	service := New(fStore, &fakeEngine{}, nil, nil, "", nil)
	_, err := service.HandleMessage(context.Background(), MessageInput{
		Connector: "telegram",
		Text:      "ok",
	})
	if err != nil {
		t.Fatalf("handle message failed: %v", err)
	}
	if fStore.lastTask.ID != "" {
		t.Fatal("expected noise message to skip task routing")
	}
}

func TestHandleDenyPropagatesErrors(t *testing.T) {
	fStore := &fakeStore{
		identityErr: errors.New("db down"),
	}
	service := New(fStore, &fakeEngine{}, nil, nil, "", nil)
	_, err := service.HandleMessage(context.Background(), MessageInput{
		Connector:  "telegram",
		ExternalID: "42",
		FromUserID: "123",
		Text:       "/deny ABC123",
	})
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestHandleSearchCommand(t *testing.T) {
	service := New(
		&fakeStore{},
		&fakeEngine{},
		&fakeRetriever{
			searchResults: []qmd.SearchResult{
				{
					Path:    "memory.md",
					Score:   0.84,
					Snippet: "Recent decisions and notes",
				},
			},
		},
		nil,
		"",
		nil,
	)
	output, err := service.HandleMessage(context.Background(), MessageInput{
		Connector:  "telegram",
		ExternalID: "42",
		Text:       "/search recent decisions",
	})
	if err != nil {
		t.Fatalf("handle message failed: %v", err)
	}
	if !output.Handled {
		t.Fatal("expected command handled")
	}
	if output.Reply == "" {
		t.Fatal("expected search response")
	}
}

func TestHandleOpenCommand(t *testing.T) {
	service := New(
		&fakeStore{},
		&fakeEngine{},
		&fakeRetriever{
			openResult: qmd.OpenResult{
				Path:      "notes/today.md",
				Content:   "hello",
				Truncated: false,
			},
		},
		nil,
		"",
		nil,
	)
	output, err := service.HandleMessage(context.Background(), MessageInput{
		Connector:  "telegram",
		ExternalID: "42",
		Text:       "/open notes/today.md",
	})
	if err != nil {
		t.Fatalf("handle message failed: %v", err)
	}
	if !output.Handled {
		t.Fatal("expected command handled")
	}
	if output.Reply == "" {
		t.Fatal("expected open response")
	}
}

func TestHandleStatusCommand(t *testing.T) {
	service := New(
		&fakeStore{},
		&fakeEngine{},
		&fakeRetriever{
			statusResult: qmd.Status{
				WorkspaceID:    "ws-1",
				WorkspaceExist: true,
				Indexed:        true,
				IndexExists:    true,
				Summary:        "collection: workspace",
			},
		},
		nil,
		"",
		nil,
	)
	output, err := service.HandleMessage(context.Background(), MessageInput{
		Connector:  "telegram",
		ExternalID: "42",
		Text:       "/status",
	})
	if err != nil {
		t.Fatalf("handle message failed: %v", err)
	}
	if !output.Handled {
		t.Fatal("expected command handled")
	}
	if !strings.Contains(output.Reply, "qmd status") {
		t.Fatalf("expected status response, got %s", output.Reply)
	}
}

func TestHandlePromptSetCommand(t *testing.T) {
	service := New(
		&fakeStore{
			identity: store.UserIdentity{
				UserID: "user-1",
				Role:   "admin",
			},
		},
		&fakeEngine{},
		&fakeRetriever{},
		nil,
		"",
		nil,
	)
	output, err := service.HandleMessage(context.Background(), MessageInput{
		Connector:  "telegram",
		ExternalID: "42",
		FromUserID: "u1",
		Text:       "/prompt set You are strict",
	})
	if err != nil {
		t.Fatalf("handle message failed: %v", err)
	}
	if !output.Handled {
		t.Fatal("expected command handled")
	}
	if !strings.Contains(output.Reply, "updated") {
		t.Fatalf("expected updated message, got %s", output.Reply)
	}
}

func TestHandleMonitorNaturalLanguageIntentCreatesObjective(t *testing.T) {
	fStore := &fakeStore{}
	service := New(fStore, &fakeEngine{}, nil, nil, "", nil)
	output, err := service.HandleMessage(context.Background(), MessageInput{
		Connector:   "telegram",
		ExternalID:  "42",
		DisplayName: "ops",
		FromUserID:  "u1",
		Text:        "set an alert and monitor dwizi pricing changes",
	})
	if err != nil {
		t.Fatalf("handle message failed: %v", err)
	}
	if !output.Handled {
		t.Fatal("expected monitor intent to be handled")
	}
	if !fStore.objectiveInvoked {
		t.Fatal("expected objective creation to be invoked")
	}
	if fStore.lastObjective.TriggerType != store.ObjectiveTriggerSchedule {
		t.Fatalf("expected schedule trigger, got %s", fStore.lastObjective.TriggerType)
	}
	if fStore.lastObjective.CronExpr != defaultObjectiveCronExpr {
		t.Fatalf("expected default objective cron expression %q, got %q", defaultObjectiveCronExpr, fStore.lastObjective.CronExpr)
	}
}

func TestHandleMonitorNaturalLanguageObjectivePhraseCreatesObjective(t *testing.T) {
	fStore := &fakeStore{}
	service := New(fStore, &fakeEngine{}, nil, nil, "", nil)
	output, err := service.HandleMessage(context.Background(), MessageInput{
		Connector:   "telegram",
		ExternalID:  "42",
		DisplayName: "ops",
		FromUserID:  "u1",
		Text:        "Create a monitoring objective for OpenAI API status and tell me what you set.",
	})
	if err != nil {
		t.Fatalf("handle message failed: %v", err)
	}
	if !output.Handled {
		t.Fatal("expected monitor objective phrase to be handled")
	}
	if !fStore.objectiveInvoked {
		t.Fatal("expected objective creation to be invoked")
	}
	if !strings.Contains(strings.ToLower(fStore.lastObjective.Prompt), "openai api status") {
		t.Fatalf("expected objective prompt to include cleaned monitor target, got %q", fStore.lastObjective.Prompt)
	}
}

func TestHandleMonitorNaturalLanguageIntentCreatesObjectiveForCodexConnector(t *testing.T) {
	fStore := &fakeStore{}
	service := New(fStore, &fakeEngine{}, nil, nil, "", nil)
	output, err := service.HandleMessage(context.Background(), MessageInput{
		Connector:   "codex",
		ExternalID:  "session-1",
		DisplayName: "Codex CLI",
		FromUserID:  "codex-cli",
		Text:        "monitor this repository for release notes changes",
	})
	if err != nil {
		t.Fatalf("handle message failed: %v", err)
	}
	if !output.Handled {
		t.Fatal("expected codex monitor intent to be handled")
	}
	if !fStore.objectiveInvoked {
		t.Fatal("expected objective creation to be invoked for codex connector")
	}
	if fStore.lastObjective.TriggerType != store.ObjectiveTriggerSchedule {
		t.Fatalf("expected schedule trigger, got %s", fStore.lastObjective.TriggerType)
	}
	if fStore.lastObjective.CronExpr != defaultObjectiveCronExpr {
		t.Fatalf("expected default objective cron expression %q, got %q", defaultObjectiveCronExpr, fStore.lastObjective.CronExpr)
	}
}

func TestHandleMonitorCommandCreatesObjectiveForCodexConnector(t *testing.T) {
	fStore := &fakeStore{}
	service := New(fStore, &fakeEngine{}, nil, nil, "", nil)
	output, err := service.HandleMessage(context.Background(), MessageInput{
		Connector:   "codex",
		ExternalID:  "session-2",
		DisplayName: "Codex CLI",
		FromUserID:  "codex-cli",
		Text:        "/monitor OpenAI API status page",
	})
	if err != nil {
		t.Fatalf("handle message failed: %v", err)
	}
	if !output.Handled {
		t.Fatal("expected codex /monitor command to be handled")
	}
	if !fStore.objectiveInvoked {
		t.Fatal("expected objective creation to be invoked for /monitor")
	}
	if fStore.lastObjective.CronExpr != defaultObjectiveCronExpr {
		t.Fatalf("expected default objective cron expression %q, got %q", defaultObjectiveCronExpr, fStore.lastObjective.CronExpr)
	}
}

func TestHandlePendingActionsCommand(t *testing.T) {
	service := New(
		&fakeStore{
			identity: store.UserIdentity{UserID: "admin-1", Role: "admin"},
			actionApprovals: []store.ActionApproval{
				{ID: "act-1", ActionType: "send_email", ActionSummary: "Send digest", Status: "pending"},
			},
		},
		&fakeEngine{},
		&fakeRetriever{},
		nil,
		"",
		nil,
	)
	output, err := service.HandleMessage(context.Background(), MessageInput{
		Connector:  "telegram",
		ExternalID: "42",
		FromUserID: "u1",
		Text:       "/pending-actions",
	})
	if err != nil {
		t.Fatalf("handle message failed: %v", err)
	}
	if !output.Handled || !strings.Contains(output.Reply, "act-1") {
		t.Fatalf("expected pending actions list, got %s", output.Reply)
	}
}

func TestHandlePendingActionsCommandHelpReturnsCommandWithoutIdentity(t *testing.T) {
	service := New(
		&fakeStore{
			identityErr: store.ErrIdentityNotFound,
		},
		&fakeEngine{},
		&fakeRetriever{},
		nil,
		"",
		nil,
	)
	output, err := service.HandleMessage(context.Background(), MessageInput{
		Connector:  "telegram",
		ExternalID: "42",
		FromUserID: "u1",
		Text:       "What command should I use to list pending actions?",
	})
	if err != nil {
		t.Fatalf("handle message failed: %v", err)
	}
	if !output.Handled {
		t.Fatal("expected command-help message to be handled")
	}
	if !strings.Contains(output.Reply, "/pending-actions") {
		t.Fatalf("expected pending-actions command guidance, got %q", output.Reply)
	}
	if strings.Contains(strings.ToLower(output.Reply), "access denied") {
		t.Fatalf("expected guidance instead of access denial, got %q", output.Reply)
	}
}

func TestHandleApprovalCommandHelpReturnsExactApproveActionWhenUnlinked(t *testing.T) {
	service := New(
		&fakeStore{
			identityErr: store.ErrIdentityNotFound,
			actionApprovals: []store.ActionApproval{
				{ID: "act_1234abcd", Connector: "codex", ExternalID: "session-1", ActionType: "run_command", Status: "pending"},
			},
		},
		&fakeEngine{},
		&fakeRetriever{},
		nil,
		"",
		nil,
	)
	output, err := service.HandleMessage(context.Background(), MessageInput{
		Connector:  "codex",
		ExternalID: "session-1",
		FromUserID: "u1",
		Text:       "If approval is needed, tell me the exact next command I should run.",
	})
	if err != nil {
		t.Fatalf("handle message failed: %v", err)
	}
	if !output.Handled {
		t.Fatal("expected approval-help message to be handled")
	}
	if !strings.Contains(output.Reply, "/approve-action act_1234abcd") {
		t.Fatalf("expected exact approve-action command with id, got %q", output.Reply)
	}
	if !strings.Contains(strings.ToLower(output.Reply), "pair") {
		t.Fatalf("expected identity-linking next step, got %q", output.Reply)
	}
}

func TestHandlePendingApprovalHowToGuidanceWithoutIdentity(t *testing.T) {
	service := New(
		&fakeStore{
			identityErr: store.ErrIdentityNotFound,
		},
		&fakeEngine{},
		&fakeRetriever{},
		nil,
		"",
		nil,
	)
	output, err := service.HandleMessage(context.Background(), MessageInput{
		Connector:  "codex",
		ExternalID: "session-1",
		FromUserID: "u1",
		Text:       "Without using slash commands, explain how I can ask for pending approvals in plain language.",
	})
	if err != nil {
		t.Fatalf("handle message failed: %v", err)
	}
	if !output.Handled {
		t.Fatal("expected pending-approval guidance to be handled")
	}
	if strings.Contains(strings.ToLower(output.Reply), "access denied") {
		t.Fatalf("expected guidance without access denial, got %q", output.Reply)
	}
	if !strings.Contains(strings.ToLower(output.Reply), "show me pending approvals") {
		t.Fatalf("expected plain-language guidance phrase, got %q", output.Reply)
	}
}

func TestHandlePendingApprovalPrioritizationGuidanceWithoutIdentity(t *testing.T) {
	service := New(
		&fakeStore{
			identityErr: store.ErrIdentityNotFound,
		},
		&fakeEngine{},
		&fakeRetriever{},
		nil,
		"",
		nil,
	)
	output, err := service.HandleMessage(context.Background(), MessageInput{
		Connector:  "codex",
		ExternalID: "session-1",
		FromUserID: "u1",
		Text:       "If there are many pending approvals, what should I do first and why?",
	})
	if err != nil {
		t.Fatalf("handle message failed: %v", err)
	}
	if !output.Handled {
		t.Fatal("expected pending-approval prioritization guidance to be handled")
	}
	if strings.Contains(strings.ToLower(output.Reply), "access denied") {
		t.Fatalf("expected guidance without access denial, got %q", output.Reply)
	}
	if !strings.Contains(strings.ToLower(output.Reply), "prioritize") {
		t.Fatalf("expected prioritization guidance, got %q", output.Reply)
	}
}

func TestHandleApproveMostRecentPendingNaturalLanguage(t *testing.T) {
	service := New(
		&fakeStore{
			identity: store.UserIdentity{UserID: "admin-1", Role: "admin"},
			actionApprovals: []store.ActionApproval{
				{ID: "act_aaaa1111", Connector: "telegram", ExternalID: "42", ActionType: "run_command", Status: "pending"},
				{ID: "act_bbbb2222", Connector: "telegram", ExternalID: "42", ActionType: "run_command", Status: "pending"},
			},
		},
		&fakeEngine{},
		&fakeRetriever{},
		nil,
		"",
		nil,
	)
	output, err := service.HandleMessage(context.Background(), MessageInput{
		Connector:  "telegram",
		ExternalID: "42",
		FromUserID: "u1",
		Text:       "Please approve the most recent pending action and continue.",
	})
	if err != nil {
		t.Fatalf("handle message failed: %v", err)
	}
	if !output.Handled {
		t.Fatal("expected most-recent approve intent to be handled")
	}
	if !strings.Contains(output.Reply, "act_bbbb2222") {
		t.Fatalf("expected latest pending action to be approved, got %q", output.Reply)
	}
}

func TestHandleTaskCreationIntentTurnThatIntoTaskCreatesTask(t *testing.T) {
	fStore := &fakeStore{}
	service := New(fStore, &fakeEngine{}, nil, nil, "", nil)
	output, err := service.HandleMessage(context.Background(), MessageInput{
		Connector:  "codex",
		ExternalID: "session-1",
		FromUserID: "u1",
		Text:       "Turn that into an actionable task in this workspace and tell me the task id.",
	})
	if err != nil {
		t.Fatalf("handle message failed: %v", err)
	}
	if !output.Handled {
		t.Fatal("expected task-creation intent to be handled")
	}
	if fStore.lastTask.ID == "" {
		t.Fatal("expected task to be created")
	}
	if !strings.Contains(output.Reply, fStore.lastTask.ID) {
		t.Fatalf("expected reply to include created task id, got %q", output.Reply)
	}
}

func TestHandlePendingActionsNaturalLanguage(t *testing.T) {
	service := New(
		&fakeStore{
			identity: store.UserIdentity{UserID: "admin-1", Role: "admin"},
			actionApprovals: []store.ActionApproval{
				{ID: "act-1", ActionType: "send_email", ActionSummary: "Send digest", Status: "pending"},
			},
		},
		&fakeEngine{},
		&fakeRetriever{},
		nil,
		"",
		nil,
	)
	output, err := service.HandleMessage(context.Background(), MessageInput{
		Connector:  "telegram",
		ExternalID: "42",
		FromUserID: "u1",
		Text:       "Can you show pending actions?",
	})
	if err != nil {
		t.Fatalf("handle message failed: %v", err)
	}
	if !output.Handled || !strings.Contains(output.Reply, "act-1") {
		t.Fatalf("expected pending actions list, got %s", output.Reply)
	}
}

func TestHandlePendingActionsFallsBackToGlobalList(t *testing.T) {
	service := New(
		&fakeStore{
			identity: store.UserIdentity{UserID: "admin-1", Role: "admin"},
			actionApprovals: []store.ActionApproval{
				{ID: "act_1234abcd", Connector: "discord", ExternalID: "chan-1", ActionType: "send_email", ActionSummary: "Send digest", Status: "pending"},
			},
		},
		&fakeEngine{},
		&fakeRetriever{},
		nil,
		"",
		nil,
	)
	output, err := service.HandleMessage(context.Background(), MessageInput{
		Connector:  "telegram",
		ExternalID: "42",
		FromUserID: "u1",
		Text:       "/pending-actions",
	})
	if err != nil {
		t.Fatalf("handle message failed: %v", err)
	}
	if !output.Handled || !strings.Contains(output.Reply, "all contexts") {
		t.Fatalf("expected global pending actions list, got %s", output.Reply)
	}
	if !strings.Contains(output.Reply, "discord/chan-1") {
		t.Fatalf("expected source details in global list, got %s", output.Reply)
	}
}

func TestHandleApproveActionCommand(t *testing.T) {
	fStore := &fakeStore{
		identity: store.UserIdentity{UserID: "admin-1", Role: "admin"},
		actionApprovals: []store.ActionApproval{
			{ID: "act-1", ActionType: "send_email", Status: "pending"},
		},
	}
	service := New(
		fStore,
		&fakeEngine{},
		&fakeRetriever{},
		nil,
		"",
		nil,
	)
	output, err := service.HandleMessage(context.Background(), MessageInput{
		Connector:  "telegram",
		ExternalID: "42",
		FromUserID: "u1",
		Text:       "/approve-action act-1",
	})
	if err != nil {
		t.Fatalf("handle message failed: %v", err)
	}
	if !output.Handled || !strings.Contains(output.Reply, "approved") {
		t.Fatalf("expected approved output, got %s", output.Reply)
	}
	if !strings.Contains(output.Reply, "Outcome:") {
		t.Fatalf("expected outcome summary in reply, got %s", output.Reply)
	}
	if !fStore.executionUpdateInvoked {
		t.Fatal("expected execution update when action is approved")
	}
	if fStore.lastExecutionUpdate.ExecutionStatus != "skipped" {
		t.Fatalf("expected skipped execution status, got %s", fStore.lastExecutionUpdate.ExecutionStatus)
	}
}

func TestHandleApproveActionCommandAcceptsQuotedID(t *testing.T) {
	fStore := &fakeStore{
		identity: store.UserIdentity{UserID: "admin-1", Role: "admin"},
		actionApprovals: []store.ActionApproval{
			{ID: "act_9999ffff", ActionType: "send_email", Status: "pending"},
		},
	}
	service := New(
		fStore,
		&fakeEngine{},
		&fakeRetriever{},
		nil,
		"",
		nil,
	)
	output, err := service.HandleMessage(context.Background(), MessageInput{
		Connector:  "telegram",
		ExternalID: "42",
		FromUserID: "u1",
		Text:       "/approve-action 'act_9999ffff'",
	})
	if err != nil {
		t.Fatalf("handle message failed: %v", err)
	}
	if !output.Handled || !strings.Contains(output.Reply, "approved") {
		t.Fatalf("expected quoted action id to be approved, got %s", output.Reply)
	}
}

func TestHandleApproveActionNaturalLanguageFallsBackToGlobalPending(t *testing.T) {
	fStore := &fakeStore{
		identity: store.UserIdentity{UserID: "admin-1", Role: "admin"},
		actionApprovals: []store.ActionApproval{
			{ID: "act_1234abcd", Connector: "discord", ExternalID: "chan-1", ActionType: "send_email", Status: "pending"},
		},
	}
	service := New(
		fStore,
		&fakeEngine{},
		&fakeRetriever{},
		nil,
		"",
		nil,
	)
	output, err := service.HandleMessage(context.Background(), MessageInput{
		Connector:  "telegram",
		ExternalID: "42",
		FromUserID: "u1",
		Text:       "yes, i approve it",
	})
	if err != nil {
		t.Fatalf("handle message failed: %v", err)
	}
	if !output.Handled || !strings.Contains(output.Reply, "act_1234abcd") {
		t.Fatalf("expected global pending action approval to run, got %s", output.Reply)
	}
}

func TestHandleApproveActionCommandExecutesPlugin(t *testing.T) {
	fStore := &fakeStore{
		identity: store.UserIdentity{UserID: "admin-1", Role: "admin"},
		actionApprovals: []store.ActionApproval{
			{ID: "act-1", ActionType: "http_request", Status: "pending"},
		},
	}
	service := New(
		fStore,
		&fakeEngine{},
		&fakeRetriever{},
		&fakeActionExecutor{
			result: executor.Result{
				Plugin:  "webhook",
				Message: "webhook request completed with status 200",
			},
		},
		"",
		nil,
	)
	output, err := service.HandleMessage(context.Background(), MessageInput{
		Connector:  "telegram",
		ExternalID: "42",
		FromUserID: "u1",
		Text:       "/approve-action act-1",
	})
	if err != nil {
		t.Fatalf("handle message failed: %v", err)
	}
	if !output.Handled || !strings.Contains(output.Reply, "ran it with") {
		t.Fatalf("expected executed output, got %s", output.Reply)
	}
	if !strings.Contains(output.Reply, "Outcome:") {
		t.Fatalf("expected outcome summary in reply, got %s", output.Reply)
	}
	if fStore.lastExecutionUpdate.ExecutionStatus != "succeeded" {
		t.Fatalf("expected succeeded status, got %s", fStore.lastExecutionUpdate.ExecutionStatus)
	}
}

func TestHandleApproveActionCommandExecutionFailure(t *testing.T) {
	fStore := &fakeStore{
		identity: store.UserIdentity{UserID: "admin-1", Role: "admin"},
		actionApprovals: []store.ActionApproval{
			{ID: "act-1", ActionType: "http_request", Status: "pending"},
		},
	}
	service := New(
		fStore,
		&fakeEngine{},
		&fakeRetriever{},
		&fakeActionExecutor{
			err: errors.New("target blocked by policy"),
		},
		"",
		nil,
	)
	output, err := service.HandleMessage(context.Background(), MessageInput{
		Connector:  "telegram",
		ExternalID: "42",
		FromUserID: "u1",
		Text:       "/approve-action act-1",
	})
	if err != nil {
		t.Fatalf("handle message failed: %v", err)
	}
	if !output.Handled || !strings.Contains(output.Reply, "failed") {
		t.Fatalf("expected execution failure output, got %s", output.Reply)
	}
	if !strings.Contains(output.Reply, "Outcome:") {
		t.Fatalf("expected outcome summary in failure reply, got %s", output.Reply)
	}
	if fStore.lastExecutionUpdate.ExecutionStatus != "failed" {
		t.Fatalf("expected failed status, got %s", fStore.lastExecutionUpdate.ExecutionStatus)
	}
}

func TestHandleDenyActionCommand(t *testing.T) {
	service := New(
		&fakeStore{
			identity: store.UserIdentity{UserID: "admin-1", Role: "admin"},
			actionApprovals: []store.ActionApproval{
				{ID: "act-1", ActionType: "send_email", Status: "pending"},
			},
		},
		&fakeEngine{},
		&fakeRetriever{},
		nil,
		"",
		nil,
	)
	output, err := service.HandleMessage(context.Background(), MessageInput{
		Connector:  "telegram",
		ExternalID: "42",
		FromUserID: "u1",
		Text:       "/deny-action act-1 not needed",
	})
	if err != nil {
		t.Fatalf("handle message failed: %v", err)
	}
	if !output.Handled || !strings.Contains(output.Reply, "denied") {
		t.Fatalf("expected denied output, got %s", output.Reply)
	}
}

func TestHandleSearchNaturalLanguage(t *testing.T) {
	service := New(
		&fakeStore{},
		&fakeEngine{},
		&fakeRetriever{
			searchResults: []qmd.SearchResult{
				{
					Path:    "memory.md",
					Score:   0.84,
					Snippet: "Recent decisions and notes",
				},
			},
		},
		nil,
		"",
		nil,
	)
	output, err := service.HandleMessage(context.Background(), MessageInput{
		Connector:  "telegram",
		ExternalID: "42",
		Text:       "search for recent decisions",
	})
	if err != nil {
		t.Fatalf("handle message failed: %v", err)
	}
	if !output.Handled || !strings.Contains(output.Reply, "memory.md") {
		t.Fatalf("expected natural-language search response, got %s", output.Reply)
	}
}

func TestHandleOpenNaturalLanguage(t *testing.T) {
	service := New(
		&fakeStore{},
		&fakeEngine{},
		&fakeRetriever{
			openResult: qmd.OpenResult{
				Path:      "notes/today.md",
				Content:   "hello",
				Truncated: false,
			},
		},
		nil,
		"",
		nil,
	)
	output, err := service.HandleMessage(context.Background(), MessageInput{
		Connector:  "telegram",
		ExternalID: "42",
		Text:       "open file notes/today.md",
	})
	if err != nil {
		t.Fatalf("handle message failed: %v", err)
	}
	if !output.Handled || !strings.Contains(output.Reply, "notes/today.md") {
		t.Fatalf("expected natural-language open response, got %s", output.Reply)
	}
}

func TestHandleStatusNaturalLanguage(t *testing.T) {
	service := New(
		&fakeStore{},
		&fakeEngine{},
		&fakeRetriever{
			statusResult: qmd.Status{
				WorkspaceID:    "ws-1",
				WorkspaceExist: true,
				Indexed:        true,
				IndexExists:    true,
				Summary:        "collection: workspace",
			},
		},
		nil,
		"",
		nil,
	)
	output, err := service.HandleMessage(context.Background(), MessageInput{
		Connector:  "telegram",
		ExternalID: "42",
		Text:       "what is the qmd status?",
	})
	if err != nil {
		t.Fatalf("handle message failed: %v", err)
	}
	if !output.Handled || !strings.Contains(output.Reply, "qmd status") {
		t.Fatalf("expected natural-language status response, got %s", output.Reply)
	}
}

func TestHandlePromptNaturalLanguage(t *testing.T) {
	service := New(
		&fakeStore{
			identity: store.UserIdentity{
				UserID: "user-1",
				Role:   "admin",
			},
		},
		&fakeEngine{},
		&fakeRetriever{},
		nil,
		"",
		nil,
	)
	output, err := service.HandleMessage(context.Background(), MessageInput{
		Connector:  "telegram",
		ExternalID: "42",
		FromUserID: "u1",
		Text:       "set prompt to You are strict",
	})
	if err != nil {
		t.Fatalf("handle message failed: %v", err)
	}
	if !output.Handled || !strings.Contains(output.Reply, "updated") {
		t.Fatalf("expected natural-language prompt update, got %s", output.Reply)
	}
}

func TestHandleAdminChannelEnableNaturalLanguage(t *testing.T) {
	fStore := &fakeStore{
		identity: store.UserIdentity{
			UserID: "user-1",
			Role:   "admin",
		},
	}
	service := New(fStore, &fakeEngine{}, nil, nil, "", nil)
	output, err := service.HandleMessage(context.Background(), MessageInput{
		Connector:  "telegram",
		ExternalID: "42",
		FromUserID: "123",
		Text:       "please enable admin channel for this chat",
	})
	if err != nil {
		t.Fatalf("handle message failed: %v", err)
	}
	if !output.Handled || !fStore.adminUpdated {
		t.Fatal("expected natural-language admin channel enable")
	}
}

func TestHandleApproveActionNaturalLanguage(t *testing.T) {
	fStore := &fakeStore{
		identity: store.UserIdentity{UserID: "admin-1", Role: "admin"},
		actionApprovals: []store.ActionApproval{
			{ID: "act_1234abcd", ActionType: "run_command", Status: "pending"},
		},
	}
	service := New(
		fStore,
		&fakeEngine{},
		&fakeRetriever{},
		&fakeActionExecutor{
			result: executor.Result{
				Plugin:  "sandbox",
				Message: "command completed",
			},
		},
		"",
		nil,
	)
	output, err := service.HandleMessage(context.Background(), MessageInput{
		Connector:  "telegram",
		ExternalID: "42",
		FromUserID: "u1",
		Text:       "Please approve action act_1234abcd now",
	})
	if err != nil {
		t.Fatalf("handle message failed: %v", err)
	}
	if !output.Handled || !strings.Contains(output.Reply, "Outcome:") {
		t.Fatalf("expected natural-language approve action to execute, got %s", output.Reply)
	}
	if !strings.Contains(output.Reply, "ran it with") {
		t.Fatalf("expected execution explanation in reply, got %s", output.Reply)
	}
}

func TestHandleApproveActionNaturalLanguageImplicitLatest(t *testing.T) {
	fStore := &fakeStore{
		identity: store.UserIdentity{UserID: "admin-1", Role: "admin"},
		actionApprovals: []store.ActionApproval{
			{ID: "act-implicit", ActionType: "run_command", Status: "pending"},
		},
	}
	service := New(
		fStore,
		&fakeEngine{},
		&fakeRetriever{},
		&fakeActionExecutor{
			result: executor.Result{
				Plugin:  "sandbox",
				Message: "command completed",
			},
		},
		"",
		nil,
	)
	output, err := service.HandleMessage(context.Background(), MessageInput{
		Connector:  "discord",
		ExternalID: "42",
		FromUserID: "u1",
		Text:       "yes, i approve it",
	})
	if err != nil {
		t.Fatalf("handle message failed: %v", err)
	}
	if !output.Handled || !strings.Contains(output.Reply, "Outcome:") {
		t.Fatalf("expected implicit nl approve action to execute, got %s", output.Reply)
	}
	if !strings.Contains(output.Reply, "ran it with") {
		t.Fatalf("expected execution explanation in reply, got %s", output.Reply)
	}
}

func TestHandleDenyActionNaturalLanguage(t *testing.T) {
	fStore := &fakeStore{
		identity: store.UserIdentity{UserID: "admin-1", Role: "admin"},
		actionApprovals: []store.ActionApproval{
			{ID: "act_9999ffff", ActionType: "send_email", Status: "pending"},
		},
	}
	service := New(
		fStore,
		&fakeEngine{},
		&fakeRetriever{},
		nil,
		"",
		nil,
	)
	output, err := service.HandleMessage(context.Background(), MessageInput{
		Connector:  "discord",
		ExternalID: "chan-1",
		FromUserID: "u1",
		Text:       "Reject action act_9999ffff because unsafe target",
	})
	if err != nil {
		t.Fatalf("handle message failed: %v", err)
	}
	if !output.Handled || !strings.Contains(output.Reply, "denied") {
		t.Fatalf("expected natural-language deny action to be handled, got %s", output.Reply)
	}
	if fStore.actionApprovals[0].DeniedReason != "unsafe target" {
		t.Fatalf("expected deny reason from natural language, got %q", fStore.actionApprovals[0].DeniedReason)
	}
}

func TestHandleDenyActionNaturalLanguageImplicitLatest(t *testing.T) {
	fStore := &fakeStore{
		identity: store.UserIdentity{UserID: "admin-1", Role: "admin"},
		actionApprovals: []store.ActionApproval{
			{ID: "act-implicit-deny", ActionType: "send_email", Status: "pending"},
		},
	}
	service := New(
		fStore,
		&fakeEngine{},
		&fakeRetriever{},
		nil,
		"",
		nil,
	)
	output, err := service.HandleMessage(context.Background(), MessageInput{
		Connector:  "telegram",
		ExternalID: "42",
		FromUserID: "u1",
		Text:       "deny it because unsafe command",
	})
	if err != nil {
		t.Fatalf("handle message failed: %v", err)
	}
	if !output.Handled || !strings.Contains(output.Reply, "denied") {
		t.Fatalf("expected implicit nl deny action to be handled, got %s", output.Reply)
	}
	if fStore.actionApprovals[0].DeniedReason != "unsafe command" {
		t.Fatalf("expected implicit deny reason, got %q", fStore.actionApprovals[0].DeniedReason)
	}
}

func TestHandleApproveActionImplicitMultiplePending(t *testing.T) {
	fStore := &fakeStore{
		identity: store.UserIdentity{UserID: "admin-1", Role: "admin"},
		actionApprovals: []store.ActionApproval{
			{ID: "act-a", ActionType: "run_command", Status: "pending"},
			{ID: "act-b", ActionType: "run_command", Status: "pending"},
		},
	}
	service := New(
		fStore,
		&fakeEngine{},
		&fakeRetriever{},
		nil,
		"",
		nil,
	)
	output, err := service.HandleMessage(context.Background(), MessageInput{
		Connector:  "discord",
		ExternalID: "42",
		FromUserID: "u1",
		Text:       "approve it",
	})
	if err != nil {
		t.Fatalf("handle message failed: %v", err)
	}
	if !output.Handled || !strings.Contains(strings.ToLower(output.Reply), "multiple pending actions") {
		t.Fatalf("expected multiple pending hint, got %s", output.Reply)
	}
}
