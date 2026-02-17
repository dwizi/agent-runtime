package app

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

	"github.com/dwizi/agent-runtime/internal/config"
	"github.com/dwizi/agent-runtime/internal/connectors"
)

type codexPublisher struct {
	endpoint    string
	bearerToken string
	httpClient  *http.Client
	logger      *slog.Logger
}

func newCodexPublisher() connectors.Publisher {
	return codexPublisher{
		logger: slog.Default(),
	}
}

func newCodexPublisherFromConfig(cfg config.Config, logger *slog.Logger) connectors.Publisher {
	timeout := time.Duration(cfg.CodexPublishTimeoutSec) * time.Second
	if timeout <= 0 {
		timeout = 8 * time.Second
	}
	if logger == nil {
		logger = slog.Default()
	}
	return codexPublisher{
		endpoint:    strings.TrimSpace(cfg.CodexPublishURL),
		bearerToken: strings.TrimSpace(cfg.CodexPublishBearerToken),
		httpClient:  &http.Client{Timeout: timeout},
		logger:      logger,
	}
}

func (p codexPublisher) Publish(ctx context.Context, externalID, text string) error {
	trimmedText := strings.TrimSpace(text)
	if trimmedText == "" {
		return nil
	}
	endpoint := strings.TrimSpace(p.endpoint)
	if endpoint == "" {
		// No callback configured; notifier still persists outbound chat logs.
		return nil
	}
	payload := map[string]any{
		"connector":   "codex",
		"external_id": strings.TrimSpace(externalID),
		"text":        trimmedText,
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal codex publish payload: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	if token := strings.TrimSpace(p.bearerToken); token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	client := p.httpClient
	if client == nil {
		client = &http.Client{Timeout: 8 * time.Second}
	}
	res, err := client.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		bodyBytes, _ := io.ReadAll(io.LimitReader(res.Body, 1024))
		return fmt.Errorf("codex publish failed: status=%d body=%s", res.StatusCode, strings.TrimSpace(string(bodyBytes)))
	}
	return nil
}
