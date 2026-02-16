package promptpolicy

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dwizi/agent-runtime/internal/llm"
	"github.com/dwizi/agent-runtime/internal/store"
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

func TestResponderLoadsGlobalSkillsWithWorkspacePrecedence(t *testing.T) {
	root := t.TempDir()
	workspaceID := "ws-1"
	contextID := "ctx-1"
	globalSkillsRoot := filepath.Join(root, "global-skills")

	workspaceAdmin := filepath.Join(root, workspaceID, "skills", "admin")
	workspaceContext := filepath.Join(root, workspaceID, "skills", "contexts", contextID)
	globalCommon := filepath.Join(globalSkillsRoot, "common")
	globalAdmin := filepath.Join(globalSkillsRoot, "admin")

	for _, dir := range []string{workspaceAdmin, workspaceContext, globalCommon, globalAdmin} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("create dir %s: %v", dir, err)
		}
	}
	if err := os.WriteFile(filepath.Join(workspaceAdmin, "tooling.md"), []byte("Workspace tool policy."), 0o644); err != nil {
		t.Fatalf("write workspace tooling: %v", err)
	}
	if err := os.WriteFile(filepath.Join(workspaceContext, "context.md"), []byte("Workspace context policy."), 0o644); err != nil {
		t.Fatalf("write workspace context: %v", err)
	}
	if err := os.WriteFile(filepath.Join(globalCommon, "tooling.md"), []byte("Global tool policy."), 0o644); err != nil {
		t.Fatalf("write global tooling: %v", err)
	}
	if err := os.WriteFile(filepath.Join(globalCommon, "qmd.md"), []byte("Use qmd before action proposals."), 0o644); err != nil {
		t.Fatalf("write global qmd: %v", err)
	}
	if err := os.WriteFile(filepath.Join(globalAdmin, "admin.md"), []byte("Admin approval rules."), 0o644); err != nil {
		t.Fatalf("write global admin: %v", err)
	}

	base := &fakeBase{reply: "ok"}
	provider := &fakeProvider{
		policy: store.ContextPolicy{
			ContextID:   contextID,
			WorkspaceID: workspaceID,
			IsAdmin:     true,
		},
	}
	responder := New(base, provider, Config{
		WorkspaceRoot:    root,
		GlobalSkillsRoot: globalSkillsRoot,
	})
	_, err := responder.Reply(context.Background(), llm.MessageInput{
		ContextID:   contextID,
		WorkspaceID: workspaceID,
		Text:        "hello",
	})
	if err != nil {
		t.Fatalf("reply failed: %v", err)
	}

	prompt := base.lastInput.SystemPrompt
	if !strings.Contains(prompt, "Workspace context policy.") {
		t.Fatalf("expected workspace context skill, got %s", prompt)
	}
	if !strings.Contains(prompt, "Workspace tool policy.") {
		t.Fatalf("expected workspace tooling skill, got %s", prompt)
	}
	if strings.Contains(prompt, "Global tool policy.") {
		t.Fatalf("expected workspace tooling to override global tooling, got %s", prompt)
	}
	if !strings.Contains(prompt, "Use qmd before action proposals.") {
		t.Fatalf("expected global qmd skill, got %s", prompt)
	}
	if !strings.Contains(prompt, "Admin approval rules.") {
		t.Fatalf("expected global admin skill, got %s", prompt)
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

func TestResponderLoadsSoulHierarchy(t *testing.T) {
	root := t.TempDir()
	globalPath := filepath.Join(root, "global-soul.md")
	workspaceID := "ws-2"
	contextID := "ctx:alpha"

	if err := os.WriteFile(globalPath, []byte("Global behavior rules."), 0o644); err != nil {
		t.Fatalf("write global soul: %v", err)
	}
	workspaceSoulPath := filepath.Join(root, workspaceID, "context", "SOUL.md")
	if err := os.MkdirAll(filepath.Dir(workspaceSoulPath), 0o755); err != nil {
		t.Fatalf("create workspace soul dir: %v", err)
	}
	if err := os.WriteFile(workspaceSoulPath, []byte("Workspace override behavior."), 0o644); err != nil {
		t.Fatalf("write workspace soul: %v", err)
	}
	contextSoulPath := filepath.Join(root, workspaceID, "context", "agents", "ctx-alpha", "SOUL.md")
	if err := os.MkdirAll(filepath.Dir(contextSoulPath), 0o755); err != nil {
		t.Fatalf("create context soul dir: %v", err)
	}
	if err := os.WriteFile(contextSoulPath, []byte("Agent-specific behavior."), 0o644); err != nil {
		t.Fatalf("write context soul: %v", err)
	}

	base := &fakeBase{reply: "ok"}
	provider := &fakeProvider{
		policy: store.ContextPolicy{
			ContextID:   contextID,
			WorkspaceID: workspaceID,
			IsAdmin:     false,
		},
	}
	responder := New(base, provider, Config{
		WorkspaceRoot:        root,
		PublicSystemPrompt:   "Public baseline prompt.",
		GlobalSoulPath:       globalPath,
		WorkspaceSoulRelPath: "context/SOUL.md",
		ContextSoulRelPath:   "context/agents/{context_id}/SOUL.md",
	})
	_, err := responder.Reply(context.Background(), llm.MessageInput{
		ContextID:   contextID,
		WorkspaceID: workspaceID,
		Text:        "hello",
	})
	if err != nil {
		t.Fatalf("reply failed: %v", err)
	}

	prompt := base.lastInput.SystemPrompt
	if !strings.Contains(prompt, "Global SOUL") || !strings.Contains(prompt, "Global behavior rules.") {
		t.Fatalf("expected global soul directives, got %s", prompt)
	}
	if !strings.Contains(prompt, "Workspace SOUL override") || !strings.Contains(prompt, "Workspace override behavior.") {
		t.Fatalf("expected workspace soul directives, got %s", prompt)
	}
	if !strings.Contains(prompt, "Agent SOUL override") || !strings.Contains(prompt, "Agent-specific behavior.") {
		t.Fatalf("expected context soul directives, got %s", prompt)
	}
}

func TestResponderLoadsSystemPromptHierarchy(t *testing.T) {
	root := t.TempDir()
	workspaceID := "ws-3"
	contextID := "ctx:beta"
	globalPath := filepath.Join(root, "global-system.md")

	if err := os.WriteFile(globalPath, []byte("Global system prompt rules."), 0o644); err != nil {
		t.Fatalf("write global system prompt: %v", err)
	}
	workspacePromptPath := filepath.Join(root, workspaceID, "context", "SYSTEM_PROMPT.md")
	if err := os.MkdirAll(filepath.Dir(workspacePromptPath), 0o755); err != nil {
		t.Fatalf("create workspace prompt dir: %v", err)
	}
	if err := os.WriteFile(workspacePromptPath, []byte("Workspace system prompt rules."), 0o644); err != nil {
		t.Fatalf("write workspace system prompt: %v", err)
	}
	contextPromptPath := filepath.Join(root, workspaceID, "context", "agents", "ctx-beta", "SYSTEM_PROMPT.md")
	if err := os.MkdirAll(filepath.Dir(contextPromptPath), 0o755); err != nil {
		t.Fatalf("create context prompt dir: %v", err)
	}
	if err := os.WriteFile(contextPromptPath, []byte("Context system prompt rules."), 0o644); err != nil {
		t.Fatalf("write context system prompt: %v", err)
	}

	base := &fakeBase{reply: "ok"}
	provider := &fakeProvider{
		policy: store.ContextPolicy{
			ContextID:   contextID,
			WorkspaceID: workspaceID,
			IsAdmin:     false,
		},
	}
	responder := New(base, provider, Config{
		WorkspaceRoot:       root,
		PublicSystemPrompt:  "Public baseline prompt.",
		GlobalSystemPrompt:  globalPath,
		WorkspacePromptPath: "context/SYSTEM_PROMPT.md",
		ContextPromptPath:   "context/agents/{context_id}/SYSTEM_PROMPT.md",
	})
	_, err := responder.Reply(context.Background(), llm.MessageInput{
		ContextID:   contextID,
		WorkspaceID: workspaceID,
		Text:        "hello",
	})
	if err != nil {
		t.Fatalf("reply failed: %v", err)
	}

	prompt := base.lastInput.SystemPrompt
	if !strings.Contains(prompt, "Global prompt") || !strings.Contains(prompt, "Global system prompt rules.") {
		t.Fatalf("expected global system prompt directives, got %s", prompt)
	}
	if !strings.Contains(prompt, "Workspace prompt override") || !strings.Contains(prompt, "Workspace system prompt rules.") {
		t.Fatalf("expected workspace system prompt directives, got %s", prompt)
	}
	if !strings.Contains(prompt, "Context prompt override") || !strings.Contains(prompt, "Context system prompt rules.") {
		t.Fatalf("expected context system prompt directives, got %s", prompt)
	}
}
