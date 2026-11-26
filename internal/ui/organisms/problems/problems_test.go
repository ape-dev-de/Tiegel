package problems

import (
	"image"
	"testing"

	"gioui.org/io/pointer"
	"gioui.org/layout"

	"github.com/ape-dev-de/tiegel/internal/ui/tokens"
	"github.com/ape-dev-de/tiegel/internal/ui/uitest"
)

func TestList(t *testing.T) {
	l := &List{Items: []Problem{
		{Path: "src/a.php", Line: 3, Col: 1, Severity: Error, Message: "Enums need PHP 8.1", Source: "php 8.0"},
		{Path: "src/a.php", Line: 9, Col: 5, Severity: Warning, Message: "Unused variable"},
		{Path: "b.php", Line: 1, Col: 1, Severity: Info, Message: "Hint"},
	}}
	var evs []Event
	w := func(gtx layout.Context, th *tokens.Theme) layout.Dimensions {
		evs = append(evs, l.Update(gtx)...)
		return l.Layout(gtx, th)
	}
	uitest.Smoke(t, "problems", image.Pt(800, 200), w)
	h := uitest.NewHarness(image.Pt(800, 200))
	h.Frame(w)
	h.Click(image.Pt(100, 24+12))
	h.Frame(w)
	if len(evs) != 1 || evs[0] != (OpenEvent{Index: 1}) {
		t.Fatalf("events %v", evs)
	}
	if e, wn := Counts(l.Items); e != 1 || wn != 1 {
		t.Fatal("counts")
	}
	uitest.Smoke(t, "empty", image.Pt(800, 200), (&List{}).Layout)
}

// A section (PHPStan findings, D556) gets a header above its first entry;
// a right click reports ContextEvent instead of OpenEvent.
func TestSectionsAndContextClick(t *testing.T) {
	l := &List{GOOS: "linux", Sections: map[string]string{"PHPStan": "PHPStan · 2 · result cache"}, Items: []Problem{
		{Path: "a.php", Line: 1, Col: 1, Severity: Error, Message: "Syntax error"},
		{Path: "a.php", Line: 3, Col: 1, Severity: Warning, Message: "Undefined method", Source: "PHPStan · method.notFound", Section: "PHPStan", Subtle: true},
		{Path: "b.php", Line: 9, Col: 1, Severity: Warning, Message: "Wrong type", Section: "PHPStan", Subtle: true, Stale: true},
	}}
	if l.header(0) != "" || l.header(1) != "PHPStan · 2 · result cache" || l.header(2) != "" {
		t.Fatal("headers")
	}
	var evs []Event
	w := func(gtx layout.Context, th *tokens.Theme) layout.Dimensions {
		evs = append(evs, l.Update(gtx)...)
		return l.Layout(gtx, th)
	}
	uitest.Smoke(t, "sections", image.Pt(800, 200), w)
	h := uitest.NewHarness(image.Pt(800, 200))
	h.Frame(w)
	h.ClickWith(image.Pt(100, 12), pointer.ButtonSecondary, 0)
	h.Frame(w)
	h.Frame(w)
	found := false
	for _, e := range evs {
		if _, ok := e.(OpenEvent); ok {
			t.Fatalf("open on right click: %v", evs)
		}
		if e == (ContextEvent{Index: 0}) {
			found = true
		}
	}
	if !found {
		t.Fatalf("events %v", evs)
	}
}
