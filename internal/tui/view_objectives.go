package tui

import (
	"fmt"
	"strings"
)

func (m model) renderObjectivesWorkbenchText(t theme, layout uiLayout) string {
	width := layout.MainWidth - 6
	if layout.Compact {
		width = layout.Width - 6
	}
	intro := []string{
		t.panelSubtle.Render("Objective lifecycle and scheduling state"),
		t.panelSubtle.Render("workspace filter + objective table"),
	}
	primary := []string{
		t.panelSubtle.Render("workspace"),
		m.objectiveWorkspaceInput.View(),
		"",
		fillLine(
			fmt.Sprintf("items %d", len(m.objectives)),
			fmt.Sprintf("healthy %d", maxInt(0, len(m.objectives)-m.dashboard.ObjectivesFailed)),
			width,
		),
		"",
		m.objectivesTable.View(),
	}
	tail := []string{t.panelSubtle.Render("actions: enter refresh | p pause/resume | x delete")}
	if strings.TrimSpace(m.errorText) != "" {
		tail = append(tail, t.panelError.Render("error: "+m.errorText))
	}
	return renderWorkbenchRhythm(intro, primary, tail)
}

func (m model) renderObjectivesInspectorText() string {
	selected, ok := m.selectedObjective()
	if !ok {
		return strings.Join([]string{
			"Objective Detail",
			"",
			"load a workspace and select an objective",
		}, "\n")
	}

	successRate := "0%"
	if selected.RunCount > 0 {
		successRate = fmt.Sprintf("%.0f%%", float64(selected.SuccessCount)/float64(selected.RunCount)*100)
	}

	lines := []string{
		"Objective Detail",
		"",
		"title      " + fallbackText(selected.Title, "untitled"),
		"id         " + fallbackText(selected.ID, "n/a"),
		"workspace  " + fallbackText(selected.WorkspaceID, "n/a"),
		"trigger    " + fallbackText(selected.TriggerType, "n/a"),
		"timezone   " + fallbackText(selected.Timezone, "UTC"),
		"state      " + map[bool]string{true: "active", false: "paused"}[selected.Active],
		"",
		fmt.Sprintf("runs       %d", selected.RunCount),
		fmt.Sprintf("success    %d", selected.SuccessCount),
		fmt.Sprintf("failure    %d", selected.FailureCount),
		"success    " + successRate,
		"avg dur    " + humanDurationMs(selected.AvgRunDurationMs),
		"next run   " + formatUnixPtr(selected.NextRunUnix),
		"last run   " + formatUnixPtr(selected.LastRunUnix),
	}
	if strings.TrimSpace(selected.LastError) != "" {
		lines = append(lines, "", "Last Error", selected.LastError)
	}
	return strings.Join(lines, "\n")
}
