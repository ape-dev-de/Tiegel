package markdown

import "github.com/ape-dev-de/tiegel/internal/core/settings"

// Settings of the Markdown reader.
const (
	// KeyOpenMode tells how a Markdown file opens in the GUI.
	KeyOpenMode = "markdown.openMode"
	// OpenSplit opens the editor with the preview to its right.
	OpenSplit = "split"
	// OpenCode opens only the editor (the preview via the View menu).
	OpenCode = "code"
	// PageMarkdown is the settings page.
	PageMarkdown = "Markdown"
)

// SettingSpecs returns the core settings of the Markdown reader.
func SettingSpecs() []settings.Spec {
	return []settings.Spec{
		{Key: KeyOpenMode, Kind: settings.KindEnum, Default: OpenSplit, Options: []string{OpenSplit, OpenCode}, Scope: settings.ScopeGlobal, Group: PageMarkdown,
			Label: "Open Markdown files", Help: "split: the editor on the left, the live preview on the right; code: only the editor (preview via View › Open Markdown Preview)."},
	}
}

// registerErr records a registration problem of SettingSpecs (tested).
var registerErr = settings.RegisterCore(SettingSpecs()...)
