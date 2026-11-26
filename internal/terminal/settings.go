package terminal

import (
	"github.com/ape-dev-de/tiegel/internal/core/settings"
)

// Settings keys of the embedded terminal (core, page "Terminal").
const (
	KeyShell        = "terminal.shell"
	KeyShellArgs    = "terminal.shellArgs"
	KeyOptionAsMeta = "terminal.optionAsMeta"
	KeyScrollback   = "terminal.scrollback"
	KeyEscape       = "terminal.escape"
	KeyFontSize     = "terminal.fontSize"
	KeyOverrideKeys = "terminal.overrideIdeShortcuts"
	KeyCloseOnExit  = "terminal.closeOnExit"
	KeyClipboard    = "terminal.allowClipboardWrite"
	KeyCopyOnSelect = "terminal.copyOnSelect"
	KeyLineHeight   = "terminal.lineHeight"
	KeyConfirmClose = "terminal.confirmClose"
)

// Values of KeyEscape.
const (
	EscapeEditor   = "editor"   // Escape moves the focus to the editor
	EscapeTerminal = "terminal" // Escape goes to the program
)

// PageTerminal is the settings page of the terminal settings.
const PageTerminal = "Terminal"

// SettingSpecs returns the core settings of the terminal.
func SettingSpecs() []settings.Spec {
	return []settings.Spec{
		{Key: KeyShell, Kind: settings.KindPath, Default: "", Scope: settings.ScopeProject, Group: PageTerminal, Executable: true,
			Label: "Shell path", Help: "Program started in new terminal tabs; empty uses $SHELL (Windows: PowerShell, else cmd.exe)."},
		{Key: KeyShellArgs, Kind: settings.KindList, Default: []string{}, Scope: settings.ScopeProject, Group: PageTerminal,
			Label: "Shell arguments", Help: "Arguments of the shell; empty starts bash, zsh and fish as login shells (-l)."},
		{Key: KeyOptionAsMeta, Kind: settings.KindBool, Default: false, Scope: settings.ScopeGlobal, Group: PageTerminal,
			Label: "Use Option as Meta key", Help: "macOS: Option+key sends Escape+key (word movement in shells) instead of typing special characters such as @ or € ."},
		{Key: KeyEscape, Kind: settings.KindEnum, Default: EscapeTerminal, Options: []string{EscapeEditor, EscapeTerminal}, Scope: settings.ScopeGlobal, Group: PageTerminal,
			Label: "Escape key", Help: "terminal: Escape goes to the program (vim, less, shell); editor: Escape moves the focus back to the editor. Shift+Escape hides the terminal either way."},
		{Key: KeyOverrideKeys, Kind: settings.KindBool, Default: true, Scope: settings.ScopeGlobal, Group: PageTerminal,
			Label: "Terminal keys override IDE shortcuts", Help: "While the terminal has the focus, keys such as Ctrl+R go to the shell; only Cmd shortcuts (macOS) and the terminal and tool window toggles stay with the IDE."},
		{Key: KeyScrollback, Kind: settings.KindInt, Default: 10000, Min: settings.IntPtr(0), Max: settings.IntPtr(1000000), Scope: settings.ScopeGlobal, Group: PageTerminal,
			Label: "Scrollback lines", Help: "Lines kept above the screen per terminal tab (applies to new tabs)."},
		{Key: KeyFontSize, Kind: settings.KindFloat, Default: 0.0, Min: settings.IntPtr(0), Max: settings.IntPtr(72), Scope: settings.ScopeGlobal, Group: PageTerminal,
			Label: "Font size", Help: "Terminal text size in sp; 0 uses the editor font size."},
		{Key: KeyCloseOnExit, Kind: settings.KindBool, Default: true, Scope: settings.ScopeGlobal, Group: PageTerminal,
			Label: "Close tab when the shell exits", Help: "Close the tab when its program ends with exit code 0; otherwise the tab stays with the last output."},
		{Key: KeyClipboard, Kind: settings.KindBool, Default: false, Scope: settings.ScopeGlobal, Group: PageTerminal,
			Label: "Programs may write the clipboard", Help: "Allow programs in the terminal (e.g. vim or tmux over SSH) to set the clipboard with OSC 52; reading the clipboard is never allowed."},
		{Key: KeyCopyOnSelect, Kind: settings.KindBool, Default: false, Scope: settings.ScopeGlobal, Group: PageTerminal,
			Label: "Copy on selection", Help: "Copy text to the clipboard as soon as it is selected with the mouse."},
		{Key: KeyLineHeight, Kind: settings.KindFloat, Default: 0.0, Min: settings.IntPtr(0), Max: settings.IntPtr(3), Scope: settings.ScopeGlobal, Group: PageTerminal,
			Label: "Line height", Help: "Terminal row height as a multiple of the font height; 0 uses the editor line height (box drawing and powerline glyphs join without gaps at any value)."},
		{Key: KeyConfirmClose, Kind: settings.KindBool, Default: true, Scope: settings.ScopeGlobal, Group: PageTerminal,
			Label: "Confirm closing a busy tab", Help: "Ask before closing a terminal tab while a program other than the shell runs in it."},
	}
}

// registerErr records a registration problem of SettingSpecs (tested).
var registerErr = settings.RegisterCore(SettingSpecs()...)
