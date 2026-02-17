package tui

import (
	"fmt"
	"strings"

	"charm.land/lipgloss/v2"
)

func (m model) renderOverviewWorkbenchText(t theme, layout uiLayout) string {
	contentWidth := layout.MainWidth - 6
	if layout.Compact {
		contentWidth = layout.Width - 6
	}
	contentWidth = maxInt(36, contentWidth)
	colWidth := maxInt(10, (contentWidth-4)/3)
	colStyle := lipgloss.NewStyle().Width(colWidth)

	objectivesCard := colStyle.Render(strings.Join([]string{
		t.cardLabel.Render("Objectives"),
		t.cardValue.Render(fmt.Sprintf("%d", m.dashboard.ObjectivesTotal)),
		t.panelSubtle.Render(fmt.Sprintf("active %d  unhealthy %d", m.dashboard.ObjectivesActive, m.dashboard.ObjectivesFailed)),
	}, "\n"))
	tasksCard := colStyle.Render(strings.Join([]string{
		t.cardLabel.Render("Tasks"),
		t.cardValue.Render(fmt.Sprintf("%d", m.dashboard.TasksTotal)),
		t.panelSubtle.Render(fmt.Sprintf("running %d  failed %d", m.dashboard.TasksRunning, m.dashboard.TasksFailed)),
	}, "\n"))
	successCard := colStyle.Render(strings.Join([]string{
		t.cardLabel.Render("Delivery"),
		t.cardValue.Render(fmt.Sprintf("%d", m.dashboard.TasksSucceeded)),
		t.panelSubtle.Render(fmt.Sprintf("queued %d", m.dashboard.TasksQueued)),
	}, "\n"))

	intro := []string{
		t.panelSubtle.Render("Operational snapshot from current objectives/tasks filters"),
		t.panelSubtle.Render("Control-plane health and queue mix"),
	}
	primary := []string{
		lipgloss.JoinHorizontal(lipgloss.Top, objectivesCard, " ", tasksCard, " ", successCard),
		"",
		t.panelSubtle.Render("Context"),
		fmt.Sprintf("objective workspace  %s", t.panelAccent.Render(strings.TrimSpace(m.objectiveWorkspaceInput.Value()))),
		fmt.Sprintf("task workspace       %s", t.panelAccent.Render(strings.TrimSpace(m.taskWorkspaceInput.Value()))),
		fmt.Sprintf("task filter          %s", t.chipInfo.Render(taskFilterLabel(m.taskStatusFilter))),
	}
	tail := []string{
		t.panelSubtle.Render("Quick Hints"),
		"2 pairings  3 objectives  4 tasks  r refresh",
	}
	if !m.dashboard.LastRefresh.IsZero() {
		tail = append(tail, "", t.panelSubtle.Render("last refresh: "+m.dashboard.LastRefresh.UTC().Format("2006-01-02 15:04:05 MST")))
	}
	return renderWorkbenchRhythm(intro, primary, tail)
}

func (m model) renderOverviewInspectorText() string {
	lines := []string{
		"health summary",
		"",
		fmt.Sprintf("objectives active: %d/%d", m.dashboard.ObjectivesActive, m.dashboard.ObjectivesTotal),
		fmt.Sprintf("objectives unhealthy: %d", m.dashboard.ObjectivesFailed),
		"",
		fmt.Sprintf("tasks queued: %d", m.dashboard.TasksQueued),
		fmt.Sprintf("tasks running: %d", m.dashboard.TasksRunning),
		fmt.Sprintf("tasks failed: %d", m.dashboard.TasksFailed),
		fmt.Sprintf("tasks succeeded: %d", m.dashboard.TasksSucceeded),
		"",
		"focus zones:",
		"sidebar | workbench | inspector | help",
	}
	if strings.TrimSpace(m.startupInfo) != "" {
		lines = append(lines, "", "startup note:", m.startupInfo)
	}
	return strings.Join(lines, "\n")
}
