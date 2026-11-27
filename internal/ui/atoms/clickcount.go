package atoms

import (
	"time"

	"gioui.org/io/event"
	"gioui.org/io/pointer"
	"gioui.org/layout"
	"gioui.org/op/clip"
)

// DoubleClickInterval is the longest pause between the presses of a
// double click: the default of macOS and Windows. Gio's own click counting
// allows only 200 ms, which turns an ordinary double click into two single
// ones.
const DoubleClickInterval = 500 * time.Millisecond

// ClickCounter counts consecutive primary presses on a widget by their
// event time (1 = single, 2 = double click, …). Wrap the widget with
// Layout (presses pass through to it) and call Update before reading the
// widget's clicks.
type ClickCounter struct {
	tag  int
	last time.Duration
	n    int
	// pending are the counts of presses whose clicks were not read yet.
	pending []int
}

// Update reads the presses since the last call.
func (c *ClickCounter) Update(gtx layout.Context) {
	if len(c.pending) > 8 {
		c.pending = c.pending[len(c.pending)-8:] // presses without clicks (drags)
	}
	for {
		ev, ok := gtx.Event(pointer.Filter{Target: &c.tag, Kinds: pointer.Press})
		if !ok {
			return
		}
		e, ok := ev.(pointer.Event)
		if !ok || (e.Source == pointer.Mouse && !e.Buttons.Contain(pointer.ButtonPrimary)) {
			continue
		}
		c.pending = append(c.pending, c.Press(e.Time))
	}
}

// Press records a press at event time t and returns its count.
func (c *ClickCounter) Press(t time.Duration) int {
	if c.n > 0 && t >= c.last && t-c.last <= DoubleClickInterval {
		c.n++
	} else {
		c.n = 1
	}
	c.last = t
	return c.n
}

// Next is the count of the press belonging to the next click read from
// the widget (in order); without a recorded press the last count.
func (c *ClickCounter) Next() int {
	if len(c.pending) == 0 {
		return max(c.n, 1)
	}
	n := c.pending[0]
	c.pending = c.pending[1:]
	return n
}

// Layout draws w and observes the presses on it without taking them.
func (c *ClickCounter) Layout(gtx layout.Context, w layout.Widget) layout.Dimensions {
	d := w(gtx)
	area := clip.Rect{Max: d.Size}.Push(gtx.Ops)
	pass := pointer.PassOp{}.Push(gtx.Ops)
	event.Op(gtx.Ops, &c.tag)
	pass.Pop()
	area.Pop()
	return d
}
