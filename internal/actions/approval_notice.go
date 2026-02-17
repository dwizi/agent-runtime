package actions

import "strings"

func FormatApprovalRequestNotice(actionID string) string {
	id := strings.TrimSpace(actionID)
	if id == "" {
		id = "(unknown-action-request)"
	}
	return "Approval required for action `" + id + "`. Reply 'approve' to execute, or 'deny' to reject."
}
