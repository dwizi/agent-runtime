package grounded

import (
	"context"
	"errors"
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
