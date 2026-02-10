package actions

import "testing"

func TestExtractProposal(t *testing.T) {
	input := "I can send this update.\n\n```action\n{\"type\":\"send_email\",\"target\":\"ops@example.com\",\"summary\":\"Send update\",\"subject\":\"Status\",\"body\":\"Done\"}\n```"
	clean, proposal := ExtractProposal(input)
	if proposal == nil {
		t.Fatal("expected proposal")
	}
	if proposal.Type != "send_email" {
		t.Fatalf("unexpected type: %s", proposal.Type)
	}
	if proposal.Target != "ops@example.com" {
		t.Fatalf("unexpected target: %s", proposal.Target)
	}
	if _, ok := proposal.Payload["subject"]; !ok {
		t.Fatal("expected subject in payload")
	}
	if clean == "" {
		t.Fatal("expected clean text")
	}
}

func TestExtractProposalNoBlock(t *testing.T) {
	clean, proposal := ExtractProposal("hello world")
	if proposal != nil {
		t.Fatal("expected no proposal")
	}
	if clean != "hello world" {
		t.Fatalf("unexpected clean text: %s", clean)
	}
}

func TestExtractProposalNestedPayload(t *testing.T) {
	input := "Fetching dwizi.com pricing right now.\n```action\n{\"type\":\"run_command\",\"target\":\"curl\",\"summary\":\"Fetch dwizi.com pricing info\",\"payload\":{\"args\":[\"-sS\",\"https://dwizi.com\"]}}\n```"
	clean, proposal := ExtractProposal(input)
	if proposal == nil {
		t.Fatal("expected proposal")
	}
	if proposal.Type != "run_command" {
		t.Fatalf("unexpected type: %s", proposal.Type)
	}
	if proposal.Target != "curl" {
		t.Fatalf("unexpected target: %s", proposal.Target)
	}
	payload, ok := proposal.Payload["payload"].(map[string]any)
	if !ok {
		t.Fatalf("expected nested payload map, got %#v", proposal.Payload["payload"])
	}
	if _, ok := payload["args"]; !ok {
		t.Fatalf("expected args in nested payload, got %#v", payload)
	}
	if clean != "Fetching dwizi.com pricing right now." {
		t.Fatalf("unexpected clean text: %s", clean)
	}
}

func TestExtractProposalInlineAction(t *testing.T) {
	input := "Fetching dwizi.com pricing right now. action {\"type\":\"run_command\",\"target\":\"curl\",\"summary\":\"Fetch dwizi.com pricing info\",\"payload\":{\"args\":[\"-sS\",\"https://dwizi.com\"]}}"
	clean, proposal := ExtractProposal(input)
	if proposal == nil {
		t.Fatal("expected proposal")
	}
	if proposal.Type != "run_command" {
		t.Fatalf("unexpected type: %s", proposal.Type)
	}
	if proposal.Target != "curl" {
		t.Fatalf("unexpected target: %s", proposal.Target)
	}
	if clean != "Fetching dwizi.com pricing right now." {
		t.Fatalf("unexpected clean text: %s", clean)
	}
}
