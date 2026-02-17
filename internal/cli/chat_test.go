package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
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
