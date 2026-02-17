package discord

import (
	"encoding/json"
	"fmt"
	"io"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

func clipDiscordMessage(content string) string {
	trimmed := strings.TrimSpace(content)
	if len(trimmed) <= 2000 {
		return trimmed
	}
	return strings.TrimSpace(trimmed[:1997]) + "..."
}

func normalize(input string) string {
	text := strings.TrimSpace(strings.ToLower(input))
	if strings.HasPrefix(text, "/") {
		text = strings.TrimPrefix(text, "/")
	}
	if strings.HasPrefix(text, "pair@") {
		return pairCommand
	}
	return text
}

func discordDisplayName(author discordAuthor) string {
	if strings.TrimSpace(author.GlobalName) != "" {
		return author.GlobalName
	}
	if strings.TrimSpace(author.Username) != "" {
		return author.Username
	}
	if strings.TrimSpace(author.ID) != "" {
		return author.ID
	}
	return strconv.FormatInt(time.Now().Unix(), 10)
}

type gatewayEnvelope struct {
	Op int             `json:"op"`
	T  string          `json:"t"`
	S  *int64          `json:"s"`
	D  json.RawMessage `json:"d"`
}

type discordHello struct {
	HeartbeatIntervalMS int64 `json:"heartbeat_interval"`
}

type discordReady struct {
	User discordAuthor `json:"user"`
}

type discordMessageCreate struct {
	ID          string              `json:"id"`
	ChannelID   string              `json:"channel_id"`
	GuildID     string              `json:"guild_id"`
	Content     string              `json:"content"`
	Author      discordAuthor       `json:"author"`
	Attachments []discordAttachment `json:"attachments"`
	Mentions    []discordAuthor     `json:"mentions"`
}

type discordInteractionCreate struct {
	ID        string                   `json:"id"`
	Type      int                      `json:"type"`
	Token     string                   `json:"token"`
	ChannelID string                   `json:"channel_id"`
	GuildID   string                   `json:"guild_id"`
	Data      discordInteractionData   `json:"data"`
	Member    discordInteractionMember `json:"member"`
	User      discordAuthor            `json:"user"`
}

func (interaction discordInteractionCreate) userID() string {
	if strings.TrimSpace(interaction.Member.User.ID) != "" {
		return strings.TrimSpace(interaction.Member.User.ID)
	}
	return strings.TrimSpace(interaction.User.ID)
}

type discordInteractionData struct {
	Name    string                     `json:"name"`
	Options []discordInteractionOption `json:"options"`
}

type discordInteractionOption struct {
	Name  string `json:"name"`
	Type  int    `json:"type"`
	Value any    `json:"value"`
}

func (option discordInteractionOption) valueAsString() string {
	if option.Value == nil {
		return ""
	}
	switch value := option.Value.(type) {
	case string:
		return value
	case float64:
		if value == float64(int64(value)) {
			return strconv.FormatInt(int64(value), 10)
		}
		return strconv.FormatFloat(value, 'f', -1, 64)
	case bool:
		if value {
			return "true"
		}
		return "false"
	default:
		return fmt.Sprintf("%v", value)
	}
}

type discordInteractionMember struct {
	User discordAuthor `json:"user"`
}

type discordAuthor struct {
	ID         string `json:"id"`
	Username   string `json:"username"`
	GlobalName string `json:"global_name"`
	Bot        bool   `json:"bot"`
}

type discordAttachment struct {
	ID          string `json:"id"`
	Filename    string `json:"filename"`
	ContentType string `json:"content_type"`
	URL         string `json:"url"`
}

var filenameSanitizer = regexp.MustCompile(`[^a-zA-Z0-9._-]+`)

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
