package discord

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

	"github.com/carlos/spinner/internal/gateway"
	"github.com/carlos/spinner/internal/llm"
	llmsafety "github.com/carlos/spinner/internal/llm/safety"
	"github.com/carlos/spinner/internal/store"
)

type fakePairingStore struct {
	requests     []store.CreatePairingRequestInput
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
		Token: "PAIRDISCORD123",
	}, nil
}

func (f *fakePairingStore) EnsureContextForExternalChannel(ctx context.Context, connector, externalID, displayName string) (store.ContextRecord, error) {
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
	return gateway.MessageOutput{Handled: true, Reply: f.reply}, nil
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

func TestHandleMessageCreatePairDM(t *testing.T) {
	pairings := &fakePairingStore{}
	commands := &fakeCommandGateway{}
	var sentBody string
	var sentAuth string

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		sentAuth = req.Header.Get("Authorization")
		bytes, _ := io.ReadAll(req.Body)
		sentBody = string(bytes)
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]any{"id": "msg-1"})
	}))
	defer server.Close()

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	connector := New("bot-token", server.URL, "wss://discord.test/ws", t.TempDir(), pairings, commands, nil, nil, logger)
	err := connector.handleMessageCreate(context.Background(), discordMessageCreate{
		ChannelID: "123",
		Content:   "pair",
		Author: discordAuthor{
			ID:       "user-1",
			Username: "alice",
		},
	})
	if err != nil {
		t.Fatalf("handleMessageCreate failed: %v", err)
	}
	if len(pairings.requests) != 1 {
		t.Fatalf("expected one pairing request, got %d", len(pairings.requests))
	}
	if !strings.Contains(sentBody, "PAIRDISCORD123") {
		t.Fatalf("expected pairing token in reply body, got %s", sentBody)
	}
	if sentAuth != "Bot bot-token" {
		t.Fatalf("expected bot auth header, got %s", sentAuth)
	}
	if len(commands.calls) != 0 {
		t.Fatalf("expected no command gateway calls, got %d", len(commands.calls))
	}
	logPath := filepath.Join(connector.workspace, "ws-1", "logs", "chats", "discord", "123.md")
	if _, err := os.Stat(logPath); err != nil {
		t.Fatalf("expected chat log file: %v", err)
	}
}

func TestHandleMessageCreateRunsGateway(t *testing.T) {
	pairings := &fakePairingStore{}
	commands := &fakeCommandGateway{reply: "Task queued: `abc`"}
	var sentBody string

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		bytes, _ := io.ReadAll(req.Body)
		sentBody = string(bytes)
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]any{"id": "msg-2"})
	}))
	defer server.Close()

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	connector := New("bot-token", server.URL, "wss://discord.test/ws", t.TempDir(), pairings, commands, nil, nil, logger)
	err := connector.handleMessageCreate(context.Background(), discordMessageCreate{
		ChannelID: "chan-1",
		GuildID:   "guild-1",
		Content:   "/task write report",
		Author: discordAuthor{
			ID:       "user-2",
			Username: "operator",
		},
	})
	if err != nil {
		t.Fatalf("handleMessageCreate failed: %v", err)
	}
	if len(commands.calls) != 1 {
		t.Fatalf("expected one gateway call, got %d", len(commands.calls))
	}
	if commands.calls[0].ExternalID != "chan-1" {
		t.Fatalf("expected external id chan-1, got %s", commands.calls[0].ExternalID)
	}
	if !strings.Contains(sentBody, "Task queued") {
		t.Fatalf("expected command reply in body, got %s", sentBody)
	}
}

func TestHandleMessageCreateIgnoresBotMessages(t *testing.T) {
	connector := New("bot-token", "https://discord.test/api/v10", "wss://discord.test/ws", t.TempDir(), &fakePairingStore{}, &fakeCommandGateway{}, nil, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	err := connector.handleMessageCreate(context.Background(), discordMessageCreate{
		ChannelID: "chan-1",
		Content:   "/task anything",
		Author: discordAuthor{
			ID:  "bot-id",
			Bot: true,
		},
	})
	if err != nil {
		t.Fatalf("expected bot message to be ignored, got %v", err)
	}
}

func TestHandleMessageCreateIngestsMarkdownAttachment(t *testing.T) {
	workspaceRoot := t.TempDir()
	pairings := &fakePairingStore{workspaceID: "workspace-77"}
	commands := &fakeCommandGateway{}
	sendCalls := 0

	attachmentServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.URL.Path == "/a/notes.md" {
			_, _ = w.Write([]byte("# Daily brief"))
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer attachmentServer.Close()

	apiServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		sendCalls++
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]any{"id": "msg-3"})
	}))
	defer apiServer.Close()

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	connector := New("bot-token", apiServer.URL, "wss://discord.test/ws", workspaceRoot, pairings, commands, nil, nil, logger)
	err := connector.handleMessageCreate(context.Background(), discordMessageCreate{
		ID:        "mid-9",
		ChannelID: "chan-9",
		GuildID:   "guild-9",
		Author: discordAuthor{
			ID: "user-7",
		},
		Attachments: []discordAttachment{
			{
				ID:          "att-1",
				Filename:    "notes.md",
				ContentType: "text/markdown",
				URL:         attachmentServer.URL + "/a/notes.md",
			},
		},
	})
	if err != nil {
		t.Fatalf("handleMessageCreate failed: %v", err)
	}

	target := filepath.Join(workspaceRoot, "workspace-77", "inbox", "discord", "chan-9", "mid-9-notes.md")
	content, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("expected saved attachment at %s: %v", target, err)
	}
	if !strings.Contains(string(content), "Daily brief") {
		t.Fatalf("unexpected attachment content: %s", string(content))
	}
	if sendCalls == 0 {
		t.Fatal("expected connector to send acknowledgment message")
	}
	logPath := filepath.Join(workspaceRoot, "workspace-77", "logs", "chats", "discord", "chan-9.md")
	if _, err := os.Stat(logPath); err != nil {
		t.Fatalf("expected chat log file: %v", err)
	}
}

func TestHandleMessageCreateDMUsesResponder(t *testing.T) {
	pairings := &fakePairingStore{}
	commands := &fakeCommandGateway{}
	responder := &fakeResponder{reply: "AI DM response"}
	var sentBody string

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		bytes, _ := io.ReadAll(req.Body)
		sentBody = string(bytes)
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]any{"id": "msg-4"})
	}))
	defer server.Close()

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	connector := New("bot-token", server.URL, "wss://discord.test/ws", t.TempDir(), pairings, commands, responder, nil, logger)
	err := connector.handleMessageCreate(context.Background(), discordMessageCreate{
		ID:        "mid-dm",
		ChannelID: "dm-1",
		GuildID:   "",
		Content:   "hello bot",
		Author: discordAuthor{
			ID: "user-dm",
		},
	})
	if err != nil {
		t.Fatalf("handleMessageCreate failed: %v", err)
	}
	if len(responder.calls) != 1 {
		t.Fatalf("expected responder call, got %d", len(responder.calls))
	}
	if !strings.Contains(sentBody, "AI DM response") {
		t.Fatalf("expected ai response in body, got %s", sentBody)
	}
}

func TestHandleMessageCreateMentionUsesResponder(t *testing.T) {
	pairings := &fakePairingStore{}
	commands := &fakeCommandGateway{}
	responder := &fakeResponder{reply: "AI mention response"}
	var sentBody string

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		bytes, _ := io.ReadAll(req.Body)
		sentBody = string(bytes)
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]any{"id": "msg-5"})
	}))
	defer server.Close()

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	connector := New("bot-token", server.URL, "wss://discord.test/ws", t.TempDir(), pairings, commands, responder, nil, logger)
	connector.botUserID = "bot-user-1"
	err := connector.handleMessageCreate(context.Background(), discordMessageCreate{
		ID:        "mid-mention",
		ChannelID: "chan-1",
		GuildID:   "guild-1",
		Content:   "<@bot-user-1> summarize this",
		Author: discordAuthor{
			ID: "user-mention",
		},
		Mentions: []discordAuthor{
			{ID: "bot-user-1"},
		},
	})
	if err != nil {
		t.Fatalf("handleMessageCreate failed: %v", err)
	}
	if len(responder.calls) != 1 {
		t.Fatalf("expected responder call, got %d", len(responder.calls))
	}
	if !strings.Contains(sentBody, "AI mention response") {
		t.Fatalf("expected ai response in body, got %s", sentBody)
	}
}

func TestHandleMessageCreatePolicyRateLimitNotice(t *testing.T) {
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
		bytes, _ := io.ReadAll(req.Body)
		sentBody = string(bytes)
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]any{"id": "msg-6"})
	}))
	defer server.Close()

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	connector := New("bot-token", server.URL, "wss://discord.test/ws", t.TempDir(), pairings, commands, responder, policy, logger)
	err := connector.handleMessageCreate(context.Background(), discordMessageCreate{
		ID:        "mid-rate",
		ChannelID: "dm-2",
		GuildID:   "",
		Content:   "hello again",
		Author: discordAuthor{
			ID: "user-rate",
		},
	})
	if err != nil {
		t.Fatalf("handleMessageCreate failed: %v", err)
	}
	if len(responder.calls) != 0 {
		t.Fatalf("expected no responder calls under rate limit, got %d", len(responder.calls))
	}
	if !strings.Contains(sentBody, "Rate limit reached") {
		t.Fatalf("expected rate limit notice in outbound body, got %s", sentBody)
	}
}

func TestHandleMessageCreateLLMActionProposalQueuesApproval(t *testing.T) {
	pairings := &fakePairingStore{}
	commands := &fakeCommandGateway{}
	responder := &fakeResponder{
		reply: "I can do that.\n\n```action\n{\"type\":\"send_email\",\"target\":\"ops@example.com\",\"summary\":\"Send update\",\"subject\":\"Status\"}\n```",
	}
	var sentBody string

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		bytes, _ := io.ReadAll(req.Body)
		sentBody = string(bytes)
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]any{"id": "msg-7"})
	}))
	defer server.Close()

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	connector := New("bot-token", server.URL, "wss://discord.test/ws", t.TempDir(), pairings, commands, responder, nil, logger)
	err := connector.handleMessageCreate(context.Background(), discordMessageCreate{
		ID:        "mid-action",
		ChannelID: "dm-action",
		GuildID:   "",
		Content:   "email the team",
		Author: discordAuthor{
			ID: "user-action",
		},
	})
	if err != nil {
		t.Fatalf("handleMessageCreate failed: %v", err)
	}
	if len(pairings.actions) != 1 {
		t.Fatalf("expected one action approval queued, got %d", len(pairings.actions))
	}
	if pairings.actions[0].ActionType != "send_email" {
		t.Fatalf("unexpected action type: %s", pairings.actions[0].ActionType)
	}
	if !strings.Contains(sentBody, "pending approval") {
		t.Fatalf("expected pending approval notice, got %s", sentBody)
	}
}
