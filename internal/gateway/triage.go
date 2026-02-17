package gateway

import (
	"fmt"
	"regexp"
	"strings"
	"time"
)

var domainReferencePattern = regexp.MustCompile(`\b[a-z0-9][a-z0-9-]*\.[a-z]{2,}\b`)

type TriageClass string

const (
	TriageQuestion   TriageClass = "question"
	TriageIssue      TriageClass = "issue"
	TriageTask       TriageClass = "task"
	TriageModeration TriageClass = "moderation"
	TriageNoise      TriageClass = "noise"
)

type TriagePriority string

const (
	TriagePriorityP1 TriagePriority = "p1"
	TriagePriorityP2 TriagePriority = "p2"
	TriagePriorityP3 TriagePriority = "p3"
)

type RouteDecision struct {
	TaskID           string
	WorkspaceID      string
	ContextID        string
	Class            TriageClass
	Priority         TriagePriority
	DueAt            time.Time
	DueWindow        time.Duration
	AssignedLane     string
	SourceConnector  string
	SourceExternalID string
	SourceUserID     string
	SourceText       string
	Reason           string
}

func normalizeTriageClass(value string) (TriageClass, bool) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case string(TriageQuestion):
		return TriageQuestion, true
	case string(TriageIssue):
		return TriageIssue, true
	case string(TriageTask):
		return TriageTask, true
	case string(TriageModeration):
		return TriageModeration, true
	case string(TriageNoise):
		return TriageNoise, true
	default:
		return "", false
	}
}

func normalizeTriagePriority(value string) (TriagePriority, bool) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "p1", "high", "urgent":
		return TriagePriorityP1, true
	case "p2", "medium", "normal":
		return TriagePriorityP2, true
	case "p3", "low":
		return TriagePriorityP3, true
	default:
		return "", false
	}
}

func classifyMessage(text string) (TriageClass, string) {
	normalized := normalizeForTriage(text)
	if normalized == "" {
		return TriageNoise, "empty message"
	}
	if isNoiseText(normalized) {
		return TriageNoise, "short acknowledgement"
	}
	if looksLikeModeration(normalized) {
		return TriageModeration, "moderation keywords"
	}
	if looksLikeIssue(normalized) {
		return TriageIssue, "issue keywords"
	}
	if looksLikeTask(normalized) {
		return TriageTask, "action request"
	}
	if looksLikeQuestion(normalized) {
		return TriageQuestion, "question pattern"
	}
	return TriageNoise, "no routing intent"
}

func shouldAutoRouteDecision(decision RouteDecision) bool {
	switch decision.Class {
	case TriageModeration, TriageIssue, TriageTask:
		return true
	case TriageQuestion:
		return questionNeedsExternalFollowUp(decision.SourceText)
	default:
		return false
	}
}

func routingDefaults(class TriageClass) (priority TriagePriority, dueWindow time.Duration, lane string) {
	switch class {
	case TriageModeration:
		return TriagePriorityP1, 2 * time.Hour, "moderation"
	case TriageIssue:
		return TriagePriorityP2, 8 * time.Hour, "operations"
	case TriageQuestion:
		return TriagePriorityP3, 48 * time.Hour, "support"
	case TriageTask:
		return TriagePriorityP2, 24 * time.Hour, "operations"
	default:
		return TriagePriorityP3, 0, "backlog"
	}
}

func deriveRouteDecision(input MessageInput, workspaceID, contextID, text string) RouteDecision {
	class, reason := classifyMessage(text)
	priority, dueWindow, lane := routingDefaults(class)
	now := time.Now().UTC()
	dueAt := time.Time{}
	if dueWindow > 0 {
		dueAt = now.Add(dueWindow)
	}
	return RouteDecision{
		Class:            class,
		Priority:         priority,
		DueAt:            dueAt,
		DueWindow:        dueWindow,
		AssignedLane:     lane,
		WorkspaceID:      workspaceID,
		ContextID:        contextID,
		SourceConnector:  strings.ToLower(strings.TrimSpace(input.Connector)),
		SourceExternalID: strings.TrimSpace(input.ExternalID),
		SourceUserID:     strings.TrimSpace(input.FromUserID),
		SourceText:       strings.TrimSpace(text),
		Reason:           reason,
	}
}

func buildRoutedTaskTitle(class TriageClass, sourceText string) string {
	prefix := "[TASK]"
	switch class {
	case TriageIssue:
		prefix = "[ISSUE]"
	case TriageModeration:
		prefix = "[MODERATION]"
	case TriageQuestion:
		prefix = "[QUESTION]"
	}
	title := prefix + " " + compactSnippet(sourceText)
	title = strings.TrimSpace(title)
	if len(title) > 72 {
		title = title[:72]
	}
	if title == "" {
		title = prefix + " Routed message"
	}
	return title
}

func buildRoutedTaskPrompt(decision RouteDecision) string {
	lines := []string{
		"Routed inbound community message for follow-up.",
		fmt.Sprintf("Classification: `%s` (%s).", decision.Class, strings.TrimSpace(decision.Reason)),
		fmt.Sprintf("Priority: `%s`.", decision.Priority),
		fmt.Sprintf("Assigned lane: `%s`.", strings.TrimSpace(decision.AssignedLane)),
		fmt.Sprintf("Source: connector=`%s` external_id=`%s` user_id=`%s`.", decision.SourceConnector, decision.SourceExternalID, decision.SourceUserID),
	}
	if !decision.DueAt.IsZero() {
		lines = append(lines, fmt.Sprintf("Due by: `%s`.", decision.DueAt.UTC().Format(time.RFC3339)))
	}
	lines = append(lines, "Original message:")
	lines = append(lines, "```")
	lines = append(lines, strings.TrimSpace(decision.SourceText))
	lines = append(lines, "```")
	return strings.Join(lines, "\n")
}

func parseDueWindow(value string) (time.Duration, error) {
	trimmed := strings.ToLower(strings.TrimSpace(value))
	if trimmed == "" {
		return 0, fmt.Errorf("due window is empty")
	}
	if strings.HasSuffix(trimmed, "d") {
		amount := strings.TrimSuffix(trimmed, "d")
		days, err := parsePositiveInt(amount)
		if err != nil {
			return 0, err
		}
		return time.Duration(days) * 24 * time.Hour, nil
	}
	duration, err := time.ParseDuration(trimmed)
	if err != nil {
		return 0, err
	}
	if duration <= 0 {
		return 0, fmt.Errorf("duration must be positive")
	}
	return duration, nil
}

func parsePositiveInt(value string) (int, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0, fmt.Errorf("value is empty")
	}
	number := 0
	for _, char := range value {
		if char < '0' || char > '9' {
			return 0, fmt.Errorf("invalid numeric value")
		}
		number = (number * 10) + int(char-'0')
	}
	if number < 1 {
		return 0, fmt.Errorf("value must be positive")
	}
	return number, nil
}

func looksLikeQuestion(text string) bool {
	if strings.Contains(text, "?") {
		return true
	}
	return strings.HasPrefix(text, "how ") ||
		strings.HasPrefix(text, "what ") ||
		strings.HasPrefix(text, "when ") ||
		strings.HasPrefix(text, "where ") ||
		strings.HasPrefix(text, "why ") ||
		strings.HasPrefix(text, "can ") ||
		strings.HasPrefix(text, "could ")
}

func looksLikeIssue(text string) bool {
	keywords := []string{
		"bug", "error", "broken", "fails", "failing", "cannot", "can't", "doesnt work", "doesn't work",
		"issue", "problem", "outage", "stuck", "exception", "not working",
	}
	return containsAny(text, keywords)
}

func looksLikeModeration(text string) bool {
	keywords := []string{
		"spam", "scam", "abuse", "harass", "report user", "ban ", "mute ", "phishing", "nsfw", "offensive",
	}
	return containsAny(text, keywords)
}

func looksLikeTask(text string) bool {
	if text == "" {
		return false
	}
	prefixes := []string{
		"please ",
		"need you to ",
		"help me ",
		"todo ",
		"create a task",
	}
	for _, prefix := range prefixes {
		if strings.HasPrefix(text, prefix) && len(strings.Fields(text)) >= 4 {
			return true
		}
	}
	keywords := []string{
		"follow up",
		"action item",
		"assign this",
		"schedule this",
		"investigate this",
		"please investigate",
		"set reminder",
		"track this",
	}
	return containsAny(text, keywords)
}

func questionNeedsExternalFollowUp(text string) bool {
	normalized := normalizeForTriage(text)
	if normalized == "" {
		return false
	}
	asyncCues := []string{
		"follow up",
		"monitor",
		"track",
		"watch",
		"remind",
		"schedule",
		"notify me",
		"later",
		"in 5 minutes",
		"in 10 minutes",
		"tomorrow",
	}
	if containsAny(normalized, asyncCues) {
		return true
	}
	researchCues := []string{
		"run a search",
		"search ",
		"web search",
		"look up",
		"lookup",
		"find ",
		"check ",
		"fetch ",
	}
	researchTopicCues := []string{
		"pricing",
		"price ",
		"cost ",
		"plans",
		"latest",
		"today",
	}
	if containsAny(normalized, researchCues) && containsAny(normalized, researchTopicCues) {
		return true
	}
	if strings.Contains(normalized, "http://") || strings.Contains(normalized, "https://") || looksLikeDomainReference(normalized) {
		webActionCues := []string{
			"search",
			"check",
			"monitor",
			"track",
			"look up",
			"verify",
			"fetch",
		}
		if containsAny(normalized, webActionCues) {
			return true
		}
	}
	return false
}

func looksLikeDomainReference(text string) bool {
	return domainReferencePattern.MatchString(text)
}

func containsAny(text string, keywords []string) bool {
	for _, keyword := range keywords {
		if strings.Contains(text, keyword) {
			return true
		}
	}
	return false
}

func isNoiseText(text string) bool {
	if len(text) < 4 {
		return true
	}
	noisePatterns := []string{
		"^ok+$", "^k+$", "^thanks!?$", "^thx!?$", "^lol+$", "^lmao+$", "^gm$", "^gn$", "^hi+$", "^yo+$",
	}
	for _, pattern := range noisePatterns {
		matched, _ := regexp.MatchString(pattern, text)
		if matched {
			return true
		}
	}
	return false
}

func normalizeForTriage(input string) string {
	value := strings.TrimSpace(strings.ToLower(input))
	value = strings.ReplaceAll(value, "\n", " ")
	value = strings.Join(strings.Fields(value), " ")
	return value
}
