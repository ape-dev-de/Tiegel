// Package settings provides the settings dialog: modules on/off with their
// dependencies, the PHP interpreter (picker, path mappings, Xdebug hints),
// editor options (tab width, theme), autosave and the opt-in update check.
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
func (IdleEvent) isSettingsEvent()        {}
func (UpdateCheckEvent) isSettingsEvent() {}
func (CheckNowEvent) isSettingsEvent()    {}
func (RuntimeEvent) isSettingsEvent()     {}
func (CloseEvent) isSettingsEvent()       {}

// Sections of the dialog.
var Sections = []string{"Modules", "PHP Interpreter", "Editor", "Autosave", "Updates"}

// TabWidths, Themes and IdleChoices are the offered values.
var (
	TabWidths   = []int{2, 4, 8}
	Themes      = []string{"dark", "light", "system"}
	IdleChoices = []int{0, 5, 15, 30, 60, 300}
)

// Panel is the settings organism.
type Panel struct {
	Section int

	Modules     []Module
	TabWidth    int
	Theme       string
	IdleSeconds int
	UpdateCheck bool
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
	idleSel   *molecules.Dropdown
	updateBox atoms.Checkbox
	checkNow  atoms.Button
	closeBtn  atoms.IconButton
	scroll    molecules.ScrollList
	inited    bool
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
	p.idleSel = molecules.NewSelect("Idle autosave", idles...)
	p.updateBox.Label = "Check for updates when Tiegel starts (contacts the release server)"
	p.checkNow = atoms.Button{Text: "Check now", Kind: atoms.Secondary, Glyph: atoms.GlyphRefresh}
	p.closeBtn = atoms.IconButton{Glyph: atoms.GlyphClose, Description: "Close (Escape)"}
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
		field("Tab width", func(gtx layout.Context) layout.Dimensions { return p.tabSel.Layout(gtx, th) })
		field("Theme", func(gtx layout.Context) layout.Dimensions { return p.themeSel.Layout(gtx, th) })
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
