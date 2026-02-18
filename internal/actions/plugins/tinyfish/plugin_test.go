package tinyfish

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/dwizi/agent-runtime/internal/store"
)

func TestPluginExecuteSyncSuccess(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/automation/run" {
			t.Fatalf("expected sync endpoint, got %s", r.URL.Path)
		}
		if got := r.Header.Get("X-API-Key"); got != "sk-test" {
			t.Fatalf("expected x-api-key auth, got %s", got)
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode body: %v", err)
		}
		if gotURL, _ := body["url"].(string); gotURL != "https://example.com/product" {
			t.Fatalf("expected url in body, got %q", gotURL)
		}
		goal, _ := body["goal"].(string)
		if !strings.Contains(goal, "Extract the current price") {
			t.Fatalf("expected goal in body, got %q", goal)
		}
		if !strings.Contains(goal, "https://example.com/product") {
			t.Fatalf("expected target url in goal, got %q", goal)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"result":"The current price is $42"}`))
	}))
	defer server.Close()

	plugin := New(Config{
		BaseURL: server.URL,
		APIKey:  "sk-test",
	})
	result, err := plugin.Execute(context.Background(), store.ActionApproval{
		ActionType:    "agentic_web",
		ActionTarget:  "https://example.com/product",
		ActionSummary: "Extract the current price",
	})
	if err != nil {
		t.Fatalf("execute failed: %v", err)
	}
	if result.Plugin != "tinyfish_agentic_web" {
		t.Fatalf("unexpected plugin key: %s", result.Plugin)
	}
	if !strings.Contains(result.Message, "price is $42") {
		t.Fatalf("unexpected message: %s", result.Message)
	}
}

func TestPluginExecuteAsyncSuccess(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/automation/run-async" {
			t.Fatalf("expected async endpoint, got %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte(`{"run_id":"run_123","status":"queued"}`))
	}))
	defer server.Close()

	plugin := New(Config{
		BaseURL: server.URL,
		APIKey:  "sk-test",
	})
	result, err := plugin.Execute(context.Background(), store.ActionApproval{
		ActionType:   "tinyfish_async",
		ActionTarget: "https://example.com",
		Payload: map[string]any{
			"goal": "Find latest headline on the page",
		},
	})
	if err != nil {
		t.Fatalf("execute failed: %v", err)
	}
	if !strings.Contains(result.Message, "run_123") {
		t.Fatalf("expected run id in message, got %s", result.Message)
	}
}

func TestPluginExecuteRequiresGoal(t *testing.T) {
	plugin := New(Config{
		BaseURL: "https://agent.tinyfish.ai",
		APIKey:  "sk-test",
	})
	_, err := plugin.Execute(context.Background(), store.ActionApproval{
		ActionType: "agentic_web",
	})
	if err == nil {
		t.Fatal("expected error for missing goal")
	}
	if !strings.Contains(err.Error(), "requires payload.goal") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestPluginExecuteRequiresURL(t *testing.T) {
	plugin := New(Config{
		BaseURL: "https://agent.tinyfish.ai",
		APIKey:  "sk-test",
	})
	_, err := plugin.Execute(context.Background(), store.ActionApproval{
		ActionType:    "agentic_web",
		ActionSummary: "find one headline",
	})
	if err == nil {
		t.Fatal("expected error for missing url")
	}
	if !strings.Contains(err.Error(), "requires payload.url") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestPluginExecuteParsesRemoteError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":{"message":"invalid goal"}}`))
	}))
	defer server.Close()

	plugin := New(Config{
		BaseURL: server.URL,
		APIKey:  "sk-test",
	})
	_, err := plugin.Execute(context.Background(), store.ActionApproval{
		ActionType:    "agentic_web",
		ActionTarget:  "https://example.com",
		ActionSummary: "test",
	})
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "invalid goal") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestPluginExecuteRequiresAPIKey(t *testing.T) {
	plugin := New(Config{
		BaseURL: "https://agent.tinyfish.ai",
	})
	_, err := plugin.Execute(context.Background(), store.ActionApproval{
		ActionType:    "agentic_web",
		ActionSummary: "test",
	})
	if err == nil {
		t.Fatal("expected error for missing api key")
	}
	if !strings.Contains(strings.ToLower(err.Error()), "missing api key") {
		t.Fatalf("unexpected error: %v", err)
	}
}
