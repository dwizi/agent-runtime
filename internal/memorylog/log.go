package memorylog

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

type Entry struct {
	WorkspaceRoot string
	WorkspaceID   string
	Connector     string
	ExternalID    string
	Direction     string
	ActorID       string
	DisplayName   string
	Text          string
	Timestamp     time.Time
}

var pathSanitizer = regexp.MustCompile(`[^a-zA-Z0-9._-]+`)

func Append(entry Entry) error {
	workspaceRoot := strings.TrimSpace(entry.WorkspaceRoot)
	workspaceID := strings.TrimSpace(entry.WorkspaceID)
	if workspaceRoot == "" || workspaceID == "" {
		return nil
	}
	text := strings.TrimSpace(entry.Text)
	if text == "" {
		return nil
	}

	connector := sanitizeSegment(entry.Connector)
	if connector == "" {
		connector = "unknown"
	}
	externalID := sanitizeSegment(entry.ExternalID)
	if externalID == "" {
		externalID = "unknown"
	}
	timestamp := entry.Timestamp.UTC()
	if timestamp.IsZero() {
		timestamp = time.Now().UTC()
	}

	baseDir := filepath.Join(workspaceRoot, workspaceID, "logs", "chats", connector)
	if err := os.MkdirAll(baseDir, 0o755); err != nil {
		return err
	}
	logPath := filepath.Join(baseDir, externalID+".md")

	header := ""
	if _, err := os.Stat(logPath); os.IsNotExist(err) {
		header = fmt.Sprintf("# Chat Log\n\n- connector: `%s`\n- external_id: `%s`\n- display_name: `%s`\n\n", connector, externalID, strings.TrimSpace(entry.DisplayName))
	}

	direction := strings.TrimSpace(strings.ToLower(entry.Direction))
	if direction == "" {
		direction = "inbound"
	}
	actor := strings.TrimSpace(entry.ActorID)
	if actor == "" {
		actor = "system"
	}
	body := fmt.Sprintf(
		"## %s `%s`\n- direction: `%s`\n- actor: `%s`\n\n%s\n\n",
		timestamp.Format(time.RFC3339),
		strings.ToUpper(direction),
		direction,
		actor,
		text,
	)

	file, err := os.OpenFile(logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer file.Close()

	if header != "" {
		if _, err := file.WriteString(header); err != nil {
			return err
		}
	}
	if _, err := file.WriteString(body); err != nil {
		return err
	}
	return nil
}

func sanitizeSegment(value string) string {
	trimmed := strings.TrimSpace(value)
	trimmed = strings.ReplaceAll(trimmed, " ", "-")
	trimmed = pathSanitizer.ReplaceAllString(trimmed, "-")
	trimmed = strings.Trim(trimmed, "-.")
	return strings.ToLower(trimmed)
}
