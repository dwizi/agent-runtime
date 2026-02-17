package app

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/dwizi/agent-runtime/internal/config"
)

func TestCodexPublisherPublishNoop(t *testing.T) {
	publisher := newCodexPublisherFromConfig(config.Config{}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if publisher == nil {
		t.Fatal("expected codex publisher")
	}
	if err := publisher.Publish(context.Background(), "codex-session-1", "hello"); err != nil {
		t.Fatalf("publish should not fail: %v", err)
	}
}

func TestCodexPublisherPublishPostsToConfiguredEndpoint(t *testing.T) {
	var authHeader string
	var payload map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		authHeader = req.Header.Get("Authorization")
		body, _ := io.ReadAll(req.Body)
		_ = json.Unmarshal(body, &payload)
		w.WriteHeader(http.StatusAccepted)
	}))
	defer server.Close()

	publisher := newCodexPublisherFromConfig(config.Config{
		CodexPublishURL:         server.URL,
		CodexPublishBearerToken: "codex-token",
		CodexPublishTimeoutSec:  2,
	}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err := publisher.Publish(context.Background(), "session-9", "objective done"); err != nil {
		t.Fatalf("publish should succeed: %v", err)
	}
	if authHeader != "Bearer codex-token" {
		t.Fatalf("expected bearer auth header, got %q", authHeader)
	}
	if payload["connector"] != "codex" {
		t.Fatalf("expected connector codex, got %v", payload["connector"])
	}
	if payload["external_id"] != "session-9" {
		t.Fatalf("expected external id session-9, got %v", payload["external_id"])
	}
	if payload["text"] != "objective done" {
		t.Fatalf("expected text objective done, got %v", payload["text"])
	}
}

func TestCodexPublisherPublishReturnsErrorOnNon2xx(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		http.Error(w, "downstream rejected", http.StatusBadGateway)
	}))
	defer server.Close()

	publisher := newCodexPublisherFromConfig(config.Config{
		CodexPublishURL:        server.URL,
		CodexPublishTimeoutSec: 2,
	}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	err := publisher.Publish(context.Background(), "session-1", "hello")
	if err == nil {
		t.Fatal("expected publish error")
	}
	if !strings.Contains(err.Error(), "status=502") {
		t.Fatalf("expected status in error, got %v", err)
	}
}
