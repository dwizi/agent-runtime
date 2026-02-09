package store

import (
	"context"
	"testing"
	"time"
)

func TestCreateAndListScheduleObjective(t *testing.T) {
	sqlStore := newTestStore(t)
	ctx := context.Background()
	nextRun := time.Now().UTC().Add(30 * time.Second)
	created, err := sqlStore.CreateObjective(ctx, CreateObjectiveInput{
		WorkspaceID:     "ws-1",
		ContextID:       "ctx-1",
		Title:           "Daily digest review",
		Prompt:          "Review recent events and post digest",
		TriggerType:     ObjectiveTriggerSchedule,
		IntervalSeconds: 300,
		NextRunAt:       nextRun,
		Active:          true,
	})
	if err != nil {
		t.Fatalf("create objective: %v", err)
	}
	if created.ID == "" {
		t.Fatal("expected objective id")
	}
	if created.TriggerType != ObjectiveTriggerSchedule {
		t.Fatalf("unexpected trigger type: %s", created.TriggerType)
	}

	listed, err := sqlStore.ListObjectives(ctx, ListObjectivesInput{
		WorkspaceID: "ws-1",
		ActiveOnly:  true,
		Limit:       10,
	})
	if err != nil {
		t.Fatalf("list objectives: %v", err)
	}
	if len(listed) != 1 {
		t.Fatalf("expected one objective, got %d", len(listed))
	}
}

func TestListDueAndUpdateObjectiveRun(t *testing.T) {
	sqlStore := newTestStore(t)
	ctx := context.Background()
	now := time.Now().UTC()
	created, err := sqlStore.CreateObjective(ctx, CreateObjectiveInput{
		WorkspaceID:     "ws-2",
		ContextID:       "ctx-2",
		Title:           "Ops heartbeat",
		Prompt:          "Send heartbeat status",
		TriggerType:     ObjectiveTriggerSchedule,
		IntervalSeconds: 60,
		NextRunAt:       now.Add(-10 * time.Second),
		Active:          true,
	})
	if err != nil {
		t.Fatalf("create objective: %v", err)
	}

	due, err := sqlStore.ListDueObjectives(ctx, now, 5)
	if err != nil {
		t.Fatalf("list due objectives: %v", err)
	}
	if len(due) != 1 || due[0].ID != created.ID {
		t.Fatalf("expected due objective %s, got %+v", created.ID, due)
	}

	nextRun := now.Add(60 * time.Second)
	updated, err := sqlStore.UpdateObjectiveRun(ctx, UpdateObjectiveRunInput{
		ID:        created.ID,
		LastRunAt: now,
		NextRunAt: nextRun,
		LastError: "",
	})
	if err != nil {
		t.Fatalf("update objective run: %v", err)
	}
	if updated.LastRunAt.IsZero() {
		t.Fatal("expected last run timestamp")
	}
	if updated.NextRunAt.IsZero() {
		t.Fatal("expected next run timestamp")
	}
}

func TestListEventObjectives(t *testing.T) {
	sqlStore := newTestStore(t)
	ctx := context.Background()
	_, err := sqlStore.CreateObjective(ctx, CreateObjectiveInput{
		WorkspaceID: "ws-3",
		ContextID:   "ctx-3",
		Title:       "React to markdown changes",
		Prompt:      "Inspect changed markdown and raise follow-up tasks",
		TriggerType: ObjectiveTriggerEvent,
		EventKey:    "markdown.updated",
		Active:      true,
	})
	if err != nil {
		t.Fatalf("create event objective: %v", err)
	}

	items, err := sqlStore.ListEventObjectives(ctx, "ws-3", "markdown.updated", 10)
	if err != nil {
		t.Fatalf("list event objectives: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("expected one event objective, got %d", len(items))
	}
	if items[0].TriggerType != ObjectiveTriggerEvent {
		t.Fatalf("unexpected trigger type: %s", items[0].TriggerType)
	}
}

func TestUpdatePauseAndDeleteObjective(t *testing.T) {
	sqlStore := newTestStore(t)
	ctx := context.Background()
	created, err := sqlStore.CreateObjective(ctx, CreateObjectiveInput{
		WorkspaceID:     "ws-4",
		ContextID:       "ctx-4",
		Title:           "Draft summary",
		Prompt:          "Draft a summary",
		TriggerType:     ObjectiveTriggerSchedule,
		IntervalSeconds: 120,
		Active:          true,
	})
	if err != nil {
		t.Fatalf("create objective: %v", err)
	}

	newTitle := "Draft weekly summary"
	newPrompt := "Draft a weekly summary from latest markdown notes"
	inactive := false
	updated, err := sqlStore.UpdateObjective(ctx, UpdateObjectiveInput{
		ID:     created.ID,
		Title:  &newTitle,
		Prompt: &newPrompt,
		Active: &inactive,
	})
	if err != nil {
		t.Fatalf("update objective: %v", err)
	}
	if updated.Title != newTitle || updated.Prompt != newPrompt {
		t.Fatalf("objective update not persisted: %+v", updated)
	}
	if updated.Active {
		t.Fatal("expected objective to be inactive after update")
	}

	resumed, err := sqlStore.SetObjectiveActive(ctx, created.ID, true)
	if err != nil {
		t.Fatalf("set objective active: %v", err)
	}
	if !resumed.Active {
		t.Fatal("expected objective to be active")
	}

	if err := sqlStore.DeleteObjective(ctx, created.ID); err != nil {
		t.Fatalf("delete objective: %v", err)
	}
	if _, err := sqlStore.LookupObjective(ctx, created.ID); err == nil {
		t.Fatal("expected lookup to fail after delete")
	}
}
