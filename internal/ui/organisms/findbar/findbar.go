// Package findbar provides the find/replace bar shown above the editor:
// a query field with match counter, options (case, whole word, regex),
// previous/next buttons and – in replace mode – a replacement field with
// "Replace" and "Replace All".
package findbar

import (
	"fmt"
	"image"

	"gioui.org/io/key"
	"gioui.org/layout"
	"gioui.org/widget"

	"github.com/ape-dev-de/tiegel/internal/ui/atoms"
	"github.com/ape-dev-de/tiegel/internal/ui/internal/draw"
	"github.com/ape-dev-de/tiegel/internal/ui/tokens"
)

// Options are the search options.
type Options struct {
	CaseSensitive, WholeWord, Regex bool
}

// Event is reported by Bar.Update.
type Event interface{ isFindEvent() }

// QueryEvent: the query or an option changed.
type QueryEvent struct {
	Query   string
	Options Options
}

// NextEvent / PrevEvent: go to the next/previous match (Enter / buttons).
type NextEvent struct{}

// PrevEvent goes to the previous match.
type PrevEvent struct{}

// ReplaceEvent replaces the current match.
type ReplaceEvent struct{ With string }

// ReplaceAllEvent replaces all matches.
type ReplaceAllEvent struct{ With string }

// CloseEvent: Escape or the close button.
type CloseEvent struct{}

func (QueryEvent) isFindEvent()      {}
func (NextEvent) isFindEvent()       {}
func (PrevEvent) isFindEvent()       {}
func (ReplaceEvent) isFindEvent()    {}
func (ReplaceAllEvent) isFindEvent() {}
func (CloseEvent) isFindEvent()      {}

// Bar is the find/replace organism.
type Bar struct {
	// ReplaceMode shows the replacement row.
	ReplaceMode bool
	// Matches is the number of matches, Current the 1-based index of the
	// selected one (0: none selected).
	Matches, Current int
	// Invalid marks a query that does not compile (bad regex).
	Invalid bool

	query, repl                atoms.TextField
	caseBox, wordBox, regexBox atoms.Checkbox
	prev, next, close          atoms.IconButton
	replaceBtn, replaceAllBtn  atoms.Button
	inited                     bool
	focusQuery                 bool
}

func (b *Bar) init() {
	if b.inited {
		return
	}
	b.inited = true
	for _, f := range []*atoms.TextField{&b.query, &b.repl} {
		f.Editor.SingleLine, f.Editor.Submit = true, true
	}
	b.query.Hint, b.query.Leading = "Find", atoms.GlyphSearch
	b.repl.Hint, b.repl.Leading = "Replace with", atoms.GlyphSwap
	b.caseBox.Label, b.wordBox.Label, b.regexBox.Label = "Aa", "Words", ".*"
	b.prev = atoms.IconButton{Glyph: atoms.GlyphChevronUp, Description: "Previous match"}
	b.next = atoms.IconButton{Glyph: atoms.GlyphChevronDown, Description: "Next match"}
	b.close = atoms.IconButton{Glyph: atoms.GlyphClose, Description: "Close (Escape)"}
	b.replaceBtn = atoms.Button{Text: "Replace", Kind: atoms.Secondary}
	b.replaceAllBtn = atoms.Button{Text: "Replace All", Kind: atoms.Secondary}
}

// Query returns the current query text.
func (b *Bar) Query() string { return b.query.Text() }

// Options returns the current options.
func (b *Bar) Options() Options {
	return Options{CaseSensitive: b.caseBox.Checked(), WholeWord: b.wordBox.Checked(), Regex: b.regexBox.Checked()}
}

// Open shows the bar with an optional initial query and focuses the query
// field on the next Layout.
func (b *Bar) Open(query string, replace bool) {
	b.init()
	if query != "" {
		b.query.SetText(query)
		b.query.Editor.SetCaret(len(query), 0)
	}
	b.ReplaceMode = replace
	b.focusQuery = true
}

// Update returns the user's intentions since the last frame.
func (b *Bar) Update(gtx layout.Context) []Event {
	b.init()
	var out []Event
	changed := false
	for _, f := range []*atoms.TextField{&b.query, &b.repl} {
		for {
			ev, ok := f.Editor.Update(gtx)
			if !ok {
				break
			}
			switch ev.(type) {
			case widget.ChangeEvent:
				changed = changed || f == &b.query
			case widget.SubmitEvent:
				if f == &b.query {
					out = append(out, NextEvent{})
				} else {
					out = append(out, ReplaceEvent{With: b.repl.Text()})
				}
			}
		}
		for {
			ev, ok := gtx.Event(key.Filter{Focus: &f.Editor, Name: key.NameEscape})
			if !ok {
				break
			}
			if e, ok := ev.(key.Event); ok && e.State == key.Press {
				out = append(out, CloseEvent{})
			}
		}
	}
	for _, c := range []*atoms.Checkbox{&b.caseBox, &b.wordBox, &b.regexBox} {
		if c.Update(gtx) {
			changed = true
		}
	}
	if changed {
		out = append(out, QueryEvent{Query: b.query.Text(), Options: b.Options()})
	}
	if b.prev.Clicked(gtx) {
		out = append(out, PrevEvent{})
	}
	if b.next.Clicked(gtx) {
		out = append(out, NextEvent{})
	}
	if b.close.Clicked(gtx) {
		out = append(out, CloseEvent{})
	}
	if b.replaceBtn.Clicked(gtx) {
		out = append(out, ReplaceEvent{With: b.repl.Text()})
	}
	if b.replaceAllBtn.Clicked(gtx) {
		out = append(out, ReplaceAllEvent{With: b.repl.Text()})
	}
	return out
}

// Layout draws the bar across the full width. Call Update first to
// observe events.
func (b *Bar) Layout(gtx layout.Context, th *tokens.Theme) layout.Dimensions {
	b.init()
	if b.focusQuery {
		b.focusQuery = false
		gtx.Execute(key.FocusCmd{Tag: &b.query.Editor})
		b.query.Editor.SetCaret(b.query.Editor.Len(), 0)
	}
	w := draw.Width(gtx, gtx.Dp(600))
	return atoms.Surface{Kind: atoms.SurfaceAlt}.Layout(gtx, th, func(gtx layout.Context) layout.Dimensions {
		gtx.Constraints = layout.Constraints{Min: image.Pt(w, 0), Max: image.Pt(w, gtx.Constraints.Max.Y)}
		return layout.UniformInset(th.Space.XS).Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			rows := []layout.FlexChild{layout.Rigid(b.findRow(th))}
			if b.ReplaceMode {
				rows = append(rows, layout.Rigid(layout.Spacer{Height: th.Space.XS}.Layout), layout.Rigid(b.replaceRow(th)))
			}
			return layout.Flex{Axis: layout.Vertical}.Layout(gtx, rows...)
		})
	})
}

func (b *Bar) counter() string {
	switch {
	case b.Invalid:
		return "Invalid pattern"
	case b.query.Text() == "":
		return ""
	case b.Matches == 0:
		return "No matches"
	case b.Current > 0:
		return fmt.Sprintf("%d of %d", b.Current, b.Matches)
	}
	return fmt.Sprintf("%d matches", b.Matches)
}

func (b *Bar) findRow(th *tokens.Theme) layout.Widget {
	return func(gtx layout.Context) layout.Dimensions {
		b.query.Invalid = b.Invalid || (b.Matches == 0 && b.query.Text() != "")
		b.prev.Disabled, b.next.Disabled = b.Matches == 0, b.Matches == 0
		return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
			layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
				gtx.Constraints.Max.X = min(gtx.Constraints.Max.X, gtx.Dp(420))
				return b.query.Layout(gtx, th)
			}),
			layout.Rigid(gap(th)),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				gtx.Constraints.Min.X = gtx.Dp(90)
				return atoms.Small(b.counter()).SingleLine().Layout(gtx, th)
			}),
			layout.Rigid(ib(th, &b.prev)),
			layout.Rigid(ib(th, &b.next)),
			layout.Rigid(gap(th)),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions { return b.caseBox.Layout(gtx, th) }),
			layout.Rigid(gap(th)),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions { return b.wordBox.Layout(gtx, th) }),
			layout.Rigid(gap(th)),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions { return b.regexBox.Layout(gtx, th) }),
			layout.Flexed(0.0001, func(gtx layout.Context) layout.Dimensions { return layout.Dimensions{Size: gtx.Constraints.Min} }),
			layout.Rigid(ib(th, &b.close)),
		)
	}
}

func (b *Bar) replaceRow(th *tokens.Theme) layout.Widget {
	return func(gtx layout.Context) layout.Dimensions {
		b.replaceBtn.Disabled, b.replaceAllBtn.Disabled = b.Matches == 0, b.Matches == 0
		return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
			layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
				gtx.Constraints.Max.X = min(gtx.Constraints.Max.X, gtx.Dp(420))
				return b.repl.Layout(gtx, th)
			}),
			layout.Rigid(gap(th)),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions { return b.replaceBtn.Layout(gtx, th) }),
			layout.Rigid(gap(th)),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions { return b.replaceAllBtn.Layout(gtx, th) }),
			layout.Flexed(0.0001, func(gtx layout.Context) layout.Dimensions { return layout.Dimensions{Size: gtx.Constraints.Min} }),
		)
	}
}

func gap(th *tokens.Theme) layout.Widget { return layout.Spacer{Width: th.Space.S}.Layout }

func ib(th *tokens.Theme, b *atoms.IconButton) layout.Widget {
	return func(gtx layout.Context) layout.Dimensions { return b.Layout(gtx, th) }
}
