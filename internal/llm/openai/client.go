package openai

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
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
		cfg.BaseURL = "https://api.openai.com/v1"
	}
	if strings.TrimSpace(cfg.Model) == "" {
		cfg.Model = "gpt-4o"
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
	// Only require API key if not local
	if requiresAPIKey(c.cfg.BaseURL) && strings.TrimSpace(c.cfg.APIKey) == "" {
		return "", fmt.Errorf("%w: missing API key for %s", llm.ErrUnavailable, c.cfg.BaseURL)
	}
	
	userText := strings.TrimSpace(input.Text)
	if userText == "" {
		return "", nil
	}

	messages := []map[string]string{}

	// 1. System Prompt
	systemPrompt := strings.TrimSpace(c.cfg.SystemPrompt)
	if strings.TrimSpace(input.SystemPrompt) != "" {
		if systemPrompt != "" {
			systemPrompt += "\n\n"
		}
		systemPrompt += strings.TrimSpace(input.SystemPrompt)
	}
	if systemPrompt != "" {
		messages = append(messages, map[string]string{
			"role":    "system",
			"content": systemPrompt,
		})
	}

	// 2. User Prompt
	// We combine metadata into the user message because standard chat APIs don't have "context" fields
	userContent := buildUserPrompt(input)
	messages = append(messages, map[string]string{
		"role":    "user",
		"content": userContent,
	})

	payload := map[string]any{
		"model":    c.cfg.Model,
		"messages": messages,
	}
	
	body, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("marshal openai request: %w", err)
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
		c.logger.Error("openai chat completion failed", "status", res.StatusCode, "body", strings.TrimSpace(string(respBody)))
		return "", fmt.Errorf("openai completion failed with status %d", res.StatusCode)
	}

	var response chatCompletionResponse
	if err := json.Unmarshal(respBody, &response); err != nil {
		return "", fmt.Errorf("decode openai response: %w", err)
	}
	if len(response.Choices) == 0 {
		return "", fmt.Errorf("openai response returned no choices")
	}
	content := strings.TrimSpace(response.Choices[0].Message.Content)
	content = sanitizeModelReply(content)
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
	// If it's a direct conversation, we can just use the text if we want to be less verbose,
	// but keeping the context header is usually helpful for the agent to know WHO it is talking to.
	header := fmt.Sprintf(
		"Context: connector=%s workspace=%s user=%s (%s)",
		input.Connector, input.WorkspaceID, input.FromUserID, input.DisplayName,
	)
	return header + "\n\n" + input.Text
}

type chatCompletionResponse struct {
	Choices []struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
	} `json:"choices"`
}

func requiresAPIKey(baseURL string) bool {
	// Heuristic: localhost/ollama usually don't need keys
	lower := strings.ToLower(baseURL)
	if strings.Contains(lower, "localhost") || strings.Contains(lower, "127.0.0.1") || strings.Contains(lower, "ollama") {
		return false
	}
	return true
}