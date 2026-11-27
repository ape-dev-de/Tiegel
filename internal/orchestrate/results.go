package orchestrate

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
)

// Structured results of the roles. Every role answers with one JSON
// object; Tiegel validates it and asks again in the same session with
// the error when it does not match (repair).

// ErrNoJSON means the answer contained no JSON object.
var ErrNoJSON = errors.New("no JSON object in the answer")

var fenceRE = regexp.MustCompile("(?s)```(?:json|JSON)?[ \t]*\r?\n(.*?)\r?\n[ \t]*```")

// ExtractJSON returns the JSON object of an answer: the last fenced block
// that is a valid object, else the outermost {...} of the text.
func ExtractJSON(answer string) ([]byte, error) {
	ms := fenceRE.FindAllStringSubmatch(answer, -1)
	for i := len(ms) - 1; i >= 0; i-- {
		b := bytes.TrimSpace([]byte(ms[i][1]))
		if len(b) > 0 && b[0] == '{' && json.Valid(b) {
			return b, nil
		}
	}
	if i, j := strings.IndexByte(answer, '{'), strings.LastIndexByte(answer, '}'); i >= 0 && j > i {
		b := []byte(answer[i : j+1])
		if json.Valid(b) {
			return b, nil
		}
		return nil, fmt.Errorf("invalid JSON: %w", json.Unmarshal(b, new(any)))
	}
	return nil, ErrNoJSON
}

// decode extracts the JSON of answer into v and runs its check.
func decode(answer string, v interface{ check() error }) error {
	b, err := ExtractJSON(answer)
	if err != nil {
		return err
	}
	dec := json.NewDecoder(bytes.NewReader(b))
	if err := dec.Decode(v); err != nil {
		return fmt.Errorf("does not match the schema: %w", err)
	}
	return v.check()
}

// FindingIn is a claim with evidence (planners).
type FindingIn struct {
	Claim    string `json:"claim"`
	Evidence string `json:"evidence"`
}

// StepIn is a plan step (planners).
type StepIn struct {
	ID     string   `json:"id,omitempty"`
	Title  string   `json:"title"`
	Detail string   `json:"detail,omitempty"`
	Files  []string `json:"files,omitempty"`
}

// TestIn is a test case (planner-tests, QA review).
type TestIn struct {
	Title string `json:"title"`
	Given string `json:"given"`
	When  string `json:"when"`
	Then  string `json:"then"`
	File  string `json:"file"`
	Kind  string `json:"kind,omitempty"`
}

// CriterionIn is an acceptance criterion with its test cases.
type CriterionIn struct {
	Text  string   `json:"text"`
	Tests []TestIn `json:"tests"`
}

// RiskIn is a risk or open question.
type RiskIn struct {
	Text string `json:"text"`
	Kind string `json:"kind,omitempty"`
}

// Contribution is the answer of the planners (each fills its fields).
type Contribution struct {
	Context  string        `json:"context,omitempty"`
	Refs     []string      `json:"refs,omitempty"`
	Findings []FindingIn   `json:"findings,omitempty"`
	Steps    []StepIn      `json:"steps,omitempty"`
	Criteria []CriterionIn `json:"criteria,omitempty"`
	Risks    []RiskIn      `json:"risks,omitempty"`
	// Replace lists sections the answer replaces (revision rounds).
	Replace []string `json:"replace,omitempty"`
}

func (c *Contribution) check() error {
	for i, f := range c.Findings {
		if strings.TrimSpace(f.Claim) == "" {
			return fmt.Errorf("findings[%d].claim is empty", i)
		}
	}
	for i, s := range c.Steps {
		if strings.TrimSpace(s.Title) == "" {
			return fmt.Errorf("steps[%d].title is empty", i)
		}
	}
	for i, cr := range c.Criteria {
		if strings.TrimSpace(cr.Text) == "" {
			return fmt.Errorf("criteria[%d].text is empty", i)
		}
		if len(cr.Tests) == 0 {
			return fmt.Errorf("criteria[%d] has no test case: every acceptance criterion needs at least one test", i)
		}
		for j, t := range cr.Tests {
			if strings.TrimSpace(t.Title) == "" {
				return fmt.Errorf("criteria[%d].tests[%d].title is empty", i, j)
			}
		}
	}
	if c.Context == "" && len(c.Findings) == 0 && len(c.Steps) == 0 && len(c.Criteria) == 0 && len(c.Risks) == 0 {
		return errors.New("the answer is empty: fill the fields of your role")
	}
	return nil
}

// Verdict is the validator's status of one finding.
type Verdict struct {
	ID       string `json:"id"`
	Status   string `json:"status"`
	Evidence string `json:"evidence,omitempty"`
	Note     string `json:"note,omitempty"`
}

// Validation is the validator's answer.
type Validation struct {
	Findings []Verdict `json:"findings"`
}

func (v *Validation) check() error {
	if v.Findings == nil {
		return errors.New("findings is missing")
	}
	for i, f := range v.Findings {
		switch f.Status {
		case "verified", "refuted", "unknown":
		default:
			return fmt.Errorf("findings[%d].status %q must be verified, refuted or unknown", i, f.Status)
		}
		if f.ID == "" {
			return fmt.Errorf("findings[%d].id is empty", i)
		}
	}
	return nil
}

// Issue is a finding of a review (QA review, critic, cross rating).
type Issue struct {
	Severity   string `json:"severity"`
	Target     string `json:"target,omitempty"`
	Section    string `json:"section,omitempty"`
	Claim      string `json:"claim"`
	Evidence   string `json:"evidence,omitempty"`
	Suggestion string `json:"suggestion,omitempty"`
}

func checkIssues(l []Issue, targets ...string) error {
	for i, is := range l {
		switch is.Severity {
		case "blocker", "major", "minor":
		default:
			return fmt.Errorf("issues[%d].severity %q must be blocker, major or minor", i, is.Severity)
		}
		if strings.TrimSpace(is.Claim) == "" {
			return fmt.Errorf("issues[%d].claim is empty", i)
		}
		if len(targets) > 0 {
			ok := false
			for _, t := range targets {
				ok = ok || is.Target == t
			}
			if !ok {
				return fmt.Errorf("issues[%d].target %q must be one of %s", i, is.Target, strings.Join(targets, ", "))
			}
		}
	}
	return nil
}

// QAReview is the senior QA's answer in the planning phase.
type QAReview struct {
	Issues   []Issue       `json:"issues"`
	Criteria []CriterionIn `json:"criteria,omitempty"`
	Risks    []RiskIn      `json:"risks,omitempty"`
}

func (q *QAReview) check() error {
	if q.Issues == nil {
		return errors.New("issues is missing (use [] when you found none)")
	}
	return checkIssues(q.Issues)
}

// CriterionRef is an acceptance criterion by test case IDs (summarizer).
type CriterionRef struct {
	Text  string   `json:"text"`
	Tests []string `json:"tests"`
}

// Resolution answers a review item (summarizer).
type Resolution struct {
	Item       string `json:"item"`
	Resolution string `json:"resolution"`
}

// Summary is the summarizer's answer.
type Summary struct {
	Summary     string         `json:"summary"`
	Criteria    []CriterionRef `json:"criteria,omitempty"`
	Resolutions []Resolution   `json:"resolutions,omitempty"`
}

func (s *Summary) check() error {
	if strings.TrimSpace(s.Summary) == "" {
		return errors.New("summary is empty")
	}
	for i, c := range s.Criteria {
		if len(c.Tests) == 0 {
			return fmt.Errorf("criteria[%d] has no test IDs", i)
		}
	}
	return nil
}

// Response answers a review item (DEV, QA).
type Response struct {
	Item     string `json:"item"`
	Verdict  string `json:"verdict"`
	Text     string `json:"text,omitempty"`
	Evidence string `json:"evidence,omitempty"`
}

func checkResponses(rs []Response) error {
	for i, r := range rs {
		switch r.Verdict {
		case "accept", "reject", "needs-info":
		default:
			return fmt.Errorf("responses[%d].verdict %q must be accept, reject or needs-info", i, r.Verdict)
		}
		if r.Item == "" {
			return fmt.Errorf("responses[%d].item is empty", i)
		}
		if r.Verdict == "reject" && strings.TrimSpace(r.Evidence) == "" && strings.TrimSpace(r.Text) == "" {
			return fmt.Errorf("responses[%d]: a rejection needs evidence (file:line) or reasoning", i)
		}
	}
	return nil
}

// StepReport is DEV's status of a plan step.
type StepReport struct {
	ID     string `json:"id"`
	Status string `json:"status"`
	Note   string `json:"note,omitempty"`
}

// DevResult is DEV's answer after a work turn.
type DevResult struct {
	Summary   string       `json:"summary"`
	Steps     []StepReport `json:"steps"`
	Responses []Response   `json:"responses,omitempty"`
}

func (d *DevResult) check() error {
	if d.Steps == nil {
		return errors.New("steps is missing: report the status of every plan step")
	}
	for i, s := range d.Steps {
		switch s.Status {
		case "done", "partial", "not-done":
		default:
			return fmt.Errorf("steps[%d].status %q must be done, partial or not-done", i, s.Status)
		}
	}
	return checkResponses(d.Responses)
}

// TestMap maps a test case to the tests QA wrote.
type TestMap struct {
	Case  string   `json:"case"`
	File  string   `json:"file"`
	Tests []string `json:"tests"`
}

// QAResult is QA's answer after a test writing turn.
type QAResult struct {
	Summary   string     `json:"summary"`
	Tests     []TestMap  `json:"tests"`
	Responses []Response `json:"responses,omitempty"`
}

func (q *QAResult) check() error {
	if q.Tests == nil {
		return errors.New("tests is missing: map every test case to the test methods you wrote")
	}
	for i, t := range q.Tests {
		if t.Case == "" {
			return fmt.Errorf("tests[%d].case is empty", i)
		}
	}
	return checkResponses(q.Responses)
}

// Rebuttal is the raiser's answer to a rejected item.
type Rebuttal struct {
	Item    string `json:"item"`
	Verdict string `json:"verdict"`
	Text    string `json:"text,omitempty"`
}

// Rating is a critic's or cross rater's answer.
type Rating struct {
	Issues    []Issue    `json:"issues"`
	Rebuttals []Rebuttal `json:"rebuttals,omitempty"`
}

func (r *Rating) check() error {
	if r.Issues == nil {
		return errors.New("issues is missing (use [] when you found none)")
	}
	if err := checkIssues(r.Issues, "impl", "tests"); err != nil {
		return err
	}
	for i, b := range r.Rebuttals {
		if b.Verdict != "withdraw" && b.Verdict != "insist" {
			return fmt.Errorf("rebuttals[%d].verdict %q must be withdraw or insist", i, b.Verdict)
		}
	}
	return nil
}

// Ruling is the mediator's decision of one item.
type Ruling struct {
	Item   string `json:"item"`
	Winner string `json:"winner"`
	Reason string `json:"reason"`
}

// Mediation is the mediator's answer.
type Mediation struct {
	Decisions []Ruling `json:"decisions"`
}

func (m *Mediation) check() error {
	if len(m.Decisions) == 0 {
		return errors.New("decisions is empty")
	}
	for i, d := range m.Decisions {
		if d.Winner != "dev" && d.Winner != "qa" {
			return fmt.Errorf("decisions[%d].winner %q must be dev or qa", i, d.Winner)
		}
		if strings.TrimSpace(d.Reason) == "" {
			return fmt.Errorf("decisions[%d].reason is empty", i)
		}
	}
	return nil
}

// Final is the summarizer's report after the execution.
type Final struct {
	Summary string `json:"summary"`
}

func (f *Final) check() error {
	if strings.TrimSpace(f.Summary) == "" {
		return errors.New("summary is empty")
	}
	return nil
}
