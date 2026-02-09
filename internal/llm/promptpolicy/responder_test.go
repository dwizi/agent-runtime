package promptpolicy

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/carlos/spinner/internal/llm"
	"github.com/carlos/spinner/internal/store"
)

type fakeBase struct {
	lastInput llm.MessageInput
	reply     string
}

func (f *fakeBase) Reply(ctx context.Context, input llm.MessageInput) (string, error) {
	f.lastInput = input
	return f.reply, nil
}

type fakeProvider struct {
	policy store.ContextPolicy
	err    error
}

func (f *fakeProvider) LookupContextPolicy(ctx context.Context, contextID string) (store.ContextPolicy, error) {
	if f.err != nil {
		return store.ContextPolicy{}, f.err
	}
	return f.policy, nil
}

func TestResponderBuildsContextPromptWithSkills(t *testing.T) {
	root := t.TempDir()
	workspaceID := "ws-1"
	contextID := "ctx-1"
	skillDir := filepath.Join(root, workspaceID, "skills", "contexts", contextID)
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatalf("create skill dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, "tone.md"), []byte("Use concise language."), 0o644); err != nil {
		t.Fatalf("write skill file: %v", err)
	}

	base := &fakeBase{reply: "ok"}
	provider := &fakeProvider{
		policy: store.ContextPolicy{
			ContextID:    contextID,
			WorkspaceID:  workspaceID,
			IsAdmin:      true,
			SystemPrompt: "Always ask clarifying questions if context is missing.",
		},
	}
	responder := New(base, provider, Config{
		WorkspaceRoot:      root,
		AdminSystemPrompt:  "Admin baseline prompt.",
		PublicSystemPrompt: "Public baseline prompt.",
	})
	_, err := responder.Reply(context.Background(), llm.MessageInput{
		ContextID:   contextID,
		WorkspaceID: workspaceID,
		Text:        "hello",
	})
	if err != nil {
		t.Fatalf("reply failed: %v", err)
	}
	if !strings.Contains(base.lastInput.SystemPrompt, "Admin baseline prompt.") {
		t.Fatalf("expected admin baseline prompt, got %s", base.lastInput.SystemPrompt)
	}
	if !strings.Contains(base.lastInput.SystemPrompt, "Always ask clarifying questions") {
		t.Fatalf("expected context prompt override, got %s", base.lastInput.SystemPrompt)
	}
	if !strings.Contains(base.lastInput.SystemPrompt, "tone.md") {
		t.Fatalf("expected skill template in system prompt, got %s", base.lastInput.SystemPrompt)
	}
}

func TestResponderFallsBackWhenContextMissing(t *testing.T) {
	base := &fakeBase{reply: "ok"}
	provider := &fakeProvider{err: errors.New("db down")}
	responder := New(base, provider, Config{
		PublicSystemPrompt: "Public baseline prompt.",
	})
	_, err := responder.Reply(context.Background(), llm.MessageInput{
		ContextID:   "ctx-x",
		WorkspaceID: "ws-x",
		Text:        "hello",
	})
	if err != nil {
		t.Fatalf("reply failed: %v", err)
	}
	if !strings.Contains(base.lastInput.SystemPrompt, "Public baseline prompt.") {
		t.Fatalf("expected public baseline in fallback, got %s", base.lastInput.SystemPrompt)
	}
}
