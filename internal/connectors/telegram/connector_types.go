package telegram

import (
	"fmt"
	"io"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

func normalizeIncoming(input string) string {
	text := strings.TrimSpace(strings.ToLower(input))
	if strings.HasPrefix(text, "/") {
		text = strings.TrimPrefix(text, "/")
	}
	if strings.HasPrefix(text, "pair@") {
		return pairingMessage
	}
	return text
}

func userDisplayName(user telegramUser) string {
	parts := []string{strings.TrimSpace(user.FirstName), strings.TrimSpace(user.LastName)}
	fullName := strings.TrimSpace(strings.Join(parts, " "))
	if fullName != "" {
		return fullName
	}
	if strings.TrimSpace(user.Username) != "" {
		return user.Username
	}
	return strconv.FormatInt(user.ID, 10)
}

type getUpdatesResponse struct {
	OK     bool             `json:"ok"`
	Result []telegramUpdate `json:"result"`
}

type telegramUpdate struct {
	UpdateID int64            `json:"update_id"`
	Message  *telegramMessage `json:"message"`
}

type telegramMessage struct {
	MessageID int64             `json:"message_id"`
	From      telegramUser      `json:"from"`
	Chat      telegramChat      `json:"chat"`
	Text      string            `json:"text"`
	Caption   string            `json:"caption"`
	Document  *telegramDocument `json:"document"`
}

type telegramChat struct {
	ID    int64  `json:"id"`
	Type  string `json:"type"`
	Title string `json:"title"`
}

type telegramUser struct {
	ID        int64  `json:"id"`
	FirstName string `json:"first_name"`
	LastName  string `json:"last_name"`
	Username  string `json:"username"`
}

type telegramDocument struct {
	FileID   string `json:"file_id"`
	FileName string `json:"file_name"`
	MimeType string `json:"mime_type"`
}

var filenameSanitizer = regexp.MustCompile(`[^a-zA-Z0-9._-]+`)
var telegramCommandSanitizer = regexp.MustCompile(`[^a-z0-9_]+`)

func sanitizeFilename(input string) string {
	base := strings.TrimSpace(filepath.Base(input))
	base = filenameSanitizer.ReplaceAllString(base, "-")
	base = strings.Trim(base, "-.")
	if base == "" {
		return "attachment.md"
	}
	return base
}

func isMarkdown(filename, mimeType string) bool {
	extension := strings.ToLower(strings.TrimSpace(filepath.Ext(filename)))
	if extension == ".md" || extension == ".markdown" {
		return true
	}
	mimeType = strings.ToLower(strings.TrimSpace(mimeType))
	return mimeType == "text/markdown" || mimeType == "text/x-markdown"
}

func ioReadAllLimited(body io.Reader, maxBytes int64) ([]byte, error) {
	limited := &io.LimitedReader{R: body, N: maxBytes + 1}
	data, err := io.ReadAll(limited)
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > maxBytes {
		return nil, fmt.Errorf("attachment too large")
	}
	return data, nil
}
