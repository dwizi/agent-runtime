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
