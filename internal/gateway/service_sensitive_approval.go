package gateway

import (
	"strings"
	"time"
)

func (s *Service) grantSensitiveToolApproval(input MessageInput, now time.Time) {
	if s == nil {
		return
	}
	key := sensitiveApprovalKey(input)
	if key == "" {
		return
	}
	s.approvalMu.Lock()
	defer s.approvalMu.Unlock()
	cutoff := now.UTC()
	for existingKey, expiry := range s.sensitiveApprovals {
		if !expiry.After(cutoff) {
			delete(s.sensitiveApprovals, existingKey)
		}
	}
	ttl := s.sensitiveApprovalTTL
	if ttl <= 0 {
		ttl = 10 * time.Minute
	}
	s.sensitiveApprovals[key] = cutoff.Add(ttl)
}
func (s *Service) consumeSensitiveToolApproval(input MessageInput, now time.Time) bool {
	if s == nil {
		return false
	}
	key := sensitiveApprovalKey(input)
	if key == "" {
		return false
	}
	s.approvalMu.Lock()
	defer s.approvalMu.Unlock()
	expiry, ok := s.sensitiveApprovals[key]
	if !ok {
		return false
	}
	delete(s.sensitiveApprovals, key)
	return expiry.After(now.UTC())
}

func sensitiveApprovalKey(input MessageInput) string {
	connector := strings.ToLower(strings.TrimSpace(input.Connector))
	externalID := strings.TrimSpace(input.ExternalID)
	fromUser := strings.TrimSpace(input.FromUserID)
	if connector == "" || externalID == "" || fromUser == "" {
		return ""
	}
	return connector + "|" + externalID + "|" + fromUser
}
