package contextack

import (
	"context"
	"errors"
	"testing"

	"github.com/carlos/spinner/internal/llm"
	llmgrounded "github.com/carlos/spinner/internal/llm/grounded"
)

type fakeResponder struct {
	reply     string
	err       error
	lastInput llm.MessageInput
	calls     int
}

func (f *fakeResponder) Reply(ctx context.Context, input llm.MessageInput) (string, error) {
	f.lastInput = input
	f.calls++
	if f.err != nil {
		return "", f.err
	}
	return f.reply, nil
}

func TestPlanAndGenerateSkipsAckWhenNoContextNeeded(t *testing.T) {
	responder := &fakeResponder{reply: "unused"}
	decision, ack := PlanAndGenerate(context.Background(), responder, llm.MessageInput{
		Connector: "discord",
		Text:      "hello",
	})
	if decision.Strategy != llmgrounded.StrategyNone {
		t.Fatalf("expected none strategy, got %s", decision.Strategy)
	}
	if ack != "" {
		t.Fatalf("expected empty ack, got %q", ack)
	}
	if responder.calls != 0 {
		t.Fatalf("expected no responder call, got %d", responder.calls)
	}
}

func TestPlanAndGenerateUsesLLMAndForcesSkipGrounding(t *testing.T) {
	responder := &fakeResponder{reply: "Sure, I’ll pull context now."}
	decision, ack := PlanAndGenerate(context.Background(), responder, llm.MessageInput{
		Connector:   "discord",
		WorkspaceID: "ws-1",
		ContextID:   "ctx-1",
		ExternalID:  "chan-1",
		FromUserID:  "u1",
		Text:        "can you search pricing in dwizi.com?",
	})
	if decision.Strategy != llmgrounded.StrategyQMD {
		t.Fatalf("expected qmd strategy, got %s", decision.Strategy)
	}
	if ack != "Sure, I’ll pull context now." {
		t.Fatalf("unexpected ack: %q", ack)
	}
	if responder.calls != 1 {
		t.Fatalf("expected one responder call, got %d", responder.calls)
	}
	if !responder.lastInput.SkipGrounding {
		t.Fatal("expected ack generation call to force SkipGrounding=true")
	}
}

func TestPlanAndGenerateFallsBackOnResponderError(t *testing.T) {
	responder := &fakeResponder{err: errors.New("boom")}
	decision, ack := PlanAndGenerate(context.Background(), responder, llm.MessageInput{
		Connector:   "telegram",
		WorkspaceID: "ws-1",
		ContextID:   "ctx-1",
		ExternalID:  "chat-1",
		FromUserID:  "u1",
		Text:        "as we discussed before, continue",
	})
	if decision.Strategy != llmgrounded.StrategyTail {
		t.Fatalf("expected tail strategy, got %s", decision.Strategy)
	}
	if ack != "Let me pull some recent context first." {
		t.Fatalf("unexpected fallback ack: %q", ack)
	}
}

