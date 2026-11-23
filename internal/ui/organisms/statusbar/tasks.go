package statusbar

import (
	"image"
	"strconv"

	"gioui.org/io/pointer"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/unit"
	"gioui.org/widget"

	"github.com/ape-dev-de/tiegel/internal/ui/atoms"
	"github.com/ape-dev-de/tiegel/internal/ui/internal/draw"
	"github.com/ape-dev-de/tiegel/internal/ui/tokens"
)

// Background tasks in the status bar (D508): TaskIndicator is the footer
// widget (latest task title, a small progress bar, the percentage and
// "+N" for further tasks; clicking it opens the task list), TaskList the
// list in the popup with one cancel button per task.

// Task is one background task as shown by TaskIndicator and TaskList.
type Task struct {
	ID     string
	Title  string
	Module string
	// Text is the detail line (current file, remote).
	Text string
	// Value is the progress in [0,1]; < 0 means indeterminate.
	Value float32
	// Cancellable tasks get a cancel button; Cancelling marks a requested
	// cancellation.
	Cancellable, Cancelling bool
	// Error marks a failed task (shown with its message until it
	// disappears).
	Error string
}

// Percent returns "42%" for a determinate task, "" otherwise.
func (t Task) Percent() string {
	if t.Value < 0 {
		return ""
	}
	return strconv.Itoa(int(min(t.Value, 1)*100+0.5)) + "%"
}

// TaskIndicator is the status bar widget of the background tasks (use it
// as Item.Widget). Show sets the latest task and the number of others;
// Clicked reports a click. Keep it in the widget state.
type TaskIndicator struct {
	// Task is the shown (most recent) task; More counts the others.
	Task Task
	More int
	// Tooltip is shown when the pointer rests on the widget.
	Tooltip string
	// BarWidth is the progress bar width (default 64dp).
	BarWidth unit.Dp

	click widget.Clickable
	tip   atoms.Tooltip
}

// Clicked reports whether the indicator was clicked since the last call.
func (t *TaskIndicator) Clicked(gtx layout.Context) bool { return t.click.Clicked(gtx) }

// Layout draws the indicator.
func (t *TaskIndicator) Layout(gtx layout.Context, th *tokens.Theme) layout.Dimensions {
	bw := t.BarWidth
	if bw == 0 {
		bw = 64
	}
	task := t.Task
	col := th.Color.TextMuted
	if task.Error != "" {
		col = th.Color.Danger
	}
	title := task.Title
	if task.Cancelling {
		title += " (cancelling)"
	}
	content := func(gtx layout.Context) layout.Dimensions {
		gtx.Constraints.Min = image.Point{}
		children := []layout.FlexChild{
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				gtx.Constraints.Max.X = min(gtx.Constraints.Max.X, gtx.Dp(260))
				return atoms.Label{Text: title, Role: atoms.RoleBody, Color: col, MaxLines: 1}.Layout(gtx, th)
			}),
			layout.Rigid(layout.Spacer{Width: th.Space.S}.Layout),
			layout.Rigid(atoms.ProgressBar{Value: task.Value, Width: bw, Danger: task.Error != ""}.Fn(th)),
		}
		if p := task.Percent(); p != "" && task.Error == "" {
			children = append(children, layout.Rigid(layout.Spacer{Width: th.Space.XS}.Layout),
				layout.Rigid(atoms.Label{Text: p, Role: atoms.RoleSmall, Color: th.Color.TextMuted, MaxLines: 1}.Fn(th)))
		}
		if t.More > 0 {
			children = append(children, layout.Rigid(layout.Spacer{Width: th.Space.XS}.Layout),
				layout.Rigid(atoms.Badge{Text: "+" + strconv.Itoa(t.More), Kind: atoms.BadgeAccent}.Fn(th)))
		}
		return layout.Flex{Alignment: layout.Middle}.Layout(gtx, children...)
	}
	pad := gtx.Dp(th.Space.XS)
	t.tip.Text, t.tip.Above = t.Tooltip, true
	return t.tip.Layout(gtx, th, func(gtx layout.Context) layout.Dimensions {
		return t.click.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			rec := op.Record(gtx.Ops)
			cd := content(gtx)
			call := rec.Stop()
			size := image.Pt(cd.Size.X+2*pad, max(cd.Size.Y, gtx.Constraints.Min.Y))
			if t.click.Hovered() {
				draw.RRect(gtx.Ops, image.Rectangle{Max: size}, gtx.Dp(th.Radius.M), th.Color.SurfaceHover)
			}
			draw.Centered(gtx.Ops, size, cd.Size, call)
			area := clip.Rect{Max: size}.Push(gtx.Ops)
			pointer.CursorPointer.Add(gtx.Ops)
			area.Pop()
			return layout.Dimensions{Size: size}
		})
	})
}

// TaskList lists background tasks with progress and a cancel button for
// each cancellable task (a failed task gets a dismiss button). Keep it in
// the widget state; Update returns the IDs whose button was clicked.
type TaskList struct {
	Tasks []Task
	// Empty is shown without tasks.
	Empty string

	buttons map[string]*atoms.Button
}

func (l *TaskList) button(id string) *atoms.Button {
	if l.buttons == nil {
		l.buttons = map[string]*atoms.Button{}
	}
	b := l.buttons[id]
	if b == nil {
		b = &atoms.Button{Kind: atoms.Ghost, Glyph: atoms.GlyphStop}
		l.buttons[id] = b
	}
	return b
}

// Update returns the IDs of the tasks whose cancel (or dismiss) button
// was clicked.
func (l *TaskList) Update(gtx layout.Context) []string {
	var out []string
	for _, t := range l.Tasks {
		if b, ok := l.buttons[t.ID]; ok && b.Clicked(gtx) {
			out = append(out, t.ID)
		}
	}
	return out
}

// Layout draws the rows, one per task.
func (l *TaskList) Layout(gtx layout.Context, th *tokens.Theme) layout.Dimensions {
	if len(l.Tasks) == 0 {
		return atoms.Muted(l.Empty).Layout(gtx, th)
	}
	// Forget the buttons of tasks that are gone.
	for id := range l.buttons {
		found := false
		for _, t := range l.Tasks {
			found = found || t.ID == id
		}
		if !found {
			delete(l.buttons, id)
		}
	}
	rows := make([]layout.FlexChild, 0, 2*len(l.Tasks))
	for i, t := range l.Tasks {
		if i > 0 {
			rows = append(rows, layout.Rigid(layout.Spacer{Height: th.Space.S}.Layout))
		}
		rows = append(rows, layout.Rigid(func(gtx layout.Context) layout.Dimensions { return l.row(gtx, th, t) }))
	}
	gtx.Constraints.Min.Y = 0
	return layout.Flex{Axis: layout.Vertical}.Layout(gtx, rows...)
}

// row is one task: title and module, the bar with the percentage, the
// detail text (or error) and the button on the right.
func (l *TaskList) row(gtx layout.Context, th *tokens.Theme, t Task) layout.Dimensions {
	gtx.Constraints.Min.X = gtx.Constraints.Max.X
	gtx.Constraints.Min.Y = 0
	var btn *atoms.Button
	switch {
	case t.Error != "":
		btn = l.button(t.ID)
		btn.Glyph, btn.Text, btn.Disabled = atoms.GlyphClose, "", false
	case t.Cancellable:
		btn = l.button(t.ID)
		btn.Glyph, btn.Text, btn.Disabled = atoms.GlyphStop, "", t.Cancelling
	}
	info := func(gtx layout.Context) layout.Dimensions {
		title := t.Title
		if t.Cancelling {
			title += " (cancelling)"
		}
		lines := []layout.FlexChild{
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return layout.Flex{Alignment: layout.Baseline}.Layout(gtx,
					layout.Flexed(1, atoms.Label{Text: title, Role: atoms.RoleBody, MaxLines: 1}.Fn(th)),
					layout.Rigid(atoms.Label{Text: t.Module, Role: atoms.RoleSmall, MaxLines: 1}.Fn(th)),
				)
			}),
			layout.Rigid(layout.Spacer{Height: th.Space.XXS}.Layout),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
					layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
						gtx.Constraints.Min.X = gtx.Constraints.Max.X
						return atoms.ProgressBar{Value: t.Value, Danger: t.Error != ""}.Layout(gtx, th)
					}),
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						p := t.Percent()
						if p == "" {
							return layout.Dimensions{}
						}
						return layout.Inset{Left: th.Space.S}.Layout(gtx, atoms.Small(p).Fn(th))
					}),
				)
			}),
		}
		detail, col := t.Text, th.Color.TextMuted
		if t.Error != "" {
			detail, col = t.Error, th.Color.Danger
		}
		if detail != "" {
			lines = append(lines, layout.Rigid(layout.Spacer{Height: th.Space.XXS}.Layout),
				layout.Rigid(atoms.Label{Text: detail, Role: atoms.RoleSmall, Color: col, MaxLines: 2}.Fn(th)))
		}
		return layout.Flex{Axis: layout.Vertical}.Layout(gtx, lines...)
	}
	return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
		layout.Flexed(1, info),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			if btn == nil {
				return layout.Dimensions{}
			}
			return layout.Inset{Left: th.Space.S}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return btn.Layout(gtx, th)
			})
		}),
	)
}
