package orchestrate

import (
	"embed"
	"fmt"
	"strings"
	"text/template"
)

//go:embed prompts/*.tmpl
var promptFS embed.FS

var promptTmpl = template.Must(template.New("prompts").ParseFS(promptFS, "prompts/*.tmpl"))

// Prompt templates of the roles (names in prompts/roles.tmpl).
const (
	TmplPlannerArch  = "planner-architecture"
	TmplPlannerCode  = "planner-code"
	TmplPlannerTests = "planner-tests"
	TmplValidator    = "validator"
	TmplQAReview     = "qa-review"
	TmplSummary      = "summary"
	TmplRevise       = "revise"
	TmplDev          = "dev"
	TmplQA           = "qa"
	TmplCritic       = "critic"
	TmplRateImpl     = "rate-impl"
	TmplRateTests    = "rate-tests"
	TmplMediator     = "mediator"
	TmplFinal        = "final"
)

var roleTitles = map[string]string{
	TmplPlannerArch: "architecture planner", TmplPlannerCode: "code and edge case planner", TmplPlannerTests: "test strategy planner",
	TmplValidator: "validator", TmplQAReview: "senior QA", TmplSummary: "summarizer", TmplRevise: "planner",
	TmplDev: "senior DEV", TmplQA: "senior QA", TmplCritic: "critic", TmplRateImpl: "senior QA", TmplRateTests: "senior DEV",
	TmplMediator: "mediator", TmplFinal: "summarizer",
}

// Part is a piece of context for a prompt (a document section, a diff,
// test failures). Priority orders what is cut first when the budget is
// exceeded (lower is cut first); Min is the length (characters) it may
// be cut down to.
type Part struct {
	Title    string
	Body     string
	Priority int
	Min      int
}

// PromptData are the inputs of a role prompt.
type PromptData struct {
	// Template is the role template (Tmpl…).
	Template string
	Project  string
	Dir      string
	ReadOnly bool
	MCP      bool
	// First marks the first work turn (DEV, QA).
	First bool
	// Switch prepends the role switch notice (one shared session).
	Switch bool
	Parts  []Part
	// Budget is the token budget of the whole prompt (0 = unlimited).
	Budget int
}

type tmplData struct {
	PromptData
	RoleTitle string
	Error     string
}

// RenderPrompt builds the prompt of a role: the shared base prompt, the
// role's task with its schema, then the context parts fitted into the
// budget.
func RenderPrompt(d PromptData) (string, error) {
	td := tmplData{PromptData: d, RoleTitle: roleTitles[d.Template]}
	if td.RoleTitle == "" {
		return "", fmt.Errorf("orchestrate: unknown prompt template %q", d.Template)
	}
	var head strings.Builder
	if d.Switch {
		if err := promptTmpl.ExecuteTemplate(&head, "switch", td); err != nil {
			return "", fmt.Errorf("orchestrate: prompt: %w", err)
		}
		head.WriteString("\n\n")
	}
	if err := promptTmpl.ExecuteTemplate(&head, "base", td); err != nil {
		return "", fmt.Errorf("orchestrate: prompt: %w", err)
	}
	head.WriteString("\n\n## Your task\n\n")
	if err := promptTmpl.ExecuteTemplate(&head, d.Template, td); err != nil {
		return "", fmt.Errorf("orchestrate: prompt: %w", err)
	}
	head.WriteString("\n")
	parts := d.Parts
	if d.Budget > 0 {
		parts = Fit(parts, d.Budget*4-head.Len())
	}
	var b strings.Builder
	b.WriteString(head.String())
	for _, p := range parts {
		if strings.TrimSpace(p.Body) == "" {
			continue
		}
		fmt.Fprintf(&b, "\n## %s\n\n%s\n", p.Title, strings.TrimRight(p.Body, "\n"))
	}
	return b.String(), nil
}

// RepairPrompt asks for a valid answer after err.
func RepairPrompt(err error) string {
	var b strings.Builder
	_ = promptTmpl.ExecuteTemplate(&b, "repair", tmplData{Error: err.Error()})
	return b.String()
}

// EstimateTokens estimates the tokens of s (characters / 4).
func EstimateTokens(s string) int { return (len(s) + 3) / 4 }

// cutNote marks shortened parts.
const cutNote = "\n… (shortened by Tiegel to fit the context budget; read the code for details)"

// Fit shortens parts so that their bodies fit into chars characters:
// the lowest priorities are cut first, each down to its Min (at least a
// line), keeping the order of the parts.
func Fit(parts []Part, chars int) []Part {
	out := append([]Part(nil), parts...)
	total := func() int {
		n := 0
		for _, p := range out {
			n += len(p.Body) + len(p.Title) + 6
		}
		return n
	}
	for {
		over := total() - max(chars, 0)
		if over <= 0 {
			return out
		}
		// The lowest priority part that can still shrink.
		best := -1
		for i, p := range out {
			if len(strings.TrimSuffix(p.Body, cutNote)) <= p.Min {
				continue
			}
			if best < 0 || p.Priority < out[best].Priority {
				best = i
			}
		}
		if best < 0 {
			return out
		}
		p := &out[best]
		body := strings.TrimSuffix(p.Body, cutNote)
		extra := len(cutNote)
		if len(body) != len(p.Body) {
			extra = 0
		}
		keep := max(len(body)-over-extra, p.Min, 0)
		p.Body = cutAtLine(body, keep) + cutNote
	}
}

// cutAtLine returns the prefix of s with at most n bytes, cut at a line
// end when possible.
func cutAtLine(s string, n int) string {
	if n >= len(s) {
		return s
	}
	if n < 0 {
		n = 0
	}
	for n > 0 && n < len(s) && s[n]&0xC0 == 0x80 {
		n--
	}
	if i := strings.LastIndexByte(s[:n], '\n'); i > n/2 {
		return s[:i]
	}
	return s[:n]
}
