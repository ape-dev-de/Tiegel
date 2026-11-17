package settings

import (
	"image"
	"testing"

	"gioui.org/layout"

	"github.com/ape-dev-de/tiegel/internal/ui/tokens"
	"github.com/ape-dev-de/tiegel/internal/ui/uitest"
)

// TestReadOnlyFieldEmitsNothing checks that a ReadOnly field is drawn
// disabled and never reports a change or reset (D249).
func TestReadOnlyFieldEmitsNothing(t *testing.T) {
	p := &Panel{Pages: []Page{{ID: "plugin:php-runtime", Title: "php-runtime", Fields: []Field{
		{Key: "php-runtime.interpreter", Kind: KindString, Label: "PHP interpreter", Value: "local", Source: "global", HasGlobal: true,
			ReadOnly: true, ReadOnlyHint: "choose it on the Interpreter page"},
		{Key: "php-runtime.flag", Kind: KindBool, Label: "Flag", Value: true, ReadOnly: true},
	}}}}
	var evs []Event
	w := func(gtx layout.Context, th *tokens.Theme) layout.Dimensions {
		evs = append(evs, p.Update(gtx)...)
		return p.Layout(gtx, th)
	}
	h := uitest.NewHarness(image.Pt(1000, 700))
	h.Frame(w)
	st := p.fields["php-runtime.interpreter"]
	if st == nil || st.text == nil || !st.text.Disabled {
		t.Fatalf("field state %+v", st)
	}
	if !p.fields["php-runtime.flag"].check.Disabled {
		t.Fatal("checkbox not disabled")
	}
	st.text.SetText("docker")
	st.focus = true // simulate focus loss: a commit for an editable field
	h.Frame(w)
	if len(evs) != 0 {
		t.Fatalf("events %+v", evs)
	}
	uitest.Smoke(t, "read-only", image.Pt(1000, 700), w)
}
