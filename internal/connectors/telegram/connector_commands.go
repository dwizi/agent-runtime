package telegram

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/dwizi/agent-runtime/internal/gateway"
)

func (c *Connector) syncCommands(ctx context.Context) error {
	endpoint := fmt.Sprintf("%s/bot%s/setMyCommands", c.apiBase, c.token)
	commands := make([]map[string]string, 0, len(gateway.SlashCommands())+1)
	for _, command := range gateway.SlashCommands() {
		name := telegramCommandName(command.Name)
		if name == "" {
			continue
		}
		commands = append(commands, map[string]string{
			"command":     name,
			"description": telegramCommandDescription(command.Description),
		})
	}
	commands = append(commands, map[string]string{
		"command":     pairingMessage,
		"description": "Link this Telegram account",
	})
	body := map[string]any{
		"commands": commands,
	}
	payload, err := json.Marshal(body)
	if err != nil {
		return err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(payload))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")

	res, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		message, _ := io.ReadAll(io.LimitReader(res.Body, 512))
		return fmt.Errorf("setMyCommands failed: status=%d body=%s", res.StatusCode, strings.TrimSpace(string(message)))
	}

	var response struct {
		OK bool `json:"ok"`
	}
	if err := json.NewDecoder(res.Body).Decode(&response); err != nil {
		return fmt.Errorf("decode setMyCommands: %w", err)
	}
	if !response.OK {
		return fmt.Errorf("telegram setMyCommands failed")
	}
	return nil
}

func telegramCommandName(name string) string {
	normalized := strings.ToLower(strings.TrimSpace(name))
	if normalized == "" {
		return ""
	}
	normalized = strings.ReplaceAll(normalized, "-", "_")
	normalized = telegramCommandSanitizer.ReplaceAllString(normalized, "")
	normalized = strings.Trim(normalized, "_")
	if len(normalized) > 32 {
		normalized = normalized[:32]
	}
	return strings.Trim(normalized, "_")
}

func telegramCommandDescription(description string) string {
	trimmed := strings.TrimSpace(description)
	if trimmed == "" {
		return "Agent Runtime command"
	}
	if len(trimmed) > 256 {
		return strings.TrimSpace(trimmed[:256])
	}
	return trimmed
}
