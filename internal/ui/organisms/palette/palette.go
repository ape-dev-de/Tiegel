// Package palette provides the chooser popup used for quick open (files),
// recent files, go to line and choosing between several navigation
// targets: a title, an input field and a filtered list. The host filters
// the items on QueryEvent; Up/Down move the selection, Enter chooses,
// Escape cancels.
package palette

import (
	"image"

	"gioui.org/io/key"
	"gioui.org/layout"
	"gioui.org/unit"
	"gioui.org/widget"

	"github.com/ape-dev-de/tiegel/internal/ui/atoms"
	"github.com/ape-dev-de/tiegel/internal/ui/internal/draw"
	"github.com/ape-dev-de/tiegel/internal/ui/molecules"
	"github.com/ape-dev-de/tiegel/internal/ui/tokens"
)

// Item is an entry of the list.
type Item struct {
	Label  string
	Detail string
	Glyph  *atoms.Glyph
	// Group is a category (e.g. "Classes" in Search Everywhere); the
	// first item of each group shows it in a column at the left.
	Group string
}

// Event is reported by Chooser.Update.
type Event interface{ isPaletteEvent() }

// QueryEvent: the input changed.
type QueryEvent struct{ Query string }

// ChooseEvent: an item was chosen (Index into Items; -1 when the list is
// empty and Enter was pressed, e.g. go to line).
type ChooseEvent struct {
	Index int
	Query string
}

// CancelEvent: Escape.
type CancelEvent struct{}

// TabEvent: Tab or Shift+Tab switched the category (Tabs); Index is the
// new Tab.
type TabEvent struct{ Index int }

func (QueryEvent) isPaletteEvent()  {}
func (ChooseEvent) isPaletteEvent() {}
func (CancelEvent) isPaletteEvent() {}
func (TabEvent) isPaletteEvent()    {}

// Width of the chooser.
const Width = unit.Dp(560)

// groupW is the width of the group column.
const groupW = unit.Dp(76)

// layoutTabs draws the category tabs (the active one emphasized).
func (c *Chooser) layoutTabs(th *tokens.Theme) layout.Widget {
	return func(gtx layout.Context) layout.Dimensions {
		if len(c.Tabs) == 0 {
			return layout.Dimensions{}
		}
		children := make([]layout.FlexChild, 0, len(c.Tabs)+1)
		for i, t := range c.Tabs {
			lb := atoms.Muted(t)
			if i == c.Tab {
				lb = atoms.Strong(t).WithColor(th.Color.Accent)
			}
			children = append(children, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return layout.Inset{Right: th.Space.M}.Layout(gtx, lb.SingleLine().Fn(th))
			}))
		}
		children = append(children, layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
			gtx.Constraints.Min.X = 0
			return layout.E.Layout(gtx, atoms.Small("Tab: next category").SingleLine().Fn(th))
		}))
		return layout.Inset{Bottom: th.Space.S}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			return layout.Flex{Alignment: layout.Baseline}.Layout(gtx, children...)
		})
	}
}

// maxRows is the number of visible rows.
const maxRows = 12

// Chooser is the palette organism.
type Chooser struct {
	Title string
	Hint  string
	Items []Item
	// Selected indexes Items.
	Selected int
	// NoList hides the list (plain input such as go to line).
	NoList bool
	// Message is shown instead of an empty list.
	Message string
	// Tabs are categories shown above the input (e.g. All, Classes,
	// Files); Tab and Shift+Tab switch between them (TabEvent).
	Tabs []string
	Tab  int

	field atoms.TextField
	rows  []molecules.ListRow
	list  molecules.ScrollList
	focus bool
	last  int
}

// Open resets the input to query and focuses it on the next Layout.
func (c *Chooser) Open(title, hint, query string) {
	c.Title, c.Hint = title, hint
	c.field.Editor.SingleLine, c.field.Editor.Submit = true, true
	c.field.SetText(query)
	c.field.Editor.SetCaret(len(query), 0)
	c.Selected, c.focus, c.NoList, c.Message = 0, true, false, ""
	c.Tabs, c.Tab = nil, 0
	c.list.ScrollTo(0)
}

// Query returns the input text.
func (c *Chooser) Query() string { return c.field.Text() }

// Update returns the user's intentions.
func (c *Chooser) Update(gtx layout.Context) []Event {
	c.field.Editor.SingleLine, c.field.Editor.Submit = true, true
	c.ensureRows()
	var out []Event
	// The navigation keys are taken before the text field sees them (a
	// single-line editor would move its caret on Up/Down).
	for {
		ev, ok := gtx.Event(
			key.Filter{Focus: &c.field.Editor, Name: key.NameEscape},
			key.Filter{Focus: &c.field.Editor, Name: key.NameUpArrow},
			key.Filter{Focus: &c.field.Editor, Name: key.NameDownArrow},
			key.Filter{Focus: &c.field.Editor, Name: key.NamePageUp},
			key.Filter{Focus: &c.field.Editor, Name: key.NamePageDown},
			key.Filter{Focus: &c.field.Editor, Name: key.NameTab, Optional: key.ModShift},
		)
		if !ok {
			break
		}
		e, ok := ev.(key.Event)
		if !ok || e.State != key.Press {
			continue
		}
		switch e.Name {
		case key.NameEscape:
			out = append(out, CancelEvent{})
		case key.NameUpArrow:
			c.move(-1)
		case key.NameDownArrow:
			c.move(1)
		case key.NamePageUp:
			c.move(-maxRows)
		case key.NamePageDown:
			c.move(maxRows)
		case key.NameTab:
			if n := len(c.Tabs); n > 0 {
				d := 1
				if e.Modifiers.Contain(key.ModShift) {
					d = n - 1
				}
				c.Tab = (c.Tab + d) % n
				c.Selected = 0
				out = append(out, TabEvent{Index: c.Tab})
			}
		}
	}
	for {
		ev, ok := c.field.Editor.Update(gtx)
		if !ok {
			break
		}
		switch ev.(type) {
		case widget.ChangeEvent:
			c.Selected = 0
			out = append(out, QueryEvent{Query: c.field.Text()})
		case widget.SubmitEvent:
			out = append(out, c.choose())
		}
	}
	for i := range c.Items {
		if cl, _ := c.rows[i].Update(gtx); cl {
			c.Selected = i
			out = append(out, c.choose())
		}
	}
	return out
}

// ensureRows grows the row widgets to match Items. Callers may replace
// Items between Update and Layout (e.g. on a QueryEvent), so both call it.
func (c *Chooser) ensureRows() {
	for len(c.rows) < len(c.Items) {
		c.rows = append(c.rows, molecules.ListRow{})
	}
}

func (c *Chooser) choose() ChooseEvent {
	if len(c.Items) == 0 || c.NoList {
		return ChooseEvent{Index: -1, Query: c.field.Text()}
	}
	return ChooseEvent{Index: min(max(c.Selected, 0), len(c.Items)-1), Query: c.field.Text()}
}

func (c *Chooser) move(d int) {
	if n := len(c.Items); n > 0 {
		c.Selected = min(max(c.Selected+d, 0), n-1)
	}
}

// Layout draws the chooser at Width (height by content).
func (c *Chooser) Layout(gtx layout.Context, th *tokens.Theme) layout.Dimensions {
	if c.focus {
		c.focus = false
		gtx.Execute(key.FocusCmd{Tag: &c.field.Editor})
	}
	c.field.Hint = c.Hint
	w := min(gtx.Dp(Width), draw.Width(gtx, gtx.Dp(Width)))
	if c.Selected != c.last {
		c.last = c.Selected
		if c.Selected < c.list.FirstVisible() {
			c.list.ScrollTo(c.Selected)
		} else if c.Selected > c.list.LastVisible() {
			c.list.ScrollTo(c.Selected - maxRows + 1)
		}
	}
	gtx.Constraints = layout.Constraints{Min: image.Pt(w, 0), Max: image.Pt(w, gtx.Constraints.Max.Y)}
	return atoms.Surface{Kind: atoms.SurfacePanel, Border: true, Rounded: true}.Layout(gtx, th, func(gtx layout.Context) layout.Dimensions {
		return layout.UniformInset(th.Space.S).Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			children := []layout.FlexChild{
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					if c.Title == "" {
						return layout.Dimensions{}
					}
					return layout.Inset{Bottom: th.Space.S}.Layout(gtx, atoms.Strong(c.Title).Fn(th))
				}),
				layout.Rigid(c.layoutTabs(th)),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions { return c.field.Layout(gtx, th) }),
			}
			if !c.NoList || c.Message != "" {
				children = append(children, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return layout.Inset{Top: th.Space.S}.Layout(gtx, c.layoutList(th))
				}))
			}
			return layout.Flex{Axis: layout.Vertical}.Layout(gtx, children...)
		})
	})
}

func (c *Chooser) layoutList(th *tokens.Theme) layout.Widget {
	return func(gtx layout.Context) layout.Dimensions {
		if len(c.Items) == 0 || c.NoList {
			msg := c.Message
			if msg == "" {
				msg = "Nothing found"
			}
			return atoms.Muted(msg).Layout(gtx, th)
		}
		c.ensureRows()
		rowH := gtx.Dp(th.Size.RowH)
		h := min(len(c.Items), maxRows) * rowH
		gtx.Constraints = layout.Exact(image.Pt(gtx.Constraints.Max.X, h))
		groups := false
		for _, it := range c.Items {
			if it.Group != "" {
				groups = true
				break
			}
		}
		return c.list.Layout(gtx, th, len(c.Items), func(gtx layout.Context, i int) layout.Dimensions {
			it := c.Items[i]
			r := &c.rows[i]
			r.Selected = i == c.Selected
			return r.Layout(gtx, th, func(gtx layout.Context) layout.Dimensions {
				return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						if !groups {
							return layout.Dimensions{}
						}
						w := gtx.Dp(groupW)
						gtx.Constraints = layout.Constraints{Max: image.Pt(w, gtx.Constraints.Max.Y)}
						if i > 0 && c.Items[i-1].Group == it.Group {
							return layout.Dimensions{Size: image.Pt(w, 0)}
						}
						d := atoms.Small(it.Group).SingleLine().Layout(gtx, th)
						return layout.Dimensions{Size: image.Pt(w, d.Size.Y), Baseline: d.Baseline}
					}),
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						if it.Glyph == nil {
							return layout.Dimensions{}
						}
						return layout.Inset{Right: th.Space.S}.Layout(gtx, atoms.Icon{Glyph: it.Glyph, Size: th.Size.IconSmall, Color: th.Color.TextMuted}.Fn(th))
					}),
					layout.Rigid(atoms.Text(it.Label).SingleLine().Fn(th)),
					layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
						gtx.Constraints.Min.X = 0
						if it.Detail == "" {
							return layout.Dimensions{}
						}
						return layout.Inset{Left: th.Space.M}.Layout(gtx, atoms.Small(it.Detail).SingleLine().Fn(th))
					}),
				)
			})
		})
	}
}
