package cli

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/dwizi/agent-runtime/internal/adminclient"
	"github.com/dwizi/agent-runtime/internal/config"
)

func TestParseChatLogContent(t *testing.T) {
	raw := strings.Join([]string{
		"# Chat Log",
		"",
		"- connector: `telegram`",
		"- external_id: `123`",
		"- display_name: `Tester`",
		"",
		"## 2026-02-16T20:10:27Z `INBOUND`",
		"- direction: `inbound`",
		"- actor: `123`",
		"",
		"hello",
		"",
		"## 2026-02-16T20:10:33Z `OUTBOUND`",
		"- direction: `outbound`",
		"- actor: `agent-runtime`",
		"",
		"hi there",
		"",
	}, "\n")

	parsed, err := parseChatLogContent(raw)
	if err != nil {
		t.Fatalf("parse chat log: %v", err)
	}
	if parsed.Connector != "telegram" || parsed.ExternalID != "123" {
		t.Fatalf("unexpected header fields: %+v", parsed)
	}
	if len(parsed.Entries) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(parsed.Entries))
	}
	if parsed.Entries[0].Direction != "inbound" || strings.TrimSpace(parsed.Entries[0].Text) != "hello" {
		t.Fatalf("unexpected first entry: %+v", parsed.Entries[0])
	}
	if parsed.Entries[1].Direction != "outbound" || strings.TrimSpace(parsed.Entries[1].Text) != "hi there" {
		t.Fatalf("unexpected second entry: %+v", parsed.Entries[1])
	}
}

func TestBuildChatTurnsAndSignals(t *testing.T) {
	parsed := parsedChatLog{
		Entries: []parsedChatEntry{
			{Direction: "inbound", Text: "/approve_action act_1"},
			{Direction: "outbound", Text: "Usage: /approve-action <action-id>"},
			{Direction: "tool", Text: "Tool call\n- tool: `run_action`\n- args: `{\"target\":\"curl\"}`"},
			{Direction: "tool", Text: "Tool call\n- tool: `run_action`\n- args: `{\"target\":\"curl\"}`"},
			{Direction: "outbound", Text: "I cannot run more tools for this request under current policy."},
		},
	}

	turns := buildChatTurns(parsed)
	if len(turns) != 1 {
		t.Fatalf("expected one turn, got %d", len(turns))
	}
	if !hasCommandUsageBounce(turns[0]) {
		t.Fatal("expected command usage bounce signal")
	}
	if got := duplicateToolSignatures(turns[0]); got != 1 {
		t.Fatalf("expected one duplicate tool signature, got %d", got)
	}
	if !hasPolicyExhaustion(turns[0]) {
		t.Fatal("expected policy exhaustion signal")
	}
}

func TestEvaluateChatLogFiles(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "chat.md")
	content := strings.Join([]string{
		"# Chat Log",
		"",
		"- connector: `telegram`",
		"- external_id: `123`",
		"- display_name: `Tester`",
		"",
		"## 2026-02-16T20:10:27Z `INBOUND`",
		"- direction: `inbound`",
		"- actor: `123`",
		"",
		"/approve_action act_1",
		"",
		"## 2026-02-16T20:10:28Z `TOOL`",
		"- direction: `tool`",
		"- actor: `agent-runtime`",
		"",
		"Tool call",
		"- tool: `run_action`",
		"- args: `{\"payload\":{\"args\":[\"-sS\",\"https://swapi.dev/api/people/3\"]}}`",
		"",
		"## 2026-02-16T20:10:29Z `TOOL`",
		"- direction: `tool`",
		"- actor: `agent-runtime`",
		"",
		"Tool call",
		"- tool: `run_action`",
		"- args: `{\"payload\":{\"args\":[\"-sS\",\"https://swapi.dev/api/people/3\"]}}`",
		"",
		"## 2026-02-16T20:10:30Z `OUTBOUND`",
		"- direction: `outbound`",
		"- actor: `agent-runtime`",
		"",
		"Usage: /approve-action <action-id>",
		"",
		"## 2026-02-16T20:10:31Z `OUTBOUND`",
		"- direction: `outbound`",
		"- actor: `agent-runtime`",
		"",
		"I cannot run more tools for this request under current policy.",
		"",
	}, "\n")
	if err := os.WriteFile(logPath, []byte(content), 0o644); err != nil {
		t.Fatalf("write log fixture: %v", err)
	}

	report := evaluateChatLogFiles([]string{logPath})
	if report.ConversationsParsed != 1 {
		t.Fatalf("expected 1 parsed conversation, got %d", report.ConversationsParsed)
	}
	if report.DuplicateToolBursts == 0 {
		t.Fatal("expected duplicate tool burst count")
	}
	if report.PolicyExhaustions == 0 {
		t.Fatal("expected policy exhaustion count")
	}
	if report.CommandUsageBounces == 0 {
		t.Fatal("expected command usage bounce count")
	}
	if len(report.Recommendations) == 0 {
		t.Fatal("expected recommendations")
	}
}

func TestResolveChatIdentityDefaultsToCodexCLI(t *testing.T) {
	connector, externalID, fromUserID, displayName := resolveChatIdentity(" ", " ", " ", " ")
	if connector != "codex" {
		t.Fatalf("expected default connector codex, got %q", connector)
	}
	if externalID != "codex-cli" {
		t.Fatalf("expected default external id codex-cli, got %q", externalID)
	}
	if fromUserID != "codex-cli" {
		t.Fatalf("expected default from user id codex-cli, got %q", fromUserID)
	}
	if displayName != "codex-cli" {
		t.Fatalf("expected default display name codex-cli, got %q", displayName)
	}
}

func TestResolveChatIdentityNormalizesConnectorAndDefaultsFromUser(t *testing.T) {
	connector, externalID, fromUserID, displayName := resolveChatIdentity(" CoDeX ", " session-99 ", "", " Codex CLI ")
	if connector != "codex" {
		t.Fatalf("expected normalized connector codex, got %q", connector)
	}
	if externalID != "session-99" {
		t.Fatalf("expected trimmed external id session-99, got %q", externalID)
	}
	if fromUserID != "session-99" {
		t.Fatalf("expected from user id to default to external id, got %q", fromUserID)
	}
	if displayName != "Codex CLI" {
		t.Fatalf("expected display name Codex CLI, got %q", displayName)
	}
}

func TestReplayTurnsSendsCodexChatRequestsAndPrintsReplies(t *testing.T) {
	var received []adminclient.ChatRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Fatalf("expected POST, got %s", r.Method)
		}
		if r.URL.Path != "/api/v1/chat" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		var payload adminclient.ChatRequest
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatalf("decode body: %v", err)
		}
		received = append(received, payload)
		_ = json.NewEncoder(w).Encode(adminclient.ChatResponse{
			Handled: true,
			Reply:   "ack: " + strings.TrimSpace(payload.Text),
		})
	}))
	defer server.Close()

	client, err := adminclient.New(config.Config{
		AdminAPIURL:         server.URL,
		AdminHTTPTimeoutSec: 10,
	})
	if err != nil {
		t.Fatalf("new admin client: %v", err)
	}

	turns := []parsedChatTurn{
		{
			Inbound:   parsedChatEntry{Text: "monitor release notes"},
			Outbounds: []parsedChatEntry{{Text: "legacy reply one"}},
		},
		{
			Inbound:   parsedChatEntry{Text: "/pending-actions"},
			Outbounds: []parsedChatEntry{{Text: "legacy reply two"}},
		},
	}
	req := replayRequest{
		Connector:    "codex",
		ExternalID:   "codex-cli",
		FromUserID:   "codex-cli",
		DisplayName:  "Codex CLI",
		ShowExpected: true,
		TimeoutSec:   10,
	}

	cmd := &cobra.Command{}
	var output bytes.Buffer
	cmd.SetOut(&output)
	cmd.SetErr(&output)

	result := replayTurns(cmd, client, turns, req)
	if result.TotalTurns != 2 || result.SentTurns != 2 || result.Failures != 0 {
		t.Fatalf("unexpected replay result: %+v", result)
	}
	if len(received) != 2 {
		t.Fatalf("expected 2 chat requests, got %d", len(received))
	}
	if received[0].Connector != "codex" || received[0].ExternalID != "codex-cli" || received[0].FromUserID != "codex-cli" {
		t.Fatalf("unexpected first request identity: %+v", received[0])
	}
	if received[1].Text != "/pending-actions" {
		t.Fatalf("unexpected second request text: %q", received[1].Text)
	}
	rendered := output.String()
	if !strings.Contains(rendered, "[1] user: monitor release notes") {
		t.Fatalf("expected first user line in output, got %q", rendered)
	}
	if !strings.Contains(rendered, "agent: ack: monitor release notes") {
		t.Fatalf("expected first agent line in output, got %q", rendered)
	}
	if !strings.Contains(rendered, "prev:  legacy reply one") {
		t.Fatalf("expected first historical comparison line in output, got %q", rendered)
	}
}

func TestReplayTurnsCountsFailuresAndContinues(t *testing.T) {
	requestCount := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestCount++
		if requestCount == 1 {
			w.WriteHeader(http.StatusInternalServerError)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "boom"})
			return
		}
		_ = json.NewEncoder(w).Encode(adminclient.ChatResponse{Handled: true, Reply: "ok"})
	}))
	defer server.Close()

	client, err := adminclient.New(config.Config{
		AdminAPIURL:         server.URL,
		AdminHTTPTimeoutSec: 10,
	})
	if err != nil {
		t.Fatalf("new admin client: %v", err)
	}

	turns := []parsedChatTurn{
		{Inbound: parsedChatEntry{Text: "first"}},
		{Inbound: parsedChatEntry{Text: "second"}},
	}

	cmd := &cobra.Command{}
	var output bytes.Buffer
	cmd.SetOut(&output)
	cmd.SetErr(&output)

	result := replayTurns(cmd, client, turns, replayRequest{
		Connector:   "codex",
		ExternalID:  "codex-cli",
		FromUserID:  "codex-cli",
		DisplayName: "Codex CLI",
		TimeoutSec:  10,
	})
	if result.Failures != 1 || result.SentTurns != 2 || result.TotalTurns != 2 {
		t.Fatalf("unexpected replay result: %+v", result)
	}
	if requestCount != 2 {
		t.Fatalf("expected replay to continue after failure, got %d requests", requestCount)
	}
	if !strings.Contains(output.String(), "error:") {
		t.Fatalf("expected error output, got %q", output.String())
	}
}
