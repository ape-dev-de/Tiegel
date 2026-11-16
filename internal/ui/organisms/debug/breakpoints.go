package debug

import (
	"image"
	"path/filepath"
	"strings"

	"gioui.org/io/key"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/widget"

	"github.com/ape-dev-de/tiegel/internal/ui/atoms"
	"github.com/ape-dev-de/tiegel/internal/ui/molecules"
	"github.com/ape-dev-de/tiegel/internal/ui/tokens"
)

// Breakpoint is a line breakpoint as shown in the list.
type Breakpoint struct {
	ID        string
	File      string // host path
	Line      int
	Condition string
	Enabled   bool
}

type bpRow struct {
	row    molecules.BreakpointRow
	edit   atoms.IconButton
	remove atoms.IconButton
}

// BreakpointsList shows all breakpoints. Clicking a row selects it and
// asks the host to jump to its location; the checkbox enables/disables it;
// the selected row offers editing the condition and removing.
type BreakpointsList struct {
	Items []Breakpoint
	// Selected is the ID of the selected breakpoint.
	Selected string

	rows    map[string]*bpRow
	editing string // ID whose condition is edited
	cond    atoms.TextField
	focus   bool
	list    molecules.ScrollList
}

func (b *BreakpointsList) init() {
	if b.rows == nil {
		b.rows = map[string]*bpRow{}
		b.cond.Hint = "Condition (PHP expression), empty for none"
		b.cond.Mono = true
		b.cond.Editor.SingleLine, b.cond.Editor.Submit = true, true
	}
}

func (b *BreakpointsList) row(it Breakpoint) *bpRow {
	r, ok := b.rows[it.ID]
	if !ok {
		r = &bpRow{}
		b.rows[it.ID] = r
	}
	// Data in: the host owns the enabled flag.
	r.row.File, r.row.Line, r.row.Condition = filepath.Base(it.File), it.Line, it.Condition
	r.row.Enabled.State = atoms.Unchecked
	if it.Enabled {
		r.row.Enabled.State = atoms.Checked
	}
	r.row.Selected = it.ID == b.Selected
	return r
}

// Editing returns the ID whose condition is being edited ("" for none).
func (b *BreakpointsList) Editing() string { return b.editing }

// EditCondition opens the condition editor for id.
func (b *BreakpointsList) EditCondition(id string) {
	b.init()
	for _, it := range b.Items {
		if it.ID == id {
			b.editing, b.Selected = id, id
			b.cond.SetText(it.Condition)
			b.cond.Editor.SetCaret(b.cond.Editor.Len(), 0)
			b.focus = true
		}
	}
}

// Update processes input and returns breakpoint events.
func (b *BreakpointsList) Update(gtx layout.Context) []Event {
	b.init()
	var out []Event
	for _, it := range b.Items {
		r := b.row(it)
		if r.remove.Clicked(gtx) {
			out = append(out, BreakpointRemoveEvent{ID: it.ID})
			continue
		}
		if r.edit.Clicked(gtx) {
			b.EditCondition(it.ID)
		}
		clicked, toggled := r.row.Update(gtx)
		if toggled {
			out = append(out, BreakpointToggleEvent{ID: it.ID, Enabled: r.row.IsEnabled()})
		}
		// The checkbox sits inside the row: a toggle is not a row click.
		if clicked && !toggled {
			b.Selected = it.ID
			out = append(out, BreakpointJumpEvent{ID: it.ID, File: it.File, Line: it.Line})
		}
	}
	found := false
	for _, it := range b.Items {
		found = found || it.ID == b.editing
	}
	if !found {
		b.editing = ""
	}
	if b.editing != "" {
		for {
			ev, ok := b.cond.Editor.Update(gtx)
			if !ok {
				break
			}
			if _, ok := ev.(widget.SubmitEvent); ok {
				out = append(out, BreakpointConditionEvent{ID: b.editing, Condition: strings.TrimSpace(b.cond.Text())})
				b.editing = ""
				break
			}
		}
	}
	if b.editing != "" {
		for {
			ev, ok := gtx.Event(key.Filter{Focus: &b.cond.Editor, Name: key.NameEscape})
			if !ok {
				break
			}
			if e, ok := ev.(key.Event); ok && e.State == key.Press {
				b.editing = ""
			}
		}
	}
	// Forget widget state of removed breakpoints.
	if len(b.rows) > len(b.Items) {
		keep := make(map[string]bool, len(b.Items))
		for _, it := range b.Items {
			keep[it.ID] = true
		}
		for id := range b.rows {
			if !keep[id] {
				delete(b.rows, id)
			}
		}
	}
	return out
}

// Layout draws the list; it fills the available space.
func (b *BreakpointsList) Layout(gtx layout.Context, th *tokens.Theme) layout.Dimensions {
	b.Update(gtx)
	if b.focus {
		gtx.Execute(key.FocusCmd{Tag: &b.cond.Editor})
		b.focus = false
	}
	if len(b.Items) == 0 {
		return emptyState(gtx, th, "", "No breakpoints – click the gutter to add one")
	}
	return b.list.Layout(gtx, th, len(b.Items), func(gtx layout.Context, i int) layout.Dimensions {
		it := b.Items[i]
		r := b.row(it)
		gtx.Constraints.Min = image.Point{}
		if it.ID != b.editing {
			return b.layoutRow(gtx, th, it, r)
		}
		return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
			layout.Rigid(func(gtx layout.Context) layout.Dimensions { return b.layoutRow(gtx, th, it, r) }),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return layout.Inset{Left: th.Space.XL, Right: th.Space.XS, Bottom: th.Space.XS}.Layout(gtx,
					func(gtx layout.Context) layout.Dimensions { return b.cond.Layout(gtx, th) })
			}),
		)
	})
}

// layoutRow draws the breakpoint row with the actions of the selected row
// on top of its right end.
func (b *BreakpointsList) layoutRow(gtx layout.Context, th *tokens.Theme, it Breakpoint, r *bpRow) layout.Dimensions {
	d := r.row.Layout(gtx, th)
	if it.ID != b.Selected {
		return d
	}
	for _, ib := range []*atoms.IconButton{&r.edit, &r.remove} {
		ib.Size, ib.IconSize = th.Size.RowH-th.Space.XS, th.Size.IconSmall
	}
	r.edit.Glyph, r.edit.Description = atoms.GlyphEdit, "Edit condition"
	r.remove.Glyph, r.remove.Description = atoms.GlyphClose, "Remove breakpoint"
	cgtx := gtx
	cgtx.Constraints.Min = image.Point{}
	m := op.Record(gtx.Ops)
	ad := layout.Flex{Alignment: layout.Middle}.Layout(cgtx,
		layout.Rigid(func(gtx layout.Context) layout.Dimensions { return r.edit.Layout(gtx, th) }),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions { return r.remove.Layout(gtx, th) }),
	)
	call := m.Stop()
	t := op.Offset(image.Pt(d.Size.X-ad.Size.X-gtx.Dp(th.Space.XS), (d.Size.Y-ad.Size.Y)/2)).Push(gtx.Ops)
	call.Add(gtx.Ops)
	t.Pop()
	return d
}
