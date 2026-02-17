package grounded

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/dwizi/agent-runtime/internal/llm"
	"github.com/dwizi/agent-runtime/internal/qmd"
)

func (r *Responder) Reply(ctx context.Context, input llm.MessageInput) (string, error) {
	if r.base == nil {
		return "", fmt.Errorf("%w: base responder missing", llm.ErrUnavailable)
	}
	augmented := input
	prompt, metrics := r.buildPrompt(ctx, input)
	augmented.Text = prompt
	r.logger.Debug("grounding context assembled",
		"connector", strings.TrimSpace(input.Connector),
		"workspace_id", strings.TrimSpace(input.WorkspaceID),
		"context_id", strings.TrimSpace(input.ContextID),
		"strategy", string(metrics.Strategy),
		"reason", metrics.Reason,
		"used_summary", metrics.UsedSummary,
		"summary_refreshed", metrics.SummaryRefreshed,
		"summary_turns", metrics.SummaryTurns,
		"used_tail", metrics.UsedTail,
		"used_qmd", metrics.UsedQMD,
		"qmd_results", metrics.QMDResultCount,
		"tokens_user", metrics.UserTokens,
		"tokens_summary", metrics.SummaryTokens,
		"tokens_tail", metrics.TailTokens,
		"tokens_qmd", metrics.QMDTokens,
		"tokens_total", metrics.PromptTokens,
	)
	return r.base.Reply(ctx, augmented)
}

func (r *Responder) buildPrompt(ctx context.Context, input llm.MessageInput) (string, PromptMetrics) {
	original := strings.TrimSpace(input.Text)
	metrics := PromptMetrics{Strategy: StrategyNone, Reason: "empty"}
	if original == "" {
		return original, metrics
	}
	if input.SkipGrounding {
		metrics.Reason = "skip_grounding"
		return original, metrics
	}
	if r.retriever == nil || strings.TrimSpace(input.WorkspaceID) == "" {
		metrics.Reason = "retriever_or_workspace_missing"
		return original, metrics
	}

	decision := DecideMemoryStrategy(input)
	metrics.Strategy = decision.Strategy
	metrics.Reason = decision.Reason

	lower := strings.ToLower(original)
	useConversationMemory := shouldIncludeConversationMemory(input, lower, decision)
	useQMD := shouldIncludeQMDRetrieval(decision)
	if !useConversationMemory && !useQMD {
		return original, metrics
	}

	budget := r.promptBudget()
	sections := []string{}

	userText := clipToTokenBudget(original, budget.User)
	if userText == "" {
		userText = original
	}
	sections = append(sections, userText)
	metrics.UserTokens = estimateTokens(userText)

	if useConversationMemory {
		summaryText, tailText, summaryMeta := r.loadConversationMemory(ctx, input, budget)
		if summaryText != "" {
			sections = append(sections, "", "Context memory summary:", summaryText)
			metrics.UsedSummary = true
			metrics.SummaryTokens = estimateTokens(summaryText)
			metrics.SummaryRefreshed = summaryMeta.Refreshed
			metrics.SummaryTurns = summaryMeta.Turns
		}
		if tailText != "" {
			sections = append(sections, "", "Recent conversation memory:", tailText)
			metrics.UsedTail = true
			metrics.TailTokens = estimateTokens(tailText)
		}
		if summaryText != "" || tailText != "" {
			sections = append(sections, "", "Use this memory only if relevant. If context is still missing, ask one concise clarifying question.")
		}
	}

	if useQMD {
		qmdContext, resultCount := r.buildQMDContext(ctx, input, original, budget.QMD)
		if qmdContext != "" {
			sections = append(sections, "", "Relevant workspace context:", qmdContext)
			metrics.UsedQMD = true
			metrics.QMDResultCount = resultCount
			metrics.QMDTokens = estimateTokens(qmdContext)
		}
	}

	if decision.Acknowledge {
		sections = append(sections, "", "Response behavior: open with one short acknowledgement that you are pulling context, then answer.")
	}

	joined := strings.TrimSpace(strings.Join(sections, "\n"))
	joined = clipToTokenBudget(joined, budget.Total)
	if r.cfg.MaxPromptBytes > 0 && len(joined) > r.cfg.MaxPromptBytes {
		joined = strings.TrimSpace(joined[:r.cfg.MaxPromptBytes])
	}
	metrics.PromptTokens = estimateTokens(joined)
	return joined, metrics
}

func (r *Responder) promptBudget() tokenBudget {
	total := maxInt(400, r.cfg.MaxPromptTokens)
	user := maxInt(80, r.cfg.UserPromptMaxTokens)
	summary := maxInt(80, r.cfg.MemorySummaryMaxTokens)
	tail := maxInt(80, r.cfg.ChatTailMaxTokens)
	qmdBudget := maxInt(120, r.cfg.QMDContextMaxTokens)

	reserved := user + summary + tail + qmdBudget
	if reserved > total {
		over := reserved - total
		qmdBudget = maxInt(120, qmdBudget-over)
	}
	if user+summary+tail+qmdBudget > total {
		total = user + summary + tail + qmdBudget
	}
	return tokenBudget{
		Total:   total,
		User:    user,
		Summary: summary,
		Tail:    tail,
		QMD:     qmdBudget,
	}
}

func shouldIncludeConversationMemory(input llm.MessageInput, lower string, decision MemoryDecision) bool {
	if strings.TrimSpace(input.Connector) == "" || strings.TrimSpace(input.ExternalID) == "" {
		return false
	}
	if looksLikeSmallTalk(lower) {
		return false
	}
	if decision.Strategy == StrategyTail {
		return true
	}
	// Default for channel-style conversations: keep continuity without relying on gateway-level duplication.
	return true
}

func shouldIncludeQMDRetrieval(decision MemoryDecision) bool {
	return decision.Strategy == StrategyQMD
}

func (r *Responder) buildQMDContext(ctx context.Context, input llm.MessageInput, query string, tokenBudget int) (string, int) {
	if tokenBudget < 1 {
		return "", 0
	}
	results, err := r.retriever.Search(ctx, input.WorkspaceID, query, r.cfg.TopK)
	if err != nil {
		if !errors.Is(err, qmd.ErrUnavailable) {
			r.logger.Error("qmd grounding search failed", "error", err, "workspace_id", input.WorkspaceID)
		}
		return "", 0
	}
	if len(results) == 0 {
		return "", 0
	}

	perDocBudget := maxInt(80, tokenBudget/maxInt(1, len(results)))
	blocks := []string{}
	used := 0
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
		snippet := clipToTokenBudget(compactWhitespace(result.Snippet), minInt(80, perDocBudget/2))
		excerpt := clipToTokenBudget(compactWhitespace(openResult.Content), minInt(perDocBudget, r.cfg.MaxDocExcerpt/4))
		if excerpt == "" {
			excerpt = clipToTokenBudget(compactWhitespace(result.Snippet), perDocBudget)
		}
		if excerpt == "" {
			continue
		}
		lines := []string{fmt.Sprintf("- source: %s", target)}
		if snippet != "" {
			lines = append(lines, "  snippet: "+snippet)
		}
		lines = append(lines, "  excerpt: "+excerpt)
		blocks = append(blocks, strings.Join(lines, "\n"))
		used++
		if estimateTokens(strings.Join(blocks, "\n")) >= tokenBudget {
			break
		}
	}
	if len(blocks) == 0 {
		return "", 0
	}
	return clipToTokenBudget(strings.Join(blocks, "\n"), tokenBudget), used
}

func (r *Responder) loadConversationMemory(ctx context.Context, input llm.MessageInput, budget tokenBudget) (string, string, summaryMetadata) {
	content := r.loadChatLogContent(ctx, input)
	if strings.TrimSpace(content) == "" {
		return "", "", summaryMetadata{}
	}

	turns := countInboundTurns(content)
	sourceLines := countSummarySourceLines(content)
	summaryText, meta := r.loadOrRefreshSummary(input, content, turns, sourceLines)
	summaryText = clipToTokenBudget(summaryText, budget.Summary)

	tailBytes := r.cfg.ChatTailBytes
	if budget.Tail > 0 {
		tailBytes = minInt(tailBytes, budget.Tail*4)
	}
	tail := extractTailLines(content, r.cfg.ChatTailLines, tailBytes)
	tail = clipToTokenBudget(tail, budget.Tail)

	return summaryText, tail, meta
}

func (r *Responder) loadChatLogContent(ctx context.Context, input llm.MessageInput) string {
	target := chatLogTarget(input.Connector, input.ExternalID)
	if strings.TrimSpace(target) == "" {
		return ""
	}

	workspaceID := strings.TrimSpace(input.WorkspaceID)
	if strings.TrimSpace(r.cfg.WorkspaceRoot) != "" && workspaceID != "" {
		path := filepath.Join(r.cfg.WorkspaceRoot, workspaceID, filepath.FromSlash(target))
		content, err := os.ReadFile(path)
		if err == nil {
			return string(content)
		}
	}

	openResult, err := r.retriever.OpenMarkdown(ctx, workspaceID, target)
	if err != nil {
		if !errors.Is(err, qmd.ErrNotFound) {
			r.logger.Debug("chat-tail grounding failed", "workspace_id", workspaceID, "target", target, "error", err)
		}
		return ""
	}
	return openResult.Content
}

func (r *Responder) loadOrRefreshSummary(input llm.MessageInput, chatLogContent string, currentTurns int, currentSourceLines int) (string, summaryMetadata) {
	path := r.contextSummaryPath(input)
	var existing string
	if path != "" {
		content, err := os.ReadFile(path)
		if err == nil {
			existing = string(content)
		}
	}
	existingTurns := parseSummaryTurns(existing)
	existingSourceLines := parseSummarySourceLines(existing)
	existingBody := extractSummaryBody(existing)

	refreshEvery := maxInt(1, r.cfg.MemorySummaryRefreshTurns)
	needsRefresh := strings.TrimSpace(existingBody) == ""
	if currentTurns > 0 {
		if existingTurns == 0 {
			needsRefresh = true
		} else if currentTurns >= existingTurns+refreshEvery {
			needsRefresh = true
		}
	}
	if !needsRefresh && currentSourceLines > 0 {
		if existingSourceLines == 0 {
			// Migrate older summaries that predate source line metadata.
			if strings.TrimSpace(existingBody) != "" && currentTurns > existingTurns {
				needsRefresh = true
			}
		} else {
			lineRefreshEvery := maxInt(6, refreshEvery*2)
			if currentSourceLines >= existingSourceLines+lineRefreshEvery {
				needsRefresh = true
			}
		}
	}

	if !needsRefresh {
		return existingBody, summaryMetadata{Refreshed: false, Turns: existingTurns}
	}

	newBody := summarizeConversation(chatLogContent, r.cfg.MemorySummarySourceMaxLines, r.cfg.MemorySummaryMaxItems)
	if strings.TrimSpace(newBody) == "" {
		if strings.TrimSpace(existingBody) != "" {
			return existingBody, summaryMetadata{Refreshed: false, Turns: existingTurns}
		}
		return "", summaryMetadata{Refreshed: false, Turns: currentTurns}
	}

	if path != "" {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			r.logger.Debug("failed to create memory summary directory", "path", path, "error", err)
		} else {
			record := buildSummaryDocument(input, currentTurns, currentSourceLines, newBody)
			if writeErr := os.WriteFile(path, []byte(record), 0o644); writeErr != nil {
				r.logger.Debug("failed to write memory summary", "path", path, "error", writeErr)
			}
		}
	}

	return newBody, summaryMetadata{Refreshed: true, Turns: currentTurns}
}

func (r *Responder) contextSummaryPath(input llm.MessageInput) string {
	root := strings.TrimSpace(r.cfg.WorkspaceRoot)
	workspaceID := strings.TrimSpace(input.WorkspaceID)
	if root == "" || workspaceID == "" {
		return ""
	}
	key := sanitizeSummaryKey(input.ContextID)
	if key == "" {
		key = sanitizeSummaryKey(strings.ToLower(strings.TrimSpace(input.Connector)) + "-" + strings.TrimSpace(input.ExternalID))
	}
	if key == "" {
		return ""
	}
	return filepath.Join(root, workspaceID, "memory", "contexts", key+".md")
}

func buildSummaryDocument(input llm.MessageInput, turns, sourceLines int, body string) string {
	contextID := strings.TrimSpace(input.ContextID)
	if contextID == "" {
		contextID = "unknown"
	}
	connector := strings.TrimSpace(input.Connector)
	if connector == "" {
		connector = "unknown"
	}
	externalID := strings.TrimSpace(input.ExternalID)
	if externalID == "" {
		externalID = "unknown"
	}
	lines := []string{
		"# Context Memory Summary",
		"",
		fmt.Sprintf("- context_id: `%s`", contextID),
		fmt.Sprintf("- connector: `%s`", connector),
		fmt.Sprintf("- external_id: `%s`", externalID),
		fmt.Sprintf("- turns: `%d`", turns),
		fmt.Sprintf("- source_lines: `%d`", maxInt(0, sourceLines)),
		fmt.Sprintf("- refreshed_at: `%s`", time.Now().UTC().Format(time.RFC3339)),
		"",
		strings.TrimSpace(body),
		"",
	}
	return strings.Join(lines, "\n")
}
