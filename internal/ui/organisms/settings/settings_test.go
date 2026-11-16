package settings

import (
	"image"
	"testing"

	"gioui.org/layout"

	"github.com/ape-dev-de/tiegel/internal/ui/organisms/runtime"
	"github.com/ape-dev-de/tiegel/internal/ui/tokens"
	"github.com/ape-dev-de/tiegel/internal/ui/uitest"
)

func TestPanel(t *testing.T) {
	p := &Panel{
		Modules: []Module{
			{ID: "php-runtime", Enabled: true, Running: true, Dependents: []string{"debug-xdebug"}},
			{ID: "debug-xdebug", DependsOn: []string{"php-runtime"}, Enabled: true},
			{ID: "git", Error: "git not found"},
			{ID: "lang-php"},
		},
		TabWidth: 4, Theme: "dark", IdleSeconds: 30,
	}
	p.Interpreter.Items = []runtime.Interpreter{{ID: "local", Label: "PHP 8.3", Kind: runtime.KindLocal}}
	p.Interpreter.Selected = "local"
	p.Mappings.Items = []runtime.Mapping{{Host: ".", Remote: "/var/www"}}
	p.Hints.Items = []runtime.Hint{{Severity: runtime.SeverityWarning, Code: "mode", Message: "xdebug.mode lacks debug"}}
	var evs []Event
	w := func(gtx layout.Context, th *tokens.Theme) layout.Dimensions {
		evs = append(evs, p.Update(gtx)...)
		return p.Layout(gtx, th)
	}
	for i := range Sections {
		p.Section = i
		uitest.Smoke(t, Sections[i], image.Pt(1000, 700), w)
	}
	p.NoProject = true
	p.Section = 1
	uitest.Smoke(t, "no project", image.Pt(1000, 700), w)

	h := uitest.NewHarness(image.Pt(1000, 700))
	p.Section = 0
	h.Frame(w)
	// Navigation: click "Updates" in the left list (5th row).
	h.Click(image.Pt(60, 49+8+4*24+12))
	h.Frame(w)
	if p.Section != 4 {
		t.Fatalf("section %d", p.Section)
	}
	// Module checkbox of the first module.
	p.Section = 0
	h.Frame(w)
	evs = nil
	for y := 100; y < 220 && len(evs) == 0; y += 4 {
		h.Click(image.Pt(205+16+8, y))
		h.Frame(w)
	}
	if len(evs) == 0 {
		t.Fatal("no module event")
	}
	if m, ok := evs[0].(ModuleEvent); !ok || m.ID != "php-runtime" || m.Enable {
		t.Fatalf("module event %+v", evs[0])
	}
	if IdleLabel(0) == "" || IdleLabel(5) != "After 5 s idle" || IdleLabel(300) != "After 5 min idle" {
		t.Fatal("idle labels")
	}
}
