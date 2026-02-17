package grounded

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dwizi/agent-runtime/internal/llm"
	"github.com/dwizi/agent-runtime/internal/qmd"
)

type fakeBase struct {
	lastInput llm.MessageInput
	reply     string
	err       error
}

func (f *fakeBase) Reply(ctx context.Context, input llm.MessageInput) (string, error) {
	f.lastInput = input
	if f.err != nil {
		return "", f.err
	}
	return f.reply, nil
}

type fakeRetriever struct {
	searchResults []qmd.SearchResult
	searchErr     error
	openByTarget  map[string]string
	searchCalls   int
	openCalls     int
}

func (f *fakeRetriever) Search(ctx context.Context, workspaceID, query string, limit int) ([]qmd.SearchResult, error) {
	f.searchCalls++
	if f.searchErr != nil {
		return nil, f.searchErr
	}
	return f.searchResults, nil
}

func (f *fakeRetriever) OpenMarkdown(ctx context.Context, workspaceID, target string) (qmd.OpenResult, error) {
	f.openCalls++
	content, ok := f.openByTarget[target]
	if !ok {
		return qmd.OpenResult{}, qmd.ErrNotFound
	}
	return qmd.OpenResult{
		Path:    target,
		Content: content,
	}, nil
}

func TestReplyAddsGroundingContext(t *testing.T) {
	base := &fakeBase{reply: "ok"}
	retriever := &fakeRetriever{
		searchResults: []qmd.SearchResult{
			{Path: "memory.md", Snippet: "summary"},
		},
		openByTarget: map[string]string{
			"memory.md": "important memory block",
		},
	}
	responder := New(base, retriever, Config{TopK: 1}, nil)

	_, err := responder.Reply(context.Background(), llm.MessageInput{
		WorkspaceID: "ws-1",
		Text:        "find workspace change summary",
	})
	if err != nil {
		t.Fatalf("reply failed: %v", err)
	}
	if !strings.Contains(base.lastInput.Text, "Relevant workspace context") {
		t.Fatalf("expected grounding text, got %s", base.lastInput.Text)
	}
	if !strings.Contains(base.lastInput.Text, "important memory block") {
		t.Fatalf("expected excerpt in prompt, got %s", base.lastInput.Text)
	}
}

func TestReplyFallsBackOnSearchError(t *testing.T) {
	base := &fakeBase{reply: "ok"}
	retriever := &fakeRetriever{
		searchErr: errors.New("boom"),
	}
	responder := New(base, retriever, Config{}, nil)
	input := llm.MessageInput{
		WorkspaceID: "ws-1",
		Text:        "plain prompt",
	}
	_, err := responder.Reply(context.Background(), input)
	if err != nil {
		t.Fatalf("reply failed: %v", err)
	}
	if base.lastInput.Text != "plain prompt" {
		t.Fatalf("expected original prompt, got %s", base.lastInput.Text)
	}
}

func TestReplySkipsGroundingWhenRequested(t *testing.T) {
	base := &fakeBase{reply: "ok"}
	retriever := &fakeRetriever{
		searchResults: []qmd.SearchResult{
			{Path: "memory.md", Snippet: "summary"},
		},
		openByTarget: map[string]string{
			"memory.md": "important memory block",
		},
	}
	responder := New(base, retriever, Config{TopK: 1}, nil)
	input := llm.MessageInput{
		WorkspaceID:   "ws-1",
		Text:          "plain prompt",
		SkipGrounding: true,
	}
	_, err := responder.Reply(context.Background(), input)
	if err != nil {
		t.Fatalf("reply failed: %v", err)
	}
	if base.lastInput.Text != "plain prompt" {
		t.Fatalf("expected original prompt when grounding is skipped, got %s", base.lastInput.Text)
	}
	if strings.Contains(base.lastInput.Text, "Relevant workspace context") {
		t.Fatalf("expected no grounding context, got %s", base.lastInput.Text)
	}
}

func TestReplySkipsQMDForSmallTalk(t *testing.T) {
	base := &fakeBase{reply: "ok"}
	retriever := &fakeRetriever{
		searchResults: []qmd.SearchResult{
			{Path: "memory.md", Snippet: "summary"},
		},
		openByTarget: map[string]string{
			"memory.md": "important memory block",
		},
	}
	responder := New(base, retriever, Config{TopK: 1}, nil)
	input := llm.MessageInput{
		Connector:   "discord",
		WorkspaceID: "ws-1",
		ContextID:   "ctx-1",
		ExternalID:  "chan-1",
		Text:        "hello",
	}
	_, err := responder.Reply(context.Background(), input)
	if err != nil {
		t.Fatalf("reply failed: %v", err)
	}
	if retriever.searchCalls != 0 {
		t.Fatalf("expected no qmd search for small talk, got %d calls", retriever.searchCalls)
	}
	if base.lastInput.Text != "hello" {
		t.Fatalf("expected original prompt for small talk, got %q", base.lastInput.Text)
	}
}

func TestReplyUsesChatTailForMemoryCue(t *testing.T) {
	base := &fakeBase{reply: "ok"}
	retriever := &fakeRetriever{
		openByTarget: map[string]string{
			"logs/chats/discord/chan-1.md": "# Chat Log\n\n## 2026-02-10T11:00:00Z `INBOUND`\n- direction: `inbound`\n- actor: `u1`\n\nfirst\n\n## 2026-02-10T11:01:00Z `OUTBOUND`\n- direction: `outbound`\n- actor: `agent-runtime`\n\nsecond\n",
		},
	}
	responder := New(base, retriever, Config{TopK: 1, ChatTailLines: 8, ChatTailBytes: 800}, nil)
	input := llm.MessageInput{
		Connector:   "discord",
		WorkspaceID: "ws-1",
		ContextID:   "ctx-1",
		ExternalID:  "chan-1",
		Text:        "as we discussed before, continue from that",
	}
	_, err := responder.Reply(context.Background(), input)
	if err != nil {
		t.Fatalf("reply failed: %v", err)
	}
	if retriever.searchCalls != 0 {
		t.Fatalf("expected no qmd search for chat-tail strategy, got %d calls", retriever.searchCalls)
	}
	if retriever.openCalls == 0 {
		t.Fatal("expected chat log to be opened for tail context")
	}
	if !strings.Contains(base.lastInput.Text, "Recent conversation memory:") {
		t.Fatalf("expected chat tail prompt, got %q", base.lastInput.Text)
	}
	if !strings.Contains(base.lastInput.Text, "second") {
		t.Fatalf("expected recent chat content in prompt, got %q", base.lastInput.Text)
	}
	if !strings.Contains(base.lastInput.Text, "Response behavior: open with one short acknowledgement") {
		t.Fatalf("expected acknowledgement instruction for chat-tail strategy, got %q", base.lastInput.Text)
	}
}

func TestReplyUsesImplicitQMDForQuestion(t *testing.T) {
	base := &fakeBase{reply: "ok"}
	retriever := &fakeRetriever{
		searchResults: []qmd.SearchResult{
			{Path: "docs/guide.md", Snippet: "guide summary"},
		},
		openByTarget: map[string]string{
			"docs/guide.md": "Longer guide content",
		},
	}
	responder := New(base, retriever, Config{TopK: 1}, nil)
	input := llm.MessageInput{
		WorkspaceID: "ws-1",
		Text:        "How should I migrate this workspace safely?",
	}
	_, err := responder.Reply(context.Background(), input)
	if err != nil {
		t.Fatalf("reply failed: %v", err)
	}
	if retriever.searchCalls == 0 {
		t.Fatal("expected implicit informational question to trigger qmd search")
	}
	if !strings.Contains(base.lastInput.Text, "Relevant workspace context:") {
		t.Fatalf("expected grounded prompt, got %q", base.lastInput.Text)
	}
}

func TestReplySkipsImplicitQMDForGenericQuestion(t *testing.T) {
	base := &fakeBase{reply: "ok"}
	retriever := &fakeRetriever{
		searchResults: []qmd.SearchResult{
			{Path: "docs/math.md", Snippet: "not expected"},
		},
		openByTarget: map[string]string{
			"docs/math.md": "not expected",
		},
	}
	responder := New(base, retriever, Config{TopK: 1}, nil)
	input := llm.MessageInput{
		WorkspaceID: "ws-1",
		Text:        "What is two plus two?",
	}
	_, err := responder.Reply(context.Background(), input)
	if err != nil {
		t.Fatalf("reply failed: %v", err)
	}
	if retriever.searchCalls != 0 {
		t.Fatalf("expected no implicit qmd search for generic question, got %d calls", retriever.searchCalls)
	}
	if strings.Contains(base.lastInput.Text, "Relevant workspace context:") {
		t.Fatalf("expected no grounded context, got %q", base.lastInput.Text)
	}
}

func TestReplyBuildsAndPersistsMemorySummary(t *testing.T) {
	base := &fakeBase{reply: "ok"}
	retriever := &fakeRetriever{}

	root := t.TempDir()
	workspaceID := "ws-1"
	chatPath := filepath.Join(root, workspaceID, "logs", "chats", "discord", "chan-1.md")
	if err := os.MkdirAll(filepath.Dir(chatPath), 0o755); err != nil {
		t.Fatalf("mkdir chat path: %v", err)
	}
	chatLog := strings.Join([]string{
		"# Chat Log",
		"",
		"## 2026-02-10T11:00:00Z `INBOUND`",
		"- direction: `inbound`",
		"- actor: `u1`",
		"",
		"Please summarize what we changed earlier.",
		"",
		"## 2026-02-10T11:01:00Z `OUTBOUND`",
		"- direction: `outbound`",
		"- actor: `agent-runtime`",
		"",
		"I'll gather the latest updates and summarize the delta.",
		"",
	}, "\n")
	if err := os.WriteFile(chatPath, []byte(chatLog), 0o644); err != nil {
		t.Fatalf("write chat log: %v", err)
	}

	responder := New(base, retriever, Config{
		WorkspaceRoot:               root,
		TopK:                        1,
		MemorySummaryRefreshTurns:   1,
		MemorySummaryMaxItems:       6,
		MemorySummarySourceMaxLines: 80,
	}, nil)

	_, err := responder.Reply(context.Background(), llm.MessageInput{
		Connector:   "discord",
		WorkspaceID: workspaceID,
		ContextID:   "ctx-1",
		ExternalID:  "chan-1",
		Text:        "continue from the previous thread and share what changed",
	})
	if err != nil {
		t.Fatalf("reply failed: %v", err)
	}
	if !strings.Contains(base.lastInput.Text, "Context memory summary:") {
		t.Fatalf("expected memory summary in prompt, got %q", base.lastInput.Text)
	}
	if !strings.Contains(base.lastInput.Text, "Recent conversation memory:") {
		t.Fatalf("expected chat tail memory in prompt, got %q", base.lastInput.Text)
	}

	summaryPath := filepath.Join(root, workspaceID, "memory", "contexts", "ctx-1.md")
	summaryBytes, err := os.ReadFile(summaryPath)
	if err != nil {
		t.Fatalf("expected summary file to be written: %v", err)
	}
	summary := string(summaryBytes)
	if !strings.Contains(summary, "# Context Memory Summary") {
		t.Fatalf("expected summary doc header, got %q", summary)
	}
	if !strings.Contains(summary, "## Recent User Intents") {
		t.Fatalf("expected summary sections, got %q", summary)
	}
}

func TestReplyRefreshesSummaryWhenCommandHeavyContextGrows(t *testing.T) {
	base := &fakeBase{reply: "ok"}
	retriever := &fakeRetriever{}

	root := t.TempDir()
	workspaceID := "ws-1"
	chatPath := filepath.Join(root, workspaceID, "logs", "chats", "codex", "session-1.md")
	if err := os.MkdirAll(filepath.Dir(chatPath), 0o755); err != nil {
		t.Fatalf("mkdir chat path: %v", err)
	}

	lines := []string{
		"# Chat Log",
		"",
		"## 2026-02-10T11:00:00Z `INBOUND`",
		"- direction: `inbound`",
		"- actor: `user-1`",
		"",
		"Memory seed for this run.",
		"",
		"## 2026-02-10T11:01:00Z `INBOUND`",
		"- direction: `inbound`",
		"- actor: `user-1`",
		"",
		"/task run sandbox checks and write a report",
		"",
	}
	for i := 1; i <= 16; i++ {
		lines = append(lines,
			fmt.Sprintf("## 2026-02-10T11:%02d:00Z `OUTBOUND`", i+1),
			"- direction: `outbound`",
			"- actor: `agent-runtime`",
			"",
			fmt.Sprintf("Synthetic assistant update %d: step-%d completed.", i, i),
			"",
		)
	}
	if err := os.WriteFile(chatPath, []byte(strings.Join(lines, "\n")), 0o644); err != nil {
		t.Fatalf("write chat log: %v", err)
	}

	summaryPath := filepath.Join(root, workspaceID, "memory", "contexts", "ctx-1.md")
	if err := os.MkdirAll(filepath.Dir(summaryPath), 0o755); err != nil {
		t.Fatalf("mkdir summary path: %v", err)
	}
	existingSummary := strings.Join([]string{
		"# Context Memory Summary",
		"",
		"- context_id: `ctx-1`",
		"- connector: `codex`",
		"- external_id: `session-1`",
		"- turns: `2`",
		"- source_lines: `4`",
		"- refreshed_at: `2026-02-10T11:01:00Z`",
		"",
		"## Recent User Intents",
		"- Memory seed for this run.",
		"",
	}, "\n")
	if err := os.WriteFile(summaryPath, []byte(existingSummary), 0o644); err != nil {
		t.Fatalf("write existing summary: %v", err)
	}

	responder := New(base, retriever, Config{
		WorkspaceRoot:               root,
		TopK:                        1,
		MemorySummaryRefreshTurns:   6,
		MemorySummaryMaxItems:       20,
		MemorySummarySourceMaxLines: 400,
	}, nil)

	_, err := responder.Reply(context.Background(), llm.MessageInput{
		Connector:   "codex",
		WorkspaceID: workspaceID,
		ContextID:   "ctx-1",
		ExternalID:  "session-1",
		Text:        "continue and use prior memory",
	})
	if err != nil {
		t.Fatalf("reply failed: %v", err)
	}

	updatedBytes, err := os.ReadFile(summaryPath)
	if err != nil {
		t.Fatalf("read updated summary: %v", err)
	}
	updated := string(updatedBytes)
	if !strings.Contains(updated, "## Recent Assistant Actions") {
		t.Fatalf("expected assistant actions after refresh, got %q", updated)
	}
	if parseSummarySourceLines(updated) <= 4 {
		t.Fatalf("expected source line metadata to increase, got %q", updated)
	}
}

func TestSummarizeConversationBuildsCanonicalFactsWithLatestCorrections(t *testing.T) {
	chatLog := strings.Join([]string{
		"# Chat Log",
		"",
		"## 2026-02-17T17:00:00Z `INBOUND`",
		"- direction: `inbound`",
		"- actor: `u1`",
		"",
		"Fact set A: codename ORBIT-7, environment staging-eu2, freeze window Tuesday 17:30 UTC, incident id INC-4421.",
		"",
		"## 2026-02-17T17:00:30Z `INBOUND`",
		"- direction: `inbound`",
		"- actor: `u1`",
		"",
		"Preference: escalate only SEV1/SEV2 after two independent checks; notify #ops-war-room.",
		"",
		"## 2026-02-17T17:01:00Z `INBOUND`",
		"- direction: `inbound`",
		"- actor: `u1`",
		"",
		"Correction: freeze window moved to Wednesday 18:15 UTC and incident id is INC-4499. These supersede prior values.",
		"",
		"## 2026-02-17T17:01:30Z `INBOUND`",
		"- direction: `inbound`",
		"- actor: `u1`",
		"",
		"Dependency map: service alpha depends on redis-r3 and payments-v2.",
		"",
		"## 2026-02-17T17:02:00Z `INBOUND`",
		"- direction: `inbound`",
		"- actor: `u1`",
		"",
		"Add owner map: release manager is Nora Chen; backup is Luis Park.",
		"",
		"## 2026-02-17T17:02:30Z `INBOUND`",
		"- direction: `inbound`",
		"- actor: `u1`",
		"",
		"Constraint: no production schema migrations during freeze window.",
		"",
	}, "\n")

	summary := summarizeConversation(chatLog, 300, 8)
	if !strings.Contains(summary, "## Canonical Facts") {
		t.Fatalf("expected canonical facts section, got %q", summary)
	}
	if !strings.Contains(summary, "Codename: ORBIT-7") {
		t.Fatalf("expected codename fact, got %q", summary)
	}
	if !strings.Contains(summary, "Environment: staging-eu2") {
		t.Fatalf("expected environment fact, got %q", summary)
	}
	if !strings.Contains(summary, "Freeze window: Wednesday 18:15 UTC") {
		t.Fatalf("expected corrected freeze window fact, got %q", summary)
	}
	if strings.Contains(summary, "Freeze window: Tuesday 17:30 UTC") {
		t.Fatalf("expected old freeze window to be superseded, got %q", summary)
	}
	if !strings.Contains(summary, "Incident ID: INC-4499") {
		t.Fatalf("expected corrected incident fact, got %q", summary)
	}
	if strings.Contains(summary, "Incident ID: INC-4421") {
		t.Fatalf("expected old incident to be superseded, got %q", summary)
	}
	if !strings.Contains(summary, "Notify channel: #ops-war-room") {
		t.Fatalf("expected notify channel fact, got %q", summary)
	}
	if !strings.Contains(summary, "Owners: release manager Nora Chen; backup Luis Park") {
		t.Fatalf("expected owner facts, got %q", summary)
	}
	if !strings.Contains(summary, "Dependencies (alpha): redis-r3, payments-v2") {
		t.Fatalf("expected dependency facts, got %q", summary)
	}
	if !strings.Contains(summary, "Migration constraint: No production schema migrations during freeze window") {
		t.Fatalf("expected migration constraint fact, got %q", summary)
	}
}
