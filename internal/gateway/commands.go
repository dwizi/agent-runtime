package gateway

import "strings"

type SlashCommand struct {
	Name                string
	Description         string
	ArgumentName        string
	ArgumentDescription string
	ArgumentRequired    bool
}

func SlashCommands() []SlashCommand {
	return []SlashCommand{
		{
			Name:                "task",
			Description:         "Create a routed task",
			ArgumentName:        "prompt",
			ArgumentDescription: "What should be done",
			ArgumentRequired:    true,
		},
		{
			Name:                "search",
			Description:         "Search workspace knowledge",
			ArgumentName:        "query",
			ArgumentDescription: "What to search for",
			ArgumentRequired:    true,
		},
		{
			Name:                "open",
			Description:         "Open a markdown path",
			ArgumentName:        "target",
			ArgumentDescription: "Path or search target",
			ArgumentRequired:    true,
		},
		{
			Name:        "status",
			Description: "Show qmd index status",
		},
		{
			Name:                "monitor",
			Description:         "Create a monitoring objective",
			ArgumentName:        "goal",
			ArgumentDescription: "Objective to monitor",
			ArgumentRequired:    true,
		},
		{
			Name:                "admin-channel",
			Description:         "Enable admin mode for this channel",
			ArgumentName:        "mode",
			ArgumentDescription: "Use: enable",
			ArgumentRequired:    true,
		},
		{
			Name:                "prompt",
			Description:         "Set the system prompt for this channel",
			ArgumentName:        "text",
			ArgumentDescription: "Prompt text",
			ArgumentRequired:    true,
		},
		{
			Name:                "approve",
			Description:         "Approve a pairing token",
			ArgumentName:        "token",
			ArgumentDescription: "Pairing token",
			ArgumentRequired:    true,
		},
		{
			Name:                "deny",
			Description:         "Deny a pairing token",
			ArgumentName:        "token_reason",
			ArgumentDescription: "Token and optional reason",
			ArgumentRequired:    true,
		},
		{
			Name:        "pending-actions",
			Description: "List pending action approvals",
		},
		{
			Name:                "approve-action",
			Description:         "Approve a pending action",
			ArgumentName:        "action_id",
			ArgumentDescription: "Action ID",
			ArgumentRequired:    true,
		},
		{
			Name:                "deny-action",
			Description:         "Deny a pending action",
			ArgumentName:        "action_reason",
			ArgumentDescription: "Action ID and optional reason",
			ArgumentRequired:    true,
		},
		{
			Name:                "route",
			Description:         "Override triage routing for a task",
			ArgumentName:        "override",
			ArgumentDescription: "task-id class [p1|p2|p3] [due-window]",
			ArgumentRequired:    true,
		},
	}
}

func NormalizeCommandName(command string) string {
	normalized := strings.ToLower(strings.TrimSpace(command))
	if normalized == "" {
		return ""
	}
	return strings.ReplaceAll(normalized, "_", "-")
}
