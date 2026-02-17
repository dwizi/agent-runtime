package grounded

import (
	"context"
	"log/slog"

	"github.com/dwizi/agent-runtime/internal/llm"
	"github.com/dwizi/agent-runtime/internal/qmd"
)

type Retriever interface {
	Search(ctx context.Context, workspaceID, query string, limit int) ([]qmd.SearchResult, error)
	OpenMarkdown(ctx context.Context, workspaceID, target string) (qmd.OpenResult, error)
}

type Config struct {
	WorkspaceRoot               string
	TopK                        int
	MaxDocExcerpt               int
	MaxPromptBytes              int
	ChatTailLines               int
	ChatTailBytes               int
	MaxPromptTokens             int
	UserPromptMaxTokens         int
	MemorySummaryMaxTokens      int
	ChatTailMaxTokens           int
	QMDContextMaxTokens         int
	MemorySummaryRefreshTurns   int
	MemorySummaryMaxItems       int
	MemorySummarySourceMaxLines int
}

type tokenBudget struct {
	Total   int
	User    int
	Summary int
	Tail    int
	QMD     int
}

type PromptMetrics struct {
	Strategy         MemoryStrategy
	Reason           string
	UsedSummary      bool
	UsedTail         bool
	UsedQMD          bool
	QMDResultCount   int
	SummaryRefreshed bool
	SummaryTurns     int
	UserTokens       int
	SummaryTokens    int
	TailTokens       int
	QMDTokens        int
	PromptTokens     int
}

type summaryMetadata struct {
	Refreshed bool
	Turns     int
}

type chatLine struct {
	Role string
	Text string
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
	if cfg.ChatTailLines < 6 {
		cfg.ChatTailLines = 24
	}
	if cfg.ChatTailBytes < 400 {
		cfg.ChatTailBytes = 1800
	}
	if cfg.MaxPromptTokens < 200 {
		if cfg.MaxPromptBytes > 0 {
			cfg.MaxPromptTokens = maxInt(600, cfg.MaxPromptBytes/4)
		} else {
			cfg.MaxPromptTokens = 2000
		}
	}
	if cfg.UserPromptMaxTokens < 80 {
		cfg.UserPromptMaxTokens = minInt(700, maxInt(180, cfg.MaxPromptTokens/3))
	}
	if cfg.MemorySummaryMaxTokens < 80 {
		cfg.MemorySummaryMaxTokens = minInt(450, maxInt(120, cfg.MaxPromptTokens/5))
	}
	if cfg.ChatTailMaxTokens < 80 {
		cfg.ChatTailMaxTokens = minInt(350, maxInt(100, cfg.MaxPromptTokens/6))
	}
	if cfg.QMDContextMaxTokens < 80 {
		remaining := cfg.MaxPromptTokens - cfg.UserPromptMaxTokens - cfg.MemorySummaryMaxTokens - cfg.ChatTailMaxTokens
		cfg.QMDContextMaxTokens = maxInt(300, remaining)
	}
	if cfg.MemorySummaryRefreshTurns < 1 {
		cfg.MemorySummaryRefreshTurns = 6
	}
	if cfg.MemorySummaryMaxItems < 3 {
		cfg.MemorySummaryMaxItems = 7
	}
	if cfg.MemorySummarySourceMaxLines < 24 {
		cfg.MemorySummarySourceMaxLines = 120
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
