package safety

import (
	"strings"
	"sync"
	"time"
)

type Config struct {
	Enabled                bool
	AllowedRoles           map[string]struct{}
	AllowedContextIDs      map[string]struct{}
	AllowDM                bool
	RequireMentionInGroups bool
	RateLimitPerWindow     int
	RateLimitWindow        time.Duration
	RateLimitMessage       string
}

type Request struct {
	Connector string
	ContextID string
	UserID    string
	UserRole  string
	IsDM      bool
	IsMention bool
}

type Decision struct {
	Allowed bool
	Notify  string
	Reason  string
}

type Policy struct {
	cfg     Config
	mu      sync.Mutex
	buckets map[string][]time.Time
}

func New(cfg Config) *Policy {
	if cfg.RateLimitPerWindow < 1 {
		cfg.RateLimitPerWindow = 8
	}
	if cfg.RateLimitWindow <= 0 {
		cfg.RateLimitWindow = time.Minute
	}
	if strings.TrimSpace(cfg.RateLimitMessage) == "" {
		cfg.RateLimitMessage = "Rate limit reached for non-admin users. Try again shortly."
	}
	return &Policy{
		cfg:     cfg,
		buckets: map[string][]time.Time{},
	}
}

func (p *Policy) Check(input Request) Decision {
	if !p.cfg.Enabled {
		return Decision{Allowed: false, Reason: "disabled"}
	}
	if input.IsDM && !p.cfg.AllowDM {
		return Decision{Allowed: false, Reason: "dm_disabled"}
	}
	if !input.IsDM && p.cfg.RequireMentionInGroups && !input.IsMention {
		return Decision{Allowed: false, Reason: "mention_required"}
	}

	contextID := normalize(input.ContextID)
	if len(p.cfg.AllowedContextIDs) > 0 {
		if _, ok := p.cfg.AllowedContextIDs[contextID]; !ok {
			return Decision{Allowed: false, Reason: "context_not_allowed"}
		}
	}

	role := normalize(input.UserRole)
	if len(p.cfg.AllowedRoles) > 0 {
		if _, ok := p.cfg.AllowedRoles[role]; !ok {
			return Decision{Allowed: false, Notify: "LLM replies are disabled for your role.", Reason: "role_not_allowed"}
		}
	}

	if isAdminRole(role) {
		return Decision{Allowed: true}
	}
	if !p.consumeRateLimit(input) {
		return Decision{Allowed: false, Notify: p.cfg.RateLimitMessage, Reason: "rate_limited"}
	}
	return Decision{Allowed: true}
}

func (p *Policy) consumeRateLimit(input Request) bool {
	key := normalize(input.Connector) + ":" + normalize(input.ContextID) + ":" + normalize(input.UserID)
	if strings.Trim(key, ":") == "" {
		key = "unknown"
	}
	now := time.Now().UTC()
	cutoff := now.Add(-p.cfg.RateLimitWindow)

	p.mu.Lock()
	defer p.mu.Unlock()
	entries := p.buckets[key]
	filtered := entries[:0]
	for _, stamp := range entries {
		if stamp.After(cutoff) {
			filtered = append(filtered, stamp)
		}
	}
	if len(filtered) >= p.cfg.RateLimitPerWindow {
		p.buckets[key] = filtered
		return false
	}
	filtered = append(filtered, now)
	p.buckets[key] = filtered
	return true
}

func normalize(value string) string {
	return strings.ToLower(strings.TrimSpace(value))
}

func isAdminRole(role string) bool {
	switch normalize(role) {
	case "overlord", "admin":
		return true
	default:
		return false
	}
}
