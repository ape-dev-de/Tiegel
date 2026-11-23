package editorview

import (
	"testing"

	"gioui.org/f32"
)

// GutterIconRect places a click on the icon column of a line.
func TestGutterIconRect(t *testing.T) {
	r := newRig(t, "a\nb\nc\n")
	rect, ok := r.e.GutterIconRect(2)
	if !ok || rect.Dy() != r.e.LineHeight() || rect.Min.Y != 2*r.e.LineHeight() {
		t.Fatalf("rect %v %v", rect, ok)
	}
	c := rect.Min.Add(rect.Size().Div(2))
	r.click(f32.Pt(float32(c.X), float32(c.Y)), 0)
	var got []GutterClickEvent
	for _, ev := range r.takeEvents() {
		if gc, ok := ev.(GutterClickEvent); ok {
			got = append(got, gc)
		}
	}
	if len(got) != 1 || got[0].Area != GutterIcons || got[0].Line != 2 {
		t.Fatalf("clicks %+v", got)
	}
	if _, ok := r.e.GutterIconRect(-1); ok {
		t.Fatal("negative line")
	}
	if _, ok := r.e.GutterIconRect(500); ok {
		t.Fatal("line below the view")
	}
}
