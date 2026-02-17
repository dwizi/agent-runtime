package tui

import (
	"charm.land/bubbles/v2/key"
)

type keyMap struct {
	Quit       key.Binding
	FocusNext  key.Binding
	FocusPrev  key.Binding
	Activate   key.Binding
	Up         key.Binding
	Down       key.Binding
	Refresh    key.Binding
	ToggleHelp key.Binding

	View1 key.Binding
	View2 key.Binding
	View3 key.Binding
	View4 key.Binding
	View5 key.Binding

	PairApprove  key.Binding
	PairDeny     key.Binding
	PairingNew   key.Binding
	PairRolePrev key.Binding
	PairRoleNext key.Binding

	ObjectiveToggle key.Binding
	ObjectiveDelete key.Binding

	TaskRetry      key.Binding
	TaskFilterPrev key.Binding
	TaskFilterNext key.Binding
}

func newKeyMap() keyMap {
	return keyMap{
		Quit: key.NewBinding(
			key.WithKeys("q", "ctrl+c"),
			key.WithHelp("q", "quit"),
		),
		FocusNext: key.NewBinding(
			key.WithKeys("tab"),
			key.WithHelp("tab", "next focus"),
		),
		FocusPrev: key.NewBinding(
			key.WithKeys("shift+tab"),
			key.WithHelp("shift+tab", "prev focus"),
		),
		Activate: key.NewBinding(
			key.WithKeys("enter"),
			key.WithHelp("enter", "activate"),
		),
		Up: key.NewBinding(
			key.WithKeys("k", "up"),
			key.WithHelp("k/up", "move up"),
		),
		Down: key.NewBinding(
			key.WithKeys("j", "down"),
			key.WithHelp("j/down", "move down"),
		),
		Refresh: key.NewBinding(
			key.WithKeys("r"),
			key.WithHelp("r", "refresh"),
		),
		ToggleHelp: key.NewBinding(
			key.WithKeys("?"),
			key.WithHelp("?", "toggle help"),
		),
		View1: key.NewBinding(
			key.WithKeys("1"),
			key.WithHelp("1", "overview"),
		),
		View2: key.NewBinding(
			key.WithKeys("2"),
			key.WithHelp("2", "pairings"),
		),
		View3: key.NewBinding(
			key.WithKeys("3"),
			key.WithHelp("3", "objectives"),
		),
		View4: key.NewBinding(
			key.WithKeys("4"),
			key.WithHelp("4", "tasks"),
		),
		View5: key.NewBinding(
			key.WithKeys("5"),
			key.WithHelp("5", "activity"),
		),
		PairApprove: key.NewBinding(
			key.WithKeys("a"),
			key.WithHelp("a", "approve pairing"),
		),
		PairDeny: key.NewBinding(
			key.WithKeys("d"),
			key.WithHelp("d", "deny pairing"),
		),
		PairingNew: key.NewBinding(
			key.WithKeys("n"),
			key.WithHelp("n", "new token"),
		),
		PairRolePrev: key.NewBinding(
			key.WithKeys("["),
			key.WithHelp("[", "prev role/filter"),
		),
		PairRoleNext: key.NewBinding(
			key.WithKeys("]"),
			key.WithHelp("]", "next role/filter"),
		),
		ObjectiveToggle: key.NewBinding(
			key.WithKeys("p"),
			key.WithHelp("p", "pause/resume"),
		),
		ObjectiveDelete: key.NewBinding(
			key.WithKeys("x"),
			key.WithHelp("x", "delete objective"),
		),
		TaskRetry: key.NewBinding(
			key.WithKeys("y"),
			key.WithHelp("y", "retry task"),
		),
		TaskFilterPrev: key.NewBinding(
			key.WithKeys("["),
			key.WithHelp("[", "prev filter"),
		),
		TaskFilterNext: key.NewBinding(
			key.WithKeys("]"),
			key.WithHelp("]", "next filter"),
		),
	}
}

func (k keyMap) ShortHelp() []key.Binding {
	return []key.Binding{
		k.FocusNext,
		k.Activate,
		k.Up,
		k.Down,
		k.Refresh,
		k.ToggleHelp,
		k.Quit,
	}
}

func (k keyMap) FullHelp() [][]key.Binding {
	return [][]key.Binding{
		{k.FocusNext, k.FocusPrev, k.Activate, k.Refresh, k.ToggleHelp, k.Quit},
		{k.View1, k.View2, k.View3, k.View4, k.View5},
		{k.PairApprove, k.PairDeny, k.PairingNew, k.PairRolePrev, k.PairRoleNext},
		{k.ObjectiveToggle, k.ObjectiveDelete, k.TaskRetry, k.TaskFilterPrev, k.TaskFilterNext},
	}
}
