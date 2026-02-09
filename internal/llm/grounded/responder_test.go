package grounded

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/carlos/spinner/internal/llm"
	"github.com/carlos/spinner/internal/qmd"
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
}

func (f *fakeRetriever) Search(ctx context.Context, workspaceID, query string, limit int) ([]qmd.SearchResult, error) {
	if f.searchErr != nil {
		return nil, f.searchErr
	}
	return f.searchResults, nil
}

func (f *fakeRetriever) OpenMarkdown(ctx context.Context, workspaceID, target string) (qmd.OpenResult, error) {
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
		Text:        "what changed?",
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
