package tui

import "charm.land/lipgloss/v2"

type theme struct {
	appBG lipgloss.Style

	brand lipgloss.Style

	headerBox lipgloss.Style
	headerTxt lipgloss.Style
	headerSub lipgloss.Style
	headerOK  lipgloss.Style
	headerErr lipgloss.Style
	headerRun lipgloss.Style

	sidebarBox      lipgloss.Style
	sidebarBoxFocus lipgloss.Style
	sidebarTitle    lipgloss.Style
	sidebarItem     lipgloss.Style
	sidebarActive   lipgloss.Style
	sidebarInactive lipgloss.Style

	panelBox      lipgloss.Style
	panelBoxFocus lipgloss.Style
	panelTitle    lipgloss.Style
	panelSubtle   lipgloss.Style
	panelAccent   lipgloss.Style
	panelWarn     lipgloss.Style
	panelError    lipgloss.Style
	panelSuccess  lipgloss.Style

	footerBox  lipgloss.Style
	footerInfo lipgloss.Style
	footerErr  lipgloss.Style
	footerWarn lipgloss.Style
	footerOK   lipgloss.Style
	footerKey  lipgloss.Style

	chipInfo    lipgloss.Style
	chipWarn    lipgloss.Style
	chipError   lipgloss.Style
	chipSuccess lipgloss.Style

	cardBox   lipgloss.Style
	cardValue lipgloss.Style
	cardLabel lipgloss.Style

	inputPrompt      lipgloss.Style
	inputText        lipgloss.Style
	inputPlaceholder lipgloss.Style

	tableHeader   lipgloss.Style
	tableCell     lipgloss.Style
	tableSelected lipgloss.Style

	spinner lipgloss.Style
}

func newTheme() theme {
	border := lipgloss.Color("238")
	text := lipgloss.Color("252")
	muted := lipgloss.Color("246")
	subtle := lipgloss.Color("243")
	accent := lipgloss.Color("111")
	success := lipgloss.Color("78")
	warn := lipgloss.Color("214")
	danger := lipgloss.Color("203")

	return theme{
		appBG: lipgloss.NewStyle().Foreground(text),
		brand: lipgloss.NewStyle().
			Bold(true).
			Foreground(accent),

		headerBox: lipgloss.NewStyle().
			Border(lipgloss.NormalBorder(), false, false, true, false).
			BorderForeground(border).
			Padding(0, 1),
		headerTxt: lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("255")),
		headerSub: lipgloss.NewStyle().Foreground(muted),
		headerOK:  lipgloss.NewStyle().Bold(true).Foreground(success),
		headerErr: lipgloss.NewStyle().Bold(true).Foreground(danger),
		headerRun: lipgloss.NewStyle().Bold(true).Foreground(warn),

		sidebarBox: lipgloss.NewStyle().
			Padding(0, 1),
		sidebarBoxFocus: lipgloss.NewStyle().
			Padding(0, 1),
		sidebarTitle: lipgloss.NewStyle().Bold(true).Foreground(accent),
		sidebarItem: lipgloss.NewStyle().
			Foreground(text).
			Padding(0, 1),
		sidebarActive: lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("255")).
			Underline(true).
			Padding(0, 1),
		sidebarInactive: lipgloss.NewStyle().Foreground(subtle),

		panelBox: lipgloss.NewStyle().
			Padding(0, 1),
		panelBoxFocus: lipgloss.NewStyle().
			Padding(0, 1),
		panelTitle:   lipgloss.NewStyle().Bold(true).Foreground(accent),
		panelSubtle:  lipgloss.NewStyle().Foreground(muted),
		panelAccent:  lipgloss.NewStyle().Foreground(lipgloss.Color("151")),
		panelWarn:    lipgloss.NewStyle().Foreground(warn),
		panelError:   lipgloss.NewStyle().Foreground(danger),
		panelSuccess: lipgloss.NewStyle().Foreground(success),

		footerBox: lipgloss.NewStyle().
			Border(lipgloss.NormalBorder(), true, false, false, false).
			BorderForeground(border).
			Padding(0, 1),
		footerInfo: lipgloss.NewStyle().Foreground(text),
		footerErr:  lipgloss.NewStyle().Bold(true).Foreground(danger),
		footerWarn: lipgloss.NewStyle().Bold(true).Foreground(warn),
		footerOK:   lipgloss.NewStyle().Bold(true).Foreground(success),
		footerKey:  lipgloss.NewStyle().Bold(true).Foreground(accent),

		chipInfo: lipgloss.NewStyle().
			Bold(true).
			Foreground(accent),
		chipWarn: lipgloss.NewStyle().
			Bold(true).
			Foreground(warn),
		chipError: lipgloss.NewStyle().
			Bold(true).
			Foreground(danger),
		chipSuccess: lipgloss.NewStyle().
			Bold(true).
			Foreground(success),

		cardBox:   lipgloss.NewStyle(),
		cardValue: lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("255")),
		cardLabel: lipgloss.NewStyle().Foreground(muted),

		inputPrompt:      lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("147")),
		inputText:        lipgloss.NewStyle().Foreground(lipgloss.Color("255")),
		inputPlaceholder: lipgloss.NewStyle().Foreground(subtle),

		tableHeader: lipgloss.NewStyle().
			Bold(true).
			Foreground(accent).
			Padding(0, 1),
		tableCell: lipgloss.NewStyle().
			Foreground(text).
			Padding(0, 1),
		tableSelected: lipgloss.NewStyle().
			Bold(true).
			Foreground(accent),

		spinner: lipgloss.NewStyle().Bold(true).Foreground(warn),
	}
}
