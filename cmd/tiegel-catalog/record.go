package main

import (
	"image"

	"gioui.org/layout"
	"gioui.org/op"
)

// recorded is a widget laid out into a macro, to be placed later.
type recorded struct {
	call op.CallOp
	dims layout.Dimensions
}

func recordOps(gtx layout.Context, w layout.Widget) recorded {
	m := op.Record(gtx.Ops)
	d := w(gtx)
	return recorded{call: m.Stop(), dims: d}
}

func (r recorded) addAt(gtx layout.Context, at image.Point) {
	t := op.Offset(at).Push(gtx.Ops)
	r.call.Add(gtx.Ops)
	t.Pop()
}
