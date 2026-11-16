package runtime

import (
	"fmt"
	"image"

	"gioui.org/layout"

	"github.com/ape-dev-de/tiegel/internal/ui/atoms"
	"github.com/ape-dev-de/tiegel/internal/ui/molecules"
	"github.com/ape-dev-de/tiegel/internal/ui/tokens"
)

// Kind tells where an interpreter runs.
type Kind string

// Interpreter kinds (same values as interp.Kind).
const (
	KindLocal   Kind = "local"
	KindCompose Kind = "compose"
	KindWSL     Kind = "wsl"
)

// Interpreter is one choice of the picker.
type Interpreter struct {
	ID    string
	Label string
	Kind  Kind
	// Version is the PHP version, e.g. "8.3.4"; empty if unknown.
	Version string
	// Detail is a path or image name.
	Detail string
	// Score and Reasons explain how likely a Compose service runs PHP
	// (compose.Candidate); zero/empty for local interpreters.
	Score   int
	Reasons []string
}

// InterpreterPicker selects the PHP interpreter of a project: a dropdown of
// local binaries and Compose candidates (best score marked), followed by
// the details of the selected one.
type InterpreterPicker struct {
	Items []Interpreter
	// Selected is the ID of the selected interpreter.
	Selected string

	dd molecules.Dropdown
}

// Best returns the index of the Compose candidate with the highest score,
// or -1.
func (p *InterpreterPicker) Best() int {
	best, score := -1, 0
	for i, it := range p.Items {
		if it.Kind == KindCompose && it.Score > score {
			best, score = i, it.Score
		}
	}
	return best
}

func (p *InterpreterPicker) sync() {
	best := p.Best()
	p.dd.Placeholder = "Choose a PHP interpreter"
	p.dd.Options = p.dd.Options[:0]
	p.dd.Selected = -1
	for i, it := range p.Items {
		o := molecules.DropdownOption{Label: it.Label, Detail: it.Detail, Glyph: kindGlyph(it.Kind), Badge: it.Version, BadgeKind: atoms.BadgeInfo}
		if it.Kind == KindCompose {
			o.Badge, o.BadgeKind = fmt.Sprintf("score %d", it.Score), atoms.BadgeNeutral
			if i == best {
				o.BadgeKind = atoms.BadgeAccent
			}
		}
		p.dd.Options = append(p.dd.Options, o)
		if it.ID == p.Selected {
			p.dd.Selected = i
		}
	}
}

func kindGlyph(k Kind) *atoms.Glyph {
	if k == KindCompose {
		return atoms.GlyphContainer
	}
	return atoms.GlyphComputer
}

// Update returns a SelectInterpreterEvent when the user picked another
// interpreter.
func (p *InterpreterPicker) Update(gtx layout.Context) []Event {
	p.sync()
	if p.dd.Update(gtx) && p.dd.Selected >= 0 {
		p.Selected = p.Items[p.dd.Selected].ID
		return []Event{SelectInterpreterEvent{ID: p.Selected}}
	}
	return nil
}

// Open opens or closes the dropdown menu.
func (p *InterpreterPicker) Open(open bool) { p.dd.SetOpen(open) }

// Layout draws the picker across the available width.
func (p *InterpreterPicker) Layout(gtx layout.Context, th *tokens.Theme) layout.Dimensions {
	p.Update(gtx)
	gtx.Constraints.Min.Y = 0
	return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
		layout.Rigid(func(gtx layout.Context) layout.Dimensions { return p.dd.Layout(gtx, th) }),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			i := p.dd.Selected
			if i < 0 {
				return layout.Dimensions{}
			}
			return layout.Inset{Top: th.Space.S, Left: th.Space.XS}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return p.layoutDetails(gtx, th, p.Items[i], i == p.Best())
			})
		}),
	)
}

func (p *InterpreterPicker) layoutDetails(gtx layout.Context, th *tokens.Theme, it Interpreter, best bool) layout.Dimensions {
	gtx.Constraints.Min = image.Point{}
	badges := []layout.FlexChild{
		layout.Rigid(atoms.Badge{Text: string(it.Kind)}.Fn(th)),
	}
	if it.Version != "" {
		badges = append(badges, layout.Rigid(layout.Spacer{Width: th.Space.XS}.Layout),
			layout.Rigid(atoms.Badge{Text: "PHP " + it.Version, Kind: atoms.BadgeInfo}.Fn(th)))
	}
	if best {
		badges = append(badges, layout.Rigid(layout.Spacer{Width: th.Space.XS}.Layout),
			layout.Rigid(atoms.Badge{Text: "recommended", Kind: atoms.BadgeAccent}.Fn(th)))
	}
	if it.Detail != "" {
		badges = append(badges, layout.Rigid(layout.Spacer{Width: th.Space.S}.Layout),
			layout.Flexed(1, atoms.Label{Text: it.Detail, Role: atoms.RoleCode, Color: th.Color.TextMuted, MaxLines: 1}.Fn(th)))
	}
	rows := []layout.FlexChild{
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Flex{Alignment: layout.Middle}.Layout(gtx, badges...)
		}),
	}
	for _, r := range it.Reasons {
		rows = append(rows, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Inset{Top: th.Space.XS}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
					layout.Rigid(atoms.Icon{Glyph: atoms.GlyphCheck, Size: th.Size.IconSmall, Color: th.Color.Success}.Fn(th)),
					layout.Rigid(layout.Spacer{Width: th.Space.XS}.Layout),
					layout.Flexed(1, atoms.Small(r).Fn(th)),
				)
			})
		}))
	}
	return layout.Flex{Axis: layout.Vertical}.Layout(gtx, rows...)
}
