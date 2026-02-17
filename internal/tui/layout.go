package tui

const (
	compactWidthBreakpoint  = 110
	compactHeightBreakpoint = 26
)

type uiLayout struct {
	Width  int
	Height int

	Compact bool

	HeaderHeight int
	FooterHeight int
	BodyHeight   int

	SidebarWidth   int
	MainWidth      int
	InspectorWidth int

	CompactSidebarHeight   int
	CompactMainHeight      int
	CompactInspectorHeight int
}

func computeLayout(width, height int) uiLayout {
	if width < 40 {
		width = 40
	}
	if height < 16 {
		height = 16
	}

	layout := uiLayout{
		Width:        width,
		Height:       height,
		HeaderHeight: 4,
		FooterHeight: 4,
	}

	layout.Compact = width < compactWidthBreakpoint || height < compactHeightBreakpoint
	if layout.Compact {
		layout.SidebarWidth = width
		layout.MainWidth = width
		layout.InspectorWidth = width
		remaining := maxInt(7, height-layout.HeaderHeight-layout.FooterHeight)
		layout.CompactSidebarHeight = 3
		remaining -= layout.CompactSidebarHeight
		if remaining < 6 {
			remaining = 6
		}
		layout.CompactInspectorHeight = maxInt(4, remaining/3)
		layout.CompactMainHeight = maxInt(5, remaining-layout.CompactInspectorHeight)
		layout.BodyHeight = layout.CompactMainHeight
		return layout
	}

	layout.BodyHeight = maxInt(6, height-layout.HeaderHeight-layout.FooterHeight)
	layout.SidebarWidth = clampInt(width*18/100, 20, 30)
	layout.InspectorWidth = clampInt(width*30/100, 30, 50)
	layout.MainWidth = maxInt(30, width-layout.SidebarWidth-layout.InspectorWidth-2)
	return layout
}
