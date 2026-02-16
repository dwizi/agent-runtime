package anthropic

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/dwizi/agent-runtime/internal/llm"
)

type Config struct {
	APIKey       string
	BaseURL      string
	Model        string
	Timeout      time.Duration
	SystemPrompt string
}

type Client struct {
	cfg        Config
	httpClient *http.Client
	logger     *slog.Logger
}

func New(cfg Config, logger *slog.Logger) *Client {
	if strings.TrimSpace(cfg.BaseURL) == "" {
		cfg.BaseURL = "https://api.anthropic.com/v1"
	}
	if strings.TrimSpace(cfg.Model) == "" {
		cfg.Model = "claude-3-5-sonnet-latest"
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = 60 * time.Second
	}
	if logger == nil {
		logger = slog.Default()
	}

	return &Client{
		cfg: cfg,
		httpClient: &http.Client{
			Timeout: cfg.Timeout,
		},
		logger: logger,
	}
}

func (c *Client) Reply(ctx context.Context, input llm.MessageInput) (string, error) {
	if strings.TrimSpace(c.cfg.APIKey) == "" {
		return "", fmt.Errorf("%w: missing ANTHROPIC_API_KEY", llm.ErrUnavailable)
	}

	systemPrompt := strings.TrimSpace(c.cfg.SystemPrompt)
	if strings.TrimSpace(input.SystemPrompt) != "" {
		if systemPrompt != "" {
			systemPrompt += "\n\n"
		}
		systemPrompt += strings.TrimSpace(input.SystemPrompt)
	}

	userContent := fmt.Sprintf("User: %s (%s)\n%s", input.DisplayName, input.FromUserID, input.Text)

	payload := map[string]any{
		"model":      c.cfg.Model,
		"max_tokens": 4096,
		"system":     systemPrompt,
		"messages": []map[string]string{
			{
				"role":    "user",
				"content": userContent,
			},
		},
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("marshal anthropic request: %w", err)
	}

	endpoint := strings.TrimRight(c.cfg.BaseURL, "/") + "/messages"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("x-api-key", c.cfg.APIKey)
	req.Header.Set("anthropic-version", "2023-06-01")
	req.Header.Set("content-type", "application/json")

	res, err := c.httpClient.Do(req)
	if err != nil {
		return "", err
	}
	defer res.Body.Close()

	respBody, err := io.ReadAll(io.LimitReader(res.Body, 4<<20))
	if err != nil {
		return "", err
	}

	if res.StatusCode < 200 || res.StatusCode >= 300 {
		c.logger.Error("anthropic request failed", "status", res.StatusCode, "body", string(respBody))
		return "", fmt.Errorf("anthropic failed with status %d", res.StatusCode)
	}

	var response messagesResponse
	if err := json.Unmarshal(respBody, &response); err != nil {
		return "", fmt.Errorf("decode anthropic response: %w", err)
	}

	if len(response.Content) == 0 {
		return "", nil
	}
	
	// Anthropic returns a list of content blocks (text, tool_use, etc.)
	// For now we just grab the first text block.
	for _, block := range response.Content {
		if block.Type == "text" {
			return strings.TrimSpace(block.Text), nil
		}
	}
	
	return "", fmt.Errorf("no text content in response")
}

type messagesResponse struct {
	Content []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"content"`
}
