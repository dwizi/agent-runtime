package adminclient

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/dwizi/agent-runtime/internal/config"
)

func TestClientStartPairing(t *testing.T) {
	t.Parallel()

	type requestPayload struct {
		Connector       string `json:"connector"`
		ConnectorUserID string `json:"connector_user_id"`
		DisplayName     string `json:"display_name"`
		ExpiresInSec    int    `json:"expires_in_sec"`
	}

	var got requestPayload
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Fatalf("expected POST, got %s", r.Method)
		}
		if r.URL.Path != "/api/v1/pairings/start" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Fatalf("decode body: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"pair-1","token":"ABCDEFGH","token_hint":"ABCD...EFGH","connector":"codex","connector_user_id":"codex-user","display_name":"Codex CLI","status":"pending","expires_at_unix":1730000000}`))
	}))
	defer server.Close()

	client := &Client{baseURL: server.URL, http: server.Client()}
	response, err := client.StartPairing(context.Background(), " codex ", " codex-user ", " Codex CLI ", 900)
	if err != nil {
		t.Fatalf("start pairing: %v", err)
	}

	if got.Connector != "codex" || got.ConnectorUserID != "codex-user" || got.DisplayName != "Codex CLI" {
		t.Fatalf("unexpected request payload: %+v", got)
	}
	if got.ExpiresInSec != 900 {
		t.Fatalf("expected expires_in_sec=900, got %d", got.ExpiresInSec)
	}
	if response.Token != "ABCDEFGH" {
		t.Fatalf("unexpected token: %s", response.Token)
	}
	if response.Connector != "codex" || response.ConnectorUserID != "codex-user" {
		t.Fatalf("unexpected response payload: %+v", response)
	}
}

func TestClientWithTimeoutClonesClient(t *testing.T) {
	t.Parallel()

	base := &Client{
		baseURL: "https://example.com",
		http:    &http.Client{Timeout: 15 * time.Second},
	}
	updated := base.WithTimeout(3 * time.Second)
	if updated == nil {
		t.Fatal("expected updated client")
	}
	if updated == base {
		t.Fatal("expected timeout update to clone client")
	}
	if updated.http == base.http {
		t.Fatal("expected timeout update to clone http client")
	}
	if updated.http.Timeout != 3*time.Second {
		t.Fatalf("expected timeout 3s, got %s", updated.http.Timeout)
	}
	if base.http.Timeout != 15*time.Second {
		t.Fatalf("expected original timeout unchanged, got %s", base.http.Timeout)
	}
}

func TestNewRespectsAdminHTTPTimeoutConfig(t *testing.T) {
	t.Parallel()

	client, err := New(config.Config{
		AdminAPIURL:         "https://example.com",
		AdminHTTPTimeoutSec: 42,
	})
	if err != nil {
		t.Fatalf("new client: %v", err)
	}
	if client.http.Timeout != 42*time.Second {
		t.Fatalf("expected timeout 42s, got %s", client.http.Timeout)
	}
}
