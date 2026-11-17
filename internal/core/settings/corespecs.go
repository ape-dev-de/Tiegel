package settings

// The kernel's own settings (decision D210). Every core setting is declared
// here, in one place; the settings dialog, the settings.schema API and the
// headless CLI are generated from these specs. Tool-specific settings never
// belong here – they are declared by their module (D190).

import "math"

// Core setting keys.
const (
	KeyTheme             = "ui.theme"
	KeyUIFontSize        = "ui.fontSize"
	KeyEditorFontFamily  = "editor.fontFamily"
	KeyEditorFontSize    = "editor.fontSize"
	KeyEditorLineHeight  = "editor.lineHeight"
	KeyEditorTabWidth    = "editor.tabWidth"
	KeyEditorInsertSpace = "editor.insertSpaces"
	KeyEditorConfig      = "files.editorconfig"
	KeyFileAssociations  = "files.associations"
	KeyWatchExclude      = "watch.exclude"
	KeyAutosaveIdle      = "autosave.idleSeconds"
	KeyOpenProjectIn     = "projects.openIn"
	KeyUpdateCheck       = "update.checkOnStart"
)

// Core pages (Spec.Group of core specs), in dialog order.
const (
	PageAppearance = "Appearance"
	PageEditor     = "Editor"
	PageFiles      = "Files"
	PageAutosave   = "Autosave"
	PageProjects   = "Projects"
	PageUpdates    = "Updates"
)

// CorePages lists the core pages in dialog order. Groups of specs
// registered by other packages (e.g. "Keymap") follow in registration
// order.
var CorePages = []string{PageAppearance, PageEditor, "Keymap", PageFiles, "Interpreter", PageAutosave, PageProjects, PageUpdates}

// Values of KeyOpenProjectIn.
const (
	OpenProjectAsk  = "ask"
	OpenProjectNew  = "new"
	OpenProjectThis = "this"
)

// kernelSpecs are registered by init.
var kernelSpecs = []Spec{
	{Key: KeyTheme, Kind: KindEnum, Default: "dark", Options: []string{"dark", "light", "system"}, Scope: ScopeGlobal,
		Label: "Theme", Help: "Color theme of the whole application; system follows the OS appearance.", Group: PageAppearance},
	{Key: KeyUIFontSize, Kind: KindFloat, Default: 13.0, Min: IntPtr(9), Max: IntPtr(24), Scope: ScopeGlobal,
		Label: "UI font size", Help: "Size of menus, panels and dialogs in sp.", Group: PageAppearance},

	{Key: KeyEditorFontFamily, Kind: KindString, Default: "", Scope: ScopeGlobal,
		Label: "Editor font", Help: "Monospace font family of the editor; empty uses the best installed one.", Group: PageEditor},
	{Key: KeyEditorFontSize, Kind: KindFloat, Default: 13.0, Min: IntPtr(6), Max: IntPtr(48), Scope: ScopeGlobal,
		Label: "Editor font size", Help: "Editor text size in sp.", Group: PageEditor},
	{Key: KeyEditorLineHeight, Kind: KindFloat, Default: 1.4, Min: IntPtr(1), Max: IntPtr(3), Scope: ScopeGlobal,
		Label: "Line height", Help: "Editor row height as a multiple of the font size.", Group: PageEditor},
	{Key: KeyEditorTabWidth, Kind: KindInt, Default: 4, Min: IntPtr(1), Max: IntPtr(16), Scope: ScopeGlobal,
		Label: "Tab width", Help: "Display width of a tab and the indent size; .editorconfig overrides it per file.", Group: PageEditor},
	{Key: KeyEditorInsertSpace, Kind: KindBool, Default: true, Scope: ScopeGlobal,
		Label: "Indent with spaces", Help: "Tab inserts spaces instead of a tab character; .editorconfig overrides it per file.", Group: PageEditor},

	{Key: KeyEditorConfig, Kind: KindBool, Default: true, Scope: ScopeProject,
		Label: "Use .editorconfig", Help: "Apply indent style and size, final newline and trailing whitespace rules from .editorconfig files.", Group: PageFiles},
	{Key: KeyFileAssociations, Kind: KindList, Default: []string{}, Scope: ScopeProject,
		Label: "File type associations", Help: "Pattern=language pairs such as *.tpl=php or Jenkinsfile=plaintext; they win over the built-in detection.", Group: PageFiles},
	{Key: KeyWatchExclude, Kind: KindList, Default: []string{"node_modules"}, Scope: ScopeProject,
		Label: "Excluded from file watching", Help: "Directory names or project-relative paths the file watcher skips entirely.", Group: PageFiles},

	// No practical maximum: values above an hour were accepted before the
	// spec existed (D249); MaxInt32 only keeps the duration from overflowing.
	{Key: KeyAutosaveIdle, Kind: KindInt, Default: 0, Min: IntPtr(0), Max: IntPtr(math.MaxInt32), Scope: ScopeGlobal,
		Label: "Save after idle seconds", Help: "Also save modified files after this many seconds without typing (0 = only on focus loss, run, debug and commit).", Group: PageAutosave},

	{Key: KeyOpenProjectIn, Kind: KindEnum, Default: OpenProjectAsk, Options: []string{OpenProjectAsk, OpenProjectNew, OpenProjectThis}, Scope: ScopeGlobal,
		Label: "Open project in", Help: "Where to open another project while one is open: ask, a new window or this window.", Group: PageProjects},

	{Key: KeyUpdateCheck, Kind: KindBool, Default: false, Scope: ScopeGlobal,
		Label: "Check for updates on start", Help: "Off by default. Contacts the release server; Tiegel never downloads anything by itself.", Group: PageUpdates},
}

// coreInitErr records a registration problem of kernelSpecs (a test
// asserts it is nil).
var coreInitErr = RegisterCore(kernelSpecs...)
