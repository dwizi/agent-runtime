package store

import (
	"context"
	"testing"
)

func TestCreateAndListAgentAuditEvents(t *testing.T) {
	sqlStore := newTestStore(t)
	ctx := context.Background()

	created, err := sqlStore.CreateAgentAuditEvent(ctx, CreateAgentAuditEventInput{
		WorkspaceID:  "ws-1",
		ContextID:    "ctx-1",
		Connector:    "telegram",
		ExternalID:   "42",
		SourceUserID: "u1",
		EventType:    "approval_required",
		Stage:        "audit.approval_required",
		ToolName:     "create_objective",
		ToolClass:    "objective",
		Blocked:      true,
		BlockReason:  "tool create_objective requires approval",
		Message:      "blocked tool=create_objective class=objective",
	})
	if err != nil {
		t.Fatalf("create audit event: %v", err)
	}
	if created.ID == "" {
		t.Fatal("expected generated audit event id")
	}

	events, err := sqlStore.ListAgentAuditEvents(ctx, ListAgentAuditEventsInput{
		WorkspaceID: "ws-1",
		EventType:   "approval_required",
		BlockedOnly: true,
		Limit:       10,
	})
	if err != nil {
		t.Fatalf("list audit events: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("expected 1 audit event, got %d", len(events))
	}
	if events[0].ToolName != "create_objective" {
		t.Fatalf("expected tool create_objective, got %s", events[0].ToolName)
	}
	if !events[0].Blocked {
		t.Fatal("expected blocked audit event")
	}
}
