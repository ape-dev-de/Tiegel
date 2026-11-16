package debug

import (
	"image"
	"strings"

	"gioui.org/io/key"
	"gioui.org/layout"
	"gioui.org/widget"

	"github.com/ape-dev-de/tiegel/internal/ui/atoms"
	"github.com/ape-dev-de/tiegel/internal/ui/molecules"
	"github.com/ape-dev-de/tiegel/internal/ui/tokens"
)

// Watch is a watched expression and its last evaluated value.
type Watch struct {
	Expression string
	Value      string
	Type       string
	// Error is shown instead of the value when evaluation failed.
	Error string
}

type watchRow struct {
	row    molecules.TreeRow
	remove atoms.IconButton
}

// WatchesList shows watch expressions with an input to add new ones. A
// double click edits an expression inline (Enter commits, Escape cancels);
// the × button removes it.
type WatchesList struct {
	Items []Watch

	input   atoms.TextField
	add     atoms.IconButton
	rows    []watchRow
	editing int // index being edited, -1 for none
	editor  atoms.TextField
	focus   bool // focus the editor in the next frame
	list    molecules.ScrollList
	inited  bool
}

func (w *WatchesList) init() {
	if w.inited {
		return
	}
	w.inited = true
	w.editing = -1
	w.input.Hint = "Add watch expression"
	w.input.Mono = true
	w.input.Editor.SingleLine, w.input.Editor.Submit = true, true
	w.editor.Mono = true
	w.editor.Editor.SingleLine, w.editor.Editor.Submit = true, true
	w.add = atoms.IconButton{Glyph: atoms.GlyphAdd, Description: "Add watch"}
}

// Editing returns the index of the watch being edited, or -1.
func (w *WatchesList) Editing() int {
	w.init()
	return w.editing
}

// Edit starts editing the watch at i.
func (w *WatchesList) Edit(i int) {
	w.init()
	if i < 0 || i >= len(w.Items) {
		return
	}
	w.editing = i
	w.editor.SetText(w.Items[i].Expression)
	w.editor.Editor.SetCaret(w.editor.Editor.Len(), 0)
	w.focus = true
}

// Input returns the add field (e.g. to prefill it).
func (w *WatchesList) Input() *atoms.TextField {
	w.init()
	return &w.input
}

// Update processes input and returns watch events.
func (w *WatchesList) Update(gtx layout.Context) []Event {
	w.init()
	if len(w.rows) != len(w.Items) {
		w.rows = make([]watchRow, len(w.Items))
	}
	if w.editing >= len(w.Items) {
		w.editing = -1
	}
	var out []Event
	submit := w.add.Clicked(gtx)
	for {
		ev, ok := w.input.Editor.Update(gtx)
		if !ok {
			break
		}
		if _, ok := ev.(widget.SubmitEvent); ok {
			submit = true
		}
	}
	if expr := strings.TrimSpace(w.input.Text()); submit && expr != "" {
		out = append(out, WatchAddEvent{Expression: expr})
		w.input.SetText("")
	}
	if w.editing >= 0 {
		for {
			ev, ok := w.editor.Editor.Update(gtx)
			if !ok {
				break
			}
			if _, ok := ev.(widget.SubmitEvent); ok {
				expr := strings.TrimSpace(w.editor.Text())
				if expr != "" && expr != w.Items[w.editing].Expression {
					out = append(out, WatchEditEvent{Index: w.editing, Expression: expr})
				}
				w.editing = -1
				break
			}
		}
	}
	if w.editing >= 0 {
		for {
			ev, ok := gtx.Event(key.Filter{Focus: &w.editor.Editor, Name: key.NameEscape})
			if !ok {
				break
			}
			if e, ok := ev.(key.Event); ok && e.State == key.Press {
				w.editing = -1
				break
			}
		}
	}
	for i := range w.rows {
		r := &w.rows[i]
		if r.remove.Clicked(gtx) {
			out = append(out, WatchRemoveEvent{Index: i})
			continue
		}
		if _, _, double := r.row.Update(gtx); double {
			w.Edit(i)
		}
	}
	return out
}

// Layout draws the input and the list; it fills the available space.
func (w *WatchesList) Layout(gtx layout.Context, th *tokens.Theme) layout.Dimensions {
	w.Update(gtx)
	if w.focus {
		gtx.Execute(key.FocusCmd{Tag: &w.editor.Editor})
		w.focus = false
	}
	return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.UniformInset(th.Space.XS).Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
					layout.Flexed(1, func(gtx layout.Context) layout.Dimensions { return w.input.Layout(gtx, th) }),
					layout.Rigid(func(gtx layout.Context) layout.Dimensions { return w.add.Layout(gtx, th) }),
				)
			})
		}),
		layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
			if len(w.Items) == 0 {
				return emptyState(gtx, th, "", "No watches")
			}
			return w.list.Layout(gtx, th, len(w.Items), func(gtx layout.Context, i int) layout.Dimensions {
				if i == w.editing {
					return layout.Inset{Left: th.Space.L, Right: th.Space.XS, Top: th.Space.XXS, Bottom: th.Space.XXS}.Layout(gtx,
						func(gtx layout.Context) layout.Dimensions { return w.editor.Layout(gtx, th) })
				}
				return w.layoutRow(gtx, th, i)
			})
		}),
	)
}

func (w *WatchesList) layoutRow(gtx layout.Context, th *tokens.Theme, i int) layout.Dimensions {
	it, r := w.Items[i], &w.rows[i]
	r.remove.Glyph, r.remove.Description = atoms.GlyphClose, "Remove watch"
	r.remove.Size, r.remove.IconSize = th.Size.RowH-th.Space.XS, th.Size.IconSmall
	r.row.Name, r.row.Detail = it.Expression, it.Type
	r.row.NameColor = th.Syntax.Variable
	r.row.Value, r.row.ValueColor = it.Value, valueColor(th, it.Type)
	if it.Error != "" {
		r.row.Value, r.row.ValueColor, r.row.Detail = it.Error, th.Color.Danger, "error"
	}
	r.row.Trailing = func(gtx layout.Context) layout.Dimensions { return r.remove.Layout(gtx, th) }
	gtx.Constraints.Min = image.Point{}
	return r.row.Layout(gtx, th)
}
