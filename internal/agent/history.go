package agent

import (
	"os"
	"path/filepath"
	"strings"
)

// GetRecentHistory retrieves the last N lines from the chat log for context.
func GetRecentHistory(workspaceRoot, workspaceID, connector, externalID string, maxLines int) string {
	if workspaceRoot == "" || workspaceID == "" || connector == "" || externalID == "" {
		return ""
	}
	if maxLines < 1 {
		maxLines = 12
	}

	path := filepath.Join(workspaceRoot, workspaceID, "logs", "chats", strings.ToLower(connector), externalID+".md")
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}

	lines := extractConversationLines(string(data))
	if len(lines) > maxLines {
		lines = lines[len(lines)-maxLines:]
	}
	lines = fitConversationBytes(lines, 2400)
	return strings.Join(lines, "\n")
}

func extractConversationLines(content string) []string {
	rawLines := strings.Split(content, "\n")
	lines := make([]string, 0, len(rawLines))
	currentRole := ""
	for _, line := range rawLines {
		clean := strings.TrimSpace(line)
		if clean == "" {
			continue
		}
		switch {
		case strings.HasPrefix(clean, "# Chat Log"):
			continue
		case strings.HasPrefix(clean, "- connector:"),
			strings.HasPrefix(clean, "- external_id:"),
			strings.HasPrefix(clean, "- display_name:"),
			strings.HasPrefix(clean, "- direction:"),
			strings.HasPrefix(clean, "- actor:"),
			strings.HasPrefix(clean, "- tool:"),
			strings.HasPrefix(clean, "- status:"),
			strings.HasPrefix(clean, "- args:"),
			strings.HasPrefix(clean, "- output:"),
			strings.HasPrefix(clean, "- error:"):
			continue
		}
		if strings.EqualFold(clean, "Tool call") {
			continue
		}
		if strings.HasPrefix(clean, "## ") {
			switch {
			case strings.Contains(clean, "`INBOUND`"):
				currentRole = "user"
			case strings.Contains(clean, "`OUTBOUND`"):
				currentRole = "assistant"
			case strings.Contains(clean, "`TOOL`"):
				currentRole = "tool"
			default:
				currentRole = ""
			}
			continue
		}
		if currentRole == "tool" {
			continue
		}
		if len(clean) > 420 {
			clean = clean[:420] + "..."
		}
		prefix := ""
		if currentRole == "user" {
			prefix = "user: "
		} else if currentRole == "assistant" {
			prefix = "assistant: "
		}
		lines = append(lines, prefix+clean)
	}
	return lines
}

func fitConversationBytes(lines []string, maxBytes int) []string {
	if maxBytes < 1 || len(lines) == 0 {
		return lines
	}
	total := 0
	start := len(lines)
	for idx := len(lines) - 1; idx >= 0; idx-- {
		cost := len(lines[idx]) + 1
		if total+cost > maxBytes {
			break
		}
		total += cost
		start = idx
	}
	if start < 0 || start >= len(lines) {
		return lines
	}
	return lines[start:]
}
