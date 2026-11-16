// Package problems provides the Problems tool window: all diagnostics of
// the project (language server and PHP feature check) with severity icon,
// message, source and location. Clicking an entry reports OpenEvent so the
// host jumps to it.
package problems

import (
	"fmt"
	"image/color"

	"gioui.org/layout"

	"github.com/ape-dev-de/tiegel/internal/ui/atoms"
	"github.com/ape-dev-de/tiegel/internal/ui/molecules"
	"github.com/ape-dev-de/tiegel/internal/ui/tokens"
)

// Severity of a problem, most severe first.
type Severity uint8

// Severities.
const (
	Error Severity = iota
	Warning
	Info
	Hint
)

// Problem is one entry.
type Problem struct {
	Path      string
	Line, Col int
	Severity  Severity
	Message   string
	Source    string
}

// Event is reported by List.Update.
type Event interface{ isProblemsEvent() }

// OpenEvent: a problem was clicked (Index into Items).
type OpenEvent struct{ Index int }

func (OpenEvent) isProblemsEvent() {}

// List is the problems organism.
type List struct {
	Items []Problem
	// Empty is shown without problems.
	Empty string

	rows     []molecules.ListRow
	list     molecules.ScrollList
	selected int
}

// Update returns clicks.
func (l *List) Update(gtx layout.Context) []Event {
	for len(l.rows) < len(l.Items) {
		l.rows = append(l.rows, molecules.ListRow{})
	}
	var out []Event
	for i := range l.Items {
		if c, _ := l.rows[i].Update(gtx); c {
			l.selected = i + 1
			out = append(out, OpenEvent{Index: i})
		}
	}
	return out
}

// Style returns the icon and color of a severity.
func Style(th *tokens.Theme, s Severity) (*atoms.Glyph, color.NRGBA) {
	switch s {
	case Error:
		return atoms.GlyphError, th.Color.Danger
	case Warning:
		return atoms.GlyphWarning, th.Color.Warning
	}
	return atoms.GlyphInfo, th.Color.Info
}

// Layout draws the list filling the available space.
func (l *List) Layout(gtx layout.Context, th *tokens.Theme) layout.Dimensions {
	l.Update(gtx)
	size := gtx.Constraints.Max
	if len(l.Items) == 0 {
		text := l.Empty
		if text == "" {
			text = "No problems"
		}
		layout.UniformInset(th.Space.M).Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
				layout.Rigid(atoms.Icon{Glyph: atoms.GlyphDone, Color: th.Color.Success}.Fn(th)),
				layout.Rigid(layout.Spacer{Width: th.Space.S}.Layout),
				layout.Rigid(atoms.Muted(text).Fn(th)),
			)
		})
		return layout.Dimensions{Size: size}
	}
	return l.list.Layout(gtx, th, len(l.Items), func(gtx layout.Context, i int) layout.Dimensions {
		p := l.Items[i]
		r := &l.rows[i]
		r.Selected = i+1 == l.selected
		g, col := Style(th, p.Severity)
		return r.Layout(gtx, th, func(gtx layout.Context) layout.Dimensions {
			return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
				layout.Rigid(atoms.Icon{Glyph: g, Size: th.Size.IconSmall, Color: col}.Fn(th)),
				layout.Rigid(layout.Spacer{Width: th.Space.S}.Layout),
				layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
					gtx.Constraints.Min.X = 0
					return layout.Flex{Alignment: layout.Baseline}.Layout(gtx,
						layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
							gtx.Constraints.Min.X = 0
							return atoms.Text(p.Message).SingleLine().Layout(gtx, th)
						}),
						layout.Rigid(func(gtx layout.Context) layout.Dimensions {
							loc := fmt.Sprintf("%s:%d:%d", p.Path, p.Line, p.Col)
							if p.Source != "" {
								loc = p.Source + "  " + loc
							}
							return layout.Inset{Left: th.Space.M}.Layout(gtx, atoms.Small(loc).SingleLine().Fn(th))
						}),
					)
				}),
			)
		})
	})
}

// Counts returns the number of errors and warnings in items.
func Counts(items []Problem) (errs, warns int) {
	for _, p := range items {
		switch p.Severity {
		case Error:
			errs++
		case Warning:
			warns++
		}
	}
	return errs, warns
}
