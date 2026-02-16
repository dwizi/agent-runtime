package webhook

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/dwizi/agent-runtime/internal/store"
)

func TestPluginExecuteSuccess(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Fatalf("expected POST method, got %s", r.Method)
		}
		if got := r.Header.Get("X-Test"); got != "yes" {
			t.Fatalf("expected X-Test header, got %s", got)
		}
		w.WriteHeader(http.StatusCreated)
	}))
	defer server.Close()

	plugin := New(5 * time.Second)
	result, err := plugin.Execute(context.Background(), store.ActionApproval{
		ActionType:   "http_request",
		ActionTarget: server.URL,
		Payload: map[string]any{
			"headers": map[string]any{"X-Test": "yes"},
			"json":    map[string]any{"ok": true},
		},
	})
	if err != nil {
		t.Fatalf("execute failed: %v", err)
	}
	if result.Plugin != "webhook" {
		t.Fatalf("unexpected plugin key: %s", result.Plugin)
	}
	if !strings.Contains(result.Message, "status 201") {
		t.Fatalf("unexpected result message: %s", result.Message)
	}
}

func TestPluginExecuteFailureStatus(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "nope", http.StatusBadGateway)
	}))
	defer server.Close()

	plugin := New(5 * time.Second)
	_, err := plugin.Execute(context.Background(), store.ActionApproval{
		ActionType:   "webhook",
		ActionTarget: server.URL,
	})
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "status=502") {
		t.Fatalf("unexpected error: %v", err)
	}
}
