package atoms

import (
	"image"

	"gioui.org/layout"
	"gioui.org/op"
)

// opOffset pushes a translation by off; call Pop on the result.
func opOffset(gtx layout.Context, off image.Point) op.TransformStack {
	return op.Offset(off).Push(gtx.Ops)
}
