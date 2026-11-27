// Package knowledgetest is a scripted stand-in for the coding agent of
// the knowledge pipeline (D565): it answers every level's describe and
// review prompts with valid JSON derived from the prompt (the inventory,
// the files to start with, the open questions), so tests of the
// pipeline, the module, headless and the GUI run without network and
// without tokens.
package knowledgetest

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"sync"

	"github.com/ape-dev-de/tiegel/internal/acp/acptest"
	kn "github.com/ape-dev-de/tiegel/internal/knowledge"
)

// Fake answers pipeline prompts.
type Fake struct {
	// Service is cited by every process step after the handler (it
	// becomes a component); empty: none.
	Service kn.Ref
	// Diagram is put into process pages (e.g. an invalid one).
	Diagram string
	// Confidence of the reviews (default 0.85).
	Confidence float64
	// Hook runs before a describe answer of a level (block, fail …); a
	// non-nil error is returned as the answer "I could not finish: …".
	Hook func(ctx context.Context, level, title string) error

	mu      sync.Mutex
	prompts []string
}

// Prompts returns the prompts seen so far.
func (f *Fake) Prompts() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.prompts...)
}

// Count counts the prompts containing s.
func (f *Fake) Count(s string) int {
	n := 0
	for _, p := range f.Prompts() {
		if strings.Contains(p, s) {
			n++
		}
	}
	return n
}

var (
	taskRE     = regexp.MustCompile(`(?m)^## Task: (overview|entry points|process|component|answer open questions|review)(?: \(([^)]*)\))?(?: – (.*))?$`)
	questionRE = regexp.MustCompile(`(?m)^- (q-[0-9a-f]{8})\b`)
	entryRE    = regexp.MustCompile(`(?m)^- (.+?)(?: → (\S+))? \(([^():]+?)(?::(\d+))?(?:, added by the entry-point page)?\)$`)
	fileRE     = regexp.MustCompile(`(?m)^- (\S+\.php)$`)
	pageRE     = regexp.MustCompile("(?s)The page:\n```json\n(.*?)\n```")
)

// Level returns the level and step ("describe" or "validate") of a
// prompt.
func Level(prompt string) (level, step, title string) {
	m := taskRE.FindStringSubmatch(prompt)
	if m == nil {
		return "", "", ""
	}
	title = strings.TrimSpace(m[3])
	switch m[1] {
	case "overview":
		return kn.LevelOverview, kn.StepDescribe, title
	case "entry points":
		return kn.LevelEntryPoints, kn.StepDescribe, title
	case "process":
		return kn.LevelProcesses, kn.StepDescribe, title
	case "component":
		return kn.LevelComponents, kn.StepDescribe, title
	case "answer open questions":
		return kn.LevelQuestions, kn.StepDescribe, title
	case "review":
		return m[2], kn.StepValidate, title
	}
	return "", "", ""
}

func fenced(v any) string {
	b, _ := json.Marshal(v)
	return "Here you go:\n```json\n" + string(b) + "\n```\n"
}

// Answer answers a prompt.
func (f *Fake) Answer(ctx context.Context, prompt string) string {
	f.mu.Lock()
	f.prompts = append(f.prompts, prompt)
	f.mu.Unlock()
	level, step, title := Level(prompt)
	if step == kn.StepValidate {
		m := pageRE.FindStringSubmatch(prompt)
		if m == nil {
			return "no page"
		}
		c := f.Confidence
		if c == 0 {
			c = 0.85
		}
		return fenced(map[string]any{"confidence": c, "issues": []string{"checked against the code"}, "result": json.RawMessage(m[1])})
	}
	if f.Hook != nil {
		if err := f.Hook(ctx, level, title); err != nil {
			return "I could not finish: " + err.Error()
		}
	}
	qs := questionRE.FindAllStringSubmatch(prompt, -1)
	files := fileRE.FindAllStringSubmatch(prompt, -1)
	first := ""
	if len(files) > 0 {
		first = files[0][1]
	}
	switch level {
	case kn.LevelOverview:
		return fenced(kn.OverviewDoc{Summary: "Orders come in over HTTP and the console and are stored in MySQL.", Architecture: "MVC web app with console commands",
			Containers: []kn.Container{{Name: "Web app", Kind: "webapp", Tech: "PHP"}, {Name: "MySQL", Kind: "database", Tech: "MySQL 8"}, {Name: "Customer", Kind: "person"}},
			Relations:  []kn.Relation{{From: "Customer", To: "Web app", Label: "places orders"}, {From: "Web app", To: "MySQL", Label: "writes orders"}},
			Folders:    []kn.Folder{{Path: "src", Role: "the application code"}},
			QA:         kn.QA{OpenQuestions: []kn.Question{{Question: "Which table keeps the order lines?"}}}})
	case kn.LevelEntryPoints:
		d := kn.EntryPointsDoc{Summary: "Data enters through " + strings.ToLower(title) + "."}
		for _, m := range entryRE.FindAllStringSubmatch(section(prompt, "Inventory Tiegel extracted"), -1) {
			line := 0
			fmt.Sscan(m[4], &line)
			d.Entries = append(d.Entries, kn.EntryDesc{Name: m[1], Handler: kn.Ref{File: m[3], Symbol: m[2], Line: line}, Input: "request data", Output: "a response", Purpose: "handles " + m[1], Process: "Orders"})
		}
		return fenced(d)
	case kn.LevelProcesses:
		d := kn.ProcessDoc{Name: strings.TrimSpace(strings.Split(title, " (")[0]), Summary: "The request data is validated and stored in the orders table.", Triggers: []string{"the entry points"},
			Inputs: []kn.Data{{Name: "order data", Kind: "request"}}, Writes: []kn.Data{{Name: "orders (table)", Kind: "database"}}, Outputs: []kn.Data{{Name: "response", Kind: "response"}}, Diagram: f.Diagram}
		handler := ""
		if m := entryRE.FindStringSubmatch(section(prompt, "Entry points of this process:")); m != nil {
			handler = m[2]
		}
		d.Steps = append(d.Steps, kn.Step{Ref: kn.Ref{File: first, Symbol: handler}, What: "receives the request"})
		if f.Service.File != "" {
			d.Steps = append(d.Steps, kn.Step{Ref: f.Service, What: "stores the order", Data: "Order"})
		}
		for _, q := range qs {
			d.Answered = append(d.Answered, kn.Answer{ID: q[1], Answer: "order_lines, written by the service", Refs: []kn.Ref{f.Service}})
			break
		}
		return fenced(d)
	case kn.LevelComponents:
		sym := f.Service.Symbol
		if sym == "" {
			sym = title + "::run"
		}
		return fenced(kn.ComponentDoc{Name: title, Summary: "Persists orders for the processes.", State: "orders table",
			Methods: []kn.Method{{Ref: kn.Ref{File: first, Symbol: sym}, Inputs: "array $data", Outputs: "Order", Writes: []string{"orders"}}},
			QA:      kn.QA{OpenQuestions: []kn.Question{{Question: "Is the save transactional?", Refs: []kn.Ref{{File: first}}}}}})
	case kn.LevelQuestions:
		var d kn.AnswersDoc
		for i, q := range qs {
			if i == 0 {
				d.Answered = append(d.Answered, kn.Answer{ID: q[1], Answer: "yes, inside a transaction", Refs: []kn.Ref{{File: first}}})
			} else {
				d.StillOpen = append(d.StillOpen, q[1])
			}
		}
		return fenced(d)
	}
	return "?"
}

// section returns the lines after the line starting with marker up to
// the next blank line.
func section(prompt, marker string) string {
	i := strings.Index(prompt, marker)
	if i < 0 {
		return ""
	}
	s := prompt[i:]
	if j := strings.IndexByte(s, '\n'); j >= 0 {
		s = s[j+1:]
	}
	if j := strings.Index(s, "\n\n"); j >= 0 {
		s = s[:j+1]
	}
	return s
}

// Script is the acptest script answering with Answer.
func (f *Fake) Script() acptest.Script {
	return func(ctx context.Context, t *acptest.Turn) string {
		t.Text(f.Answer(ctx, t.Prompt))
		return ""
	}
}

// Agent is a knowledge.Agent answering with Answer (no ACP).
func (f *Fake) Agent() kn.Agent { return agent{f} }

type agent struct{ f *Fake }

func (a agent) NewSession(context.Context) (kn.Session, error) { return session(a), nil }

type session struct{ f *Fake }

func (s session) Prompt(ctx context.Context, text string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	return s.f.Answer(ctx, text), nil
}

func (s session) Close() {}
