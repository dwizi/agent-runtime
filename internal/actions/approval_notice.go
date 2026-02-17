package actions

import "strings"

func FormatApprovalRequestNotice(actionID string) string {
	id := strings.TrimSpace(actionID)
	if id == "" {
		id = "(unknown-action-request)"
	}
	return "Admin approval required. Reply 'approve' to execute action '" + id + "', or 'deny' to reject."
}
