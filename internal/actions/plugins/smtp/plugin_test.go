package smtp

import (
	"context"
	gosmtp "net/smtp"
	"strings"
	"testing"

	"github.com/carlos/spinner/internal/store"
)

func TestPluginExecuteSendsEmail(t *testing.T) {
	plugin := New(Config{
		Host: "smtp.example.com",
		Port: 2525,
		From: "Spinner Bot <bot@example.com>",
	})

	var called bool
	plugin.sendMail = func(addr string, auth gosmtp.Auth, from string, to []string, msg []byte) error {
		called = true
		if addr != "smtp.example.com:2525" {
			t.Fatalf("unexpected smtp addr: %s", addr)
		}
		if from != "bot@example.com" {
			t.Fatalf("unexpected sender: %s", from)
		}
		if len(to) != 1 || to[0] != "alice@example.com" {
			t.Fatalf("unexpected recipients: %+v", to)
		}
		body := string(msg)
		if !strings.Contains(body, "Subject: Team Update") {
			t.Fatalf("expected subject in message, got: %s", body)
		}
		if !strings.Contains(body, "Content-Type: text/plain; charset=UTF-8") {
			t.Fatalf("expected plain content type in message, got: %s", body)
		}
		return nil
	}

	result, err := plugin.Execute(context.Background(), store.ActionApproval{
		ActionType:    "send_email",
		ActionTarget:  "alice@example.com",
		ActionSummary: "Daily update",
		Payload: map[string]any{
			"subject": "Team Update",
			"body":    "Status: done",
		},
	})
	if err != nil {
		t.Fatalf("execute failed: %v", err)
	}
	if !called {
		t.Fatal("expected sendMail to be called")
	}
	if result.Plugin != "smtp_email" {
		t.Fatalf("unexpected plugin key: %s", result.Plugin)
	}
	if !strings.Contains(result.Message, "email sent to 1 recipient") {
		t.Fatalf("unexpected result message: %s", result.Message)
	}
}

func TestPluginExecuteWithPayloadToAndHTML(t *testing.T) {
	plugin := New(Config{
		Host:     "smtp.example.com",
		Port:     587,
		Username: "bot@example.com",
		Password: "secret",
		From:     "Spinner Bot <bot@example.com>",
	})

	plugin.sendMail = func(addr string, auth gosmtp.Auth, from string, to []string, msg []byte) error {
		if addr != "smtp.example.com:587" {
			t.Fatalf("unexpected smtp addr: %s", addr)
		}
		if auth == nil {
			t.Fatal("expected smtp auth")
		}
		if len(to) != 2 {
			t.Fatalf("expected two recipients, got %d", len(to))
		}
		body := string(msg)
		if !strings.Contains(body, "Content-Type: text/html; charset=UTF-8") {
			t.Fatalf("expected html content type in message, got: %s", body)
		}
		if !strings.Contains(body, "<b>Hello</b>") {
			t.Fatalf("expected html body in message, got: %s", body)
		}
		return nil
	}

	_, err := plugin.Execute(context.Background(), store.ActionApproval{
		ActionType: "smtp_email",
		Payload: map[string]any{
			"to":      []any{"Alice <alice@example.com>", "bob@example.com"},
			"subject": "Greeting",
			"html":    "<b>Hello</b>",
		},
	})
	if err != nil {
		t.Fatalf("execute failed: %v", err)
	}
}

func TestPluginExecuteReturnsValidationError(t *testing.T) {
	plugin := New(Config{
		Host: "smtp.example.com",
		From: "Spinner Bot <bot@example.com>",
	})
	_, err := plugin.Execute(context.Background(), store.ActionApproval{
		ActionType:   "send_email",
		ActionTarget: "not-an-email",
		Payload: map[string]any{
			"subject": "oops",
			"body":    "x",
		},
	})
	if err == nil {
		t.Fatal("expected validation error")
	}
	if !strings.Contains(err.Error(), "invalid recipient") {
		t.Fatalf("unexpected error: %v", err)
	}
}
