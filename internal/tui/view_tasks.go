package tui

import (
	"fmt"
	"strings"
)

func (m model) renderTasksWorkbenchText(t theme, layout uiLayout) string {
	width := layout.MainWidth - 6
	if layout.Compact {
		width = layout.Width - 6
	}
	intro := []string{
		t.panelSubtle.Render("Task queue operations and retry control"),
		t.panelSubtle.Render("workspace/status filter + task table"),
	}
	primary := []string{
		t.panelSubtle.Render("workspace"),
		m.taskWorkspaceInput.View(),
		"",
		fillLine(
			"filter "+taskFilterLabel(m.taskStatusFilter),
			fmt.Sprintf("failed %d", m.dashboard.TasksFailed),
			width,
		),
		"",
		m.tasksTable.View(),
	}
	tail := []string{t.panelSubtle.Render("actions: enter refresh | [ ] filter | y retry failed")}
	if strings.TrimSpace(m.errorText) != "" {
		tail = append(tail, t.panelError.Render("error: "+m.errorText))
	}
	return renderWorkbenchRhythm(intro, primary, tail)
}

func (m model) renderTasksInspectorText() string {
	selected, ok := m.selectedTask()
	if !ok {
		return strings.Join([]string{
			"Task Detail",
			"",
			"load a workspace and select a task",
		}, "\n")
	}

	lines := []string{
		"Task Detail",
		"",
		"title      " + fallbackText(selected.Title, "untitled"),
		"id         " + fallbackText(selected.ID, "n/a"),
		"workspace  " + fallbackText(selected.WorkspaceID, "n/a"),
		"kind       " + fallbackText(selected.Kind, "n/a"),
		"status     " + fallbackText(selected.Status, "unknown"),
		fmt.Sprintf("attempts   %d", selected.Attempts),
		"created    " + formatUnix(selected.CreatedAtUnix),
		"updated    " + formatUnix(selected.UpdatedAtUnix),
	}
	if strings.TrimSpace(selected.ResultPath) != "" {
		lines = append(lines, "output     "+selected.ResultPath)
	}
	if strings.TrimSpace(selected.ResultSummary) != "" {
		lines = append(lines, "summary    "+selected.ResultSummary)
	}
	if strings.TrimSpace(selected.ErrorMessage) != "" {
		lines = append(lines, "error      "+selected.ErrorMessage)
	}
	if m.taskRetryMsg != nil {
		lines = append(lines,
			"",
			"Last Retry",
			"new task   "+fallbackText(m.taskRetryMsg.TaskID, "n/a"),
			"retry of   "+fallbackText(m.taskRetryMsg.RetryOfTask, "n/a"),
		)
	}
	return strings.Join(lines, "\n")
}
