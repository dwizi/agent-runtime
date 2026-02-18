package tinyfish

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/dwizi/agent-runtime/internal/actions/executor"
	"github.com/dwizi/agent-runtime/internal/store"
)

const (
	defaultBaseURL     = "https://agent.tinyfish.ai"
	defaultTimeout     = 90 * time.Second
	maxResponseBodyLen = 128 * 1024
)

type Config struct {
	BaseURL string
	APIKey  string
	Timeout time.Duration
}

type Plugin struct {
	baseURL string
	apiKey  string
	client  *http.Client
}

func New(cfg Config) *Plugin {
	baseURL := strings.TrimSpace(cfg.BaseURL)
	if baseURL == "" {
		baseURL = defaultBaseURL
	}
	baseURL = strings.TrimRight(baseURL, "/")
	timeout := cfg.Timeout
	if timeout <= 0 {
		timeout = defaultTimeout
	}
	return &Plugin{
		baseURL: baseURL,
		apiKey:  strings.TrimSpace(cfg.APIKey),
		client:  &http.Client{Timeout: timeout},
	}
}

func (p *Plugin) PluginKey() string {
	return "tinyfish_agentic_web"
}

func (p *Plugin) ActionTypes() []string {
	return []string{"agentic_web", "tinyfish_sync", "tinyfish_async"}
}

func (p *Plugin) Execute(ctx context.Context, approval store.ActionApproval) (executor.Result, error) {
	if p == nil {
		return executor.Result{}, fmt.Errorf("tinyfish plugin is not configured")
	}
	if strings.TrimSpace(p.apiKey) == "" {
		return executor.Result{}, fmt.Errorf("tinyfish plugin is not configured: missing api key")
	}
	if p.client == nil {
		p.client = &http.Client{Timeout: defaultTimeout}
	}

	async := isAsyncAction(approval)
	endpoint := "/v1/automation/run"
	if async {
		endpoint = "/v1/automation/run-async"
	}

	body, err := buildRequestBody(approval)
	if err != nil {
		return executor.Result{}, err
	}

	encoded, err := json.Marshal(body)
	if err != nil {
		return executor.Result{}, fmt.Errorf("encode tinyfish request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.baseURL+endpoint, bytes.NewReader(encoded))
	if err != nil {
		return executor.Result{}, fmt.Errorf("build tinyfish request: %w", err)
	}
	req.Header.Set("X-API-Key", p.apiKey)
	req.Header.Set("Content-Type", "application/json")

	res, err := p.client.Do(req)
	if err != nil {
		return executor.Result{}, fmt.Errorf("tinyfish request failed: %w", err)
	}
	defer res.Body.Close()

	rawBody, _ := io.ReadAll(io.LimitReader(res.Body, maxResponseBodyLen))
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return executor.Result{}, fmt.Errorf("tinyfish request failed: status=%d message=%s", res.StatusCode, parseErrorMessage(rawBody))
	}

	return executor.Result{
		Plugin:  p.PluginKey(),
		Message: summarizeResponse(async, rawBody),
	}, nil
}

func buildRequestBody(approval store.ActionApproval) (map[string]any, error) {
	payload := approval.Payload
	if payload == nil {
		payload = map[string]any{}
	}

	if rawRequest, ok := getPayloadValue(payload, "request"); ok {
		if request, ok := rawRequest.(map[string]any); ok {
			goal := strings.TrimSpace(getMapString(request, "goal"))
			if goal == "" {
				goal = resolveGoal(approval)
				if goal == "" {
					return nil, fmt.Errorf("tinyfish action requires payload.goal, payload.task, or summary")
				}
				request["goal"] = goal
			}
			url := strings.TrimSpace(getMapString(request, "url"))
			if url == "" {
				url = resolveURL(approval)
				if url == "" {
					return nil, fmt.Errorf("tinyfish action requires payload.url, payload.request.url, or action_target")
				}
				request["url"] = url
			}
			return request, nil
		}
	}

	goal := resolveGoal(approval)
	if goal == "" {
		return nil, fmt.Errorf("tinyfish action requires payload.goal, payload.task, or summary")
	}
	url := resolveURL(approval)
	if url == "" {
		return nil, fmt.Errorf("tinyfish action requires payload.url, payload.request.url, or action_target")
	}
	return map[string]any{
		"url":  url,
		"goal": goal,
	}, nil
}

func resolveGoal(approval store.ActionApproval) string {
	payload := approval.Payload
	goal := getString(payload, "goal")
	if goal == "" {
		goal = getString(payload, "task")
	}
	if goal == "" {
		goal = strings.TrimSpace(approval.ActionSummary)
	}
	target := strings.TrimSpace(approval.ActionTarget)
	if target != "" && goal != "" && !strings.Contains(strings.ToLower(goal), strings.ToLower(target)) {
		goal = goal + "\nTarget URL: " + target
	}
	return strings.TrimSpace(goal)
}

func resolveURL(approval store.ActionApproval) string {
	payload := approval.Payload
	if rawRequest, ok := getPayloadValue(payload, "request"); ok {
		if request, ok := rawRequest.(map[string]any); ok {
			if url := getMapString(request, "url"); url != "" {
				return strings.TrimSpace(url)
			}
		}
	}
	if url := getString(payload, "url"); url != "" {
		return strings.TrimSpace(url)
	}
	return strings.TrimSpace(approval.ActionTarget)
}

func isAsyncAction(approval store.ActionApproval) bool {
	actionType := strings.ToLower(strings.TrimSpace(approval.ActionType))
	if actionType == "tinyfish_async" {
		return true
	}
	if getBool(approval.Payload, "async") {
		return true
	}
	return strings.EqualFold(getString(approval.Payload, "mode"), "async")
}

func summarizeResponse(async bool, raw []byte) string {
	text := strings.TrimSpace(string(raw))
	if text == "" {
		if async {
			return "tinyfish run queued"
		}
		return "tinyfish run completed"
	}

	decoded := map[string]any{}
	if err := json.Unmarshal(raw, &decoded); err != nil {
		return "tinyfish request completed: " + compact(text)
	}

	runID := firstNonEmpty(
		getMapString(decoded, "run_id"),
		getNestedMapString(decoded, "data", "run_id"),
		getNestedMapString(decoded, "run", "id"),
	)
	if async {
		if runID != "" {
			return "tinyfish run queued with run_id " + runID
		}
		return "tinyfish run queued"
	}

	output := firstNonEmpty(
		getMapString(decoded, "result"),
		getMapString(decoded, "output"),
		getMapString(decoded, "message"),
		getNestedMapString(decoded, "data", "result"),
		getNestedMapString(decoded, "data", "output"),
	)
	if output != "" {
		if runID != "" {
			return "tinyfish run completed (" + runID + "): " + compact(output)
		}
		return "tinyfish run completed: " + compact(output)
	}
	if runID != "" {
		return "tinyfish run completed with run_id " + runID
	}
	return "tinyfish run completed"
}

func parseErrorMessage(raw []byte) string {
	text := strings.TrimSpace(string(raw))
	if text == "" {
		return "no response body"
	}

	decoded := map[string]any{}
	if err := json.Unmarshal(raw, &decoded); err != nil {
		return compact(text)
	}

	errorValue, ok := decoded["error"]
	if ok {
		switch casted := errorValue.(type) {
		case string:
			if strings.TrimSpace(casted) != "" {
				return compact(casted)
			}
		case map[string]any:
			if msg := getMapString(casted, "message"); msg != "" {
				return compact(msg)
			}
		}
	}

	return compact(firstNonEmpty(
		getMapString(decoded, "message"),
		getMapString(decoded, "detail"),
		text,
	))
}

func getPayloadValue(payload map[string]any, key string) (any, bool) {
	if payload == nil {
		return nil, false
	}
	if value, ok := payload[key]; ok {
		return value, true
	}
	nestedRaw, ok := payload["payload"]
	if !ok || nestedRaw == nil {
		return nil, false
	}
	nested, ok := nestedRaw.(map[string]any)
	if !ok {
		return nil, false
	}
	value, ok := nested[key]
	return value, ok
}

func getString(payload map[string]any, key string) string {
	value, ok := getPayloadValue(payload, key)
	if !ok || value == nil {
		return ""
	}
	switch casted := value.(type) {
	case string:
		return strings.TrimSpace(casted)
	default:
		return strings.TrimSpace(fmt.Sprintf("%v", value))
	}
}

func getBool(payload map[string]any, key string) bool {
	value, ok := getPayloadValue(payload, key)
	if !ok || value == nil {
		return false
	}
	switch casted := value.(type) {
	case bool:
		return casted
	case string:
		return strings.EqualFold(strings.TrimSpace(casted), "true")
	default:
		return false
	}
}

func getMapString(values map[string]any, key string) string {
	if values == nil {
		return ""
	}
	value, ok := values[key]
	if !ok || value == nil {
		return ""
	}
	switch casted := value.(type) {
	case string:
		return strings.TrimSpace(casted)
	default:
		return strings.TrimSpace(fmt.Sprintf("%v", casted))
	}
}

func getNestedMapString(values map[string]any, parentKey, childKey string) string {
	if values == nil {
		return ""
	}
	parentRaw, ok := values[parentKey]
	if !ok || parentRaw == nil {
		return ""
	}
	parent, ok := parentRaw.(map[string]any)
	if !ok {
		return ""
	}
	return getMapString(parent, childKey)
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			return value
		}
	}
	return ""
}

func compact(value string) string {
	text := strings.TrimSpace(value)
	text = strings.Join(strings.Fields(text), " ")
	if len(text) <= 300 {
		return text
	}
	return text[:300] + "..."
}

func resolveAPIKey(rawKey, envName string) string {
	key := strings.TrimSpace(rawKey)
	if key != "" {
		return key
	}
	envName = strings.TrimSpace(envName)
	if envName == "" {
		return ""
	}
	return strings.TrimSpace(os.Getenv(envName))
}

func NewFromExternalConfig(baseURL, rawKey, envName string, timeoutSec int) *Plugin {
	timeout := defaultTimeout
	if timeoutSec > 0 {
		timeout = time.Duration(timeoutSec) * time.Second
	}
	return New(Config{
		BaseURL: baseURL,
		APIKey:  resolveAPIKey(rawKey, envName),
		Timeout: timeout,
	})
}
