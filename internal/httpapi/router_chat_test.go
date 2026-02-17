package httpapi

import (
	"bytes"
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

	"github.com/dwizi/agent-runtime/internal/config"
	"github.com/dwizi/agent-runtime/internal/gateway"
	"github.com/dwizi/agent-runtime/internal/orchestrator"
)

type fakeMessageGateway struct {
	calls  int
	last   gateway.MessageInput
	output gateway.MessageOutput
	err    error
}

func (f *fakeMessageGateway) HandleMessage(ctx context.Context, input gateway.MessageInput) (gateway.MessageOutput, error) {
	f.calls++
	f.last = input
	if f.err != nil {
		return gateway.MessageOutput{}, f.err
	}
	return f.output, nil
}

func TestChatEndpointRoutesAndAppendsLogs(t *testing.T) {
	sqlStore := newRouterTestStore(t)
	workspaceRoot := t.TempDir()
	fakeGateway := &fakeMessageGateway{output: gateway.MessageOutput{Handled: true, Reply: "reply from gateway"}}

	handler := NewRouter(Dependencies{
		Config:  config.Config{WorkspaceRoot: workspaceRoot},
		Store:   sqlStore,
		Engine:  orchestrator.New(1, slog.New(slog.NewTextHandler(io.Discard, nil))),
		Gateway: fakeGateway,
		Logger:  slog.New(slog.NewTextHandler(io.Discard, nil)),
	})

	body, _ := json.Marshal(map[string]string{
		"connector":    "codex",
		"external_id":  "session-1",
		"display_name": "Codex Session",
		"from_user_id": "user-1",
		"text":         "hello from test",
	})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/chat", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)

	if res.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d body=%s", res.Code, res.Body.String())
	}
	if fakeGateway.calls != 1 {
		t.Fatalf("expected gateway call count 1, got %d", fakeGateway.calls)
	}
	if fakeGateway.last.Connector != "codex" || fakeGateway.last.ExternalID != "session-1" || fakeGateway.last.Text != "hello from test" {
		t.Fatalf("unexpected gateway input: %+v", fakeGateway.last)
	}

	policy, err := sqlStore.LookupContextPolicyByExternal(context.Background(), "codex", "session-1")
	if err != nil {
		t.Fatalf("lookup context policy: %v", err)
	}
	logPath := filepath.Join(workspaceRoot, policy.WorkspaceID, "logs", "chats", "codex", "session-1.md")
	content, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("read chat log: %v", err)
	}
	text := string(content)
	if !strings.Contains(text, "hello from test") {
		t.Fatalf("chat log missing inbound text: %s", text)
	}
	if !strings.Contains(text, "reply from gateway") {
		t.Fatalf("chat log missing outbound reply: %s", text)
	}
}

func TestChatEndpointRejectsMissingText(t *testing.T) {
	sqlStore := newRouterTestStore(t)
	fakeGateway := &fakeMessageGateway{output: gateway.MessageOutput{Handled: true, Reply: "ok"}}
	handler := NewRouter(Dependencies{
		Config:  config.Config{WorkspaceRoot: t.TempDir()},
		Store:   sqlStore,
		Engine:  orchestrator.New(1, slog.New(slog.NewTextHandler(io.Discard, nil))),
		Gateway: fakeGateway,
		Logger:  slog.New(slog.NewTextHandler(io.Discard, nil)),
	})

	req := httptest.NewRequest(http.MethodPost, "/api/v1/chat", bytes.NewReader([]byte(`{"text":"   "}`)))
	req.Header.Set("Content-Type", "application/json")
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)

	if res.Code != http.StatusBadRequest {
		t.Fatalf("expected status 400, got %d", res.Code)
	}
	if fakeGateway.calls != 0 {
		t.Fatalf("expected no gateway calls, got %d", fakeGateway.calls)
	}
}

func TestChatEndpointUnavailableWithoutGateway(t *testing.T) {
	sqlStore := newRouterTestStore(t)
	handler := NewRouter(Dependencies{
		Config: config.Config{WorkspaceRoot: t.TempDir()},
		Store:  sqlStore,
		Engine: orchestrator.New(1, slog.New(slog.NewTextHandler(io.Discard, nil))),
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	})

	req := httptest.NewRequest(http.MethodPost, "/api/v1/chat", bytes.NewReader([]byte(`{"text":"hello"}`)))
	req.Header.Set("Content-Type", "application/json")
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)

	if res.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected status 503, got %d", res.Code)
	}
}
