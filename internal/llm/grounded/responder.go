package grounded

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"github.com/carlos/spinner/internal/llm"
	"github.com/carlos/spinner/internal/qmd"
)

type Retriever interface {
	Search(ctx context.Context, workspaceID, query string, limit int) ([]qmd.SearchResult, error)
	OpenMarkdown(ctx context.Context, workspaceID, target string) (qmd.OpenResult, error)
}

type Config struct {
	TopK           int
	MaxDocExcerpt  int
	MaxPromptBytes int
}

type Responder struct {
	base      llm.Responder
	retriever Retriever
	cfg       Config
	logger    *slog.Logger
}

func New(base llm.Responder, retriever Retriever, cfg Config, logger *slog.Logger) *Responder {
	if cfg.TopK < 1 {
		cfg.TopK = 3
	}
	if cfg.MaxDocExcerpt < 200 {
		cfg.MaxDocExcerpt = 1200
	}
	if cfg.MaxPromptBytes < 500 {
		cfg.MaxPromptBytes = 8000
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &Responder{
		base:      base,
		retriever: retriever,
		cfg:       cfg,
		logger:    logger,
	}
}

func (r *Responder) Reply(ctx context.Context, input llm.MessageInput) (string, error) {
	if r.base == nil {
		return "", fmt.Errorf("%w: base responder missing", llm.ErrUnavailable)
	}
	augmented := input
	augmented.Text = r.buildPrompt(ctx, input)
	return r.base.Reply(ctx, augmented)
}

func (r *Responder) buildPrompt(ctx context.Context, input llm.MessageInput) string {
	original := strings.TrimSpace(input.Text)
	if original == "" {
		return original
	}
	if input.SkipGrounding {
		return original
	}
	if r.retriever == nil || strings.TrimSpace(input.WorkspaceID) == "" {
		return original
	}

	results, err := r.retriever.Search(ctx, input.WorkspaceID, original, r.cfg.TopK)
	if err != nil {
		if !errors.Is(err, qmd.ErrUnavailable) {
			r.logger.Error("qmd grounding search failed", "error", err, "workspace_id", input.WorkspaceID)
		}
		return original
	}
	if len(results) == 0 {
		return original
	}

	lines := []string{
		original,
		"",
		"Relevant workspace context:",
	}
	for _, result := range results {
		target := strings.TrimSpace(result.Path)
		if target == "" {
			target = strings.TrimSpace(result.DocID)
		}
		if target == "" {
			continue
		}
		openResult, err := r.retriever.OpenMarkdown(ctx, input.WorkspaceID, target)
		if err != nil {
			continue
		}
		excerpt := compactWhitespace(openResult.Content)
		if len(excerpt) > r.cfg.MaxDocExcerpt {
			excerpt = excerpt[:r.cfg.MaxDocExcerpt] + "..."
		}
		lines = append(lines, fmt.Sprintf("- source: %s", target))
		if strings.TrimSpace(result.Snippet) != "" {
			lines = append(lines, "  snippet: "+compactWhitespace(result.Snippet))
		}
		if excerpt != "" {
			lines = append(lines, "  excerpt: "+excerpt)
		}
	}
	joined := strings.Join(lines, "\n")
	if len(joined) > r.cfg.MaxPromptBytes {
		return joined[:r.cfg.MaxPromptBytes]
	}
	return joined
}

func compactWhitespace(input string) string {
	return strings.Join(strings.Fields(strings.TrimSpace(input)), " ")
}
