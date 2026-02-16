package webhook

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/dwizi/agent-runtime/internal/actions/executor"
	"github.com/dwizi/agent-runtime/internal/store"
)

type Plugin struct {
	client *http.Client
}

func New(timeout time.Duration) *Plugin {
	if timeout <= 0 {
		timeout = 15 * time.Second
	}
	return &Plugin{
		client: &http.Client{Timeout: timeout},
	}
}

func (p *Plugin) PluginKey() string {
	return "webhook"
}

func (p *Plugin) ActionTypes() []string {
	return []string{"http_request", "webhook"}
}

func (p *Plugin) Execute(ctx context.Context, approval store.ActionApproval) (executor.Result, error) {
	if p.client == nil {
		p.client = &http.Client{Timeout: 15 * time.Second}
	}
	method := strings.ToUpper(getString(approval.Payload, "method"))
	if method == "" {
		method = "POST"
	}
	url := strings.TrimSpace(approval.ActionTarget)
	if url == "" {
		url = getString(approval.Payload, "url")
	}
	if url == "" {
		return executor.Result{}, fmt.Errorf("webhook action requires target url")
	}
	if !strings.HasPrefix(strings.ToLower(url), "http://") && !strings.HasPrefix(strings.ToLower(url), "https://") {
		return executor.Result{}, fmt.Errorf("unsupported webhook url scheme")
	}

	bodyBytes, err := resolveBody(approval.Payload)
	if err != nil {
		return executor.Result{}, err
	}
	req, err := http.NewRequestWithContext(ctx, method, url, bytes.NewReader(bodyBytes))
	if err != nil {
		return executor.Result{}, err
	}

	headers := getMap(approval.Payload, "headers")
	for key, value := range headers {
		req.Header.Set(key, value)
	}
	if len(bodyBytes) > 0 && req.Header.Get("Content-Type") == "" {
		req.Header.Set("Content-Type", "application/json")
	}

	res, err := p.client.Do(req)
	if err != nil {
		return executor.Result{}, err
	}
	defer res.Body.Close()

	responseBody, _ := io.ReadAll(io.LimitReader(res.Body, 1024))
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return executor.Result{}, fmt.Errorf("webhook request failed: status=%d body=%s", res.StatusCode, strings.TrimSpace(string(responseBody)))
	}
	message := fmt.Sprintf("webhook request completed with status %d", res.StatusCode)
	return executor.Result{
		Plugin:  p.PluginKey(),
		Message: message,
	}, nil
}

func resolveBody(payload map[string]any) ([]byte, error) {
	if payload == nil {
		return nil, nil
	}
	if rawBody, ok := payload["body"]; ok {
		body := strings.TrimSpace(fmt.Sprintf("%v", rawBody))
		if body != "" {
			return []byte(body), nil
		}
	}
	if jsonBody, ok := payload["json"]; ok {
		encoded, err := json.Marshal(jsonBody)
		if err != nil {
			return nil, fmt.Errorf("encode webhook json body: %w", err)
		}
		return encoded, nil
	}
	return nil, nil
}

func getString(payload map[string]any, key string) string {
	if payload == nil {
		return ""
	}
	value, ok := payload[key]
	if !ok || value == nil {
		return ""
	}
	text, ok := value.(string)
	if ok {
		return strings.TrimSpace(text)
	}
	return strings.TrimSpace(fmt.Sprintf("%v", value))
}

func getMap(payload map[string]any, key string) map[string]string {
	result := map[string]string{}
	if payload == nil {
		return result
	}
	value, ok := payload[key]
	if !ok || value == nil {
		return result
	}
	switch casted := value.(type) {
	case map[string]any:
		for headerKey, headerValue := range casted {
			name := strings.TrimSpace(headerKey)
			val := strings.TrimSpace(fmt.Sprintf("%v", headerValue))
			if name != "" && val != "" {
				result[name] = val
			}
		}
	case map[string]string:
		for headerKey, headerValue := range casted {
			name := strings.TrimSpace(headerKey)
			val := strings.TrimSpace(headerValue)
			if name != "" && val != "" {
				result[name] = val
			}
		}
	}
	return result
}
