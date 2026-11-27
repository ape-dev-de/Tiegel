package atoms

import (
	"image"
	"testing"

	"gioui.org/io/key"
	"gioui.org/layout"

	"github.com/ape-dev-de/tiegel/internal/ui/tokens"
	"github.com/ape-dev-de/tiegel/internal/ui/uitest"
)

func TestFocusProbe(t *testing.T) {
	tf := NewTextField("hint")
	var p FocusProbe
	h := uitest.NewHarness(image.Pt(300, 40))
	w := func(gtx layout.Context, th *tokens.Theme) layout.Dimensions {
		p.Attach(&gtx)
		return tf.Layout(gtx, th)
	}
	h.Frame(w)
	if p.Active() {
		t.Fatal("active without focus")
	}
	h.Frame(func(gtx layout.Context, th *tokens.Theme) layout.Dimensions {
		gtx.Execute(key.FocusCmd{Tag: &tf.Editor})
		return w(gtx, th)
	})
	h.Frame(w)
	if !p.Active() {
		t.Fatal("focused field not reported")
	}
	tf.SetText("abc")
	h.Frame(func(gtx layout.Context, th *tokens.Theme) layout.Dimensions {
		d := w(gtx, th)
		if f := p.Focused(gtx); f != &tf.Editor {
			t.Fatalf("focused %v", f)
		}
		ApplyEdit(gtx, &tf.Editor, EditSelectAll)
		ApplyEdit(gtx, &tf.Editor, EditCut)
		ApplyEdit(gtx, &tf.Editor, EditPaste)
		return d
	})
	if tf.Text() != "" {
		t.Fatalf("cut left %q", tf.Text())
	}
	if _, c, ok := h.Router.WriteClipboard(); !ok || string(c) != "abc" {
		t.Fatalf("clipboard %q", c)
	}
	if !h.Router.ClipboardRequested() {
		t.Fatal("paste did not read the clipboard")
	}
}
