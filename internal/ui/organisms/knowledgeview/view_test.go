package knowledgeview

import (
	"image"
	"image/png"
	"os"
	"path/filepath"
	"testing"

	"gioui.org/font/gofont"
	"gioui.org/layout"
	"gioui.org/text"

	"github.com/ape-dev-de/tiegel/internal/ui/atoms"
	"github.com/ape-dev-de/tiegel/internal/ui/tokens"
	"github.com/ape-dev-de/tiegel/internal/ui/uitest"
)

func sample() *View {
	return &View{
		Summary: "4 units · 1 to update · 1 failed", Details: []string{"Agent: opencode acp · fake/large", "Search: ollama · embeddinggemma-2:270m (local)"},
		Exists: true, Stale: 1,
		Notice:  &Notice{Title: "Embedding model missing", Detail: "Pull downloads it: ollama pull embeddinggemma-2:270m", Action: "Pull model", ActionID: "pull"},
		Running: true, Total: 3, Done: 1,
		Steps: []Step{{Title: `App\Billing`, Step: "write", State: "done"}, {Title: `App\Shop`, Step: "validate", State: "running"}, {Title: "App", State: "queued"},
			{Title: "Broken", Step: "describe", State: "failed", Error: "unusable answer"}},
		Hits: []Hit{{Heading: `App\Billing › Data flows`, Text: "Invoices are created from orders and stored.", Score: 0.91}},
		Units: []Unit{{ID: "a", Title: `App\Billing`, Files: 12, Confidence: 0.85, HasDoc: true}, {ID: "b", Title: `App\Shop`, Files: 1, State: "changed", HasDoc: true},
			{ID: "c", Title: "App", Files: 3, State: "new"}, {ID: "d", Title: "Broken", Files: 2, State: "failed"}},
	}
}

func TestView(t *testing.T) {
	v := sample()
	var evs []Event
	w := func(gtx layout.Context, th *tokens.Theme) layout.Dimensions {
		evs = append(evs, v.Update(gtx)...)
		return v.Layout(gtx, th)
	}
	uitest.Smoke(t, "running", image.Pt(320, 700), w)
	v.Running, v.Confirm, v.Notice = false, "3 units, 9 files. The tokens are billed by your agent's provider.", &Notice{Title: "x", Error: true}
	v.Phase = "embedding"
	uitest.Smoke(t, "confirm", image.Pt(320, 700), w)
	uitest.Smoke(t, "empty", image.Pt(320, 400), (&View{Summary: "No knowledge base yet"}).Layout)
	uitest.Smoke(t, "disabled", image.Pt(320, 400), (&View{Disabled: "Open a project."}).Layout)
	if v.Query() != "" {
		t.Fatal("query")
	}
	// A click on the first unit row opens its document.
	h := uitest.NewHarness(image.Pt(320, 700))
	v = &View{Summary: "s", Units: []Unit{{ID: "a", Title: "A", HasDoc: true}}}
	evs = nil
	h.Frame(w)
	dims := h.Frame(w)
	_ = dims
	for y := 100; y < 260 && len(evs) == 0; y += 6 {
		h.Click(image.Pt(150, y))
		h.Frame(w)
	}
	found := false
	for _, e := range evs {
		if e == (OpenUnitEvent{ID: "a"}) {
			found = true
		}
	}
	if !found {
		t.Fatalf("events %v", evs)
	}
	if plural(1, "file") != "1 file" || plural(2, "file") != "2 files" {
		t.Fatal("plural")
	}
	for state, kind := range map[string]atoms.BadgeKind{"failed": atoms.BadgeDanger, "new": atoms.BadgeInfo, "changed": atoms.BadgeWarning} {
		if _, k := stateBadge(Unit{State: state}); k != kind {
			t.Fatal(state)
		}
	}
	// Screenshots for review (TIEGEL_SHOTS).
	if dir := os.Getenv("TIEGEL_SHOTS"); dir != "" {
		shaper := text.NewShaper(text.WithCollection(gofont.Collection()))
		for _, mode := range []string{"dark", "light"} {
			th := tokens.Dark(shaper)
			if mode == "light" {
				th = tokens.Light(shaper)
			}
			s := sample()
			img, err := uitest.Render(th, image.Pt(320, 640), func(gtx layout.Context, th *tokens.Theme) layout.Dimensions { return s.Layout(gtx, th) })
			if err != nil {
				t.Skip(err)
			}
			f, _ := os.Create(filepath.Join(dir, "knowledgeview-running-"+mode+".png"))
			_ = png.Encode(f, img)
			f.Close()
		}
	}
}
