package gateway

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/carlos/spinner/internal/actions/executor"
	"github.com/carlos/spinner/internal/orchestrator"
	"github.com/carlos/spinner/internal/qmd"
	"github.com/carlos/spinner/internal/store"
)

type fakeStore struct {
	contextRecord          store.ContextRecord
	contextPolicy          store.ContextPolicy
	identity               store.UserIdentity
	identityErr            error
	lastTask               store.CreateTaskInput
	adminUpdated           bool
	approved               bool
	denied                 bool
	actionApprovals        []store.ActionApproval
	lastExecutionUpdate    store.UpdateActionExecutionInput
	executionUpdateInvoked bool
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
	return nil
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

func (f *fakeActionExecutor) Execute(ctx context.Context, approval store.ActionApproval) (executor.Result, error) {
	if f.err != nil {
		return executor.Result{}, f.err
	}
	return f.result, nil
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
	service := New(fStore, fEngine, nil, nil)

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

func TestHandleAdminChannelEnableRequiresAdmin(t *testing.T) {
	fStore := &fakeStore{
		identityErr: store.ErrIdentityNotFound,
	}
	service := New(fStore, &fakeEngine{}, nil, nil)
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
	service := New(fStore, &fakeEngine{}, nil, nil)
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
	service := New(fStore, &fakeEngine{}, nil, nil)
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

func TestHandleIgnoresUnknownMessage(t *testing.T) {
	service := New(&fakeStore{}, &fakeEngine{}, nil, nil)
	output, err := service.HandleMessage(context.Background(), MessageInput{
		Connector: "telegram",
		Text:      "hello world",
	})
	if err != nil {
		t.Fatalf("handle message failed: %v", err)
	}
	if output.Handled {
		t.Fatal("expected unknown message to be ignored")
	}
}

func TestHandleDenyPropagatesErrors(t *testing.T) {
	fStore := &fakeStore{
		identityErr: errors.New("db down"),
	}
	service := New(fStore, &fakeEngine{}, nil, nil)
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
	if !fStore.executionUpdateInvoked {
		t.Fatal("expected execution update when action is approved")
	}
	if fStore.lastExecutionUpdate.ExecutionStatus != "skipped" {
		t.Fatalf("expected skipped execution status, got %s", fStore.lastExecutionUpdate.ExecutionStatus)
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
	if !output.Handled || !strings.Contains(output.Reply, "executed") {
		t.Fatalf("expected executed output, got %s", output.Reply)
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
