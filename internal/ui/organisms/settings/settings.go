// Package settings provides the settings dialog: modules on/off with their
// dependencies, the PHP interpreter (picker, path mappings, Xdebug hints),
// editor options (tab width, theme, fonts), autosave and the opt-in update check.
// Everything is stored in Tiegel's internal settings by the host; there is
// no file for the user to edit.
package settings

import (
	"fmt"
	"image"
	"strings"

	"gioui.org/layout"
	"gioui.org/unit"

	"github.com/ape-dev-de/tiegel/internal/ui/atoms"
	"github.com/ape-dev-de/tiegel/internal/ui/molecules"
	"github.com/ape-dev-de/tiegel/internal/ui/organisms/runtime"
	"github.com/ape-dev-de/tiegel/internal/ui/tokens"
)

// Module is a module row.
type Module struct {
	ID        string
	DependsOn []string
	// Dependents are enabled modules that need this one.
	Dependents []string
	Enabled    bool
	Running    bool
	Error      string
}

// Event is reported by Panel.Update.
type Event interface{ isSettingsEvent() }

// ModuleEvent: a module checkbox was toggled.
type ModuleEvent struct {
	ID     string
	Enable bool
}

// TabWidthEvent: the tab width was chosen.
type TabWidthEvent struct{ Width int }

// ThemeEvent: "dark", "light" or "system" was chosen.
type ThemeEvent struct{ Theme string }

// FontFamilyEvent: an editor font family was chosen ("" = the default
// fallback chain).
type FontFamilyEvent struct{ Family string }

// FontSizeEvent: an editor font size (sp) was chosen.
type FontSizeEvent struct{ Size float32 }

// LineHeightEvent: an editor line height multiplier was chosen.
type LineHeightEvent struct{ Value float32 }

// UIFontSizeEvent: a UI font size (sp) was chosen.
type UIFontSizeEvent struct{ Size float32 }

// IdleEvent: the autosave idle delay was chosen (0 = off).
type IdleEvent struct{ Seconds int }

// UpdateCheckEvent: the update check option was toggled.
type UpdateCheckEvent struct{ On bool }

// CheckNowEvent: "Check now" was clicked.
type CheckNowEvent struct{}

// RuntimeEvent wraps an event of the interpreter section.
type RuntimeEvent struct{ Event runtime.Event }

// CloseEvent: the dialog was closed.
type CloseEvent struct{}

func (ModuleEvent) isSettingsEvent()      {}
func (TabWidthEvent) isSettingsEvent()    {}
func (ThemeEvent) isSettingsEvent()       {}
func (FontFamilyEvent) isSettingsEvent()  {}
func (FontSizeEvent) isSettingsEvent()    {}
func (LineHeightEvent) isSettingsEvent()  {}
func (UIFontSizeEvent) isSettingsEvent()  {}
func (IdleEvent) isSettingsEvent()        {}
func (UpdateCheckEvent) isSettingsEvent() {}
func (CheckNowEvent) isSettingsEvent()    {}
func (RuntimeEvent) isSettingsEvent()     {}
func (CloseEvent) isSettingsEvent()       {}

// Sections of the dialog.
var Sections = []string{"Modules", "PHP Interpreter", "Editor", "Autosave", "Updates"}

// TabWidths, Themes, FontSizes, LineHeights, UIFontSizes and IdleChoices
// are the offered values.
var (
	TabWidths   = []int{2, 4, 8}
	Themes      = []string{"dark", "light", "system"}
	FontSizes   = []float32{10, 11, 12, 13, 14, 15, 16, 18, 20, 24}
	LineHeights = []float32{1.0, 1.2, 1.4, 1.6, 1.8, 2.0}
	UIFontSizes = []float32{11, 12, 13, 14, 15, 16}
	IdleChoices = []int{0, 5, 15, 30, 60, 300}
)

// FontPreview is the sample line shown in the editor font: Latin with
// accents, PHP syntax, CJK and a symbol from the fallback fonts.
const FontPreview = "<?php $café = fn(int $x) => $x * 2; // 漢字 ✓"

// DefaultFontLabel names the default editor font chain in the family list.
const DefaultFontLabel = "System default"

// Panel is the settings organism.
type Panel struct {
	Section int

	Modules  []Module
	TabWidth int
	Theme    string
	// EditorFont is the chosen editor font family ("" = default chain).
	EditorFont string
	// EditorFonts lists the installed monospace families offered in the
	// family dropdown (the host fills it, e.g. from
	// tokens.MonospaceFamilies in the background). An empty list offers
	// only the default and the current family.
	EditorFonts []string
	// EditorFontSize and UIFontSize are font sizes in sp, LineHeight the
	// editor row height multiplier; zero values show the defaults.
	EditorFontSize float32
	LineHeight     float32
	UIFontSize     float32
	IdleSeconds    int
	UpdateCheck    bool
	// UpdateStatus is shown next to "Check now".
	UpdateStatus string
	// NoProject disables the interpreter section.
	NoProject bool

	// The interpreter section reuses the runtime organisms; the host owns
	// them and fills their data.
	Interpreter runtime.InterpreterPicker
	Mappings    runtime.PathMappingEditor
	Hints       runtime.XdebugHintsList

	nav       []molecules.ListRow
	modBoxes  map[string]*atoms.Checkbox
	tabSel    *molecules.Dropdown
	themeSel  *molecules.Dropdown
	fontSel   *molecules.Dropdown
	sizeSel   *molecules.Dropdown
	lineSel   *molecules.Dropdown
	uiSizeSel *molecules.Dropdown
	idleSel   *molecules.Dropdown
	// fontValues maps fontSel options to families; fontKey is the state
	// (current family and list) they were built from.
	fontValues []string
	fontKey    string
	updateBox  atoms.Checkbox
	checkNow   atoms.Button
	closeBtn   atoms.IconButton
	scroll     molecules.ScrollList
	inited     bool
}

func (p *Panel) init() {
	if p.inited {
		return
	}
	p.inited = true
	p.nav = make([]molecules.ListRow, len(Sections))
	p.modBoxes = map[string]*atoms.Checkbox{}
	var tabs, themes, idles []molecules.DropdownOption
	for _, w := range TabWidths {
		tabs = append(tabs, molecules.DropdownOption{Label: fmt.Sprintf("%d spaces", w)})
	}
	for _, t := range Themes {
		themes = append(themes, molecules.DropdownOption{Label: strings.ToUpper(t[:1]) + t[1:]})
	}
	for _, s := range IdleChoices {
		idles = append(idles, molecules.DropdownOption{Label: IdleLabel(s)})
	}
	p.tabSel = molecules.NewSelect("Tab width", tabs...)
	p.themeSel = molecules.NewSelect("Theme", themes...)
	p.fontSel = molecules.NewSelect("Font")
	p.sizeSel = molecules.NewSelect("Size", numberOptions(FontSizes, "%g")...)
	p.lineSel = molecules.NewSelect("Line height", numberOptions(LineHeights, "%.1f")...)
	p.uiSizeSel = molecules.NewSelect("UI font size", numberOptions(UIFontSizes, "%g")...)
	p.idleSel = molecules.NewSelect("Idle autosave", idles...)
	p.updateBox.Label = "Check for updates when Tiegel starts (contacts the release server)"
	p.checkNow = atoms.Button{Text: "Check now", Kind: atoms.Secondary, Glyph: atoms.GlyphRefresh}
	p.closeBtn = atoms.IconButton{Glyph: atoms.GlyphClose, Description: "Close (Escape)"}
}

func numberOptions(vals []float32, format string) []molecules.DropdownOption {
	out := make([]molecules.DropdownOption, len(vals))
	for i, v := range vals {
		out[i] = molecules.DropdownOption{Label: fmt.Sprintf(format, v)}
	}
	return out
}

// syncFonts rebuilds the family options when EditorFont or EditorFonts
// changed: the default chain first, then the installed families (plus the
// current one if it is not installed).
func (p *Panel) syncFonts() {
	key := p.EditorFont + "\x00" + strings.Join(p.EditorFonts, "\x00")
	if key == p.fontKey && p.fontValues != nil {
		return
	}
	p.fontKey = key
	p.fontValues = append(p.fontValues[:0], "")
	opts := []molecules.DropdownOption{{Label: DefaultFontLabel, Detail: "Best installed monospace font"}}
	cur := strings.TrimSpace(p.EditorFont)
	found := cur == ""
	for _, f := range p.EditorFonts {
		if f == "" || indexOf(p.fontValues, f) >= 0 {
			continue
		}
		found = found || strings.EqualFold(f, cur)
		p.fontValues = append(p.fontValues, f)
		opts = append(opts, molecules.DropdownOption{Label: f})
	}
	if !found {
		p.fontValues = append(p.fontValues, cur)
		opts = append(opts, molecules.DropdownOption{Label: cur, Detail: "Not installed"})
	}
	p.fontSel.Options = opts
}

// fontIndex returns the option index of the current family.
func (p *Panel) fontIndex() int {
	cur := strings.TrimSpace(p.EditorFont)
	for i, f := range p.fontValues {
		if strings.EqualFold(f, cur) {
			return i
		}
	}
	return 0
}

// selectNumber selects v in d (or shows it as the placeholder if it is not
// one of vals); zero selects def.
func selectNumber(d *molecules.Dropdown, vals []float32, v, def float32, format string) {
	if v == 0 {
		v = def
	}
	d.Selected = indexOf(vals, v)
	if d.Selected < 0 {
		d.Placeholder = fmt.Sprintf(format, v)
	}
}

// IdleLabel describes an idle delay.
func IdleLabel(secs int) string {
	switch {
	case secs <= 0:
		return "Off (save on focus loss only)"
	case secs < 60:
		return fmt.Sprintf("After %d s idle", secs)
	}
	return fmt.Sprintf("After %d min idle", secs/60)
}

func indexOf[T comparable](list []T, v T) int {
	for i, x := range list {
		if x == v {
			return i
		}
	}
	return -1
}

func (p *Panel) box(id string) *atoms.Checkbox {
	b := p.modBoxes[id]
	if b == nil {
		b = &atoms.Checkbox{}
		p.modBoxes[id] = b
	}
	return b
}

// Update returns the user's changes.
func (p *Panel) Update(gtx layout.Context) []Event {
	p.init()
	var out []Event
	for i := range p.nav {
		if c, _ := p.nav[i].Update(gtx); c {
			p.Section = i
		}
	}
	for _, m := range p.Modules {
		b := p.box(m.ID)
		b.State = checkState(m.Enabled)
		if b.Update(gtx) {
			out = append(out, ModuleEvent{ID: m.ID, Enable: b.Checked()})
		}
	}
	if i, ok := p.tabSel.Update(gtx); ok {
		out = append(out, TabWidthEvent{Width: TabWidths[i]})
	}
	if i, ok := p.themeSel.Update(gtx); ok {
		out = append(out, ThemeEvent{Theme: Themes[i]})
	}
	p.syncFonts()
	if i, ok := p.fontSel.Update(gtx); ok && i < len(p.fontValues) {
		out = append(out, FontFamilyEvent{Family: p.fontValues[i]})
	}
	if i, ok := p.sizeSel.Update(gtx); ok {
		out = append(out, FontSizeEvent{Size: FontSizes[i]})
	}
	if i, ok := p.lineSel.Update(gtx); ok {
		out = append(out, LineHeightEvent{Value: LineHeights[i]})
	}
	if i, ok := p.uiSizeSel.Update(gtx); ok {
		out = append(out, UIFontSizeEvent{Size: UIFontSizes[i]})
	}
	if i, ok := p.idleSel.Update(gtx); ok {
		out = append(out, IdleEvent{Seconds: IdleChoices[i]})
	}
	p.updateBox.State = checkState(p.UpdateCheck)
	if p.updateBox.Update(gtx) {
		out = append(out, UpdateCheckEvent{On: p.updateBox.Checked()})
	}
	if p.checkNow.Clicked(gtx) {
		out = append(out, CheckNowEvent{})
	}
	if p.closeBtn.Clicked(gtx) {
		out = append(out, CloseEvent{})
	}
	if !p.NoProject {
		for _, e := range p.Interpreter.Update(gtx) {
			out = append(out, RuntimeEvent{e})
		}
		for _, e := range p.Mappings.Update(gtx) {
			out = append(out, RuntimeEvent{e})
		}
		for _, e := range p.Hints.Update(gtx) {
			out = append(out, RuntimeEvent{e})
		}
	}
	return out
}

func checkState(on bool) atoms.CheckState {
	if on {
		return atoms.Checked
	}
	return atoms.Unchecked
}

// Size is the dialog size.
var Size = image.Pt(820, 560)

// Layout draws the dialog (Size dp, bounded by the constraints).
func (p *Panel) Layout(gtx layout.Context, th *tokens.Theme) layout.Dimensions {
	p.init()
	size := image.Pt(min(gtx.Dp(unit.Dp(Size.X)), gtx.Constraints.Max.X), min(gtx.Dp(unit.Dp(Size.Y)), gtx.Constraints.Max.Y))
	gtx.Constraints = layout.Exact(size)
	return atoms.Surface{Kind: atoms.SurfacePanel, Border: true, Rounded: true}.Layout(gtx, th, func(gtx layout.Context) layout.Dimensions {
		return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return layout.Inset{Left: th.Space.L, Right: th.Space.S, Top: th.Space.S, Bottom: th.Space.S}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
						layout.Flexed(1, atoms.Heading("Settings").Fn(th)),
						layout.Rigid(func(gtx layout.Context) layout.Dimensions { return p.closeBtn.Layout(gtx, th) }),
					)
				})
			}),
			layout.Rigid(atoms.Divider{}.Fn(th)),
			layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
				return layout.Flex{}.Layout(gtx,
					layout.Rigid(func(gtx layout.Context) layout.Dimensions { return p.layoutNav(gtx, th) }),
					layout.Rigid(atoms.Divider{Vertical: true}.Fn(th)),
					layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
						return layout.UniformInset(th.Space.L).Layout(gtx, func(gtx layout.Context) layout.Dimensions {
							return p.layoutSection(gtx, th)
						})
					}),
				)
			}),
		)
	})
}

func (p *Panel) layoutNav(gtx layout.Context, th *tokens.Theme) layout.Dimensions {
	w := gtx.Dp(180)
	gtx.Constraints = layout.Exact(image.Pt(w, gtx.Constraints.Max.Y))
	return layout.Inset{Top: th.Space.S}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		children := make([]layout.FlexChild, len(Sections))
		for i, s := range Sections {
			children[i] = layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				p.nav[i].Selected = i == p.Section
				return p.nav[i].Layout(gtx, th, atoms.Text(s).Fn(th))
			})
		}
		return layout.Flex{Axis: layout.Vertical}.Layout(gtx, children...)
	})
}

func (p *Panel) layoutSection(gtx layout.Context, th *tokens.Theme) layout.Dimensions {
	var rows []layout.Widget
	title := func(t, sub string) {
		rows = append(rows, atoms.Strong(t).Fn(th))
		if sub != "" {
			rows = append(rows, atoms.Small(sub).Fn(th))
		}
		rows = append(rows, layout.Spacer{Height: th.Space.M}.Layout)
	}
	field := func(label string, w layout.Widget) {
		rows = append(rows, func(gtx layout.Context) layout.Dimensions {
			return layout.Inset{Bottom: th.Space.M}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						gtx.Constraints.Min.X = gtx.Dp(140)
						return atoms.Text(label).Layout(gtx, th)
					}),
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						gtx.Constraints.Max.X = min(gtx.Constraints.Max.X, gtx.Dp(300))
						gtx.Constraints.Min.X = gtx.Constraints.Max.X
						return w(gtx)
					}),
				)
			})
		})
	}
	switch Sections[p.Section] {
	case "Modules":
		title("Modules", "Every feature outside the core is a module. Changes apply immediately.")
		for _, m := range p.Modules {
			rows = append(rows, p.moduleRow(th, m))
		}
	case "PHP Interpreter":
		if p.NoProject {
			title("PHP Interpreter", "Open a project to choose its interpreter.")
			break
		}
		title("PHP Interpreter", "Local PHP binaries and Docker Compose services of this project.")
		rows = append(rows, func(gtx layout.Context) layout.Dimensions {
			gtx.Constraints.Max.X = min(gtx.Constraints.Max.X, gtx.Dp(480))
			return p.Interpreter.Layout(gtx, th)
		}, layout.Spacer{Height: th.Space.L}.Layout,
			molecules.SectionHeader{Title: "Path mappings"}.Fn(th),
			func(gtx layout.Context) layout.Dimensions {
				gtx.Constraints.Max.Y = min(gtx.Constraints.Max.Y, gtx.Dp(160))
				return p.Mappings.Layout(gtx, th)
			}, layout.Spacer{Height: th.Space.M}.Layout,
			molecules.SectionHeader{Title: "Xdebug"}.Fn(th),
			func(gtx layout.Context) layout.Dimensions {
				gtx.Constraints.Max.Y = min(gtx.Constraints.Max.Y, gtx.Dp(140))
				return p.Hints.Layout(gtx, th)
			})
	case "Editor":
		title("Editor", "")
		p.tabSel.Selected = indexOf(TabWidths, p.TabWidth)
		p.themeSel.Selected = indexOf(Themes, p.Theme)
		p.syncFonts()
		p.fontSel.Selected = p.fontIndex()
		selectNumber(p.sizeSel, FontSizes, p.EditorFontSize, 13, "%g")
		selectNumber(p.lineSel, LineHeights, p.LineHeight, 1.4, "%.1f")
		selectNumber(p.uiSizeSel, UIFontSizes, p.UIFontSize, 13, "%g")
		field("Tab width", func(gtx layout.Context) layout.Dimensions { return p.tabSel.Layout(gtx, th) })
		field("Theme", func(gtx layout.Context) layout.Dimensions { return p.themeSel.Layout(gtx, th) })
		rows = append(rows, layout.Spacer{Height: th.Space.S}.Layout, molecules.SectionHeader{Title: "Fonts"}.Fn(th),
			layout.Spacer{Height: th.Space.S}.Layout)
		field("Editor font", func(gtx layout.Context) layout.Dimensions { return p.fontSel.Layout(gtx, th) })
		field("Editor font size", func(gtx layout.Context) layout.Dimensions { return p.sizeSel.Layout(gtx, th) })
		field("Line height", func(gtx layout.Context) layout.Dimensions { return p.lineSel.Layout(gtx, th) })
		rows = append(rows, p.fontPreview(th), layout.Spacer{Height: th.Space.M}.Layout)
		field("UI font size", func(gtx layout.Context) layout.Dimensions { return p.uiSizeSel.Layout(gtx, th) })
	case "Autosave":
		title("Autosave", "Tiegel saves when the window, the editor or the tab loses focus, and before run, debug and commit. Every save is kept in the Local History.")
		p.idleSel.Selected = indexOf(IdleChoices, p.IdleSeconds)
		field("Also save", func(gtx layout.Context) layout.Dimensions { return p.idleSel.Layout(gtx, th) })
	case "Updates":
		title("Updates", "Off by default. Tiegel never downloads anything by itself; a check only shows whether a newer release exists.")
		rows = append(rows, func(gtx layout.Context) layout.Dimensions { return p.updateBox.Layout(gtx, th) },
			layout.Spacer{Height: th.Space.M}.Layout,
			func(gtx layout.Context) layout.Dimensions {
				return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
					layout.Rigid(func(gtx layout.Context) layout.Dimensions { return p.checkNow.Layout(gtx, th) }),
					layout.Rigid(layout.Spacer{Width: th.Space.M}.Layout),
					layout.Rigid(atoms.Small(p.UpdateStatus).Fn(th)),
				)
			})
	}
	return p.scroll.Layout(gtx, th, len(rows), func(gtx layout.Context, i int) layout.Dimensions { return rows[i](gtx) })
}

// fontPreview shows FontPreview in the editor font of th (the host applies
// the chosen font preferences to the theme).
func (p *Panel) fontPreview(th *tokens.Theme) layout.Widget {
	return func(gtx layout.Context) layout.Dimensions {
		gtx.Constraints.Max.X = min(gtx.Constraints.Max.X, gtx.Dp(440))
		gtx.Constraints.Min.X = gtx.Constraints.Max.X
		return atoms.Surface{Kind: atoms.SurfaceEditor, Border: true, Rounded: true}.Layout(gtx, th, func(gtx layout.Context) layout.Dimensions {
			return layout.UniformInset(th.Space.S).Layout(gtx, atoms.Code(FontPreview).SingleLine().Fn(th))
		})
	}
}

func (p *Panel) moduleRow(th *tokens.Theme, m Module) layout.Widget {
	return func(gtx layout.Context) layout.Dimensions {
		b := p.box(m.ID)
		b.Label = m.ID
		b.State = checkState(m.Enabled)
		return layout.Inset{Bottom: th.Space.S}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
						layout.Rigid(func(gtx layout.Context) layout.Dimensions { return b.Layout(gtx, th) }),
						layout.Rigid(layout.Spacer{Width: th.Space.S}.Layout),
						layout.Rigid(func(gtx layout.Context) layout.Dimensions {
							switch {
							case m.Error != "":
								return atoms.Badge{Text: "error", Kind: atoms.BadgeDanger}.Layout(gtx, th)
							case m.Running:
								return atoms.Badge{Text: "running", Kind: atoms.BadgeSuccess}.Layout(gtx, th)
							case m.Enabled:
								return atoms.Badge{Text: "enabled"}.Layout(gtx, th)
							}
							return atoms.Badge{Text: "off"}.Layout(gtx, th)
						}),
					)
				}),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					var parts []string
					if len(m.DependsOn) > 0 {
						parts = append(parts, "requires "+strings.Join(m.DependsOn, ", "))
					}
					if len(m.Dependents) > 0 {
						parts = append(parts, "needed by "+strings.Join(m.Dependents, ", "))
					}
					if m.Error != "" {
						parts = append(parts, m.Error)
					}
					if len(parts) == 0 {
						return layout.Dimensions{}
					}
					return layout.Inset{Left: th.Size.Checkbox + th.Space.S}.Layout(gtx, atoms.Small(strings.Join(parts, " · ")).Fn(th))
				}),
			)
		})
	}
}
