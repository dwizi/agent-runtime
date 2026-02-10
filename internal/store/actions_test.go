package store

import (
	"context"
	"testing"
	"time"
)

func TestActionApprovalLifecycle(t *testing.T) {
	sqlStore := newTestStore(t)
	ctx := context.Background()

	created, err := sqlStore.CreateActionApproval(ctx, CreateActionApprovalInput{
		WorkspaceID:     "ws-1",
		ContextID:       "ctx-1",
		Connector:       "telegram",
		ExternalID:      "42",
		RequesterUserID: "user-1",
		ActionType:      "send_email",
		ActionTarget:    "ops@example.com",
		ActionSummary:   "Send digest",
		Payload: map[string]any{
			"subject": "Daily digest",
		},
	})
	if err != nil {
		t.Fatalf("create action approval: %v", err)
	}
	if created.ID == "" {
		t.Fatal("expected action approval id")
	}

	pending, err := sqlStore.ListPendingActionApprovals(ctx, "telegram", "42", 10)
	if err != nil {
		t.Fatalf("list pending action approvals: %v", err)
	}
	if len(pending) != 1 {
		t.Fatalf("expected one pending action, got %d", len(pending))
	}

	approved, err := sqlStore.ApproveActionApproval(ctx, ApproveActionApprovalInput{
		ID:             created.ID,
		ApproverUserID: "admin-1",
	})
	if err != nil {
		t.Fatalf("approve action approval: %v", err)
	}
	if approved.Status != "approved" {
		t.Fatalf("expected approved status, got %s", approved.Status)
	}
}

func TestActionApprovalDeny(t *testing.T) {
	sqlStore := newTestStore(t)
	ctx := context.Background()

	created, err := sqlStore.CreateActionApproval(ctx, CreateActionApprovalInput{
		WorkspaceID:     "ws-1",
		ContextID:       "ctx-1",
		Connector:       "discord",
		ExternalID:      "chan-1",
		RequesterUserID: "user-1",
		ActionType:      "post_message",
	})
	if err != nil {
		t.Fatalf("create action approval: %v", err)
	}

	denied, err := sqlStore.DenyActionApproval(ctx, DenyActionApprovalInput{
		ID:             created.ID,
		ApproverUserID: "admin-1",
		Reason:         "not allowed",
	})
	if err != nil {
		t.Fatalf("deny action approval: %v", err)
	}
	if denied.Status != "denied" {
		t.Fatalf("expected denied status, got %s", denied.Status)
	}
	if denied.DeniedReason != "not allowed" {
		t.Fatalf("unexpected denied reason: %s", denied.DeniedReason)
	}
}

func TestActionApprovalExecutionUpdate(t *testing.T) {
	sqlStore := newTestStore(t)
	ctx := context.Background()

	created, err := sqlStore.CreateActionApproval(ctx, CreateActionApprovalInput{
		WorkspaceID:     "ws-1",
		ContextID:       "ctx-1",
		Connector:       "telegram",
		ExternalID:      "42",
		RequesterUserID: "user-1",
		ActionType:      "http_request",
		ActionTarget:    "https://example.com/webhook",
	})
	if err != nil {
		t.Fatalf("create action approval: %v", err)
	}

	approved, err := sqlStore.ApproveActionApproval(ctx, ApproveActionApprovalInput{
		ID:             created.ID,
		ApproverUserID: "admin-1",
	})
	if err != nil {
		t.Fatalf("approve action approval: %v", err)
	}
	if approved.Status != "approved" {
		t.Fatalf("expected approved status, got %s", approved.Status)
	}

	executedAt := time.Now().UTC()
	updated, err := sqlStore.UpdateActionExecution(ctx, UpdateActionExecutionInput{
		ID:               created.ID,
		ExecutionStatus:  "succeeded",
		ExecutionMessage: "webhook request completed with status 200",
		ExecutorPlugin:   "webhook",
		ExecutedAt:       executedAt,
	})
	if err != nil {
		t.Fatalf("update action execution: %v", err)
	}
	if updated.ExecutionStatus != "succeeded" {
		t.Fatalf("unexpected execution status: %s", updated.ExecutionStatus)
	}
	if updated.ExecutorPlugin != "webhook" {
		t.Fatalf("unexpected executor plugin: %s", updated.ExecutorPlugin)
	}

	lookup, err := sqlStore.LookupActionApproval(ctx, created.ID)
	if err != nil {
		t.Fatalf("lookup action approval: %v", err)
	}
	if lookup.ExecutionStatus != "succeeded" {
		t.Fatalf("expected persisted execution status, got %s", lookup.ExecutionStatus)
	}
	if lookup.ExecutorPlugin != "webhook" {
		t.Fatalf("expected persisted plugin key, got %s", lookup.ExecutorPlugin)
	}
}

func TestListPendingActionApprovalsGlobal(t *testing.T) {
	sqlStore := newTestStore(t)
	ctx := context.Background()

	if _, err := sqlStore.CreateActionApproval(ctx, CreateActionApprovalInput{
		WorkspaceID:     "ws-1",
		ContextID:       "ctx-1",
		Connector:       "telegram",
		ExternalID:      "42",
		RequesterUserID: "user-1",
		ActionType:      "send_email",
	}); err != nil {
		t.Fatalf("create telegram action approval: %v", err)
	}
	second, err := sqlStore.CreateActionApproval(ctx, CreateActionApprovalInput{
		WorkspaceID:     "ws-1",
		ContextID:       "ctx-2",
		Connector:       "discord",
		ExternalID:      "chan-1",
		RequesterUserID: "user-2",
		ActionType:      "run_command",
		ActionTarget:    "curl",
	})
	if err != nil {
		t.Fatalf("create discord action approval: %v", err)
	}
	if _, err := sqlStore.DenyActionApproval(ctx, DenyActionApprovalInput{
		ID:             second.ID,
		ApproverUserID: "admin-1",
		Reason:         "unsafe command",
	}); err != nil {
		t.Fatalf("deny action approval: %v", err)
	}

	pending, err := sqlStore.ListPendingActionApprovalsGlobal(ctx, 10)
	if err != nil {
		t.Fatalf("list global pending approvals: %v", err)
	}
	if len(pending) != 1 {
		t.Fatalf("expected one global pending action, got %d", len(pending))
	}
	if pending[0].Connector != "telegram" || pending[0].ExternalID != "42" {
		t.Fatalf("unexpected pending action source: %s/%s", pending[0].Connector, pending[0].ExternalID)
	}
}
