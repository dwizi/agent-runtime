package zai

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/carlos/spinner/internal/llm"
)

func TestReplySuccess(t *testing.T) {
	var receivedAuth string
	var receivedModel string
	var receivedUserPrompt string
	var receivedSystemPrompt string

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		receivedAuth = req.Header.Get("Authorization")
		var body struct {
			Model    string `json:"model"`
			Messages []struct {
				Role    string `json:"role"`
				Content string `json:"content"`
			} `json:"messages"`
		}
		if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		receivedModel = body.Model
		if len(body.Messages) > 1 {
			receivedUserPrompt = body.Messages[1].Content
		}
		if len(body.Messages) > 0 {
			receivedSystemPrompt = body.Messages[0].Content
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{
				{
					"message": map[string]any{"content": "hello from glm"},
				},
			},
		})
	}))
	defer server.Close()

	client := New(Config{
		APIKey:  "secret",
		BaseURL: server.URL,
		Model:   "glm-4.7-flash",
	}, slog.New(slog.NewTextHandler(io.Discard, nil)))

	reply, err := client.Reply(context.Background(), llm.MessageInput{
		Connector:    "telegram",
		WorkspaceID:  "ws-1",
		ContextID:    "ctx-1",
		ExternalID:   "42",
		DisplayName:  "ops",
		FromUserID:   "u1",
		Text:         "summarize this",
		IsDM:         true,
		SystemPrompt: "Context override prompt",
	})
	if err != nil {
		t.Fatalf("reply failed: %v", err)
	}
	if reply != "hello from glm" {
		t.Fatalf("unexpected reply: %s", reply)
	}
	if receivedAuth != "Bearer secret" {
		t.Fatalf("expected auth bearer, got %s", receivedAuth)
	}
	if receivedModel != "glm-4.7-flash" {
		t.Fatalf("unexpected model: %s", receivedModel)
	}
	if !strings.Contains(receivedUserPrompt, "User message") {
		t.Fatalf("expected user prompt in payload, got %s", receivedUserPrompt)
	}
	if !strings.Contains(receivedSystemPrompt, "Context override prompt") {
		t.Fatalf("expected system prompt override, got %s", receivedSystemPrompt)
	}
}

func TestReplyUnavailableWithoutAPIKey(t *testing.T) {
	client := New(Config{}, nil)
	_, err := client.Reply(context.Background(), llm.MessageInput{
		Text: "hello",
	})
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "missing SPINNER_ZAI_API_KEY") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestReplyLocalEndpointWithoutAPIKey(t *testing.T) {
	var receivedAuth string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		receivedAuth = req.Header.Get("Authorization")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{
				{
					"message": map[string]any{"content": "hello from local qwen"},
				},
			},
		})
	}))
	defer server.Close()

	client := New(Config{
		BaseURL: server.URL,
		Model:   "qwen2.5:7b-instruct",
	}, slog.New(slog.NewTextHandler(io.Discard, nil)))

	reply, err := client.Reply(context.Background(), llm.MessageInput{
		Text: "hello",
	})
	if err != nil {
		t.Fatalf("reply failed: %v", err)
	}
	if reply != "hello from local qwen" {
		t.Fatalf("unexpected reply: %s", reply)
	}
	if strings.TrimSpace(receivedAuth) != "" {
		t.Fatalf("expected no authorization header for local endpoint, got %q", receivedAuth)
	}
}

func TestReplyStripsThinkBlocks(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{
				{
					"message": map[string]any{
						"content": "<think>\ninternal reasoning\n</think>\n\nHey there! What's up?",
					},
				},
			},
		})
	}))
	defer server.Close()

	client := New(Config{
		BaseURL: server.URL,
		Model:   "qwen2.5:7b-instruct",
	}, slog.New(slog.NewTextHandler(io.Discard, nil)))

	reply, err := client.Reply(context.Background(), llm.MessageInput{
		Text: "hello",
	})
	if err != nil {
		t.Fatalf("reply failed: %v", err)
	}
	if strings.Contains(strings.ToLower(reply), "<think>") {
		t.Fatalf("expected think block stripped, got %q", reply)
	}
	if reply != "Hey there! What's up?" {
		t.Fatalf("unexpected sanitized reply: %q", reply)
	}
}
