package grounded

import (
	"regexp"
	"strings"

	"github.com/dwizi/agent-runtime/internal/llm"
)

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
