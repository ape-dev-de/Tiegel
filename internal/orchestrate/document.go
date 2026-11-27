// Package orchestrate runs plan mode (D567–D570): several coding agent
// sessions in fixed roles (planners, validator, senior QA, senior DEV,
// critic, mediator, summarizer) work on one structured working document.
// No role ever sees the whole conversation; every call gets the shared
// base prompt, its role prompt and only the document sections it needs,
// cut to a token budget. Tiegel owns the document: agents answer with
// strict JSON (or propose changes through Tiegel's MCP tools) and Tiegel
// merges, numbers, validates and renders it as Markdown with a revision
// history.
//
// Phase A (planning, read-only) drafts, validates, reviews and
// summarizes the plan up to the user's acceptance. Phase B (execution)
// lets DEV implement and QA write tests in an isolated workspace, rates
// both crosswise, runs a fresh critic after every turn, debates findings
// with an optional mediator, and enforces mechanical gates (acceptance
// criteria mapped to passing tests, hollow tests, red check on the base
// code, mutation-lite, changed-lines coverage, unfinished work) until
// everything passes or the iteration limit is reached.
//
// The package imports no Gio; agents, workspace, test runner and the
// symbol index are interfaces wired by the planmode module.
package orchestrate

import (
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/ape-dev-de/tiegel/internal/api"
)

// Section IDs of the working document.
const (
	SecGoal      = "goal"
	SecContext   = "context"
	SecFindings  = "findings"
	SecPlan      = "plan"
	SecTests     = "tests"
	SecCriteria  = "criteria"
	SecRisks     = "risks"
	SecReview    = "review"
	SecDecisions = "decisions"
	SecGates     = "gates"
	SecSummary   = "summary"
	SecComments  = "comments"
)

// Sections lists the sections in document order.
var Sections = []string{SecGoal, SecComments, SecContext, SecFindings, SecPlan, SecTests, SecCriteria, SecRisks, SecReview, SecDecisions, SecGates, SecSummary}

var sectionTitles = map[string]string{
	SecGoal: "Goal", SecComments: "Change requests", SecContext: "Context", SecFindings: "Findings", SecPlan: "Plan",
	SecTests: "Test plan", SecCriteria: "Acceptance criteria", SecRisks: "Risks and open questions",
	SecReview: "Review log", SecDecisions: "Decisions", SecGates: "Gates", SecSummary: "Summary",
}

// NewDocument starts the working document of a plan.
func NewDocument(id, goal string, now time.Time) api.PlanDocument {
	return api.PlanDocument{ID: id, Title: titleOf(goal), Status: api.PlanDrafting, Phase: api.PhaseDraft,
		Created: now, Updated: now, Goal: strings.TrimSpace(goal)}
}

// titleOf derives a short title from the goal (first line, ≤ 72 runes).
func titleOf(goal string) string {
	t := strings.TrimSpace(goal)
	if i := strings.IndexAny(t, "\n.!?"); i > 0 {
		t = t[:i]
	}
	r := []rune(t)
	if len(r) > 72 {
		t = strings.TrimSpace(string(r[:71])) + "…"
	}
	return t
}

// nextID returns prefix + (highest number among ids + 1).
func nextID(prefix string, ids []string) string {
	n := 0
	for _, id := range ids {
		if v, err := strconv.Atoi(strings.TrimPrefix(id, prefix)); err == nil && strings.HasPrefix(id, prefix) && v > n {
			n = v
		}
	}
	return prefix + strconv.Itoa(n+1)
}

func findingIDs(d *api.PlanDocument) []string {
	out := make([]string, len(d.Findings))
	for i, f := range d.Findings {
		out[i] = f.ID
	}
	return out
}

func stepIDs(d *api.PlanDocument) []string {
	out := make([]string, len(d.Steps))
	for i, s := range d.Steps {
		out[i] = s.ID
	}
	return out
}

func testIDs(d *api.PlanDocument) []string {
	out := make([]string, len(d.Tests))
	for i, s := range d.Tests {
		out[i] = s.ID
	}
	return out
}

func criterionIDs(d *api.PlanDocument) []string {
	out := make([]string, len(d.Criteria))
	for i, s := range d.Criteria {
		out[i] = s.ID
	}
	return out
}

func riskIDs(d *api.PlanDocument) []string {
	out := make([]string, len(d.Risks))
	for i, s := range d.Risks {
		out[i] = s.ID
	}
	return out
}

func reviewIDs(d *api.PlanDocument) []string {
	out := make([]string, len(d.Review))
	for i, s := range d.Review {
		out[i] = s.ID
	}
	return out
}

// AddFinding appends a finding with a new ID.
func AddFinding(d *api.PlanDocument, f api.PlanFinding) string {
	f.ID = nextID("F", findingIDs(d))
	if f.Status == "" {
		f.Status = api.FindingUnknown
	}
	d.Findings = append(d.Findings, f)
	return f.ID
}

// AddStep appends a plan step with a new ID.
func AddStep(d *api.PlanDocument, s api.PlanStep) string {
	s.ID = nextID("S", stepIDs(d))
	if s.Status == "" {
		s.Status = "open"
	}
	d.Steps = append(d.Steps, s)
	return s.ID
}

// AddTest appends a test case with a new ID.
func AddTest(d *api.PlanDocument, t api.PlanTest) string {
	t.ID = nextID("T", testIDs(d))
	if t.Kind == "" {
		t.Kind = "new"
	}
	d.Tests = append(d.Tests, t)
	return t.ID
}

// AddCriterion appends an acceptance criterion with a new ID.
func AddCriterion(d *api.PlanDocument, c api.PlanCriterion) string {
	c.ID = nextID("AC", criterionIDs(d))
	if c.Tests == nil {
		c.Tests = []string{}
	}
	d.Criteria = append(d.Criteria, c)
	return c.ID
}

// AddRisk appends a risk or open question with a new ID.
func AddRisk(d *api.PlanDocument, r api.PlanRisk) string {
	r.ID = nextID("R", riskIDs(d))
	if r.Kind == "" {
		r.Kind = "risk"
	}
	d.Risks = append(d.Risks, r)
	return r.ID
}

// AddReview appends a review item with a new ID.
func AddReview(d *api.PlanDocument, it api.PlanReviewItem) string {
	it.ID = nextID("Q", reviewIDs(d))
	if it.Status == "" {
		it.Status = api.ReviewOpen
	}
	if it.Severity == "" {
		it.Severity = api.SeverityMajor
	}
	d.Review = append(d.Review, it)
	return it.ID
}

// AddDecision records a decision.
func AddDecision(d *api.PlanDocument, text, by string, now time.Time) {
	d.Decisions = append(d.Decisions, api.PlanDecisionLog{ID: nextID("D", decisionIDs(d)), Text: text, By: by, Time: now})
}

func decisionIDs(d *api.PlanDocument) []string {
	out := make([]string, len(d.Decisions))
	for i, s := range d.Decisions {
		out[i] = s.ID
	}
	return out
}

// Review returns the review item with id, or nil.
func Review(d *api.PlanDocument, id string) *api.PlanReviewItem {
	for i := range d.Review {
		if strings.EqualFold(d.Review[i].ID, id) {
			return &d.Review[i]
		}
	}
	return nil
}

// TestCase returns the test case with id, or nil.
func TestCase(d *api.PlanDocument, id string) *api.PlanTest {
	for i := range d.Tests {
		if strings.EqualFold(d.Tests[i].ID, id) {
			return &d.Tests[i]
		}
	}
	return nil
}

// Step returns the step with id, or nil.
func Step(d *api.PlanDocument, id string) *api.PlanStep {
	for i := range d.Steps {
		if strings.EqualFold(d.Steps[i].ID, id) {
			return &d.Steps[i]
		}
	}
	return nil
}

// Finding returns the finding with id, or nil.
func Finding(d *api.PlanDocument, id string) *api.PlanFinding {
	for i := range d.Findings {
		if strings.EqualFold(d.Findings[i].ID, id) {
			return &d.Findings[i]
		}
	}
	return nil
}

// IsOpen tells whether a review item still needs an answer or decision.
func IsOpen(it api.PlanReviewItem) bool {
	switch it.Status {
	case api.ReviewOpen, api.ReviewRejected, api.ReviewNeedsInfo, api.ReviewDisputed:
		return true
	}
	return false
}

// OpenFor returns the open review items addressed to role (all roles
// for "").
func OpenFor(d *api.PlanDocument, role string) []api.PlanReviewItem {
	var out []api.PlanReviewItem
	for _, it := range d.Review {
		if (role == "" || it.To == role) && IsOpen(it) && it.Status != api.ReviewDisputed {
			out = append(out, it)
		}
	}
	return out
}

// Disputes returns the items waiting for the user.
func Disputes(d *api.PlanDocument) []api.PlanReviewItem {
	var out []api.PlanReviewItem
	for _, it := range d.Review {
		if it.Status == api.ReviewDisputed && it.Decision == nil {
			out = append(out, it)
		}
	}
	return out
}

// ---- Markdown ---------------------------------------------------------------

// Render returns the Markdown form of the document: YAML front matter
// (id, title, status, phase, created, revision) and one level-2 section
// per part with a stable anchor ({#id}).
func Render(d api.PlanDocument) string {
	var b strings.Builder
	b.WriteString("---\n")
	fmt.Fprintf(&b, "id: %s\ntitle: %q\nstatus: %s\nphase: %s\ncreated: %s\nupdated: %s\nrevision: %d\n",
		d.ID, d.Title, d.Status, d.Phase, d.Created.UTC().Format(time.RFC3339), d.Updated.UTC().Format(time.RFC3339), d.Revision)
	b.WriteString("---\n\n")
	fmt.Fprintf(&b, "# %s\n", d.Title)
	for _, s := range Sections {
		body := SectionMarkdown(d, s)
		if body == "" && s != SecGoal {
			continue
		}
		fmt.Fprintf(&b, "\n## %s {#%s}\n\n%s\n", sectionTitles[s], s, strings.TrimRight(body, "\n"))
	}
	return b.String()
}

// SectionTitle returns the heading of a section.
func SectionTitle(s string) string { return sectionTitles[s] }

// ValidSection tells whether s names a section.
func ValidSection(s string) bool { return slices.Contains(Sections, s) }

// SectionMarkdown renders one section ("" when empty).
func SectionMarkdown(d api.PlanDocument, sec string) string {
	var b strings.Builder
	switch sec {
	case SecGoal:
		b.WriteString(d.Goal + "\n")
	case SecComments:
		for _, c := range d.Comments {
			fmt.Fprintf(&b, "- %s (%s)\n", oneLine(c.Text), c.Time.UTC().Format("2006-01-02 15:04"))
		}
	case SecContext:
		if d.Context != "" {
			b.WriteString(strings.TrimSpace(d.Context) + "\n")
		}
		if len(d.Refs) > 0 {
			b.WriteString("\nReferences: " + strings.Join(d.Refs, ", ") + "\n")
		}
	case SecFindings:
		for _, f := range d.Findings {
			fmt.Fprintf(&b, "- **%s** [%s] %s", f.ID, f.Status, oneLine(f.Claim))
			if f.Evidence != "" {
				fmt.Fprintf(&b, " — `%s`", f.Evidence)
			}
			if f.Note != "" {
				fmt.Fprintf(&b, " _(%s)_", oneLine(f.Note))
			}
			b.WriteString("\n")
		}
	case SecPlan:
		for _, s := range d.Steps {
			box := " "
			if s.Status == "done" {
				box = "x"
			}
			fmt.Fprintf(&b, "- [%s] **%s** %s", box, s.ID, oneLine(s.Title))
			if len(s.Files) > 0 {
				fmt.Fprintf(&b, " (`%s`)", strings.Join(s.Files, "`, `"))
			}
			if s.Status != "" && s.Status != "open" && s.Status != "done" {
				fmt.Fprintf(&b, " — %s", s.Status)
			}
			b.WriteString("\n")
			if s.Detail != "" {
				fmt.Fprintf(&b, "  %s\n", oneLine(s.Detail))
			}
			if s.Note != "" {
				fmt.Fprintf(&b, "  _Note: %s_\n", oneLine(s.Note))
			}
		}
	case SecTests:
		for _, t := range d.Tests {
			fmt.Fprintf(&b, "- **%s** %s", t.ID, oneLine(t.Title))
			if t.Kind != "" {
				fmt.Fprintf(&b, " [%s]", t.Kind)
			}
			if t.Status != "" {
				fmt.Fprintf(&b, " — %s", t.Status)
			}
			b.WriteString("\n")
			if t.Given != "" || t.When != "" || t.Then != "" {
				fmt.Fprintf(&b, "  Given %s; when %s; then %s.\n", oneLine(t.Given), oneLine(t.When), oneLine(t.Then))
			}
			if t.File != "" {
				fmt.Fprintf(&b, "  File: `%s`", t.File)
				if len(t.Tests) > 0 {
					fmt.Fprintf(&b, " · tests: %s", strings.Join(t.Tests, ", "))
				}
				b.WriteString("\n")
			}
		}
	case SecCriteria:
		for _, c := range d.Criteria {
			box := " "
			if c.Met {
				box = "x"
			}
			fmt.Fprintf(&b, "- [%s] **%s** %s", box, c.ID, oneLine(c.Text))
			if len(c.Tests) > 0 {
				fmt.Fprintf(&b, " ← %s", strings.Join(c.Tests, ", "))
			} else {
				b.WriteString(" ← (no test)")
			}
			b.WriteString("\n")
		}
	case SecRisks:
		for _, r := range d.Risks {
			fmt.Fprintf(&b, "- **%s** (%s) %s\n", r.ID, r.Kind, oneLine(r.Text))
		}
	case SecReview:
		for _, it := range d.Review {
			fmt.Fprintf(&b, "- **%s** [%s/%s] %s → %s (%s): %s", it.ID, it.Severity, it.Status, it.From, it.To, it.Target, oneLine(it.Claim))
			if it.Evidence != "" {
				fmt.Fprintf(&b, " — `%s`", it.Evidence)
			}
			b.WriteString("\n")
			if it.Suggestion != "" {
				fmt.Fprintf(&b, "  Suggestion: %s\n", oneLine(it.Suggestion))
			}
			for _, r := range it.Responses {
				fmt.Fprintf(&b, "  - %s: **%s** %s", r.Role, r.Verdict, oneLine(r.Text))
				if r.Evidence != "" {
					fmt.Fprintf(&b, " — `%s`", r.Evidence)
				}
				b.WriteString("\n")
			}
			if it.Decision != nil {
				fmt.Fprintf(&b, "  - decided by %s: %s is right. %s\n", it.Decision.By, orDash(it.Decision.Winner), oneLine(it.Decision.Reason))
			}
		}
	case SecDecisions:
		for _, x := range d.Decisions {
			fmt.Fprintf(&b, "- **%s** (%s) %s\n", x.ID, x.By, oneLine(x.Text))
		}
	case SecGates:
		for _, g := range d.Gates {
			mark := "passed"
			switch {
			case g.Skipped != "":
				mark = "skipped: " + g.Skipped
			case !g.Passed:
				mark = "FAILED"
			}
			fmt.Fprintf(&b, "- **%s** %s", g.Name, mark)
			if g.Summary != "" {
				fmt.Fprintf(&b, " — %s", oneLine(g.Summary))
			}
			b.WriteString("\n")
		}
	case SecSummary:
		if d.Summary != "" {
			b.WriteString(strings.TrimSpace(d.Summary) + "\n")
		}
	}
	return b.String()
}

func orDash(s string) string {
	if s == "" {
		return "—"
	}
	return s
}

// oneLine joins the lines of s (list items stay one line).
func oneLine(s string) string {
	return strings.Join(strings.Fields(s), " ")
}
