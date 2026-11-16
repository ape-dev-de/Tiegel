// Package gui is Tiegel's graphical shell: the Gio windows with the main
// workbench (editor groups with tabs, tool windows docked left, right and
// at the bottom, status bar), the welcome screen, menus, choosers and the
// settings dialog.
//
// The GUI is a client of the API (D41): all state and logic live in the
// Gio-free package controller, which calls API methods by name through a
// Backend and reacts to bus events; the View only lays out organisms from
// internal/ui and turns their events into controller calls. Editors edit
// the shared document buffers in-process (api.App.Document) under the
// document lock.
//
// # Windows and projects
//
// Every window shows one project and owns one api.App (an App holds one
// project); Run starts further windows with their own App in the same
// process, sharing the global settings (api.Options.GlobalSettings).
// Closing a window saves its documents and closes its App; the process
// ends with the last window. File → Open… uses the native folder dialog
// on macOS (package native, D110), elsewhere a folder browser built from
// the chooser. Opening a project while one is open asks "This Window" or
// "New Window" (WindowHost); a project already open in another window is
// brought to the front.
//
// # Tool windows
//
// Registry.AddToolWindow registers a panel with its default Side (Left,
// Right, Bottom), stripe icon, order and an optional shortcut (Mac/PC; the
// registry adds the toggle action under View → Tool Windows). The user's
// placement (side, open panel per side, sizes) is a controller.ToolLayout,
// persisted per project (setting ui.toolWindows). Each side shows one tool
// window at a time; Cmd+Shift+F12 hides all (editor maximized) and
// restores them, Shift+Escape hides the active one, Escape focuses the
// editor. Features use View.ShowTool/HideTool/ToggleTool/MoveTool.
//
// # Editor groups (splits)
//
// controller.Controller.Split/Unsplit build a tree of editor groups, each
// with its own tabs. The same file in two groups shares one document
// (same doc.open generation; doc.close only when its last tab closes);
// every group has its own editor widget (caret, scroll) on that buffer.
//
// # Menus and actions
//
// Actions (Registry.AddAction) carry Mac/PC chords, a scope and a menu
// place (Action.Menu "File", "View/Tool Windows", …; Section groups).
// The menu bar is built from them: native on macOS (D111, shortcuts only
// displayed, the window handles keys), an in-app bar elsewhere. Find
// Action (Cmd+Shift+A) searches all actions.
//
// # Docking point: context menus (AP10.4)
//
// Context menus are collected per place (ContextID): ContextEditor,
// ContextEditorGutter, ContextProjectTree, ContextTab and
// ContextToolWindow. Features add entries with
//
//	r.AddContextMenu(gui.ContextProjectTree, func(v *gui.View, t gui.ContextTarget) []gui.MenuItem {
//		return []gui.MenuItem{{Title: "Rename…", Run: func() { … t.Paths … }}}
//	})
//
// The shell opens them with View.ShowContextMenu(ctx, target, builtin):
// built-in entries first, then the registered ones separated by
// separators. Presentation goes through View.MenuPresenter, which shows
// the molecules.ContextMenu popup (actionmodel.go). Menus of the action
// model come from View.ShowActionMenu (the editor and gutter menus,
// editormenu.go).
//
// # Action model (D160)
//
// internal/actions is the only source of menus, Find Action and the
// native menu bar: Registry.AddAction mirrors every action into
// Registry.Model() (standard actions keep their label and state
// functions); the Registry keeps the handlers (ID → Run) and the key
// bindings. An entry of a model menu without a handler is disabled, so a
// feature docks onto existing menu entries just by registering
// Action{ID: "vcs.annotate", Run: …}.
//
// # Code navigation (D161–D164)
//
// navdock.go, codedock.go, finddock.go and symbols.go show
// controller.CodeNav: jumps, gutter marks, Go to Class/Symbol, Search
// Everywhere, Usages, Structure, Hierarchy and Find tool windows, rename
// and context action popups.
//
// # Docking point: virtual tabs (AP10.6)
//
// Editor tabs need not be files. A feature registers a tab type
//
//	r.AddTabType(gui.TabType{Kind: "diff", Glyph: atoms.GlyphSwap,
//		Layout: func(gtx layout.Context, th *tokens.Theme, tab *controller.Tab) layout.Dimensions { … tab.Data … },
//		OnClose: func(tab *controller.Tab) { … }})
//
// and opens tabs with View.OpenVirtualTab(kind, id, title, data) (or
// controller.Controller.OpenVirtual). The tab's File is the key
// "virtual:<kind>:<id>", Doc is nil; reopening the same id activates the
// tab and replaces Data. Virtual tabs take part in groups and splits, are
// not added to recent files and never call doc.* methods.
//
// # Docking points: editor banners, gutter columns, tab decorators
//
// Registry.AddEditorBanner shows a widget above the editor of a file tab
// (e.g. "File changed on disk"), AddGutterColumn adds an extra gutter
// column (the git annotations) and AddTabDecorator adjusts a tab's
// tooltip and icon.
//
// # The work dock (files, external changes, VCS, diffs)
//
// filesdock.go, externaldock.go, diffdock.go and vcsremote.go are the
// view side of controller.Workspace: the project tree's context menu and
// keys, the tab menu additions, the external change banners, the diff,
// file history and Local History tabs (tab kinds "diff", "fileHistory",
// "localHistory"), annotations and the remote git UI (D170–D174). Their
// actions include vcs.annotate, vcs.fileHistory, vcs.compareWithHead and
// vcs.rollback, which other menus (editor context menu) can reference.
//
// Further extension points (gutter markers and clicks, status items,
// per-frame handlers) are in Registry; the git feature (gitdock.go) and
// the debugger (debugdock.go) dock onto the shell only through them and
// live in controller.Git / controller.Debug.
package gui
