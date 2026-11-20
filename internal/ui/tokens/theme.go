// Package tokens holds Tiegel's design tokens: colors (light and dark),
// fonts, text sizes, spacing, radii and component sizes.
//
// Components never hard-code colors or measurements; they read them from a
// *Theme. Switching between light and dark mode means passing a different
// Theme to Layout – component state is unaffected.
package tokens

import (
	"image/color"
	"runtime"

	"gioui.org/font"
	"gioui.org/text"
	"gioui.org/unit"
)

// goos selects the default font chains; tests may override it.
var goos = runtime.GOOS

// Mode identifies the brightness variant of a theme.
type Mode int

const (
	// ModeDark is the default, dark theme.
	ModeDark Mode = iota
	// ModeLight is the light theme.
	ModeLight
)

// String returns a human readable name for the mode.
func (m Mode) String() string {
	if m == ModeLight {
		return "light"
	}
	return "dark"
}

// Palette holds the semantic UI colors.
type Palette struct {
	Background   color.NRGBA // window background
	Surface      color.NRGBA // panels, cards, inputs
	SurfaceAlt   color.NRGBA // headers, tab bars, alternating rows
	SurfaceHover color.NRGBA // hovered rows and ghost buttons
	Border       color.NRGBA // outlines and dividers
	BorderStrong color.NRGBA // input borders, emphasized outlines
	Text         color.NRGBA // primary text
	TextMuted    color.NRGBA // secondary text, hints
	TextDisabled color.NRGBA // text of disabled controls
	Accent       color.NRGBA // primary actions, focus, active markers
	AccentHover  color.NRGBA // hovered primary actions
	OnAccent     color.NRGBA // text/icons drawn on Accent
	Danger       color.NRGBA
	Warning      color.NRGBA
	Success      color.NRGBA
	Info         color.NRGBA
	Selection    color.NRGBA // selected rows (background)
	Focus        color.NRGBA // focus rings
	TooltipBg    color.NRGBA
	TooltipText  color.NRGBA
}

// EditorPalette holds the colors of the code editor surface.
type EditorPalette struct {
	Background       color.NRGBA
	CurrentLine      color.NRGBA
	Gutter           color.NRGBA
	LineNumber       color.NRGBA
	LineNumberActive color.NRGBA
	Cursor           color.NRGBA
	Selection        color.NRGBA
	Text             color.NRGBA
	Breakpoint       color.NRGBA
	// Whitespace marks visible spaces and tabs; Ruler is the right
	// margin line. Both stay subtle.
	Whitespace color.NRGBA
	Ruler      color.NRGBA
	// Diff backgrounds and markers used by diff and merge views.
	DiffAddedBg   color.NRGBA
	DiffRemovedBg color.NRGBA
	DiffAddedFg   color.NRGBA
	DiffRemovedFg color.NRGBA
	DiffHeaderBg  color.NRGBA
}

// SyntaxPalette maps highlighting roles to colors.
type SyntaxPalette struct {
	Keyword     color.NRGBA
	String      color.NRGBA
	Number      color.NRGBA
	Comment     color.NRGBA
	Variable    color.NRGBA
	Function    color.NRGBA
	Type        color.NRGBA
	Constant    color.NRGBA
	Operator    color.NRGBA
	Punctuation color.NRGBA
	Tag         color.NRGBA
	Attribute   color.NRGBA
	Property    color.NRGBA
}

// SyntaxRole names a highlighting role. The editor maps Tree-sitter captures
// and LSP semantic tokens onto these roles.
type SyntaxRole int

// Highlighting roles.
const (
	RolePlain SyntaxRole = iota
	RoleKeyword
	RoleString
	RoleNumber
	RoleComment
	RoleVariable
	RoleFunction
	RoleType
	RoleConstant
	RoleOperator
	RolePunctuation
	RoleTag
	RoleAttribute
	RoleProperty
)

// SyntaxRoles lists all roles except RolePlain, in declaration order.
var SyntaxRoles = []SyntaxRole{
	RoleKeyword, RoleString, RoleNumber, RoleComment, RoleVariable, RoleFunction, RoleType,
	RoleConstant, RoleOperator, RolePunctuation, RoleTag, RoleAttribute, RoleProperty,
}

var roleNames = [...]string{"plain", "keyword", "string", "number", "comment", "variable",
	"function", "type", "constant", "operator", "punctuation", "tag", "attribute", "property"}

// String returns the lower-case role name.
func (r SyntaxRole) String() string {
	if r < 0 || int(r) >= len(roleNames) {
		return "unknown"
	}
	return roleNames[r]
}

// SyntaxColor returns the color for role; RolePlain and unknown roles map to
// the editor text color.
func (th *Theme) SyntaxColor(r SyntaxRole) color.NRGBA {
	s := th.Syntax
	switch r {
	case RoleKeyword:
		return s.Keyword
	case RoleString:
		return s.String
	case RoleNumber:
		return s.Number
	case RoleComment:
		return s.Comment
	case RoleVariable:
		return s.Variable
	case RoleFunction:
		return s.Function
	case RoleType:
		return s.Type
	case RoleConstant:
		return s.Constant
	case RoleOperator:
		return s.Operator
	case RolePunctuation:
		return s.Punctuation
	case RoleTag:
		return s.Tag
	case RoleAttribute:
		return s.Attribute
	case RoleProperty:
		return s.Property
	}
	return th.Editor.Text
}

// GitPalette holds VCS colors for the gutter and file status.
type GitPalette struct {
	// Gutter markers.
	Added    color.NRGBA
	Modified color.NRGBA
	Deleted  color.NRGBA
	// File name colors in trees and lists.
	FileAdded     color.NRGBA
	FileModified  color.NRGBA
	FileDeleted   color.NRGBA
	FileUntracked color.NRGBA
	FileIgnored   color.NRGBA
	FileConflict  color.NRGBA
}

// Fonts selects the typefaces. Both typefaces are comma-separated fallback
// chains of family names (see DefaultFamilies): the shaper picks the first
// installed family, resolves runes it lacks (CJK, emoji, …) from other
// installed fonts and ends with the bundled Go fonts, so text renders even
// without system fonts.
type Fonts struct {
	UI   font.Font // proportional UI font chain
	Mono font.Font // monospace editor font chain
}

// TextSizes holds the type scale.
type TextSizes struct {
	Small   unit.Sp // badges, captions
	Body    unit.Sp // default UI text
	Large   unit.Sp // emphasized text
	Heading unit.Sp // section headings
	Title   unit.Sp // dialog titles
	Code    unit.Sp // editor text
	// CodeLineHeight is the editor row height as a multiple of the code
	// font's ascent+descent (0 means 1.4).
	CodeLineHeight float32
}

// Spacing is the 4dp grid. Use the named steps instead of raw numbers.
type Spacing struct {
	XXS unit.Dp // 2 – hairline gaps only
	XS  unit.Dp // 4
	S   unit.Dp // 8
	M   unit.Dp // 12
	L   unit.Dp // 16
	XL  unit.Dp // 24
	XXL unit.Dp // 32
}

// Step returns n grid units (n × 4dp).
func (Spacing) Step(n int) unit.Dp { return unit.Dp(4 * n) }

// Radii holds corner radii.
type Radii struct {
	S    unit.Dp // checkboxes, small chips
	M    unit.Dp // buttons, inputs
	L    unit.Dp // panels, tooltips
	Pill unit.Dp // fully rounded badges (clamped to half the height)
}

// Sizes holds common component dimensions.
type Sizes struct {
	Icon        unit.Dp // default icon size
	IconSmall   unit.Dp
	Checkbox    unit.Dp
	ControlH    unit.Dp // height of buttons and inputs
	RowH        unit.Dp // height of list/tree rows
	TabH        unit.Dp // height of editor tabs
	IndentStep  unit.Dp // tree indentation per level
	BorderWidth unit.Dp
	// Workbench chrome: stripes, panel headers, footer, toolbar.
	StripeW      unit.Dp // width of the left/right tool window stripes
	StripeButton unit.Dp // square hit target of a stripe button
	StripeIcon   unit.Dp // icon size in a stripe button
	ActionButton unit.Dp // square hit target of icon buttons in headers
	PanelHeaderH unit.Dp // height of a tool window panel header
	StatusH      unit.Dp // minimum height of the status bar (footer)
	ToolbarH     unit.Dp // height of the main toolbar (header slot)
}

// Theme bundles all tokens. Treat a Theme as read-only; to switch modes,
// replace the pointer (see Toggled).
type Theme struct {
	Mode   Mode
	Color  Palette
	Editor EditorPalette
	Syntax SyntaxPalette
	Git    GitPalette
	// Terminal holds the colors of the embedded terminal.
	Terminal TerminalPalette
	Font     Fonts
	Text     TextSizes
	Space    Spacing
	Radius   Radii
	Size     Sizes
	// Shaper shapes text. It must not be shared between windows.
	Shaper *text.Shaper
}

// New returns the theme for mode with the default font chains of the
// running OS (see DefaultFamilies) and default sizes; apply user font
// preferences with WithFonts. If shaper is nil, a new bundled-fonts shaper
// (NewBundledShaper) is created, which keeps tests deterministic; the
// application passes a NewShaper to use system fonts.
func New(mode Mode, shaper *text.Shaper) *Theme {
	if shaper == nil {
		shaper = NewBundledShaper()
	}
	ui, mono := DefaultFamilies(goos)
	th := &Theme{
		Mode:   mode,
		Font:   Fonts{UI: font.Font{Typeface: font.Typeface(ui)}, Mono: font.Font{Typeface: font.Typeface(mono)}},
		Text:   scaledText(defaultUISize),
		Space:  Spacing{XXS: 2, XS: 4, S: 8, M: 12, L: 16, XL: 24, XXL: 32},
		Radius: Radii{S: 3, M: 5, L: 8, Pill: 999},
		Size: Sizes{
			Icon: 16, IconSmall: 14, Checkbox: 14, ControlH: 28, RowH: 24, TabH: 32,
			IndentStep: 16, BorderWidth: 1,
			StripeW: 40, StripeButton: 32, StripeIcon: 18, ActionButton: 24,
			PanelHeaderH: 32, StatusH: 26, ToolbarH: 40,
		},
		Shaper: shaper,
	}
	if mode == ModeLight {
		th.Color, th.Editor, th.Syntax, th.Git = lightPalette(), lightEditor(), lightSyntax(), lightGit()
		th.Terminal = lightTerminal()
	} else {
		th.Color, th.Editor, th.Syntax, th.Git = darkPalette(), darkEditor(), darkSyntax(), darkGit()
		th.Terminal = darkTerminal()
	}
	return th
}

// Dark returns the dark theme.
func Dark(shaper *text.Shaper) *Theme { return New(ModeDark, shaper) }

// Light returns the light theme.
func Light(shaper *text.Shaper) *Theme { return New(ModeLight, shaper) }

// Toggled returns the theme of the opposite mode, sharing the shaper and
// keeping the fonts and text sizes.
func (th *Theme) Toggled() *Theme {
	mode := ModeLight
	if th.Mode == ModeLight {
		mode = ModeDark
	}
	out := New(mode, th.Shaper)
	out.Font, out.Text = th.Font, th.Text
	return out
}
