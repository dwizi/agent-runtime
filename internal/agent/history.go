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

	path := filepath.Join(workspaceRoot, workspaceID, "logs", "chats", strings.ToLower(connector), externalID+".md")
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}

	lines := strings.Split(string(data), "\n")
	if len(lines) > maxLines {
		lines = lines[len(lines)-maxLines:]
	}

	return strings.Join(lines, "\n")
}
