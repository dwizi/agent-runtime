package grounded

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"regexp"
	"strings"

	"github.com/dwizi/agent-runtime/internal/llm"
	"github.com/dwizi/agent-runtime/internal/qmd"
)

type Retriever interface {
	Search(ctx context.Context, workspaceID, query string, limit int) ([]qmd.SearchResult, error)
	OpenMarkdown(ctx context.Context, workspaceID, target string) (qmd.OpenResult, error)
}

type Config struct {
	TopK           int
	MaxDocExcerpt  int
	MaxPromptBytes int
	ChatTailLines  int
	ChatTailBytes  int
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

	decision := DecideMemoryStrategy(input)
	switch decision.Strategy {
	case StrategyNone:
		return original
	case StrategyTail:
		tailPrompt, ok := r.buildChatTailPrompt(ctx, input, original, decision)
		if ok {
			return tailPrompt
		}
		// Fallback to plain prompt when chat tail cannot be loaded.
		return original
	case StrategyQMD:
		// Continue with qmd retrieval below.
	default:
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
	if decision.Acknowledge {
		lines = append(lines, "Response behavior: open with one short acknowledgement that you are pulling context, then answer.")
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

func (r *Responder) buildChatTailPrompt(ctx context.Context, input llm.MessageInput, original string, decision MemoryDecision) (string, bool) {
	target := chatLogTarget(input.Connector, input.ExternalID)
	if strings.TrimSpace(target) == "" {
		return "", false
	}
	openResult, err := r.retriever.OpenMarkdown(ctx, input.WorkspaceID, target)
	if err != nil {
		if !errors.Is(err, qmd.ErrNotFound) {
			r.logger.Debug("chat-tail grounding failed", "workspace_id", input.WorkspaceID, "target", target, "error", err)
		}
		return "", false
	}
	tail := extractTailLines(openResult.Content, r.cfg.ChatTailLines, r.cfg.ChatTailBytes)
	if strings.TrimSpace(tail) == "" {
		return "", false
	}
	lines := []string{
		original,
		"",
		"Recent conversation memory:",
		tail,
		"",
		"Use this memory only if relevant. If context is still missing, ask one concise clarifying question.",
	}
	if decision.Acknowledge {
		lines = append(lines, "Response behavior: open with one short acknowledgement that you are pulling context, then answer.")
	}
	joined := strings.Join(lines, "\n")
	if len(joined) > r.cfg.MaxPromptBytes {
		return joined[:r.cfg.MaxPromptBytes], true
	}
	return joined, true
}

type MemoryStrategy string

const (
	StrategyNone MemoryStrategy = "none"
	StrategyTail MemoryStrategy = "tail"
	StrategyQMD  MemoryStrategy = "qmd"
)

type MemoryDecision struct {
	Strategy    MemoryStrategy
	Reason      string
	Acknowledge bool
}

var (
	domainReferencePattern = regexp.MustCompile(`\b[a-z0-9][a-z0-9-]*\.[a-z]{2,}\b`)
	logPathSanitizer       = regexp.MustCompile(`[^a-zA-Z0-9._-]+`)
	nonAlphaNumPattern     = regexp.MustCompile(`[^a-z0-9]+`)
)

func DecideMemoryStrategy(input llm.MessageInput) MemoryDecision {
	original := strings.TrimSpace(input.Text)
	trimmed := strings.TrimSpace(original)
	lower := strings.ToLower(trimmed)
	if trimmed == "" {
		return MemoryDecision{Strategy: StrategyNone, Reason: "empty"}
	}
	if looksLikeSmallTalk(lower) {
		return MemoryDecision{Strategy: StrategyNone, Reason: "small_talk"}
	}
	if shouldUseChatTail(lower) && strings.TrimSpace(input.Connector) != "" && strings.TrimSpace(input.ExternalID) != "" {
		return MemoryDecision{
			Strategy:    StrategyTail,
			Reason:      "chat_memory_cue",
			Acknowledge: shouldAcknowledgeContextLoad(input),
		}
	}
	if shouldUseQMD(lower) {
		return MemoryDecision{
			Strategy:    StrategyQMD,
			Reason:      "workspace_retrieval_cue",
			Acknowledge: shouldAcknowledgeContextLoad(input),
		}
	}
	return MemoryDecision{Strategy: StrategyNone, Reason: "no_retrieval_cue"}
}

func shouldUseChatTail(lower string) bool {
	cues := []string{
		"as we discussed",
		"as we talked",
		"earlier",
		"before",
		"previous",
		"last message",
		"last time",
		"remember",
		"you said",
		"i said",
		"we said",
		"follow up",
		"continue from",
		"what did we talk",
		"what changed",
	}
	return containsAny(lower, cues)
}

func shouldUseQMD(lower string) bool {
	retrievalCues := []string{
		"search",
		"find",
		"lookup",
		"look up",
		"docs",
		"documentation",
		"readme",
		"policy",
		"pricing",
		"where is",
		"which file",
		"in workspace",
		"in memory",
		"knowledge base",
	}
	if containsAny(lower, retrievalCues) {
		return true
	}
	if strings.Contains(lower, "http://") || strings.Contains(lower, "https://") {
		return true
	}
	if domainReferencePattern.MatchString(lower) {
		return true
	}
	return false
}

func shouldAcknowledgeContextLoad(input llm.MessageInput) bool {
	connector := strings.ToLower(strings.TrimSpace(input.Connector))
	return connector == "discord" || connector == "telegram"
}

func looksLikeSmallTalk(lower string) bool {
	compact := normalizeCueText(lower)
	if compact == "" {
		return true
	}
	shortPhrases := []string{
		"hi",
		"hello",
		"hey",
		"yo",
		"sup",
		"thanks",
		"thank you",
		"ok",
		"okay",
		"cool",
		"nice",
		"are you there",
		"how are you",
		"how fast are you",
		"what's up",
		"whats up",
		"kmon, let say hey",
	}
	if containsSmallTalkPhrase(compact, shortPhrases) {
		return true
	}
	words := strings.Fields(compact)
	return len(words) <= 3 && !strings.Contains(compact, "?")
}

func containsSmallTalkPhrase(input string, phrases []string) bool {
	framed := " " + input + " "
	for _, phrase := range phrases {
		phrase = normalizeCueText(phrase)
		if phrase == "" {
			continue
		}
		if strings.Contains(framed, " "+phrase+" ") {
			return true
		}
	}
	return false
}

func normalizeCueText(input string) string {
	lower := strings.ToLower(strings.TrimSpace(input))
	if lower == "" {
		return ""
	}
	lower = nonAlphaNumPattern.ReplaceAllString(lower, " ")
	return strings.Join(strings.Fields(lower), " ")
}

func containsAny(input string, values []string) bool {
	for _, value := range values {
		value = strings.TrimSpace(strings.ToLower(value))
		if value == "" {
			continue
		}
		if strings.Contains(input, value) {
			return true
		}
	}
	return false
}

func chatLogTarget(connector, externalID string) string {
	connector = sanitizeLogPathSegment(connector)
	externalID = sanitizeLogPathSegment(externalID)
	if connector == "" || externalID == "" {
		return ""
	}
	return "logs/chats/" + connector + "/" + externalID + ".md"
}

func sanitizeLogPathSegment(value string) string {
	trimmed := strings.TrimSpace(value)
	trimmed = strings.ReplaceAll(trimmed, " ", "-")
	trimmed = logPathSanitizer.ReplaceAllString(trimmed, "-")
	trimmed = strings.Trim(trimmed, "-.")
	return strings.ToLower(trimmed)
}

func extractTailLines(content string, maxLines, maxBytes int) string {
	if maxLines < 1 || maxBytes < 1 {
		return ""
	}
	lines := strings.Split(strings.TrimSpace(content), "\n")
	collected := make([]string, 0, maxLines)
	total := 0
	for index := len(lines) - 1; index >= 0; index-- {
		line := strings.TrimSpace(lines[index])
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "# Chat Log") || strings.HasPrefix(line, "- connector:") || strings.HasPrefix(line, "- external_id:") || strings.HasPrefix(line, "- display_name:") {
			continue
		}
		if len(line) > 320 {
			line = line[:320] + "..."
		}
		size := len(line) + 1
		if total+size > maxBytes {
			break
		}
		collected = append(collected, line)
		total += size
		if len(collected) >= maxLines {
			break
		}
	}
	if len(collected) == 0 {
		return ""
	}
	for left, right := 0, len(collected)-1; left < right; left, right = left+1, right-1 {
		collected[left], collected[right] = collected[right], collected[left]
	}
	return strings.Join(collected, "\n")
}

func compactWhitespace(input string) string {
	return strings.Join(strings.Fields(strings.TrimSpace(input)), " ")
}
