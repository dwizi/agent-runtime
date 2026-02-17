package grounded

import (
	"strings"
)

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
