// Package ui is the root of Tiegel's Atomic Design component system.
//
// # Layers
//
//   - tokens:    design tokens – colors (light/dark), fonts, text sizes,
//     4dp spacing grid, radii, component sizes. The only source of colors
//     and measurements.
//   - atoms:     Label, Icon/Glyph, Button, IconButton (optionally an Active
//     toggle), TextField, Checkbox, Badge, Tooltip/TooltipBubble, Divider,
//     Surface.
//   - molecules: SearchField, FileRow, Tab, BreakpointRow, DiffBlock,
//     CodeLine, ListRow, TreeRow, Banner, ScrollList, Split, Dropdown
//     (menu button or select field), Toolbar, SectionHeader, Table.
//   - organisms: git (CommitPanel, DiffView, LogView, BranchList,
//     MergeView), debug (DebugToolbar, CallStackList, VariablesTree,
//     WatchesList, BreakpointsList, SessionList and the DebugPanel
//     template), runtime (InterpreterPicker, PathMappingEditor,
//     XdebugHintsList, StatusbarVersion), filetree, tabs, statusbar,
//     findbar, completion (completion list, info popup), problems,
//     palette (choosers), settings (dialog), welcome.
//   - templates: window layouts; Workbench is the main window (sidebar,
//     tabs, editor, tool window area, right panel, status bar, overlay).
//   - editorview: the code editor widget (virtual rendering, gutter,
//     keymap-driven input, IME); see that package.
//
// Modules and plugins build their UI only from these packages and never
// paint directly or hard-code colors (see CLAUDE.md).
//
// # Component convention
//
// Every component implements [Component]:
//
//	Layout(gtx layout.Context, th *tokens.Theme) layout.Dimensions
//
// The theme is passed on every call instead of being stored, so switching
// light/dark is just passing another *tokens.Theme; widget state survives.
//
// There are two kinds of components:
//
//   - Value components (Label, Icon, Badge, Divider, TooltipBubble) are
//     plain structs without input state. Build them inline each frame,
//     either as literals or via small constructors (atoms.Text("…"),
//     atoms.Muted("…"), atoms.NewIcon(g)). Fn(th) adapts them to a
//     layout.Widget for use inside Gio layouts.
//
//   - Stateful components (Button, IconButton, TextField, Checkbox, Tooltip,
//     SearchField, FileRow, Tab, BreakpointRow, DiffBlock) hold Gio widget
//     state (widget.Clickable, widget.Editor, …) in unexported fields. Keep
//     them in your own state – as pointers or struct fields – and never
//     copy them after first use. Configuration lives in exported fields
//     (Text, Kind, Disabled, …) and may change between frames. Constructors
//     named NewX set sensible defaults; the zero value is valid too.
//
// Input handling follows Gio's pattern: a component exposes Update(gtx)
// (or Clicked(gtx) for plain buttons) returning what happened since the
// last frame. Layout calls Update itself so a component also works when the
// caller ignores events; call Update *before* Layout when you need them.
//
// Organisms take their data through exported fields (plain view-model
// types of the organism package) and report user intentions as a slice of
// package-specific events: Update(gtx) []Event. They never call backend
// services; the host applies the events to its data and feeds the result
// back in the next frame. Purely visual state (selection, expanded nodes,
// split ratios, inline editors) stays inside the organism.
//
// Wrapper components that decorate or arrange arbitrary content
// (atoms.Tooltip, atoms.Surface, molecules.ListRow, molecules.Split,
// molecules.ScrollList) take the content as additional last parameters:
//
//	Layout(gtx, th, content layout.Widget)
//
// Block components (TextField, FileRow, BreakpointRow, DiffBlock, Divider)
// fill the maximum width of their constraints; inline components (Button,
// Badge, Tab, Checkbox, Label, Icon) take their intrinsic size. No component
// exceeds its constraints.
//
// The catalog app (cmd/tiegel-catalog) shows every component with its
// variants in both themes and is the living reference.
package ui
