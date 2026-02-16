package telegram

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/dwizi/agent-runtime/internal/gateway"
	"github.com/dwizi/agent-runtime/internal/llm"
	llmsafety "github.com/dwizi/agent-runtime/internal/llm/safety"
	"github.com/dwizi/agent-runtime/internal/store"
)

type fakePairingStore struct {
	requests     []store.CreatePairingRequestInput
	contexts     []string
	workspaceID  string
	identityRole string
	actions      []store.CreateActionApprovalInput
}

func (f *fakePairingStore) CreatePairingRequest(ctx context.Context, input store.CreatePairingRequestInput) (store.PairingRequestWithToken, error) {
	f.requests = append(f.requests, input)
	now := time.Now().UTC()
	return store.PairingRequestWithToken{
		PairingRequest: store.PairingRequest{
			ID:        "pair-1",
			TokenHint: "ABCD...WXYZ",
			Connector: input.Connector,
			ExpiresAt: now.Add(10 * time.Minute),
		},
		Token: "ABCDEF123456",
	}, nil
}

func (f *fakePairingStore) EnsureContextForExternalChannel(ctx context.Context, connector, externalID, displayName string) (store.ContextRecord, error) {
	f.contexts = append(f.contexts, connector+":"+externalID)
	workspaceID := f.workspaceID
	if workspaceID == "" {
		workspaceID = "ws-1"
	}
	return store.ContextRecord{
		ID:          "ctx-1",
		WorkspaceID: workspaceID,
	}, nil
}

func (f *fakePairingStore) LookupUserIdentity(ctx context.Context, connector, connectorUserID string) (store.UserIdentity, error) {
	if strings.TrimSpace(f.identityRole) == "" {
		return store.UserIdentity{}, store.ErrIdentityNotFound
	}
	return store.UserIdentity{
		UserID: "user-1",
		Role:   f.identityRole,
	}, nil
}

func (f *fakePairingStore) CreateActionApproval(ctx context.Context, input store.CreateActionApprovalInput) (store.ActionApproval, error) {
	f.actions = append(f.actions, input)
	return store.ActionApproval{
		ID:            "act-1",
		WorkspaceID:   input.WorkspaceID,
		ContextID:     input.ContextID,
		Connector:     input.Connector,
		ExternalID:    input.ExternalID,
		ActionType:    input.ActionType,
		ActionSummary: input.ActionSummary,
		Status:        "pending",
	}, nil
}

type fakeCommandGateway struct {
	calls []gateway.MessageInput
	reply string
}

func (f *fakeCommandGateway) HandleMessage(ctx context.Context, input gateway.MessageInput) (gateway.MessageOutput, error) {
	f.calls = append(f.calls, input)
	if f.reply == "" {
		return gateway.MessageOutput{}, nil
	}
	return gateway.MessageOutput{
		Handled: true,
		Reply:   f.reply,
	}, nil
}

type fakeResponder struct {
	calls []string
	reply string
}

func (f *fakeResponder) Reply(ctx context.Context, input llm.MessageInput) (string, error) {
	f.calls = append(f.calls, input.Text)
	return f.reply, nil
}

type fakePolicy struct {
	decision llmsafety.Decision
}

func (f *fakePolicy) Check(input llmsafety.Request) llmsafety.Decision {
	if f.decision.Allowed || f.decision.Notify != "" || f.decision.Reason != "" {
		return f.decision
	}
	return llmsafety.Decision{Allowed: true}
}

func TestSyncCommandsRegistersTelegramCommands(t *testing.T) {
	var payload struct {
		Commands []struct {
			Command     string `json:"command"`
			Description string `json:"description"`
		} `json:"commands"`
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if !strings.Contains(req.URL.Path, "/setMyCommands") {
			http.NotFound(w, req)
			return
		}
		if err := json.NewDecoder(req.Body).Decode(&payload); err != nil {
			t.Fatalf("decode payload: %v", err)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true})
	}))
	defer server.Close()

	connector := New("test-token", server.URL, t.TempDir(), 1, &fakePairingStore{}, &fakeCommandGateway{}, nil, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err := connector.syncCommands(context.Background()); err != nil {
		t.Fatalf("syncCommands failed: %v", err)
	}
	if len(payload.Commands) == 0 {
		t.Fatal("expected command payload")
	}
	seenTask := false
	seenPair := false
	for _, command := range payload.Commands {
		if command.Command == "task" {
			seenTask = true
		}
		if command.Command == "pair" {
			seenPair = true
		}
	}
	if !seenTask {
		t.Fatal("expected task command in payload")
	}
	if !seenPair {
		t.Fatal("expected pair command in payload")
	}
}

func TestPollOncePairDM(t *testing.T) {
	pairings := &fakePairingStore{}
	commands := &fakeCommandGateway{}
	var sentBody string

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		switch {
		case strings.Contains(req.URL.Path, "/getUpdates"):
			_ = json.NewEncoder(w).Encode(map[string]any{
				"ok": true,
				"result": []map[string]any{
					{
						"update_id": 101,
						"message": map[string]any{
							"message_id": 1,
							"text":       "pair",
							"chat": map[string]any{
								"id":   9999,
								"type": "private",
							},
							"from": map[string]any{
								"id":         123456,
								"first_name": "Alice",
							},
						},
					},
				},
			})
		case strings.Contains(req.URL.Path, "/sendMessage"):
			bytes, _ := io.ReadAll(req.Body)
			sentBody = string(bytes)
			_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "result": map[string]any{}})
		default:
			http.NotFound(w, req)
		}
	}))
	defer server.Close()

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	connector := New("test-token", server.URL, t.TempDir(), 1, pairings, commands, nil, nil, logger)
	if err := connector.pollOnce(context.Background()); err != nil {
		t.Fatalf("pollOnce returned error: %v", err)
	}

	if len(pairings.requests) != 1 {
		t.Fatalf("expected one pairing request, got %d", len(pairings.requests))
	}
	if pairings.requests[0].Connector != "telegram" {
		t.Fatalf("expected telegram connector, got %s", pairings.requests[0].Connector)
	}
	if pairings.requests[0].ConnectorUserID != "123456" {
		t.Fatalf("expected connector user id 123456, got %s", pairings.requests[0].ConnectorUserID)
	}
	if !strings.Contains(sentBody, "ABCDEF123456") {
		t.Fatalf("expected token in sendMessage payload, got %s", sentBody)
	}
	if len(commands.calls) != 0 {
		t.Fatalf("expected no command gateway calls for pair, got %d", len(commands.calls))
	}
	logPath := filepath.Join(connector.workspace, "ws-1", "logs", "chats", "telegram", "9999.md")
	if _, err := os.Stat(logPath); err != nil {
		t.Fatalf("expected chat log file: %v", err)
	}
}

func TestPollOnceIgnoresNonPrivateChats(t *testing.T) {
	pairings := &fakePairingStore{}
	commands := &fakeCommandGateway{}
	sendMessageCalled := false

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		switch {
		case strings.Contains(req.URL.Path, "/getUpdates"):
			_ = json.NewEncoder(w).Encode(map[string]any{
				"ok": true,
				"result": []map[string]any{
					{
						"update_id": 11,
						"message": map[string]any{
							"message_id": 1,
							"text":       "pair",
							"chat": map[string]any{
								"id":   -10001,
								"type": "group",
							},
							"from": map[string]any{
								"id":         555,
								"first_name": "Bob",
							},
						},
					},
				},
			})
		case strings.Contains(req.URL.Path, "/sendMessage"):
			sendMessageCalled = true
			_ = json.NewEncoder(w).Encode(map[string]any{"ok": true})
		default:
			http.NotFound(w, req)
		}
	}))
	defer server.Close()

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	connector := New("test-token", server.URL, t.TempDir(), 1, pairings, commands, nil, nil, logger)
	if err := connector.pollOnce(context.Background()); err != nil {
		t.Fatalf("pollOnce returned error: %v", err)
	}
	if len(pairings.requests) != 0 {
		t.Fatalf("expected no pairing requests, got %d", len(pairings.requests))
	}
	if sendMessageCalled {
		t.Fatal("sendMessage should not be called for non-private chat")
	}
}

func TestPollOnceRunsCommandGateway(t *testing.T) {
	pairings := &fakePairingStore{}
	commands := &fakeCommandGateway{reply: "Task queued: `task-1`"}
	var sentBody string

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		switch {
		case strings.Contains(req.URL.Path, "/getUpdates"):
			_ = json.NewEncoder(w).Encode(map[string]any{
				"ok": true,
				"result": []map[string]any{
					{
						"update_id": 400,
						"message": map[string]any{
							"message_id": 10,
							"text":       "/task write a summary",
							"chat": map[string]any{
								"id":    42,
								"type":  "supergroup",
								"title": "ops",
							},
							"from": map[string]any{
								"id":         999,
								"first_name": "Operator",
							},
						},
					},
				},
			})
		case strings.Contains(req.URL.Path, "/sendMessage"):
			bytes, _ := io.ReadAll(req.Body)
			sentBody = string(bytes)
			_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "result": map[string]any{}})
		default:
			http.NotFound(w, req)
		}
	}))
	defer server.Close()

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	connector := New("test-token", server.URL, t.TempDir(), 1, pairings, commands, nil, nil, logger)
	if err := connector.pollOnce(context.Background()); err != nil {
		t.Fatalf("pollOnce returned error: %v", err)
	}
	if len(commands.calls) != 1 {
		t.Fatalf("expected one gateway call, got %d", len(commands.calls))
	}
	if commands.calls[0].ExternalID != "42" {
		t.Fatalf("expected chat external id 42, got %s", commands.calls[0].ExternalID)
	}
	if !strings.Contains(sentBody, "Task queued") {
		t.Fatalf("expected gateway reply to be sent, got %s", sentBody)
	}
}

func TestPollOnceIngestsMarkdownAttachment(t *testing.T) {
	workspaceRoot := t.TempDir()
	pairings := &fakePairingStore{workspaceID: "workspace-42"}
	commands := &fakeCommandGateway{}
	sendCount := 0

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		switch {
		case strings.Contains(req.URL.Path, "/getUpdates"):
			_ = json.NewEncoder(w).Encode(map[string]any{
				"ok": true,
				"result": []map[string]any{
					{
						"update_id": 700,
						"message": map[string]any{
							"message_id": 88,
							"chat": map[string]any{
								"id":    42,
								"type":  "supergroup",
								"title": "ops",
							},
							"from": map[string]any{
								"id": 999,
							},
							"document": map[string]any{
								"file_id":   "file-1",
								"file_name": "notes.md",
								"mime_type": "text/markdown",
							},
						},
					},
				},
			})
		case strings.Contains(req.URL.Path, "/getFile"):
			_ = json.NewEncoder(w).Encode(map[string]any{
				"ok": true,
				"result": map[string]any{
					"file_path": "docs/notes.md",
				},
			})
		case strings.Contains(req.URL.Path, "/file/bottest-token/"):
			_, _ = w.Write([]byte("# Ops notes"))
		case strings.Contains(req.URL.Path, "/sendMessage"):
			sendCount++
			_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "result": map[string]any{}})
		default:
			http.NotFound(w, req)
		}
	}))
	defer server.Close()

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	connector := New("test-token", server.URL, workspaceRoot, 1, pairings, commands, nil, nil, logger)
	if err := connector.pollOnce(context.Background()); err != nil {
		t.Fatalf("pollOnce returned error: %v", err)
	}

	target := filepath.Join(workspaceRoot, "workspace-42", "inbox", "telegram", "42", "88-notes.md")
	content, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("expected saved attachment at %s: %v", target, err)
	}
	if !strings.Contains(string(content), "Ops notes") {
		t.Fatalf("unexpected attachment content: %s", string(content))
	}
	if sendCount == 0 {
		t.Fatal("expected acknowledgment message for saved attachment")
	}
	logPath := filepath.Join(workspaceRoot, "workspace-42", "logs", "chats", "telegram", "42.md")
	if _, err := os.Stat(logPath); err != nil {
		t.Fatalf("expected chat log file: %v", err)
	}
}

func TestPollOnceDMUsesResponder(t *testing.T) {
	pairings := &fakePairingStore{}
	commands := &fakeCommandGateway{}
	responder := &fakeResponder{reply: "AI response"}
	var sentBody string

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		switch {
		case strings.Contains(req.URL.Path, "/getUpdates"):
			_ = json.NewEncoder(w).Encode(map[string]any{
				"ok": true,
				"result": []map[string]any{
					{
						"update_id": 801,
						"message": map[string]any{
							"message_id": 12,
							"text":       "hello there",
							"chat": map[string]any{
								"id":   10,
								"type": "private",
							},
							"from": map[string]any{
								"id": 777,
							},
						},
					},
				},
			})
		case strings.Contains(req.URL.Path, "/sendMessage"):
			bytes, _ := io.ReadAll(req.Body)
			sentBody = string(bytes)
			_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "result": map[string]any{}})
		default:
			http.NotFound(w, req)
		}
	}))
	defer server.Close()

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	connector := New("test-token", server.URL, t.TempDir(), 1, pairings, commands, responder, nil, logger)
	if err := connector.pollOnce(context.Background()); err != nil {
		t.Fatalf("pollOnce returned error: %v", err)
	}
	if len(responder.calls) != 1 {
		t.Fatalf("expected responder call, got %d", len(responder.calls))
	}
	if !strings.Contains(sentBody, "AI response") {
		t.Fatalf("expected ai response in sendMessage payload, got %s", sentBody)
	}
}

func TestPollOnceMentionUsesResponder(t *testing.T) {
	pairings := &fakePairingStore{}
	commands := &fakeCommandGateway{}
	responder := &fakeResponder{reply: "Mention response"}
	var sentBody string

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		switch {
		case strings.Contains(req.URL.Path, "/getUpdates"):
			_ = json.NewEncoder(w).Encode(map[string]any{
				"ok": true,
				"result": []map[string]any{
					{
						"update_id": 802,
						"message": map[string]any{
							"message_id": 13,
							"text":       "@spinnerbot review this",
							"chat": map[string]any{
								"id":    11,
								"type":  "group",
								"title": "ops",
							},
							"from": map[string]any{
								"id": 888,
							},
						},
					},
				},
			})
		case strings.Contains(req.URL.Path, "/sendMessage"):
			bytes, _ := io.ReadAll(req.Body)
			sentBody = string(bytes)
			_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "result": map[string]any{}})
		default:
			http.NotFound(w, req)
		}
	}))
	defer server.Close()

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	connector := New("test-token", server.URL, t.TempDir(), 1, pairings, commands, responder, nil, logger)
	connector.botUsername = "spinnerbot"
	if err := connector.pollOnce(context.Background()); err != nil {
		t.Fatalf("pollOnce returned error: %v", err)
	}
	if len(responder.calls) != 1 {
		t.Fatalf("expected responder call, got %d", len(responder.calls))
	}
	if !strings.Contains(sentBody, "Mention response") {
		t.Fatalf("expected mention response in sendMessage payload, got %s", sentBody)
	}
}

func TestPollOncePolicyRateLimitNotice(t *testing.T) {
	pairings := &fakePairingStore{}
	commands := &fakeCommandGateway{}
	responder := &fakeResponder{reply: "should-not-send"}
	policy := &fakePolicy{
		decision: llmsafety.Decision{
			Allowed: false,
			Notify:  "Rate limit reached for non-admin users. Try again shortly.",
			Reason:  "rate_limited",
		},
	}
	var sentBody string

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		switch {
		case strings.Contains(req.URL.Path, "/getUpdates"):
			_ = json.NewEncoder(w).Encode(map[string]any{
				"ok": true,
				"result": []map[string]any{
					{
						"update_id": 900,
						"message": map[string]any{
							"message_id": 14,
							"text":       "hello there",
							"chat": map[string]any{
								"id":   12,
								"type": "private",
							},
							"from": map[string]any{
								"id": 1010,
							},
						},
					},
				},
			})
		case strings.Contains(req.URL.Path, "/sendMessage"):
			bytes, _ := io.ReadAll(req.Body)
			sentBody = string(bytes)
			_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "result": map[string]any{}})
		default:
			http.NotFound(w, req)
		}
	}))
	defer server.Close()

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	connector := New("test-token", server.URL, t.TempDir(), 1, pairings, commands, responder, policy, logger)
	if err := connector.pollOnce(context.Background()); err != nil {
		t.Fatalf("pollOnce returned error: %v", err)
	}
	if len(responder.calls) != 0 {
		t.Fatalf("expected no responder calls under rate limit, got %d", len(responder.calls))
	}
	if !strings.Contains(sentBody, "Rate limit reached") {
		t.Fatalf("expected rate limit notice in response, got %s", sentBody)
	}
}

func TestPollOnceLLMActionProposalQueuesApproval(t *testing.T) {
	pairings := &fakePairingStore{}
	commands := &fakeCommandGateway{}
	responder := &fakeResponder{
		reply: "I can do that.\n\n```action\n{\"type\":\"send_email\",\"target\":\"ops@example.com\",\"summary\":\"Send update\",\"subject\":\"Status\"}\n```",
	}
	var sentBody string

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		switch {
		case strings.Contains(req.URL.Path, "/getUpdates"):
			_ = json.NewEncoder(w).Encode(map[string]any{
				"ok": true,
				"result": []map[string]any{
					{
						"update_id": 901,
						"message": map[string]any{
							"message_id": 15,
							"text":       "please email the team",
							"chat": map[string]any{
								"id":   15,
								"type": "private",
							},
							"from": map[string]any{
								"id": 2020,
							},
						},
					},
				},
			})
		case strings.Contains(req.URL.Path, "/sendMessage"):
			bytes, _ := io.ReadAll(req.Body)
			sentBody = string(bytes)
			_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "result": map[string]any{}})
		default:
			http.NotFound(w, req)
		}
	}))
	defer server.Close()

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	connector := New("test-token", server.URL, t.TempDir(), 1, pairings, commands, responder, nil, logger)
	if err := connector.pollOnce(context.Background()); err != nil {
		t.Fatalf("pollOnce returned error: %v", err)
	}
	if len(pairings.actions) != 1 {
		t.Fatalf("expected one action approval queued, got %d", len(pairings.actions))
	}
	if pairings.actions[0].ActionType != "send_email" {
		t.Fatalf("unexpected action type: %s", pairings.actions[0].ActionType)
	}
	if !strings.Contains(sentBody, "Admin approval required.") {
		t.Fatalf("expected compact approval notice in response, got %s", sentBody)
	}
	if !strings.Contains(sentBody, "'act-1'") {
		t.Fatalf("expected action id in compact notice, got %s", sentBody)
	}
}

func TestSendMessageIncludesTelegramErrorDetails(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"ok":          false,
			"error_code":  400,
			"description": "Bad Request: chat not found",
		})
	}))
	defer server.Close()

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	connector := New("test-token", server.URL, t.TempDir(), 1, nil, nil, nil, nil, logger)

	err := connector.sendMessage(context.Background(), 99, "hello")
	if err == nil {
		t.Fatal("expected sendMessage to fail")
	}
	if !strings.Contains(err.Error(), "error_code=400") {
		t.Fatalf("expected telegram error code in message, got %v", err)
	}
	if !strings.Contains(strings.ToLower(err.Error()), "chat not found") {
		t.Fatalf("expected telegram description in message, got %v", err)
	}
}
