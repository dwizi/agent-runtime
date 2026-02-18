package mcp

import (
	"crypto/sha1"
	"encoding/hex"
	"strings"
)

func BuildRegisteredToolName(serverID, toolName string) string {
	serverPart := sanitizeName(serverID)
	toolPart := sanitizeName(toolName)
	if serverPart == "" {
		serverPart = "server"
	}
	if toolPart == "" {
		toolPart = "tool"
	}
	base := "mcp_" + serverPart + "__" + toolPart
	if len(base) <= 128 {
		return base
	}
	return withHashSuffix(base, serverID+"::"+toolName)
}

func EnsureUniqueRegisteredNames(serverID string, toolNames []string) map[string]string {
	result := make(map[string]string, len(toolNames))
	seen := map[string]struct{}{}
	for _, name := range toolNames {
		candidate := BuildRegisteredToolName(serverID, name)
		if _, exists := seen[candidate]; exists {
			candidate = withHashSuffix(candidate, serverID+"::"+name)
		}
		seen[candidate] = struct{}{}
		result[name] = candidate
	}
	return result
}

func sanitizeName(raw string) string {
	value := strings.ToLower(strings.TrimSpace(raw))
	if value == "" {
		return ""
	}
	builder := strings.Builder{}
	for _, r := range value {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '_' {
			builder.WriteRune(r)
			continue
		}
		builder.WriteByte('_')
	}
	result := strings.Trim(builder.String(), "_")
	return result
}

func withHashSuffix(base, seed string) string {
	hash := sha1.Sum([]byte(seed))
	suffix := hex.EncodeToString(hash[:])[:8]
	if len(base) > 118 {
		base = base[:118]
	}
	base = strings.TrimRight(base, "_")
	return base + "_" + suffix
}
