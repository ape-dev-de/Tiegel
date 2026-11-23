// Package projectsetup is the project setup sheet (D526): the values
// discovered for an unconfigured project (PHP root, interpreter, PHPUnit,
// quality tools), each prefilled with the recommended choice. Nothing is
// written until the user clicks Apply; only the checked rows are applied.
//
// The host fills View from project.setupProposal and turns ApplyEvent into
// project.setupApply.
package projectsetup

import (
	"gioui.org/layout"
	"gioui.org/unit"

	"github.com/ape-dev-de/tiegel/internal/ui/atoms"
	"github.com/ape-dev-de/tiegel/internal/ui/molecules"
	"github.com/ape-dev-de/tiegel/internal/ui/tokens"
)

// Choice is a value of an item.
type Choice struct {
	Value       any
	Label       string
	Detail      string
	Recommended bool
}

// Item is a proposed setting.
type Item struct {
	Key   string
	Label string
	// Choices are the candidates; the first recommended one is preselected.
	Choices []Choice
}

// Value is an applied value.
type Value struct {
	Key   string
	Value any
}

// Event is reported by Update.
type Event interface{ isSetupEvent() }

// ApplyEvent: Apply was clicked; Values are the checked rows with the
// chosen values.
type ApplyEvent struct{ Values []Value }

// LaterEvent: "Not Now" – close, ask again next time.
type LaterEvent struct{}

// DismissEvent: "Don't Ask Again" for this project.
type DismissEvent struct{}

func (ApplyEvent) isSetupEvent()   {}
func (LaterEvent) isSetupEvent()   {}
func (DismissEvent) isSetupEvent() {}

// View is the setup sheet.
type View struct {
	Items []Item
	// Busy disables the buttons while applying; Message reports the
	// result (Error in red).
	Busy    bool
	Message string
	Error   bool

	key     string
	checks  []atoms.Checkbox
	selects []*molecules.Dropdown
	apply   atoms.Button
	later   atoms.Button
	dismiss atoms.Button
	scroll  molecules.ScrollList
}

func (v *View) sync() {
	key := ""
	for _, it := range v.Items {
		key += it.Key + "\x00"
		for _, c := range it.Choices {
			key += c.Label + "\x01"
		}
	}
	if key == v.key && len(v.checks) == len(v.Items) {
		return
	}
	v.key = key
	v.checks = make([]atoms.Checkbox, len(v.Items))
	v.selects = make([]*molecules.Dropdown, len(v.Items))
	for i, it := range v.Items {
		v.checks[i] = atoms.Checkbox{Label: it.Label, State: atoms.Checked}
		opts := make([]molecules.DropdownOption, len(it.Choices))
		sel := -1
		for j, c := range it.Choices {
			opts[j] = molecules.DropdownOption{Label: c.Label, Detail: c.Detail}
			if c.Recommended {
				opts[j].Badge, opts[j].BadgeKind = "recommended", atoms.BadgeAccent
				if sel < 0 {
					sel = j
				}
			}
		}
		if sel < 0 && len(opts) > 0 {
			sel = 0
		}
		d := molecules.NewSelect(it.Label, opts...)
		d.Selected = sel
		v.selects[i] = d
	}
	v.apply = atoms.Button{Text: "Apply", Kind: atoms.Primary, Glyph: atoms.GlyphCheck}
	v.later = atoms.Button{Text: "Not Now", Kind: atoms.Secondary}
	v.dismiss = atoms.Button{Text: "Don't Ask Again", Kind: atoms.Secondary}
}

// Selected returns the checked rows with their chosen values.
func (v *View) Selected() []Value {
	v.sync()
	var out []Value
	for i, it := range v.Items {
		s := v.selects[i].Selected
		if !v.checks[i].Checked() || s < 0 || s >= len(it.Choices) {
			continue
		}
		out = append(out, Value{Key: it.Key, Value: it.Choices[s].Value})
	}
	return out
}

// Update returns the user's actions.
func (v *View) Update(gtx layout.Context) []Event {
	v.sync()
	var out []Event
	for i := range v.Items {
		v.checks[i].Update(gtx)
		v.selects[i].Update(gtx)
	}
	if v.Busy {
		return nil
	}
	if v.apply.Clicked(gtx) {
		if vals := v.Selected(); len(vals) > 0 {
			out = append(out, ApplyEvent{Values: vals})
		}
	}
	if v.later.Clicked(gtx) {
		out = append(out, LaterEvent{})
	}
	if v.dismiss.Clicked(gtx) {
		out = append(out, DismissEvent{})
	}
	return out
}

// Layout draws the rows and the buttons.
func (v *View) Layout(gtx layout.Context, th *tokens.Theme) layout.Dimensions {
	v.sync()
	rows := []layout.Widget{}
	if len(v.Items) == 0 {
		rows = append(rows, atoms.Small("Nothing to set up: every setting is configured or nothing was found.").Fn(th))
	}
	for i := range v.Items {
		i := i
		rows = append(rows, func(gtx layout.Context) layout.Dimensions {
			return layout.Inset{Bottom: th.Space.M}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						v.checks[i].Disabled = v.Busy
						return v.checks[i].Layout(gtx, th)
					}),
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						return layout.Inset{Top: th.Space.XS, Left: th.Size.Checkbox + th.Space.S}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
							gtx.Constraints.Max.X = min(gtx.Constraints.Max.X, gtx.Dp(unit.Dp(520)))
							gtx.Constraints.Min.X = gtx.Constraints.Max.X
							v.selects[i].Disabled = v.Busy || !v.checks[i].Checked()
							return v.selects[i].Layout(gtx, th)
						})
					}),
				)
			})
		})
	}
	return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
		layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
			return v.scroll.Layout(gtx, th, len(rows), func(gtx layout.Context, i int) layout.Dimensions { return rows[i](gtx) })
		}),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			if v.Message == "" {
				return layout.Dimensions{}
			}
			l := atoms.Small(v.Message)
			if v.Error {
				l.Color = th.Color.Danger
			}
			return layout.Inset{Top: th.Space.S}.Layout(gtx, l.Fn(th))
		}),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Inset{Top: th.Space.M}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						v.apply.Disabled, v.later.Disabled, v.dismiss.Disabled = v.Busy, v.Busy, v.Busy
						return v.dismiss.Layout(gtx, th)
					}),
					layout.Flexed(1, layout.Spacer{}.Layout),
					layout.Rigid(func(gtx layout.Context) layout.Dimensions { return v.later.Layout(gtx, th) }),
					layout.Rigid(layout.Spacer{Width: th.Space.S}.Layout),
					layout.Rigid(func(gtx layout.Context) layout.Dimensions { return v.apply.Layout(gtx, th) }),
				)
			})
		}),
	)
}
