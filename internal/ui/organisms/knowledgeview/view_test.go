package knowledgeview

import (
	"image"
	"image/png"
	"os"
	"path/filepath"
	"testing"
	"time"

	"gioui.org/font/gofont"
	"gioui.org/layout"
	"gioui.org/text"

	"github.com/ape-dev-de/tiegel/internal/ui/atoms"
	"github.com/ape-dev-de/tiegel/internal/ui/tokens"
	"github.com/ape-dev-de/tiegel/internal/ui/uitest"
)

var t0 = time.Date(2026, 11, 27, 10, 0, 0, 0, time.UTC)

func sample() *View {
	return &View{
		Summary: "7 pages · 2 to update · 1 failed", Details: []string{"Agent: opencode acp · fake/large", "Search: ollama · embeddinggemma-2:270m (local)", "Overview 1 · Entry points 2 · Processes 3 · Components 1"},
		Exists: true, Stale: 2, Questions: "3 open · 5 answered",
		Notice:  &Notice{Title: "Embedding model missing", Detail: "Pull downloads it: ollama pull embeddinggemma-2:270m", Action: "Pull model", ActionID: "pull"},
		Running: true, Total: 6, Done: 3, Started: t0.Add(-3*time.Minute - 12*time.Second), Now: func() time.Time { return t0 },
		Steps: []Step{
			{ID: "overview", Level: "Overview", Title: "Overview", Step: "write", State: "done", Tools: 9, Reads: 9, Chars: 5200},
			{ID: "process-app-shop", Level: "Processes", Title: "CheckoutController (HTTP)", Step: "validate", State: "running", StepIndex: 2, Steps: 3, Started: t0.Add(-42 * time.Second), Tools: 14, Reads: 12, Chars: 6100},
			{ID: "process-app-cli", Level: "Processes", Title: "ImportCommand (CLI)", State: "queued"},
			{ID: "process-broken", Level: "Processes", Title: "Broken", Step: "describe", State: "failed", Error: "unusable answer"}},
		Peek: "process-app-shop",
		Transcript: []string{"» describe (step 1 of 3)", "thought: The route posts to CheckoutController::place.", "  It calls CartService first.",
			"tool: read src/Controller/CheckoutController.php [pending]", "tool: read src/Controller/CheckoutController.php → completed", "tool: grep OrderRepository", "```json", `{"name": "Checkout", "steps": [...]}`, "```",
			"» validate (step 2 of 3)", "thought: Checking the cited lines."},
		Hits: []Hit{{Heading: `Checkout › Writes`, Text: "orders (table): INSERT with status 'new'.", Score: 0.91}},
		Units: []Unit{
			{ID: "overview", Title: "Overview", Level: "overview", LevelTitle: "Overview", Files: 4, Confidence: 0.85, HasDoc: true},
			{ID: "entrypoints-http", Title: "HTTP routes", Level: "entrypoints", LevelTitle: "Entry points", Files: 12, Confidence: -1, HasDoc: true},
			{ID: "process-app-shop", Title: "CheckoutController (HTTP)", Level: "processes", LevelTitle: "Processes", Files: 6, State: "changed", HasDoc: true, Confidence: 0.7},
			{ID: "process-broken", Title: "Broken", Level: "processes", LevelTitle: "Processes", Files: 2, State: "failed"},
			{ID: "questions", Title: "Open questions", Level: "questions", LevelTitle: "Questions", Dir: "3 open", HasDoc: true}},
	}
}

func TestView(t *testing.T) {
	v := sample()
	var evs []Event
	w := func(gtx layout.Context, th *tokens.Theme) layout.Dimensions {
		evs = append(evs, v.Update(gtx)...)
		return v.Layout(gtx, th)
	}
	uitest.Smoke(t, "running", image.Pt(320, 900), w)
	v.Running, v.Confirm, v.Notice = false, "3 pages. The tokens are billed by your agent's provider.", &Notice{Title: "x", Error: true}
	v.Phase = "embedding"
	uitest.Smoke(t, "confirm", image.Pt(320, 700), w)
	v.Confirm, v.LastRun, v.Peek = "", true, ""
	uitest.Smoke(t, "last run", image.Pt(320, 700), w)
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
	h.Frame(w)
	for y := 100; y < 260 && len(evs) == 0; y += 6 {
		h.Click(image.Pt(150, y))
		h.Frame(w)
	}
	if !hasEvent(evs, OpenUnitEvent{ID: "a"}) {
		t.Fatalf("events %v", evs)
	}
	// A click on a running page opens its transcript; again closes it.
	v = &View{Summary: "s", Running: true, Total: 2, Steps: []Step{{ID: "p", Title: "P", Step: "describe", State: "running", StepIndex: 1, Steps: 3}}}
	h = uitest.NewHarness(image.Pt(320, 700))
	evs = nil
	h.Frame(w)
	h.Frame(w)
	for y := 120; y < 360 && len(evs) == 0; y += 4 {
		h.Click(image.Pt(150, y))
		h.Frame(w)
	}
	if !hasEvent(evs, PeekEvent{ID: "p"}) {
		t.Fatalf("events %v", evs)
	}
	v.Peek, v.Transcript = "p", []string{"» describe (step 1 of 3)", "tool: read a.php"}
	h.Frame(w)
	if v.peekLines != 2 {
		t.Fatal(v.peekLines)
	}
	if f := v.fraction(); f != 0 {
		t.Fatal(f)
	}
	v.Steps[0].StepIndex = 3
	if f := v.fraction(); f < 0.33 || f > 0.34 {
		t.Fatal(f)
	}
	if plural(1, "file") != "1 file" || plural(2, "file") != "2 files" {
		t.Fatal("plural")
	}
	if Elapsed(42*time.Second) != "0:42" || Elapsed(3725*time.Second) != "1:02:05" || Elapsed(-1) != "0:00" {
		t.Fatal(Elapsed(3725 * time.Second))
	}
	if Counters(Step{Tools: 3, Reads: 2, Chars: 6100}) != "3 tool calls (2 reads) · ~1.5k tokens" || Counters(Step{Chars: 40}) != "~10 tokens" || Counters(Step{}) != "" {
		t.Fatal(Counters(Step{Tools: 3, Reads: 2, Chars: 6100}))
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
			s.Notice = nil
			img, err := uitest.Render(th, image.Pt(360, 900), func(gtx layout.Context, th *tokens.Theme) layout.Dimensions { return s.Layout(gtx, th) })
			if err != nil {
				t.Skip(err)
			}
			f, _ := os.Create(filepath.Join(dir, "knowledgeview-running-"+mode+".png"))
			_ = png.Encode(f, img)
			f.Close()
		}
	}
}

func hasEvent(evs []Event, want Event) bool {
	for _, e := range evs {
		if e == want {
			return true
		}
	}
	return false
}
