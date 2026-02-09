package store

import (
	"context"
	"testing"
)

func TestSetAndLookupContextSystemPrompt(t *testing.T) {
	sqlStore := newTestStore(t)
	ctx := context.Background()

	policy, err := sqlStore.SetContextSystemPromptByExternal(ctx, "telegram", "42", "You are an ops assistant")
	if err != nil {
		t.Fatalf("set context system prompt: %v", err)
	}
	if policy.ContextID == "" {
		t.Fatal("expected context id")
	}
	if policy.WorkspaceID == "" {
		t.Fatal("expected workspace id")
	}
	if policy.SystemPrompt != "You are an ops assistant" {
		t.Fatalf("unexpected prompt: %s", policy.SystemPrompt)
	}

	loaded, err := sqlStore.LookupContextPolicy(ctx, policy.ContextID)
	if err != nil {
		t.Fatalf("lookup context policy: %v", err)
	}
	if loaded.SystemPrompt != "You are an ops assistant" {
		t.Fatalf("expected persisted prompt, got %s", loaded.SystemPrompt)
	}
}

func TestLookupContextPolicyByExternal(t *testing.T) {
	sqlStore := newTestStore(t)
	ctx := context.Background()
	_, err := sqlStore.EnsureContextForExternalChannel(ctx, "discord", "chan-1", "ops")
	if err != nil {
		t.Fatalf("ensure context: %v", err)
	}

	policy, err := sqlStore.LookupContextPolicyByExternal(ctx, "discord", "chan-1")
	if err != nil {
		t.Fatalf("lookup context policy by external: %v", err)
	}
	if policy.ContextID == "" {
		t.Fatal("expected context id")
	}
}
