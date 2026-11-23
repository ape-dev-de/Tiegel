package projectsetup

import (
	"image"
	"reflect"
	"testing"

	"gioui.org/layout"

	"github.com/ape-dev-de/tiegel/internal/ui/tokens"
	"github.com/ape-dev-de/tiegel/internal/ui/uitest"
)

func sample() *View {
	return &View{Items: []Item{
		{Key: "project.phpRoot", Label: "PHP root", Choices: []Choice{{Value: "", Label: "."}, {Value: "project", Label: "project", Recommended: true}}},
		{Key: "php-runtime.interpreter", Label: "PHP interpreter", Choices: []Choice{{Value: "compose:x:app", Label: "compose app", Recommended: true}}},
		{Key: "phpunit.executable", Label: "PHPUnit script", Choices: []Choice{{Value: "/p/vendor/bin/phpunit", Label: "vendor/bin/phpunit"}}},
	}}
}

func TestSheet(t *testing.T) {
	v := sample()
	var evs []Event
	w := func(gtx layout.Context, th *tokens.Theme) layout.Dimensions {
		evs = append(evs, v.Update(gtx)...)
		return v.Layout(gtx, th)
	}
	h := uitest.NewHarness(image.Pt(640, 420))
	h.Frame(w)
	want := []Value{{"project.phpRoot", "project"}, {"php-runtime.interpreter", "compose:x:app"}, {"phpunit.executable", "/p/vendor/bin/phpunit"}}
	if got := v.Selected(); !reflect.DeepEqual(got, want) {
		t.Fatalf("preselected %+v", got)
	}
	// Unchecked rows are not applied.
	v.checks[2].State = 0
	if got := v.Selected(); len(got) != 2 {
		t.Fatalf("unchecked %+v", got)
	}
	if len(evs) != 0 {
		t.Fatalf("events without input %+v", evs)
	}
	// The buttons sit at the bottom: Apply on the right.
	for x := 630; x > 300 && len(evs) == 0; x -= 10 {
		h.Click(image.Pt(x, 405))
		h.Frame(w)
	}
	if len(evs) == 0 {
		t.Fatal("no button event")
	}
	if a, ok := evs[0].(ApplyEvent); !ok || len(a.Values) != 2 {
		t.Fatalf("first event %+v", evs[0])
	}
	evs = nil
	for x := 10; x < 300 && len(evs) == 0; x += 10 {
		h.Click(image.Pt(x, 405))
		h.Frame(w)
	}
	if len(evs) == 0 {
		t.Fatal("no dismiss")
	}
	if _, ok := evs[0].(DismissEvent); !ok {
		t.Fatalf("dismiss %+v", evs[0])
	}
	v.Busy, v.Message, v.Error = true, "failed", true
	uitest.Smoke(t, "busy", image.Pt(640, 420), w)
	uitest.Smoke(t, "empty", image.Pt(640, 420), (&View{}).Layout)
	_ = LaterEvent{}
}
