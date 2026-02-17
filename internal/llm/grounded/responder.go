package grounded

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

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
	summaryText, meta := r.loadOrRefreshSummary(input, content, turns)
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

func (r *Responder) loadOrRefreshSummary(input llm.MessageInput, chatLogContent string, currentTurns int) (string, summaryMetadata) {
	path := r.contextSummaryPath(input)
	var existing string
	if path != "" {
		content, err := os.ReadFile(path)
		if err == nil {
			existing = string(content)
		}
	}
	existingTurns := parseSummaryTurns(existing)
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
			record := buildSummaryDocument(input, currentTurns, newBody)
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

func buildSummaryDocument(input llm.MessageInput, turns int, body string) string {
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
		fmt.Sprintf("- refreshed_at: `%s`", time.Now().UTC().Format(time.RFC3339)),
		"",
		strings.TrimSpace(body),
		"",
	}
	return strings.Join(lines, "\n")
}

func summarizeConversation(content string, sourceMaxLines, maxItems int) string {
	lines := extractConversationSummaryLines(content, sourceMaxLines)
	if len(lines) == 0 {
		return ""
	}
	if maxItems < 3 {
		maxItems = 6
	}
	userLines := make([]string, 0, len(lines))
	assistantLines := make([]string, 0, len(lines))
	questionLines := make([]string, 0, len(lines))
	for _, line := range lines {
		if line.Role == "user" {
			userLines = append(userLines, line.Text)
			if strings.HasSuffix(strings.TrimSpace(line.Text), "?") {
				questionLines = append(questionLines, line.Text)
			}
			continue
		}
		if line.Role == "assistant" {
			assistantLines = append(assistantLines, line.Text)
		}
	}

	userIntents := collectLatestUnique(userLines, maxItems)
	pendingQuestions := collectLatestUnique(questionLines, maxInt(2, maxItems/2))
	actionLines := filterAssistantActions(assistantLines)
	if len(actionLines) == 0 {
		actionLines = assistantLines
	}
	assistantActions := collectLatestUnique(actionLines, maxItems)
	canonicalFacts := extractCanonicalFacts(lines, maxInt(8, maxItems+2))

	sections := []string{}
	if len(canonicalFacts) > 0 {
		sections = append(sections, "## Canonical Facts\n"+bulletize(canonicalFacts))
	}
	if len(userIntents) > 0 {
		sections = append(sections, "## Recent User Intents\n"+bulletize(userIntents))
	}
	if len(assistantActions) > 0 {
		sections = append(sections, "## Recent Assistant Actions\n"+bulletize(assistantActions))
	}
	if len(pendingQuestions) > 0 {
		sections = append(sections, "## Open Questions\n"+bulletize(pendingQuestions))
	}
	if len(sections) == 0 {
		return ""
	}
	return strings.TrimSpace(strings.Join(sections, "\n\n"))
}

func extractConversationSummaryLines(content string, maxLines int) []chatLine {
	if maxLines < 1 {
		maxLines = 120
	}
	raw := strings.Split(strings.TrimSpace(content), "\n")
	lines := make([]chatLine, 0, len(raw))
	currentRole := ""
	for _, rawLine := range raw {
		line := strings.TrimSpace(rawLine)
		if line == "" {
			continue
		}
		switch {
		case strings.HasPrefix(line, "# Chat Log"):
			continue
		case strings.HasPrefix(line, "- connector:"),
			strings.HasPrefix(line, "- external_id:"),
			strings.HasPrefix(line, "- display_name:"),
			strings.HasPrefix(line, "- direction:"),
			strings.HasPrefix(line, "- actor:"),
			strings.HasPrefix(line, "- tool:"),
			strings.HasPrefix(line, "- status:"),
			strings.HasPrefix(line, "- args:"),
			strings.HasPrefix(line, "- output:"),
			strings.HasPrefix(line, "- error:"):
			continue
		case strings.EqualFold(line, "Tool call"):
			currentRole = ""
			continue
		case strings.HasPrefix(line, "## "):
			switch {
			case strings.Contains(line, "`INBOUND`"):
				currentRole = "user"
			case strings.Contains(line, "`OUTBOUND`"):
				currentRole = "assistant"
			default:
				currentRole = ""
			}
			continue
		}
		if currentRole == "" {
			continue
		}
		line = compactWhitespace(line)
		line = truncateLine(line, 220)
		if line == "" {
			continue
		}
		lines = append(lines, chatLine{Role: currentRole, Text: line})
	}
	if len(lines) > maxLines {
		lines = lines[len(lines)-maxLines:]
	}
	return lines
}

func filterAssistantActions(lines []string) []string {
	if len(lines) == 0 {
		return nil
	}
	cues := []string{
		"i'll",
		"i will",
		"task queued",
		"created",
		"updated",
		"approved",
		"denied",
		"completed",
		"finished",
		"next step",
	}
	out := []string{}
	for _, line := range lines {
		lower := strings.ToLower(line)
		if containsAny(lower, cues) {
			out = append(out, line)
		}
	}
	return out
}

func collectLatestUnique(lines []string, limit int) []string {
	if limit < 1 {
		limit = 1
	}
	seen := map[string]struct{}{}
	picked := []string{}
	for idx := len(lines) - 1; idx >= 0; idx-- {
		line := compactWhitespace(lines[idx])
		if line == "" {
			continue
		}
		normalized := normalizeCueText(line)
		if normalized == "" {
			continue
		}
		if _, exists := seen[normalized]; exists {
			continue
		}
		seen[normalized] = struct{}{}
		picked = append(picked, truncateLine(line, 190))
		if len(picked) >= limit {
			break
		}
	}
	for left, right := 0, len(picked)-1; left < right; left, right = left+1, right-1 {
		picked[left], picked[right] = picked[right], picked[left]
	}
	return picked
}

func bulletize(lines []string) string {
	if len(lines) == 0 {
		return ""
	}
	out := make([]string, 0, len(lines))
	for _, line := range lines {
		compact := compactWhitespace(line)
		if compact == "" {
			continue
		}
		out = append(out, "- "+compact)
	}
	return strings.Join(out, "\n")
}

var (
	codenameFactPattern        = regexp.MustCompile(`(?i)\bcodename\b(?:\s+is|\s*[:=])?\s*([A-Za-z0-9._-]+)`)
	environmentFactPattern     = regexp.MustCompile(`(?i)\benvironment\b(?:\s+is|\s*[:=])?\s*([A-Za-z0-9._-]+)`)
	freezeWindowFactPattern    = regexp.MustCompile(`(?i)\bfreeze window\b(?:\s+(?:is|moved to)|\s*[:=])?\s*([^.;\n]+)`)
	incidentIDFactPattern      = regexp.MustCompile(`(?i)\bincident\s*id\b(?:\s+is|\s*[:=])?\s*([A-Za-z0-9._-]+)`)
	releaseManagerFactPattern  = regexp.MustCompile(`(?i)\brelease manager\b(?:\s+is|\s*[:=])?\s*([A-Za-z][A-Za-z .'-]+)`)
	backupOwnerFactPattern     = regexp.MustCompile(`(?i)\bbackup\b(?:\s+is|\s*[:=])?\s*([A-Za-z][A-Za-z .'-]+)`)
	serviceDependsFactPattern  = regexp.MustCompile(`(?i)\bservice\s+([A-Za-z0-9._-]+)\s+depends on\s+(.+)$`)
	arrowDependencyFactPattern = regexp.MustCompile(`(?i)^([A-Za-z0-9._-]+)\s*[→>-]+\s*([A-Za-z0-9._-]+)$`)
	notifyChannelFactPattern   = regexp.MustCompile(`(?i)(#[A-Za-z0-9._-]+)`)
	escalationFactPattern      = regexp.MustCompile(`(?i)\bescalate\s+([^.;\n]+)`)
	freezeWindowTimePattern    = regexp.MustCompile(`(?i)\b(?:\d{1,2}:\d{2}|utc|gmt|z)\b`)
	incidentIDValuePattern     = regexp.MustCompile(`(?i)^[A-Z]{2,}[-_ ]?[0-9][A-Z0-9-]*$`)
	dependencyTokenPattern     = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{1,48}$`)
)

func extractCanonicalFacts(lines []chatLine, limit int) []string {
	if len(lines) == 0 {
		return nil
	}
	if limit < 1 {
		limit = 8
	}

	codename := ""
	environment := ""
	freezeWindow := ""
	incidentID := ""
	escalation := ""
	notifyChannel := ""
	releaseManager := ""
	backupOwner := ""
	migrationConstraint := ""
	dependencyOrder := []string{}
	dependencyMap := map[string][]string{}

	for _, line := range lines {
		if line.Role != "user" {
			continue
		}
		normalized := normalizeFactLine(line.Text)
		if normalized == "" {
			continue
		}
		lower := strings.ToLower(normalized)

		if match := codenameFactPattern.FindStringSubmatch(normalized); len(match) > 1 {
			codename = cleanFactValue(match[1])
		}
		if match := environmentFactPattern.FindStringSubmatch(normalized); len(match) > 1 {
			environment = cleanFactValue(match[1])
		}
		if match := freezeWindowFactPattern.FindStringSubmatch(normalized); len(match) > 1 {
			if candidate := cleanFreezeWindowValue(match[1]); candidate != "" {
				freezeWindow = candidate
			}
		}
		if match := incidentIDFactPattern.FindStringSubmatch(normalized); len(match) > 1 {
			if candidate := cleanIncidentIDValue(match[1]); candidate != "" {
				incidentID = candidate
			}
		}
		if match := releaseManagerFactPattern.FindStringSubmatch(normalized); len(match) > 1 {
			if candidate := cleanOwnerName(match[1]); candidate != "" {
				releaseManager = candidate
			}
		}
		if match := backupOwnerFactPattern.FindStringSubmatch(normalized); len(match) > 1 {
			if candidate := cleanOwnerName(match[1]); candidate != "" {
				backupOwner = candidate
			}
		}
		if match := escalationFactPattern.FindStringSubmatch(normalized); len(match) > 1 {
			if candidate := cleanEscalationValue(match[1]); candidate != "" {
				escalation = candidate
			}
		}
		if match := notifyChannelFactPattern.FindStringSubmatch(normalized); len(match) > 1 {
			notifyChannel = cleanFactValue(match[1])
		}
		if strings.Contains(lower, "no production schema migrations") {
			migrationConstraint = "No production schema migrations during freeze window"
		}

		if match := serviceDependsFactPattern.FindStringSubmatch(normalized); len(match) > 2 {
			service := strings.ToLower(cleanFactValue(match[1]))
			targets := splitDependencyTargets(match[2])
			if service != "" && len(targets) > 0 {
				if _, exists := dependencyMap[service]; !exists {
					dependencyOrder = append(dependencyOrder, service)
				}
				dependencyMap[service] = mergeUniqueStrings(dependencyMap[service], targets)
			}
		}
		if match := arrowDependencyFactPattern.FindStringSubmatch(normalized); len(match) > 2 {
			service := strings.ToLower(cleanFactValue(match[1]))
			target := strings.ToLower(cleanDependencyToken(match[2]))
			if service != "" && target != "" {
				if _, exists := dependencyMap[service]; !exists {
					dependencyOrder = append(dependencyOrder, service)
				}
				dependencyMap[service] = mergeUniqueStrings(dependencyMap[service], []string{target})
			}
		}
	}

	facts := make([]string, 0, 12)
	appendFact := func(label, value string) {
		value = cleanFactValue(value)
		if value == "" {
			return
		}
		facts = append(facts, fmt.Sprintf("%s: %s", label, value))
	}

	appendFact("Codename", codename)
	appendFact("Environment", environment)
	appendFact("Freeze window", freezeWindow)
	appendFact("Incident ID", incidentID)
	appendFact("Escalation policy", escalation)
	appendFact("Notify channel", notifyChannel)
	if releaseManager != "" || backupOwner != "" {
		ownerParts := []string{}
		if releaseManager != "" {
			ownerParts = append(ownerParts, "release manager "+releaseManager)
		}
		if backupOwner != "" {
			ownerParts = append(ownerParts, "backup "+backupOwner)
		}
		appendFact("Owners", strings.Join(ownerParts, "; "))
	}
	for _, service := range dependencyOrder {
		targets := dependencyMap[service]
		if len(targets) == 0 {
			continue
		}
		appendFact("Dependencies ("+service+")", strings.Join(targets, ", "))
	}
	appendFact("Migration constraint", migrationConstraint)

	if len(facts) > limit {
		return facts[:limit]
	}
	return facts
}

func normalizeFactLine(input string) string {
	value := compactWhitespace(input)
	if value == "" {
		return ""
	}
	value = strings.TrimSpace(strings.TrimLeft(value, "-*"))
	value = strings.ReplaceAll(value, "`", "")
	value = strings.ReplaceAll(value, "**", "")
	value = strings.ReplaceAll(value, "__", "")
	return compactWhitespace(value)
}

func cleanFactValue(input string) string {
	value := compactWhitespace(input)
	if value == "" {
		return ""
	}
	value = strings.Trim(value, " \"'`*")
	if strings.Contains(value, "->") {
		parts := strings.Split(value, "->")
		value = strings.TrimSpace(parts[len(parts)-1])
	}
	lower := strings.ToLower(value)
	trimMarkers := []string{" (replaces", "; replaces", ", replaces", " supersede", " superseded"}
	for _, marker := range trimMarkers {
		index := strings.Index(lower, marker)
		if index > 0 {
			value = strings.TrimSpace(value[:index])
			lower = strings.ToLower(value)
		}
	}
	return strings.Trim(value, " .,;")
}

func cleanFreezeWindowValue(input string) string {
	value := cleanFactValue(input)
	if value == "" {
		return ""
	}
	lower := strings.ToLower(value)
	cutMarkers := []string{
		" and incident id",
		", incident id",
		" incident id",
		"; incident id",
		" and incident",
	}
	for _, marker := range cutMarkers {
		index := strings.Index(lower, marker)
		if index > 0 {
			value = strings.TrimSpace(value[:index])
			lower = strings.ToLower(value)
		}
	}
	value = cleanFactValue(value)
	lower = strings.ToLower(value)
	if value == "" || value == "?" {
		return ""
	}
	if strings.Contains(lower, "not specified") || strings.Contains(lower, "unknown") {
		return ""
	}
	if !freezeWindowTimePattern.MatchString(value) {
		return ""
	}
	return value
}

func cleanIncidentIDValue(input string) string {
	value := cleanFactValue(input)
	if value == "" {
		return ""
	}
	value = strings.TrimSpace(strings.Trim(value, " .,;"))
	if value == "" {
		return ""
	}
	value = strings.ToUpper(value)
	if !incidentIDValuePattern.MatchString(value) {
		return ""
	}
	return value
}

func cleanOwnerName(input string) string {
	value := cleanFactValue(input)
	if value == "" {
		return ""
	}
	lower := strings.ToLower(value)
	cutMarkers := []string{
		"; backup",
		", backup",
		" and backup",
		"; release manager",
	}
	for _, marker := range cutMarkers {
		index := strings.Index(lower, marker)
		if index > 0 {
			value = strings.TrimSpace(value[:index])
			lower = strings.ToLower(value)
		}
	}
	return strings.Trim(value, " .,;")
}

func cleanEscalationValue(input string) string {
	value := cleanFactValue(input)
	if value == "" {
		return ""
	}
	lower := strings.ToLower(value)
	cutMarkers := []string{
		"; notify",
		", notify",
		" notify #",
	}
	for _, marker := range cutMarkers {
		index := strings.Index(lower, marker)
		if index > 0 {
			value = strings.TrimSpace(value[:index])
			lower = strings.ToLower(value)
		}
	}
	value = cleanFactValue(value)
	value = strings.TrimSpace(strings.TrimPrefix(value, "only "))
	value = strings.TrimSpace(strings.TrimPrefix(value, "Only "))
	if value == "" {
		return ""
	}
	return "only " + value
}

func splitDependencyTargets(input string) []string {
	normalized := cleanFactValue(input)
	if normalized == "" {
		return nil
	}
	normalized = strings.ReplaceAll(normalized, " and ", ",")
	normalized = strings.ReplaceAll(normalized, " AND ", ",")
	parts := strings.Split(normalized, ",")
	targets := make([]string, 0, len(parts))
	for _, part := range parts {
		cleaned := cleanDependencyToken(part)
		if cleaned == "" {
			continue
		}
		targets = append(targets, cleaned)
	}
	return mergeUniqueStrings(nil, targets)
}

func cleanDependencyToken(input string) string {
	value := strings.ToLower(cleanFactValue(input))
	value = strings.TrimSpace(strings.Trim(value, ".,;"))
	value = strings.TrimPrefix(value, "and ")
	if !dependencyTokenPattern.MatchString(value) {
		return ""
	}
	return value
}

func mergeUniqueStrings(existing, additions []string) []string {
	if len(additions) == 0 {
		return existing
	}
	seen := map[string]struct{}{}
	merged := make([]string, 0, len(existing)+len(additions))
	for _, item := range existing {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		key := strings.ToLower(item)
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		merged = append(merged, item)
	}
	for _, item := range additions {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		key := strings.ToLower(item)
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		merged = append(merged, item)
	}
	return merged
}

var summaryTurnsPattern = regexp.MustCompile("(?m)^- turns:\\s*`?(\\d+)`?\\s*$")

func parseSummaryTurns(content string) int {
	if strings.TrimSpace(content) == "" {
		return 0
	}
	match := summaryTurnsPattern.FindStringSubmatch(content)
	if len(match) < 2 {
		return 0
	}
	parsed, err := strconv.Atoi(strings.TrimSpace(match[1]))
	if err != nil || parsed < 0 {
		return 0
	}
	return parsed
}

func extractSummaryBody(content string) string {
	trimmed := strings.TrimSpace(content)
	if trimmed == "" {
		return ""
	}
	lines := strings.Split(trimmed, "\n")
	start := -1
	for idx, line := range lines {
		if strings.HasPrefix(strings.TrimSpace(line), "## ") {
			start = idx
			break
		}
	}
	if start < 0 {
		return ""
	}
	return strings.TrimSpace(strings.Join(lines[start:], "\n"))
}

func countInboundTurns(content string) int {
	if strings.TrimSpace(content) == "" {
		return 0
	}
	count := 0
	for _, rawLine := range strings.Split(content, "\n") {
		line := strings.TrimSpace(rawLine)
		if strings.HasPrefix(line, "## ") && strings.Contains(line, "`INBOUND`") {
			count++
		}
	}
	return count
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
	if shouldUseImplicitQMD(lower) {
		return MemoryDecision{
			Strategy:    StrategyQMD,
			Reason:      "implicit_workspace_question",
			Acknowledge: false,
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
	strongCues := []string{
		"docs",
		"documentation",
		"readme",
		"policy",
		"pricing",
		"which file",
		"where is",
		"in workspace",
		"in memory",
		"knowledge base",
		"repo",
		"repository",
		"codebase",
	}
	weakCues := []string{
		"search",
		"find",
		"lookup",
		"look up",
	}
	if strings.Contains(lower, "http://") || strings.Contains(lower, "https://") {
		return true
	}
	if domainReferencePattern.MatchString(lower) {
		return true
	}
	score := 0
	if containsAny(lower, strongCues) {
		score += 2
	}
	if containsAny(lower, weakCues) {
		score++
	}
	if containsWorkspaceAnchor(lower) {
		score++
	}
	return score >= 2
}

func shouldUseImplicitQMD(lower string) bool {
	trimmed := strings.TrimSpace(lower)
	if trimmed == "" {
		return false
	}
	words := strings.Fields(trimmed)
	if len(words) < 5 {
		return false
	}
	isQuestion := strings.Contains(trimmed, "?") || hasQuestionPrefix(trimmed)
	if !isQuestion {
		return false
	}
	if strings.Contains(trimmed, "http://") || strings.Contains(trimmed, "https://") || domainReferencePattern.MatchString(trimmed) {
		return true
	}
	if !containsWorkspaceAnchor(trimmed) {
		return false
	}
	return true
}

func hasQuestionPrefix(trimmed string) bool {
	questionPrefixes := []string{
		"how ",
		"what ",
		"why ",
		"when ",
		"where ",
		"which ",
		"can ",
		"could ",
		"do ",
		"does ",
		"is ",
		"are ",
	}
	for _, prefix := range questionPrefixes {
		if strings.HasPrefix(trimmed, prefix) {
			return true
		}
	}
	return false
}

func containsWorkspaceAnchor(lower string) bool {
	anchors := []string{
		"workspace",
		"repo",
		"repository",
		"project",
		"codebase",
		"file",
		"folder",
		"path",
		"docs",
		"doc",
		"readme",
		"markdown",
		".md",
		"memory",
		"context",
		"task",
		"objective",
		"log",
		"logs",
	}
	return containsAny(lower, anchors)
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

func sanitizeSummaryKey(value string) string {
	return sanitizeLogPathSegment(value)
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

func truncateLine(input string, maxLen int) string {
	value := strings.TrimSpace(input)
	if maxLen < 1 || len(value) <= maxLen {
		return value
	}
	return strings.TrimSpace(value[:maxLen]) + "..."
}

func estimateTokens(input string) int {
	trimmed := strings.TrimSpace(input)
	if trimmed == "" {
		return 0
	}
	charBased := (len(trimmed) + 3) / 4
	wordBased := maxInt(1, len(strings.Fields(trimmed)))
	if wordBased > charBased {
		return wordBased
	}
	return charBased
}

func clipToTokenBudget(input string, maxTokens int) string {
	trimmed := strings.TrimSpace(input)
	if trimmed == "" || maxTokens < 1 {
		return ""
	}
	if estimateTokens(trimmed) <= maxTokens {
		return trimmed
	}
	maxBytes := maxTokens * 4
	if maxBytes < 64 {
		maxBytes = 64
	}
	if len(trimmed) <= maxBytes {
		return trimmed
	}
	clipped := strings.TrimSpace(trimmed[:maxBytes])
	if idx := strings.LastIndexAny(clipped, " \n\t"); idx > maxBytes/2 {
		clipped = strings.TrimSpace(clipped[:idx])
	}
	if clipped == "" {
		return ""
	}
	if strings.HasSuffix(clipped, "...") {
		return clipped
	}
	return clipped + "..."
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}
