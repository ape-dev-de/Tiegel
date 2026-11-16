package actions

// Code navigation, refactoring and search actions of the editor (GUI
// integration of nav.*, lsp.* and search.*, D160) and the editor and
// gutter context menus.
const (
	NavSuper            = "navigate.super"            // nav.super
	NavTypeHierarchy    = "navigate.typeHierarchy"    // nav.typeHierarchy
	NavGotoClass        = "navigate.gotoClass"        // nav.symbols kinds=type
	NavGotoSymbol       = "navigate.gotoSymbol"       // nav.symbols
	NavSearchEverywhere = "navigate.searchEverywhere" // classes, files, symbols, actions
	NavBack             = "navigate.back"
	NavForward          = "navigate.forward"

	RefactorRename     = "refactor.rename"     // lsp.prepareRename + lsp.rename
	CodeReformat       = "code.reformat"       // lsp.formatting / lsp.rangeFormatting
	CodeContextActions = "code.contextActions" // lsp.codeActions + lsp.applyCodeAction
	ViewStructure      = "view.structure"      // syntax.outline / lsp.documentSymbols

	FindInPath    = "find.inPath"        // search.inProject (streamed)
	ReplaceInPath = "find.replaceInPath" // search.replaceInProject

	DebugEditCondition = "debug.editCondition"
	DebugEnableToggle  = "debug.enableBreakpoint"

	// Git actions of the editor menu. Their handlers come from the git
	// integration (file history, diff window); without a handler they are
	// shown disabled.
	EditorGitAnnotate = "vcs.annotate"
	EditorGitHistory  = "vcs.fileHistory"
	EditorGitCompare  = "vcs.compareWithHead"
	EditorGitRollback = "vcs.rollback"
)

// editorActions are the code navigation, refactoring and search actions.
func editorActions() []Action {
	hasLine := func(c Context) bool { return c.Line > 0 }
	return []Action{
		{ID: NavSuper, Label: "Go to Super Method", Shortcut: Shortcut{Mac: "Cmd+U", PC: "Ctrl+U"}, Enabled: php, Keywords: []string{"parent", "overridden", "interface"}},
		{ID: NavTypeHierarchy, Label: "Type Hierarchy", Icon: "branch", Shortcut: Shortcut{Mac: "Ctrl+H", PC: "Ctrl+H"}, Enabled: php, Keywords: []string{"subclasses", "supertypes", "inheritance"}},
		{ID: NavGotoClass, Label: "Go to Class…", Icon: "file-code", Shortcut: Shortcut{Mac: "Cmd+O", PC: "Ctrl+N"}, Keywords: []string{"interface", "trait", "enum", "type"}},
		{ID: NavGotoSymbol, Label: "Go to Symbol…", Shortcut: Shortcut{Mac: "Cmd+Alt+O", PC: "Ctrl+Alt+Shift+N"}, Keywords: []string{"method", "function", "constant", "property"}},
		{ID: NavSearchEverywhere, Label: "Search Everywhere", Icon: "search", Keywords: []string{"shift shift", "double shift", "find"}},
		{ID: RefactorRename, Label: "Rename Symbol…", Icon: "edit", Shortcut: Shortcut{Mac: "Shift+F6", PC: "Shift+F6"}, Enabled: php, Keywords: []string{"refactor"}},
		{ID: CodeReformat, Label: "Reformat Code", Shortcut: Shortcut{Mac: "Cmd+Alt+L", PC: "Ctrl+Alt+L"}, Enabled: php, Keywords: []string{"format", "psr-12", "selection"}},
		{ID: CodeContextActions, Label: "Show Context Actions", Shortcut: Shortcut{Mac: "Alt+Enter", PC: "Alt+Enter"}, Enabled: php, Keywords: []string{"quick fix", "intention", "code action", "import"}},
		{ID: ViewStructure, Label: "Structure", Icon: "list", Shortcut: Shortcut{Mac: "Cmd+7", PC: "Alt+7"}, Keywords: []string{"outline", "members"}},
		{ID: FindInPath, Label: "Find in Files…", Icon: "search", Shortcut: Shortcut{Mac: "Cmd+Shift+F", PC: "Ctrl+Shift+F"}, Keywords: []string{"search", "path", "project"}},
		{ID: ReplaceInPath, Label: "Replace in Files…", Shortcut: Shortcut{Mac: "Cmd+Shift+R", PC: "Ctrl+Shift+R"}, Keywords: []string{"replace in path", "project"}},
		{ID: DebugEditCondition, Label: "Edit Breakpoint Condition…", Enabled: hasValue("breakpoint"), Visible: php},
		{ID: DebugEnableToggle, Label: "Enabled", Check: CheckBox, Checked: func(c Context) bool { return c.Value("breakpointEnabled") != "" }, Enabled: hasValue("breakpoint"), Visible: php},
		{ID: EditorGitAnnotate, Label: "Annotate with Git Blame", Icon: "branch", Enabled: hasLine, Visible: hasValue("git")},
		{ID: EditorGitHistory, Label: "Show History", Icon: "history", Visible: hasValue("git")},
		{ID: EditorGitCompare, Label: "Compare with HEAD", Icon: "swap", Visible: hasValue("git")},
		{ID: EditorGitRollback, Label: "Rollback…", Visible: hasValue("git")},
	}
}

// editorMenu is the context menu of the editor text area.
func editorMenu() []MenuItem {
	gotoMenu := Sub("Go To", Item(NavDefinition), Item(NavImplement), Item(NavSuper), Item(NavTypeHierarchy))
	git := Sub("Git", Item(EditorGitAnnotate), Item(EditorGitHistory), Item(EditorGitCompare), Separator(), Item(EditorGitRollback))
	git.Icon = "branch"
	return []MenuItem{
		Item(CodeContextActions), Separator(),
		Item(EditCut), Item(EditCopy), Item(EditPaste), Separator(),
		gotoMenu, Item(EditorFindUses), Item(RefactorRename), Separator(),
		Item(CodeReformat), Item(EditComment), Separator(),
		git, Separator(),
		Item(FileReveal), Item(FileHistory),
	}
}

// gutterMenu is the context menu of the gutter (line numbers, breakpoint
// column).
func gutterMenu() []MenuItem {
	return []MenuItem{
		Item(GutterToggleBreakpoint), Item(DebugEnableToggle), Item(DebugEditCondition), Separator(),
		Item(GutterShowLineNumbers), Item(EditorGitAnnotate),
	}
}
