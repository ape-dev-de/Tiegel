package orchestrate

import (
	"errors"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ape-dev-de/tiegel/internal/api"
)

var update = flag.Bool("update", false, "rewrite the golden files")

func golden(t *testing.T, name, got string) {
	t.Helper()
	path := filepath.Join("testdata", "golden", name)
	if *update {
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("%v (run go test -update)", err)
	}
	if string(want) != got {
		t.Errorf("%s differs (go test -update to accept):\n%s", name, got)
	}
}

// TestPromptsGolden renders every role prompt with fixed inputs.
func TestPromptsGolden(t *testing.T) {
	parts := []Part{{Title: "Goal", Body: "Make Calc::add return the sum.", Priority: 100}, {Title: "Plan", Body: "- [ ] **S1** Implement Calc::add", Priority: 90}}
	for _, tmpl := range []string{TmplPlannerArch, TmplPlannerCode, TmplPlannerTests, TmplValidator, TmplQAReview, TmplSummary, TmplRevise,
		TmplDev, TmplQA, TmplCritic, TmplRateImpl, TmplRateTests, TmplMediator, TmplFinal} {
		ro := tmpl != TmplDev && tmpl != TmplQA && tmpl != TmplRateImpl && tmpl != TmplRateTests
		got, err := RenderPrompt(PromptData{Template: tmpl, Project: "calc", Dir: "/work/calc", ReadOnly: ro, First: true, MCP: tmpl == TmplPlannerArch, Parts: parts})
		if err != nil {
			t.Fatal(err)
		}
		golden(t, tmpl+".txt", got)
	}
	got, _ := RenderPrompt(PromptData{Template: TmplDev, Dir: "/w", Switch: true, Parts: parts})
	golden(t, "dev-followup-switch.txt", got)
	golden(t, "repair.txt", RepairPrompt(errors.New("steps is missing")))
	if _, err := RenderPrompt(PromptData{Template: "nope"}); err == nil {
		t.Fatal("unknown template")
	}
}

func TestFitBudget(t *testing.T) {
	long := strings.Repeat("line of context\n", 500)
	parts := []Part{{Title: "Goal", Body: "keep me", Priority: 100, Min: 1 << 30}, {Title: "Context", Body: long, Priority: 10, Min: 200}, {Title: "Plan", Body: long, Priority: 90, Min: 1000}}
	out := Fit(parts, 3000)
	if out[0].Body != "keep me" || len(out[1].Body) > 200+len(cutNote) || !strings.HasSuffix(out[1].Body, cutNote) || len(out[2].Body) > 3000 {
		t.Fatalf("%d %d", len(out[1].Body), len(out[2].Body))
	}
	if parts[1].Body != long {
		t.Fatal("input changed")
	}
	if out := Fit(parts, 1<<20); out[1].Body != long {
		t.Fatal("cut without need")
	}
	// The budget applies to the whole prompt.
	p, _ := RenderPrompt(PromptData{Template: TmplCritic, Parts: parts, Budget: 1500})
	if EstimateTokens(p) > 1600 || !strings.Contains(p, "keep me") {
		t.Fatal(EstimateTokens(p))
	}
}

func TestDocumentRender(t *testing.T) {
	now := time.Date(2026, 11, 27, 10, 0, 0, 0, time.UTC)
	d := NewDocument("p1", "Add a discount. It must apply to totals over 100.", now)
	if d.Title != "Add a discount" {
		t.Fatal(d.Title)
	}
	AddFinding(&d, api.PlanFinding{Claim: "Totals are in `Cart::total`", Evidence: "src/Cart.php:10", Status: api.FindingVerified, Note: "seen"})
	AddStep(&d, api.PlanStep{Title: "Add Discount", Files: []string{"src/Discount.php"}, Detail: "new class", Note: "n"})
	AddStep(&d, api.PlanStep{Title: "Wire it", Status: "partial"})
	tid := AddTest(&d, api.PlanTest{Title: "discount over 100", Given: "a cart of 120", When: "total()", Then: "108", File: "tests/CartTest.php", Tests: []string{"CartTest::testDiscount"}, Status: TestPassed})
	AddCriterion(&d, api.PlanCriterion{Text: "10% off over 100", Tests: []string{tid}, Met: true})
	AddCriterion(&d, api.PlanCriterion{Text: "unmapped"})
	AddRisk(&d, api.PlanRisk{Text: "rounding"})
	id := AddReview(&d, api.PlanReviewItem{From: "critic", To: "dev", Target: "impl", Claim: "rounding is off", Evidence: "src/Discount.php:5", Suggestion: "round half up",
		Responses: []api.PlanResponse{{Role: "dev", Verdict: "reject", Text: "it is fine", Evidence: "src/Discount.php:6"}}})
	Review(&d, id).Decision = &api.PlanDecision{By: "mediator", Winner: "qa", Reason: "spec says half up"}
	AddDecision(&d, "Q1: qa is right", "mediator", now)
	d.Gates = []api.PlanGate{{Name: "tests", Passed: true, Summary: "1 tests"}, {Name: "red", Skipped: "in place"}, {Name: "hollow"}}
	d.Comments = []api.PlanComment{{Text: "Also\nnegative totals", Time: now}}
	d.Context, d.Refs, d.Summary = "Carts compute totals.", []string{"src/Cart.php"}, "Adds a discount."
	golden(t, "document.md", Render(d))
	if SectionMarkdown(d, "nope") != "" || !ValidSection(SecPlan) || ValidSection("x") {
		t.Fatal("sections")
	}
	if len(OpenFor(&d, "dev")) != 1 || len(Disputes(&d)) != 0 || TestCase(&d, "t1") == nil || Step(&d, "S9") != nil || Finding(&d, "f1") == nil {
		t.Fatal("lookups")
	}
	if titleOf(strings.Repeat("word ", 30)) == "" || len([]rune(titleOf(strings.Repeat("x", 100)))) != 72 {
		t.Fatal(titleOf(strings.Repeat("x", 100)))
	}
}

func TestStoreAndInbox(t *testing.T) {
	s := &Store{Dir: t.TempDir()}
	st := &State{Doc: NewDocument("p1", "goal", time.Now())}
	if err := s.Save(st); err != nil || st.Doc.Revision != 1 {
		t.Fatal(err, st.Doc.Revision)
	}
	if err := s.Save(st); err != nil || st.Doc.Revision != 1 || len(s.History("p1")) != 1 {
		t.Fatal("unchanged save made a revision")
	}
	st.Doc.Goal = "goal 2"
	_ = s.Save(st)
	if st.Doc.Revision != 2 || len(s.History("p1")) != 2 {
		t.Fatal(st.Doc.Revision)
	}
	if err := s.Save(&State{Doc: api.PlanDocument{ID: "../x"}}); err == nil {
		t.Fatal("bad id")
	}
	if _, err := s.Load("nope"); err == nil {
		t.Fatal("load missing")
	}
	if err := s.Propose(api.PlanProposeParams{ID: "p1", Role: "dev", Op: "finding", Claim: "c"}); err != nil {
		t.Fatal(err)
	}
	if err := s.Propose(api.PlanProposeParams{ID: "p1"}); err == nil {
		t.Fatal("propose without role")
	}
	if got := s.TakeInbox("p1", "dev"); len(got) != 1 || got[0].Claim != "c" {
		t.Fatal(got)
	}
	if got := s.TakeInbox("p1", "dev"); got != nil {
		t.Fatal("inbox not emptied")
	}
	if err := s.SaveTranscript("p1", "critic", []api.AgentEntry{{Kind: "agent", Text: "x"}}); err != nil {
		t.Fatal(err)
	}
	if es, err := s.LoadTranscript("p1", "critic"); err != nil || len(es) != 1 {
		t.Fatal(es, err)
	}
	if ids := s.List(); len(ids) != 1 || ids[0] != "p1" {
		t.Fatal(ids)
	}
	if err := s.Remove("p1"); err != nil || len(s.List()) != 0 {
		t.Fatal(err)
	}
	if len(NewID(time.Now())) < 16 {
		t.Fatal(NewID(time.Now()))
	}
	c := st.Clone()
	c.Doc.Goal = "changed"
	if st.Doc.Goal == "changed" {
		t.Fatal("clone shares")
	}
	st.Note("a")
	st.Note("a")
	if len(st.Notes) != 1 {
		t.Fatal(st.Notes)
	}
}
