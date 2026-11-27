package planview

import (
	"image"
	"image/png"
	"os"
	"path/filepath"
	"testing"

	"gioui.org/layout"

	"github.com/ape-dev-de/tiegel/internal/ui/tokens"
	"github.com/ape-dev-de/tiegel/internal/ui/uitest"
)

func sample(state string) *View {
	v := &View{Title: "Make Calc::add return the sum and reject overflows", State: state, Phase: "summary",
		Roles: []Role{
			{Name: "planner-architecture", Title: "Planner · architecture", State: "done", Elapsed: "38s", Task: "draft"},
			{Name: "planner-code", Title: "Planner · code", State: "done", Elapsed: "41s", Task: "draft"},
			{Name: "planner-tests", Title: "Planner · tests", State: "done", Elapsed: "29s", Task: "draft"},
			{Name: "validator", Title: "Validator", State: "done", Elapsed: "12s"},
			{Name: "qa", Title: "QA", State: "running", Elapsed: "9s", Task: "review the plan"},
			{Name: "summarizer", Title: "Summary", State: "idle"},
		},
		Summary:   "Calc::add (src/Calc.php:6) returns 0. S1 implements the sum, S2 throws OverflowException. T1–T3 cover sum, negative numbers and overflow.",
		Workspace: "Worktree .tiegel/worktrees/p1 on branch tiegel/plan-p1 (base 98cb312)",
		Worktree:  true,
	}
	switch state {
	case StateNeedsUser:
		v.Phase, v.Iteration = "debate", 2
		v.Disputes = []Dispute{{ID: "Q4", Claim: "add() must reject PHP_INT_MAX overflow", Evidence: "src/Calc.php:6",
			Positions: []Position{{Role: "qa", Text: "T3 expects OverflowException; the sum silently becomes a float."}, {Role: "dev", Text: "int params make the overflow a TypeError already."}}}}
		v.Gates = []Gate{{Name: "tests", State: "failed", Text: "1 of 3 failing: CalcTest::testOverflow"}, {Name: "hollow", State: "passed"}, {Name: "coverage", State: "skipped", Text: "no pcov/xdebug"}}
	case StateDone:
		v.Phase, v.Iteration = "final", 2
		v.Summary = "Calc::add returns the sum and throws on overflow. All 3 tests pass; the red check showed them failing on the base."
		v.Gates = []Gate{{Name: "tests", State: "passed", Text: "3 passed"}, {Name: "red", State: "passed", Text: "3 new tests fail on the base"},
			{Name: "mutation", State: "passed", Text: "4 of 4 mutants killed"}, {Name: "coverage", State: "skipped", Text: "no pcov/xdebug"}}
		v.Changes = []Change{{Path: "src/Calc.php", Added: 6, Removed: 1}, {Path: "tests/CalcTest.php", Added: 24}}
	}
	return v
}

func TestLayoutAndShots(t *testing.T) {
	for _, st := range []string{StateReview, StateNeedsUser, StateDone, StateCancelled, StateDrafting} {
		v := sample(st)
		v.Running = st == StateDrafting
		uitest.Smoke(t, st, image.Pt(420, 900), v.Layout)
		uitest.Smoke(t, st+"-narrow", image.Pt(220, 300), v.Layout)
	}
	dir := os.Getenv("TIEGEL_SHOTS")
	if dir == "" {
		return
	}
	sh := tokens.NewBundledShaper()
	for _, th := range []*tokens.Theme{tokens.Dark(sh), tokens.Light(sh)} {
		for _, st := range []string{StateReview, StateNeedsUser, StateDone} {
			img, err := uitest.Render(th, image.Pt(420, 900), sample(st).Layout)
			if err != nil {
				t.Skipf("no renderer: %v", err)
			}
			f, err := os.Create(filepath.Join(dir, "planview-"+st+"-"+th.Mode.String()+".png"))
			if err != nil {
				t.Fatal(err)
			}
			_ = png.Encode(f, img)
			f.Close()
		}
	}
}

func TestEvents(t *testing.T) {
	size := image.Pt(420, 900)
	seen := map[string]bool{}
	for _, st := range []string{StateReview, StateNeedsUser, StateDone, StateCancelled, StateDrafting} {
		for y := 4; y < size.Y; y += 8 {
			for _, x := range []int{30, 120, 200, 260, 330, 390} {
				v := sample(st)
				v.Running = st == StateDrafting
				v.comment.SetText("please check")
				h := uitest.NewHarness(size)
				var evs []Event
				w := func(gtx layout.Context, th *tokens.Theme) layout.Dimensions {
					evs = append(evs, v.Update(gtx)...)
					return v.Layout(gtx, th)
				}
				h.Frame(w)
				h.Click(image.Pt(x, y))
				h.Frame(w)
				h.Frame(w)
				for _, e := range evs {
					switch e := e.(type) {
					case ResolveEvent:
						seen["resolve-"+e.Decision] = true
					default:
						seen[typeName(e)] = true
					}
				}
			}
		}
	}
	for _, want := range []string{"accept", "changes", "document", "role", "resolve-dev", "resolve-qa", "resolve-comment", "apply", "keep", "cancel", "resume", "diff", "close"} {
		if !seen[want] {
			t.Errorf("no %s event (seen %v)", want, seen)
		}
	}
	if s, k := StateText("bogus"); s != "bogus" || k != 0 {
		t.Fatal(s)
	}
}

func typeName(e Event) string {
	switch e.(type) {
	case AcceptEvent:
		return "accept"
	case ChangesEvent:
		return "changes"
	case DiscardEvent:
		return "discard"
	case DocumentEvent:
		return "document"
	case RoleEvent:
		return "role"
	case ApplyEvent:
		return "apply"
	case KeepEvent:
		return "keep"
	case CancelEvent:
		return "cancel"
	case ResumeEvent:
		return "resume"
	case DiffEvent:
		return "diff"
	case CloseEvent:
		return "close"
	}
	return "?"
}
