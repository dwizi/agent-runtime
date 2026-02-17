package gateway

import "testing"

func TestQuestionNeedsExternalFollowUpRequiresClearCue(t *testing.T) {
	if questionNeedsExternalFollowUp("what is your favorite language?") {
		t.Fatal("expected conversational question to skip follow-up routing")
	}
	if !questionNeedsExternalFollowUp("can you run a search in dwizi.com and tell me pricing?") {
		t.Fatal("expected explicit research question to require follow-up")
	}
	if !questionNeedsExternalFollowUp("can you monitor this page and notify me later?") {
		t.Fatal("expected async monitoring question to require follow-up")
	}
}

func TestLooksLikeTaskNeedsExplicitActionCue(t *testing.T) {
	if looksLikeTask("should we update docs this week?") {
		t.Fatal("expected speculative question to not be classified as task")
	}
	if !looksLikeTask("please investigate this deployment regression today") {
		t.Fatal("expected explicit action request to be classified as task")
	}
}
