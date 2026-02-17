package grounded

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

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
var summarySourceLinesPattern = regexp.MustCompile("(?m)^- source_lines:\\s*`?(\\d+)`?\\s*$")

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

func parseSummarySourceLines(content string) int {
	if strings.TrimSpace(content) == "" {
		return 0
	}
	match := summarySourceLinesPattern.FindStringSubmatch(content)
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

func countSummarySourceLines(content string) int {
	if strings.TrimSpace(content) == "" {
		return 0
	}
	return len(extractConversationSummaryLines(content, 1000000))
}
