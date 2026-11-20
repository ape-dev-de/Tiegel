package toolbar

import (
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/ape-dev-de/tiegel/internal/actions"
	"github.com/ape-dev-de/tiegel/internal/ui/atoms"
)

// ProjectState is the life-cycle state of an open project (AP18.5).
type ProjectState uint8

// Project states.
const (
	// ProjectActive is the project shown in the window.
	ProjectActive ProjectState = iota
	// ProjectBackground keeps its language servers, watchers and tabs.
	ProjectBackground
	// ProjectHibernated has released its processes and memory; resuming
	// restores tabs and layout.
	ProjectHibernated
)

// String returns the state name shown in the project popup.
func (s ProjectState) String() string {
	switch s {
	case ProjectBackground:
		return "background"
	case ProjectHibernated:
		return "hibernated"
	}
	return "active"
}

// Project is an open project.
type Project struct {
	ID   string
	Name string
	// Path is shown muted in the popup.
	Path  string
	State ProjectState
}

// RecentProject is an entry of the recent projects list.
type RecentProject struct {
	Name string
	Path string
}

// RunConfig is a run configuration of the run dropdown.
type RunConfig struct {
	ID   string
	Name string
	// Glyph defaults to Icons.Run.
	Glyph *atoms.Glyph
	// Detail is shown muted in the popup (e.g. the configuration type).
	Detail string
}

// QuickAction is a plugin quick-action button (e.g. "Claude Code",
// "opencode"). Build them from actions.Registry.Toolbar with
// QuickActions, or directly.
type QuickAction struct {
	// ID is reported by QuickActionClicked.
	ID    string
	Glyph *atoms.Glyph
	// Tooltip is the tooltip text and accessible name.
	Tooltip string
	// Active draws the button as a pressed toggle (e.g. agent running).
	Active   bool
	Disabled bool
	// Badge marks the button with a dot (e.g. "needs attention"); the text
	// is appended to the tooltip.
	Badge string
	// Separator starts a new group (a gap) instead of a button.
	Separator bool
}

// QuickActions converts resolved toolbar buttons (actions.Registry.Toolbar)
// to quick actions; icon names become atoms glyphs (unknown names fall
// back to atoms.GlyphMore).
func QuickActions(bs []actions.ToolbarButton) []QuickAction {
	out := make([]QuickAction, 0, len(bs))
	for _, b := range bs {
		g := atoms.GlyphNamed(b.Icon)
		if g == nil {
			g = atoms.GlyphMore
		}
		out = append(out, QuickAction{
			ID: b.ID, Glyph: g, Tooltip: b.Tooltip, Active: b.Active, Disabled: b.Disabled,
			Badge: b.Badge, Separator: b.Separator,
		})
	}
	return out
}

// Icons are the glyphs of the toolbar's own controls. They are data so a
// new icon set (AP18.1) can replace them; DefaultIcons uses atoms glyphs.
type Icons struct {
	Chevron, Branch, Run, Debug, Stop, Listen, Search, Settings *atoms.Glyph
	// Popup rows.
	Folder, Add, Tag, Open, Hibernate, Resume, Check, Close *atoms.Glyph
}

// DefaultIcons returns the atoms glyphs.
func DefaultIcons() Icons {
	return Icons{
		Chevron: atoms.GlyphChevronDown, Branch: atoms.GlyphBranch, Run: atoms.GlyphRun,
		Debug: atoms.GlyphDebug, Stop: atoms.GlyphStop, Listen: atoms.GlyphListen,
		Search: atoms.GlyphSearch, Settings: atoms.GlyphSettings,
		Folder: atoms.GlyphFolder, Add: atoms.GlyphAdd, Tag: atoms.GlyphTag, Open: atoms.GlyphFolderOpen,
		Hibernate: atoms.GlyphPause, Resume: atoms.GlyphRun, Check: atoms.GlyphCheck, Close: atoms.GlyphClose,
	}
}

// Initials returns the two-letter avatar text of a project name: the
// first letters of the first two words ("my-shop" → "MS"), or the first
// two letters of a single word ("tiegel" → "TI"). Empty names give "?".
func Initials(name string) string {
	words := strings.FieldsFunc(name, func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	})
	var rs []rune
	switch {
	case len(words) >= 2:
		r1, _ := utf8.DecodeRuneInString(words[0])
		r2, _ := utf8.DecodeRuneInString(words[1])
		rs = []rune{r1, r2}
	case len(words) == 1:
		rs = []rune(words[0])
		if len(rs) > 2 {
			rs = rs[:2]
		}
	default:
		return "?"
	}
	return strings.ToUpper(string(rs))
}
