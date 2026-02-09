package safety

import (
	"testing"
	"time"
)

func TestPolicyRateLimitsNonAdminOnly(t *testing.T) {
	policy := New(Config{
		Enabled:                true,
		AllowDM:                true,
		RequireMentionInGroups: false,
		RateLimitPerWindow:     1,
		RateLimitWindow:        time.Minute,
	})

	first := policy.Check(Request{
		Connector: "telegram",
		ContextID: "ctx-1",
		UserID:    "user-1",
		UserRole:  "member",
		IsDM:      true,
	})
	if !first.Allowed {
		t.Fatalf("first request should pass: %+v", first)
	}
	second := policy.Check(Request{
		Connector: "telegram",
		ContextID: "ctx-1",
		UserID:    "user-1",
		UserRole:  "member",
		IsDM:      true,
	})
	if second.Allowed {
		t.Fatal("second request should be rate limited")
	}
	if second.Notify == "" {
		t.Fatal("expected rate limit notice")
	}
}

func TestPolicyBypassesRateLimitForAdmin(t *testing.T) {
	policy := New(Config{
		Enabled:                true,
		AllowDM:                true,
		RequireMentionInGroups: false,
		RateLimitPerWindow:     1,
		RateLimitWindow:        time.Minute,
	})
	for i := 0; i < 5; i++ {
		decision := policy.Check(Request{
			Connector: "discord",
			ContextID: "ctx-1",
			UserID:    "admin-1",
			UserRole:  "admin",
			IsDM:      false,
			IsMention: true,
		})
		if !decision.Allowed {
			t.Fatalf("admin request should not be limited: %+v", decision)
		}
	}
}

func TestPolicyMentionRequirement(t *testing.T) {
	policy := New(Config{
		Enabled:                true,
		AllowDM:                true,
		RequireMentionInGroups: true,
		RateLimitPerWindow:     5,
		RateLimitWindow:        time.Minute,
	})

	decision := policy.Check(Request{
		Connector: "discord",
		ContextID: "ctx-1",
		UserID:    "user-1",
		UserRole:  "member",
		IsDM:      false,
		IsMention: false,
	})
	if decision.Allowed {
		t.Fatal("expected non-mention group message to be denied")
	}
}

func TestPolicyAllowlistedContexts(t *testing.T) {
	policy := New(Config{
		Enabled:                true,
		AllowDM:                true,
		RequireMentionInGroups: false,
		AllowedContextIDs: map[string]struct{}{
			"ctx-allowed": {},
		},
		RateLimitPerWindow: 5,
		RateLimitWindow:    time.Minute,
	})

	allowed := policy.Check(Request{
		ContextID: "ctx-allowed",
		UserID:    "u1",
		UserRole:  "member",
		IsDM:      true,
	})
	if !allowed.Allowed {
		t.Fatalf("expected allowed context to pass: %+v", allowed)
	}
	denied := policy.Check(Request{
		ContextID: "ctx-other",
		UserID:    "u1",
		UserRole:  "member",
		IsDM:      true,
	})
	if denied.Allowed {
		t.Fatal("expected non-allowlisted context to be denied")
	}
}
