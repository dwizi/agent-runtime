package tui

import (
	"fmt"
	"strings"
	"time"
)

func (m model) renderPairingsWorkbenchText(t theme, layout uiLayout) string {
	intro := []string{
		t.panelSubtle.Render("Approve one-time connector pairing tokens"),
		"approval role " + t.chipInfo.Render(m.currentPairingRole()),
	}
	primary := make([]string, 0, 12)
	tail := make([]string, 0, 4)
	if m.activePair == nil {
		primary = append(primary,
			t.panelSubtle.Render("lookup token"),
			m.tokenInput.View(),
		)
		tail = append(tail, t.panelSubtle.Render("enter to lookup"))
	} else {
		primary = append(primary,
			t.panelSubtle.Render("token"),
			m.tokenInput.View(),
			"",
			fmt.Sprintf("connector     %s", fallbackText(m.activePair.Connector, "n/a")),
			fmt.Sprintf("user id       %s", fallbackText(m.activePair.ConnectorUserID, "n/a")),
			fmt.Sprintf("display name  %s", fallbackText(m.activePair.DisplayName, "n/a")),
			fmt.Sprintf("status        %s", fallbackText(m.activePair.Status, "n/a")),
		)
		tail = append(tail, t.panelSubtle.Render("actions: a approve | d deny | n clear"))
	}

	if strings.TrimSpace(m.errorText) != "" {
		tail = append(tail, t.panelError.Render("error: "+m.errorText))
	}
	_ = layout
	return renderWorkbenchRhythm(intro, primary, tail)
}

func (m model) renderPairingsInspectorText() string {
	lines := []string{
		"Pairing Detail",
		"",
		"approver id  " + fallbackText(m.cfg.TUIApproverUserID, "unset"),
		"default role " + fallbackText(m.cfg.TUIApprovalRole, "unset"),
		"active role  " + m.currentPairingRole(),
	}

	if m.activePair != nil {
		expiresAt := "unknown"
		if m.activePair.ExpiresAtUnix > 0 {
			expiresAt = time.Unix(m.activePair.ExpiresAtUnix, 0).UTC().Format(time.RFC3339)
		}
		lines = append(lines,
			"",
			"Pending Request",
			"id             "+fallbackText(m.activePair.ID, "n/a"),
			"connector      "+fallbackText(m.activePair.Connector, "n/a"),
			"connector user "+fallbackText(m.activePair.ConnectorUserID, "n/a"),
			"display name   "+fallbackText(m.activePair.DisplayName, "n/a"),
			"expires        "+expiresAt,
		)
	}

	if m.approvedMsg != nil {
		lines = append(lines,
			"",
			"Last Approval",
			"user id    "+fallbackText(m.approvedMsg.ApprovedUserID, "n/a"),
			"identity   "+fallbackText(m.approvedMsg.IdentityID, "n/a"),
			"connector  "+fallbackText(m.approvedMsg.Connector, "n/a"),
		)
	}

	return strings.Join(lines, "\n")
}
