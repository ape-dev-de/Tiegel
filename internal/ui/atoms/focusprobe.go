package atoms

import (
	"strings"

	"gioui.org/io/clipboard"
	"gioui.org/layout"
	"gioui.org/widget"
)

// focusProbeKey is the layout.Context.Values key of the window's
// FocusProbe.
const focusProbeKey = "tiegel.focusProbe"

// FocusProbe records which text field had the keyboard focus during a
// frame, so window-level commands (Edit > Copy, Paste, Select All) can act
// on it instead of on the code editor. The window installs it with Attach
// at the start of each frame; text fields report themselves while they
// lay out.
type FocusProbe struct {
	cur, last *widget.Editor
}

// Attach makes p the probe of the frame gtx lays out: the field focused
// in the previous frame becomes Focused, and fields report anew.
func (p *FocusProbe) Attach(gtx *layout.Context) {
	p.last, p.cur = p.cur, nil
	if gtx.Values == nil {
		gtx.Values = map[string]any{}
	}
	gtx.Values[focusProbeKey] = p
}

// Focused returns the text field focused in the last frame, if it still
// has the focus.
func (p *FocusProbe) Focused(gtx layout.Context) *widget.Editor {
	for _, e := range []*widget.Editor{p.cur, p.last} {
		if e != nil && gtx.Focused(e) {
			return e
		}
	}
	return nil
}

// Active reports whether a text field had the focus in the frame laid
// out last (for code running between frames, like menu actions).
func (p *FocusProbe) Active() bool { return p.cur != nil }

// noteFocus reports a focused text field to the frame's probe.
func noteFocus(gtx layout.Context, e *widget.Editor) {
	if p, ok := gtx.Values[focusProbeKey].(*FocusProbe); ok && gtx.Focused(e) {
		p.cur = e
	}
}

// EditCommand is a clipboard or selection command for a text field.
type EditCommand uint8

// Edit commands.
const (
	EditCut EditCommand = iota + 1
	EditCopy
	EditPaste
	EditSelectAll
)

// ApplyEdit runs cmd on the text field e (copy and cut write the
// selection to the clipboard, paste asks the clipboard for text the
// field then inserts).
func ApplyEdit(gtx layout.Context, e *widget.Editor, cmd EditCommand) {
	switch cmd {
	case EditCut, EditCopy:
		if s := e.SelectedText(); s != "" {
			gtx.Execute(clipboard.WriteCmd{Type: "application/text", Data: nopCloser{strings.NewReader(s)}})
			if cmd == EditCut {
				e.Insert("")
			}
		}
	case EditPaste:
		gtx.Execute(clipboard.ReadCmd{Tag: e})
	case EditSelectAll:
		e.SetCaret(e.Len(), 0)
	}
}

type nopCloser struct{ *strings.Reader }

func (nopCloser) Close() error { return nil }
