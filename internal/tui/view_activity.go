package tui

import (
	"fmt"
	"strings"
)

func (m model) renderActivityWorkbenchText(t theme, _ uiLayout) string {
	intro := []string{
		t.panelSubtle.Render("Session-local operator and API event feed"),
		t.panelSubtle.Render("latest events first"),
	}
	primary := []string{
		m.activityViewport.View(),
	}
	tail := []string{
		t.panelSubtle.Render("j/k or arrows to scroll. r to refresh data views."),
	}
	if strings.TrimSpace(m.errorText) != "" {
		tail = append(tail, t.panelError.Render("error: "+m.errorText))
	}
	return renderWorkbenchRhythm(intro, primary, tail)
}

func (m model) renderActivityInspectorText() string {
	lines := []string{
		"Session Detail",
		"",
		fmt.Sprintf("events      %d", len(m.activity)),
		fmt.Sprintf("loads       %d", m.pendingLoads),
		fmt.Sprintf("mutations   %d", m.pendingMutations),
		"active view " + string(m.activeView),
		"focus       " + focusLabel(m.focus),
	}
	if !m.dashboard.LastRefresh.IsZero() {
		lines = append(lines, "last refresh "+m.dashboard.LastRefresh.UTC().Format("2006-01-02 15:04:05 MST"))
	}
	if len(m.activity) > 0 {
		last := m.activity[len(m.activity)-1]
		lines = append(lines,
			"",
			"Last Event",
			last.At.UTC().Format("2006-01-02 15:04:05 MST")+" ["+strings.ToUpper(last.Level)+"]",
			last.Message,
		)
	}
	return strings.Join(lines, "\n")
}
