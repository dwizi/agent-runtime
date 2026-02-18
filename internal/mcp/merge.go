package mcp

import "strings"

func MergeServer(base ServerConfig, override ServerOverride) ServerConfig {
	merged := base
	if override.Enabled != nil {
		merged.Enabled = *override.Enabled
	}
	if override.Transport != nil {
		if value := strings.TrimSpace(override.Transport.Type); value != "" {
			merged.Transport.Type = value
		}
		if value := strings.TrimSpace(override.Transport.Endpoint); value != "" {
			merged.Transport.Endpoint = value
		}
	}
	if override.HTTP != nil {
		if override.HTTP.TimeoutSeconds > 0 {
			merged.HTTP.TimeoutSeconds = override.HTTP.TimeoutSeconds
		}
		if override.HTTP.Headers != nil {
			headers := make(map[string]string, len(merged.HTTP.Headers)+len(override.HTTP.Headers))
			for key, value := range merged.HTTP.Headers {
				headers[key] = value
			}
			for key, value := range override.HTTP.Headers {
				headers[key] = value
			}
			merged.HTTP.Headers = headers
		}
	}
	if override.RefreshSeconds != nil && *override.RefreshSeconds > 0 {
		merged.RefreshSeconds = *override.RefreshSeconds
	}
	if override.Policy != nil {
		if override.Policy.DefaultToolClass != nil {
			merged.Policy.DefaultToolClass = strings.ToLower(strings.TrimSpace(*override.Policy.DefaultToolClass))
		}
		if override.Policy.DefaultRequiresApproval != nil {
			merged.Policy.DefaultRequiresApproval = *override.Policy.DefaultRequiresApproval
		}
		if override.Policy.ToolOverrides != nil {
			if merged.Policy.ToolOverrides == nil {
				merged.Policy.ToolOverrides = map[string]ToolPolicy{}
			}
			for name, incoming := range override.Policy.ToolOverrides {
				trimmed := strings.TrimSpace(name)
				if trimmed == "" {
					continue
				}
				effective := merged.Policy.ToolOverrides[trimmed]
				if strings.TrimSpace(effective.ToolClass) == "" {
					effective.ToolClass = merged.Policy.DefaultToolClass
				}
				effective.RequiresApproval = merged.Policy.DefaultRequiresApproval
				if current, ok := merged.Policy.ToolOverrides[trimmed]; ok {
					effective = current
				}
				if incoming.ToolClass != nil {
					effective.ToolClass = strings.ToLower(strings.TrimSpace(*incoming.ToolClass))
				}
				if incoming.RequiresApproval != nil {
					effective.RequiresApproval = *incoming.RequiresApproval
				}
				merged.Policy.ToolOverrides[trimmed] = effective
			}
		}
	}
	return merged
}

func FindOverride(overrides WorkspaceCatalog, serverID string) (ServerOverride, bool) {
	serverID = strings.ToLower(strings.TrimSpace(serverID))
	if serverID == "" {
		return ServerOverride{}, false
	}
	for _, item := range overrides.Servers {
		if strings.EqualFold(strings.TrimSpace(item.ID), serverID) {
			return item, true
		}
	}
	return ServerOverride{}, false
}
