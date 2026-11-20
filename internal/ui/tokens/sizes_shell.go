package tokens

import "gioui.org/unit"

// ShellSizes are the fixed sizes of the window shell: the header toolbar
// with its popups and the workbench template (splits, drag and drop).
// They are tokens like Sizes so no organism or template hard-codes dp
// values (U10, D497).
type ShellSizes struct {
	// Header toolbar popups.
	ProjectPopupW unit.Dp // width of the project switcher popup
	BranchPopupW  unit.Dp // width of the branch popup
	RunPopupW     unit.Dp // width of the run configuration popup
	SubMenuW      unit.Dp // width of a popup submenu
	PopupMaxH     unit.Dp // default maximum popup height
	// Caps of the truncated names in the header toolbar.
	ProjectNameCap unit.Dp
	BranchNameCap  unit.Dp
	ConfigNameCap  unit.Dp
	// FallbackW is the width of a full-width bar laid out without a
	// width constraint (tests, measuring).
	FallbackW unit.Dp

	// Workbench splits.
	MinSidePane  unit.Dp // minimum width of a left/right panel and the editor next to it
	MinOtherPane unit.Dp // minimum size of the bottom panel and divided panes
	// Drag and drop of tool windows.
	DragThreshold  unit.Dp // distance a press moves before it becomes a drag
	FullWidthStrip unit.Dp // height of the "full width" drop zone at the bottom edge
	DropOutline    unit.Dp // outline of the drop preview
	PointerGap     unit.Dp // distance of the drag label from the pointer
	TooltipMaxW    unit.Dp // maximum width of tooltip bubbles
	TabMark        unit.Dp // thickness of the active tab underline in panel headers

	// Multi-column tool windows (Debug).
	PanelColumnMin  unit.Dp // minimum column width; narrower windows stack the columns
	PanelSectionMin unit.Dp // minimum height of a stacked section
	// PanelFallback is the size of a tool window laid out without
	// constraints (tests, measuring).
	PanelFallbackW, PanelFallbackH unit.Dp
}

// DefaultShell returns the shell sizes of the default theme.
func DefaultShell() ShellSizes {
	return ShellSizes{
		ProjectPopupW: 340, BranchPopupW: 360, RunPopupW: 300, SubMenuW: 260, PopupMaxH: 480,
		ProjectNameCap: 200, BranchNameCap: 240, ConfigNameCap: 200, FallbackW: 1200,
		MinSidePane: 160, MinOtherPane: 80,
		DragThreshold: 6, FullWidthStrip: 24, DropOutline: 2, PointerGap: 14, TooltipMaxW: 320, TabMark: 2,
		PanelColumnMin: 220, PanelSectionMin: 56, PanelFallbackW: 720, PanelFallbackH: 420,
	}
}

// Shell returns the shell sizes of the theme.
func (th *Theme) Shell() ShellSizes { return DefaultShell() }
