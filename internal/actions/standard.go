package actions

import "github.com/ape-dev-de/tiegel/internal/editor/keymap"

// IDs of the standard actions. File actions map to the files.* API
// methods; editor actions reuse the keymap action names so the editor's
// key table and the menus share shortcuts.
const (
	FileNewFile     = "file.newFile"     // files.createFile
	FileNewDir      = "file.newDir"      // files.createDir
	FileNewPHPClass = "file.newPhpClass" // files.createPhpClass
	FileRename      = "file.rename"      // files.rename
	FileDelete      = "file.delete"      // files.delete (trash)
	FileCopy        = "file.copy"        // files.copy (duplicate)
	FileReveal      = "file.reveal"      // files.reveal
	FileCopyPath    = "file.copyPath"    // files.copyPath (absolute)
	FileCopyRelPath = "file.copyRelativePath"
	FileSave        = "file.save"
	FileSaveAll     = "file.saveAll"
	FileHistory     = "file.localHistory"

	TabClose       = "tab.close"
	TabCloseOthers = "tab.closeOthers"
	TabCloseAll    = "tab.closeAll"
	TabCloseRight  = "tab.closeRight"
	TabPin         = "tab.pin"
	TabSplitRight  = "tab.splitRight"

	EditCut       = string(keymap.Cut)
	EditCopy      = string(keymap.Copy)
	EditPaste     = string(keymap.Paste)
	EditSelectAll = string(keymap.SelectAll)
	EditComment   = string(keymap.ToggleComment)
	NavDefinition = "navigate.definition"
	// EditCopyReference copies the reference of the symbol at the caret
	// (App\Room::size) or path:line; EditCopyPathLine copies path:line.
	EditCopyReference = "edit.copyReference"
	EditCopyPathLine  = "edit.copyPathLine"
	NavImplement      = "navigate.implementation"
	EditorFindUses    = "navigate.usages"

	GutterToggleBreakpoint = "debug.toggleBreakpoint"
	GutterShowLineNumbers  = "view.lineNumbers"
	GutterAnnotate         = "git.annotate"

	GitRollback      = "git.rollback"
	GitShowDiff      = "git.showDiff"
	GitStage         = "git.stage"
	GitCommitFiles   = "git.commitFiles"
	GitCopyHash      = "git.copyHash"
	GitCheckout      = "git.checkoutRevision"
	GitCherryPick    = "git.cherryPick"
	GitNewBranchHere = "git.newBranchHere"

	ToolHide     = "toolWindow.hide"
	ToolMaximize = "toolWindow.maximize"
	ToolFloat    = "toolWindow.float"
)

// hasPaths is enabled when something is selected.
func hasPaths(c Context) bool { return len(c.Paths) > 0 }

// single is enabled for exactly one selected path.
func single(c Context) bool { return c.Single() }

// php is visible for PHP documents.
func php(c Context) bool { return c.Language == "php" }

func hasValue(key string) func(Context) bool {
	return func(c Context) bool { return c.Value(key) != "" }
}

// StandardActions returns the built-in actions. Module actions (git,
// debug) are part of it so that menus can be declared in one place; the
// host hides them with Registry.Remove when the module is disabled.
func StandardActions() []Action {
	return append([]Action{
		{ID: FileNewFile, Label: "File…", Icon: "file", Keywords: []string{"new", "create"}},
		{ID: FileNewDir, Label: "Directory…", Icon: "folder", Keywords: []string{"new", "create", "folder"}},
		{ID: FileNewPHPClass, Label: "PHP Class…", Icon: "file-code", Keywords: []string{"new", "create", "interface", "trait", "enum"}},
		{ID: FileRename, Label: "Rename…", Icon: "edit", Shortcut: Shortcut{Mac: "Shift+F6", PC: "Shift+F6"}, Enabled: single, Keywords: []string{"move", "refactor"}},
		{ID: FileDelete, Label: "Delete…", Icon: "delete", Shortcut: Shortcut{Mac: "Cmd+Backspace", PC: "Delete"}, Enabled: hasPaths, Keywords: []string{"remove", "trash"}},
		{ID: FileCopy, Label: "Duplicate…", Icon: "copy", Enabled: single, Keywords: []string{"copy"}},
		{ID: FileReveal, Label: revealLabel(), Icon: "folder-open", Enabled: single, Keywords: []string{"finder", "explorer", "show"}},
		{ID: FileCopyPath, Label: "Copy Absolute Path", Icon: "copy", Shortcut: Shortcut{Mac: "Cmd+Shift+C", PC: "Ctrl+Shift+C"}, Enabled: hasPaths},
		{ID: FileCopyRelPath, Label: "Copy Path from Project Root", Enabled: hasPaths},
		{ID: FileSave, Label: "Save", Shortcut: Shortcut{Mac: "Cmd+S", PC: "Ctrl+S"}},
		{ID: FileSaveAll, Label: "Save All", Shortcut: Shortcut{Mac: "Cmd+Alt+S", PC: "Ctrl+Alt+Shift+S"}},
		{ID: FileHistory, Label: "Local History…", Icon: "history", Enabled: single},

		{ID: TabClose, Label: "Close", Icon: "close", Shortcut: Shortcut{Mac: "Cmd+W", PC: "Ctrl+F4"}},
		{ID: TabCloseOthers, Label: "Close Other Tabs"},
		{ID: TabCloseRight, Label: "Close Tabs to the Right"},
		{ID: TabCloseAll, Label: "Close All Tabs"},
		{ID: TabPin, Label: "Pin Tab", Check: CheckBox, Checked: func(c Context) bool { return c.Value("pinned") != "" }},
		{ID: TabSplitRight, Label: "Split Right", Icon: "swap"},

		{ID: EditCut, Label: "Cut", Shortcut: Shortcut{Mac: "Cmd+X", PC: "Ctrl+X"}, Enabled: func(c Context) bool { return c.Selection }},
		{ID: EditCopy, Label: "Copy", Icon: "copy", Shortcut: Shortcut{Mac: "Cmd+C", PC: "Ctrl+C"}, Enabled: func(c Context) bool { return c.Selection }},
		{ID: EditPaste, Label: "Paste", Shortcut: Shortcut{Mac: "Cmd+V", PC: "Ctrl+V"}},
		{ID: EditSelectAll, Label: "Select All", Shortcut: Shortcut{Mac: "Cmd+A", PC: "Ctrl+A"}},
		{ID: EditComment, Label: "Comment Line", Shortcut: Shortcut{Mac: "Cmd+/", PC: "Ctrl+/"}},
		{ID: NavDefinition, Label: "Go to Definition", Shortcut: Shortcut{Mac: "Cmd+B", PC: "Ctrl+B"}, Enabled: php},
		{ID: NavImplement, Label: "Go to Implementation", Shortcut: Shortcut{Mac: "Cmd+Alt+B", PC: "Ctrl+Alt+B"}, Enabled: php},
		{ID: EditorFindUses, Label: "Find Usages", Shortcut: Shortcut{Mac: "Alt+F7", PC: "Alt+F7"}, Enabled: phpLSP},

		{ID: GutterToggleBreakpoint, Label: "Toggle Breakpoint", Icon: "debug", Shortcut: Shortcut{Mac: "Cmd+F8", PC: "Ctrl+F8"}, Visible: php},
		{ID: GutterShowLineNumbers, Label: "Show Line Numbers", Check: CheckBox, Checked: func(c Context) bool { return c.Value("lineNumbers") != "" }},
		{ID: GutterAnnotate, Label: "Annotate with Git Blame", Icon: "branch", Visible: hasValue("git")},

		{ID: GitShowDiff, Label: "Show Diff", Shortcut: Shortcut{Mac: "Cmd+D", PC: "Ctrl+D"}, Enabled: single},
		{ID: GitRollback, Label: "Rollback…", Icon: "history", Shortcut: Shortcut{Mac: "Cmd+Alt+Z", PC: "Ctrl+Alt+Z"}, Enabled: hasPaths},
		{ID: GitStage, Label: "Add to VCS", Icon: "add", Enabled: hasPaths},
		{ID: GitCommitFiles, Label: "Commit Files…", Icon: "commit", Enabled: hasPaths},
		{ID: GitCopyHash, Label: "Copy Revision Number", Icon: "copy", Enabled: hasValue("commit")},
		{ID: GitCheckout, Label: "Checkout Revision", Enabled: hasValue("commit")},
		{ID: GitCherryPick, Label: "Cherry-Pick", Enabled: hasValue("commit")},
		{ID: GitNewBranchHere, Label: "New Branch…", Icon: "branch", Enabled: hasValue("commit")},

		{ID: ToolHide, Label: "Hide", Shortcut: Shortcut{Mac: "Shift+Escape", PC: "Shift+Escape"}},
		{ID: ToolMaximize, Label: "Maximize", Check: CheckBox, Checked: func(c Context) bool { return c.Value("maximized") != "" }},
		{ID: ToolFloat, Label: "Floating Window", Check: CheckBox, Checked: func(c Context) bool { return c.Value("floating") != "" }},
	}, editorActions()...)
}

// revealLabel is the platform name of "show in file manager".
func revealLabel() string { return "Reveal in File Manager" }

// RevealLabel returns the platform label of FileReveal ("Reveal in
// Finder" on macOS, "Show in Explorer" on Windows).
func RevealLabel(goos string) string {
	switch goos {
	case "darwin":
		return "Reveal in Finder"
	case "windows":
		return "Show in Explorer"
	}
	return revealLabel()
}

// fileOps is the shared file submenu of tree and tab.
func fileOps() []MenuItem {
	return []MenuItem{Item(FileCopyPath), Item(FileCopyRelPath), Item(FileReveal)}
}

// StandardMenus returns the default context menus and the main menu.
func StandardMenus() map[ContextID][]MenuItem {
	newMenu := Sub("New", Item(FileNewFile), Item(FileNewDir), Separator(), Item(FileNewPHPClass))
	newMenu.Icon = "add"
	return map[ContextID][]MenuItem{
		CtxProjectTree: append([]MenuItem{
			newMenu, Separator(),
			Item(FileRename), Item(FileCopy), Item(FileDelete), Separator(),
		}, append(fileOps(), Separator(), Item(FileHistory),
			Sub("Git", Item(GitStage), Item(GitCommitFiles), Item(GitShowDiff), Item(GitRollback)))...),
		CtxEditor:       editorMenu(),
		CtxEditorGutter: gutterMenu(),
		CtxTab: append([]MenuItem{
			Item(TabClose), Item(TabCloseOthers), Item(TabCloseRight), Item(TabCloseAll), Separator(),
			Item(TabPin), Item(TabSplitRight), Separator(), Item(FileRename),
		}, fileOps()...),
		CtxToolWindow: {Item(ToolMaximize), Item(ToolFloat), Separator(), Item(ToolHide)},
		CtxGitChanges: {Item(GitCommitFiles), Item(GitShowDiff), Item(GitRollback), Separator(), Item(FileReveal), Item(FileCopyPath)},
		CtxGitLog:     {Item(GitCopyHash), Separator(), Item(GitCheckout), Item(GitCherryPick), Item(GitNewBranchHere)},
		CtxMain: {
			Sub("File", newMenu, Separator(), Item(FileSave), Item(FileSaveAll), Separator(), Item(FileRename), Item(FileDelete), Item(FileHistory)),
			Sub("Edit", Item(EditCut), Item(EditCopy), Item(EditPaste), Separator(), Item(EditSelectAll), Item(EditComment)),
			Sub("Navigate", Item(NavDefinition), Item(NavImplement), Item(EditorFindUses)),
			Sub("Window", Item(TabClose), Item(TabCloseOthers), Item(TabCloseAll), Separator(), Item(ToolHide)),
		},
	}
}

// Standard returns a registry with StandardActions and StandardMenus. The
// reveal label follows goos.
func Standard(goos string) *Registry {
	r := NewRegistry()
	as := StandardActions()
	for i := range as {
		if as[i].ID == FileReveal {
			as[i].Label = RevealLabel(goos)
		}
	}
	_ = r.Add(as...) // static table, validated by tests
	for ctx, items := range StandardMenus() {
		r.SetMenu(ctx, items...)
	}
	return r
}
