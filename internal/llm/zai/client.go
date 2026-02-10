package zai

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/carlos/spinner/internal/llm"
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
		cfg.BaseURL = "https://api.z.ai/api/paas/v4"
	}
	if strings.TrimSpace(cfg.Model) == "" {
		cfg.Model = "glm-4.7-flash"
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = 45 * time.Second
	}
	if strings.TrimSpace(cfg.SystemPrompt) == "" {
		cfg.SystemPrompt = "You are Spinner, a concise community operations assistant. Provide practical, accurate, policy-safe replies."
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
	if requiresAPIKey(c.cfg.BaseURL) && strings.TrimSpace(c.cfg.APIKey) == "" {
		return "", fmt.Errorf("%w: missing SPINNER_ZAI_API_KEY", llm.ErrUnavailable)
	}
	userText := strings.TrimSpace(input.Text)
	if userText == "" {
		return "", nil
	}

	userPrompt := buildUserPrompt(input)
	systemPrompt := strings.TrimSpace(c.cfg.SystemPrompt)
	if strings.TrimSpace(input.SystemPrompt) != "" {
		if systemPrompt != "" {
			systemPrompt += "\n\n"
		}
		systemPrompt += strings.TrimSpace(input.SystemPrompt)
	}
	payload := map[string]any{
		"model": c.cfg.Model,
		"messages": []map[string]string{
			{
				"role":    "system",
				"content": systemPrompt,
			},
			{
				"role":    "user",
				"content": userPrompt,
			},
		},
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("marshal zai request: %w", err)
	}

	endpoint := strings.TrimRight(c.cfg.BaseURL, "/") + "/chat/completions"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	if apiKey := strings.TrimSpace(c.cfg.APIKey); apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+apiKey)
	}
	req.Header.Set("Content-Type", "application/json")

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
		c.logger.Error("z.ai chat completion failed", "status", res.StatusCode, "body", strings.TrimSpace(string(respBody)))
		return "", fmt.Errorf("z.ai completion failed with status %d", res.StatusCode)
	}

	var response chatCompletionResponse
	if err := json.Unmarshal(respBody, &response); err != nil {
		return "", fmt.Errorf("decode zai response: %w", err)
	}
	if len(response.Choices) == 0 {
		return "", fmt.Errorf("z.ai response returned no choices")
	}
	content := strings.TrimSpace(response.Choices[0].Message.Content)
	content = sanitizeModelReply(content)
	if content == "" {
		return "", nil
	}
	return content, nil
}

var (
	thinkBlockPattern = regexp.MustCompile(`(?is)<think\b[^>]*>.*?</think>`)
	thinkFencePattern = regexp.MustCompile("(?is)```think\\s*.*?```")
)

func sanitizeModelReply(input string) string {
	trimmed := strings.TrimSpace(input)
	if trimmed == "" {
		return ""
	}
	trimmed = thinkBlockPattern.ReplaceAllString(trimmed, "")
	trimmed = thinkFencePattern.ReplaceAllString(trimmed, "")
	trimmed = strings.ReplaceAll(trimmed, "<think>", "")
	trimmed = strings.ReplaceAll(trimmed, "</think>", "")
	return strings.TrimSpace(trimmed)
}

func buildUserPrompt(input llm.MessageInput) string {
	channelType := "channel"
	if input.IsDM {
		channelType = "direct-message"
	}
	return fmt.Sprintf(
		"Connector: %s\nWorkspace: %s\nContext: %s\nChannel: %s (%s)\nUser: %s\nType: %s\n\nUser message:\n%s",
		strings.TrimSpace(input.Connector),
		strings.TrimSpace(input.WorkspaceID),
		strings.TrimSpace(input.ContextID),
		strings.TrimSpace(input.ExternalID),
		strings.TrimSpace(input.DisplayName),
		strings.TrimSpace(input.FromUserID),
		channelType,
		strings.TrimSpace(input.Text),
	)
}

type chatCompletionResponse struct {
	Choices []struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
	} `json:"choices"`
}

func requiresAPIKey(baseURL string) bool {
	trimmed := strings.TrimSpace(baseURL)
	if trimmed == "" {
		return true
	}
	parsed, err := url.Parse(trimmed)
	if err != nil {
		return true
	}
	host := strings.ToLower(strings.TrimSpace(parsed.Hostname()))
	if host == "" {
		return true
	}
	if host == "api.z.ai" {
		return true
	}
	if host == "localhost" || host == "127.0.0.1" || host == "::1" {
		return false
	}
	if parsedIP := net.ParseIP(host); parsedIP != nil {
		return parsedIP.IsLoopback()
	}
	return false
}
