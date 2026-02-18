package tui

import (
	"fmt"
	"strings"

	"charm.land/lipgloss/v2"
)

func (m model) renderView() string {
	if m.quitting {
		return "agent-runtime tui closed\n"
	}

	t := newTheme()
	layout := computeLayout(m.width, m.height)
	if layout.Compact {
		return m.renderCompactView(t, layout)
	}

	header := m.renderHeader(t, layout)
	sidebar := m.renderSidebar(t, layout)
	workbench := m.renderWorkbench(t, layout)
	inspector := m.renderInspector(t, layout)
	footer := m.renderFooter(t, layout)

	sep := t.panelSubtle.Render("│")
	body := lipgloss.JoinHorizontal(lipgloss.Top, sidebar, sep, workbench, sep, inspector)
	ui := lipgloss.JoinVertical(lipgloss.Left, header, body, footer)
	return t.appBG.Width(layout.Width).Height(layout.Height).Render(ui)
}

func (m model) renderCompactView(t theme, layout uiLayout) string {
	header := m.renderHeader(t, layout)
	nav := m.renderSidebar(t, layout)
	main := m.renderWorkbench(t, layout)
	inspector := m.renderInspector(t, layout)
	footer := m.renderFooter(t, layout)

	content := lipgloss.JoinVertical(lipgloss.Left, header, nav, main, inspector, footer)
	return t.appBG.Width(layout.Width).Height(layout.Height).Render(content)
}

func (m model) renderHeader(t theme, layout uiLayout) string {
	statusChip := t.chipSuccess.Render("READY")
	if m.errorText != "" {
		statusChip = t.chipError.Render("ERROR")
	} else if m.busy() {
		statusChip = t.chipWarn.Render(m.spinner.View() + " BUSY")
	}

	style := sizedStyle(t.headerBox, layout.Width, layout.HeaderHeight)
	contentWidth := innerWidth(t.headerBox, layout.Width)

	line1 := fillLine(t.brand.Render("Agent Runtime Control Plane"), statusChip, contentWidth)
	line2 := fillLine(
		t.headerSub.Render(trimToWidth("env: "+fallbackText(m.cfg.Environment, "unset")+" | focus: "+focusLabel(m.focus), maxInt(20, contentWidth/2))),
		t.headerSub.Render(trimToWidth("approver: "+fallbackText(m.cfg.TUIApproverUserID, "unset")+" | utc "+m.clock.UTC().Format("15:04:05"), maxInt(20, contentWidth/2))),
		contentWidth,
	)

	return style.Render(strings.Join([]string{line1, line2}, "\n"))
}

func (m model) renderSidebar(t theme, layout uiLayout) string {
	style := t.sidebarBox

	if layout.Compact {
		items := make([]string, 0, len(allViews()))
		for i, view := range allViews() {
			label := fmt.Sprintf("%d:%s", i+1, viewLabel(view))
			if view == m.activeView {
				label = t.sidebarActive.Render(label)
			} else {
				label = t.sidebarItem.Render(label)
			}
			items = append(items, label)
		}
		line := strings.Join(items, "  ")
		if m.focus == focusSidebar {
			line = paneLabel("nav", true) + " " + line
		}
		return sizedStyle(style, layout.Width, layout.CompactSidebarHeight).Render(trimToWidth(line, innerWidth(style, layout.Width)))
	}

	lines := []string{t.sidebarTitle.Render(paneLabel("Navigation", m.focus == focusSidebar)), ""}
	for index, view := range allViews() {
		cursor := " "
		if index == m.sidebarIndex {
			cursor = ">"
		}
		label := fmt.Sprintf("%s %d. %s", cursor, index+1, viewLabel(view))
		style := t.sidebarItem
		if view == m.activeView {
			style = t.sidebarActive
		}
		lines = append(lines, style.Render(trimToWidth(label, innerWidth(t.sidebarBox, layout.SidebarWidth)-2)))
	}

	lines = append(lines, "", t.sidebarInactive.Render("tab: next focus"), t.sidebarInactive.Render("enter: activate"))

	return sizedStyle(style, layout.SidebarWidth, layout.BodyHeight).Render(strings.Join(lines, "\n"))
}

func (m model) renderWorkbench(t theme, layout uiLayout) string {
	var title string
	var content string

	switch m.activeView {
	case viewPairings:
		title = "Pairings"
		content = m.renderPairingsWorkbenchText(t, layout)
	case viewObjectives:
		title = "Objectives"
		content = m.renderObjectivesWorkbenchText(t, layout)
	case viewTasks:
		title = "Tasks"
		content = m.renderTasksWorkbenchText(t, layout)
	case viewActivity:
		title = "Activity"
		content = m.renderActivityWorkbenchText(t, layout)
	default:
		title = "Overview"
		content = m.renderOverviewWorkbenchText(t, layout)
	}

	bodyWidth := layout.MainWidth
	bodyHeight := layout.BodyHeight
	if layout.Compact {
		bodyWidth = layout.Width
		bodyHeight = layout.CompactMainHeight
	}

	style := t.panelBox
	titleStyle := t.panelTitle
	if m.focus == focusWorkbench {
		titleStyle = t.panelAccent.Copy().Bold(true)
	}
	header := fillLine(titleStyle.Render(paneLabel(title, m.focus == focusWorkbench)), t.panelSubtle.Render(viewSubtitle(m.activeView)), innerWidth(style, bodyWidth))
	return sizedStyle(style, bodyWidth, bodyHeight).Render(header + "\n" + content)
}

func (m model) renderInspector(t theme, layout uiLayout) string {
	title := "Inspector"
	if m.activeView == viewActivity {
		title = "Session"
	}

	width := layout.InspectorWidth
	height := layout.BodyHeight
	if layout.Compact {
		width = layout.Width
		height = layout.CompactInspectorHeight
	}

	style := t.panelBox
	titleStyle := t.panelTitle
	if m.focus == focusInspector {
		titleStyle = t.panelAccent.Copy().Bold(true)
	}
	head := fillLine(titleStyle.Render(paneLabel(title, m.focus == focusInspector)), t.panelSubtle.Render(string(m.activeView)), innerWidth(style, width))
	return sizedStyle(style, width, height).Render(head + "\n" + m.inspectorViewport.View())
}

func (m model) renderFooter(t theme, layout uiLayout) string {
	status := "status: " + fallbackText(m.statusText, "idle")
	statusStyled := t.footerOK.Render(status)
	if m.busy() {
		statusStyled = t.footerWarn.Render(status)
	}
	if strings.TrimSpace(m.errorText) != "" {
		statusStyled = t.footerErr.Render("status: " + m.errorText)
	}

	style := t.footerBox

	helpPrefix := ""
	if m.focus == focusHelp {
		helpPrefix = t.footerKey.Render("› ")
	}
	helpLine := t.footerInfo.Render(helpPrefix + m.help.View(m.keys))

	startup := ""
	if strings.TrimSpace(m.startupInfo) != "" {
		startup = "\n" + t.footerWarn.Render(trimToWidth("startup: "+m.startupInfo, innerWidth(style, layout.Width)))
	}

	return sizedStyle(style, layout.Width, layout.FooterHeight).Render(helpLine + "\n" + trimToWidth(statusStyled, innerWidth(style, layout.Width)) + startup)
}

func fillLine(left, right string, width int) string {
	if width <= 0 {
		return strings.TrimSpace(left + " " + right)
	}
	lw := lipgloss.Width(left)
	rw := lipgloss.Width(right)
	if lw+rw+1 > width {
		return trimToWidth(left+" "+right, width)
	}
	return left + strings.Repeat(" ", width-lw-rw) + right
}

func trimToWidth(value string, width int) string {
	if width <= 0 {
		return ""
	}
	runes := []rune(strings.TrimSpace(value))
	if len(runes) <= width {
		return string(runes)
	}
	if width <= 3 {
		return string(runes[:width])
	}
	return string(runes[:width-3]) + "..."
}

func sizedStyle(style lipgloss.Style, width, height int) lipgloss.Style {
	contentWidth := maxInt(1, width-style.GetHorizontalFrameSize())
	contentHeight := maxInt(1, height-style.GetVerticalFrameSize())
	return style.Width(contentWidth).Height(contentHeight)
}

func innerWidth(style lipgloss.Style, width int) int {
	return maxInt(1, width-style.GetHorizontalFrameSize())
}

func viewSubtitle(view viewID) string {
	switch view {
	case viewPairings:
		return "connector approvals"
	case viewObjectives:
		return "objective lifecycle"
	case viewTasks:
		return "task operations"
	case viewActivity:
		return "session event feed"
	default:
		return "runtime health"
	}
}

func paneLabel(label string, focused bool) string {
	if focused {
		return "› " + label
	}
	return "  " + label
}
