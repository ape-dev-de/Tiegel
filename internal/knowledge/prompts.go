package knowledge

import (
	"bytes"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"text/template"
)

// The prompts are Go text templates (D565), embedded from prompts/*.tmpl:
//
//	base.tmpl         shared role and rules ("base"), the facts block
//	                  ("facts"), open questions ("questions") and the
//	                  answer format ("answer")
//	overview.tmpl     level 0
//	entrypoints.tmpl  level 1 (one page per entry point kind)
//	processes.tmpl    level 2 (one page per process)
//	components.tmpl   level 3 (one page per class)
//	questions.tmpl    the "answer open questions" iteration
//	validate.tmpl     the review of every page (second agent)
//	repair.tmpl       asks again after an unusable answer
//
// A file with the same name in an override directory (Tiegel uses
// <config>/knowledge-prompts) replaces the embedded one. Templates get
// PromptData; the function "budget N TEXT" cuts TEXT to about N tokens,
// "trim" trims white space and "lower" lower-cases.

//go:embed prompts/*.tmpl
var promptFS embed.FS

// maxListedFiles bounds the file list in a prompt.
const maxListedFiles = 60

// PromptData is the data of a prompt template.
type PromptData struct {
	Project string
	// Level and LevelTitle of the page; Title, ID and Group of its item.
	Level, LevelTitle string
	Title, ID, Group  string
	// Facts are the compact mechanical facts, Overview the compact
	// overview page (from the earlier level, "" before it exists).
	Facts, Overview string
	// Entries are the entry points of the page (compact lines).
	Entries string
	// Context is level specific (processes: what the entry-point pages
	// say; components: the process steps through the class).
	Context string
	// Files are the sources to start with; MoreFiles the rest not listed.
	Files     []string
	MoreFiles int
	// Questions are the open questions the page should see.
	Questions []PromptQuestion
	// Schema is the JSON shape of the answer.
	Schema string
	// Doc, Notes and MoreNotes are for the review: the page as JSON and
	// the findings of the mechanical check.
	Doc       string
	Notes     []string
	MoreNotes int
	// Error is for the repair prompt.
	Error string
}

// PromptQuestion is an open question in a prompt.
type PromptQuestion struct {
	ID, Text, Item string
	// Refs are the cited files and symbols, comma separated.
	Refs string
}

// Prompts renders the prompt templates.
type Prompts struct {
	t *template.Template
	// Overrides are the files that replaced embedded templates.
	Overrides []string
}

// Budget cuts s to about tokens tokens (four characters each) at a line
// break and marks the cut.
func Budget(tokens int, s string) string {
	limit := tokens * 4
	if len(s) <= limit {
		return s
	}
	cut := s[:limit]
	if i := strings.LastIndexByte(cut, '\n'); i > limit/2 {
		cut = cut[:i]
	}
	return cut + "\n… (shortened)"
}

var promptFuncs = template.FuncMap{"budget": Budget, "lower": strings.ToLower, "trim": strings.TrimSpace}

// LoadPrompts parses the embedded templates and, when overrideDir holds
// *.tmpl files of the same names, replaces those. A broken override is
// an error (the embedded set is returned with it).
func LoadPrompts(overrideDir string) (*Prompts, error) {
	t, err := template.New("prompts").Funcs(promptFuncs).ParseFS(promptFS, "prompts/*.tmpl")
	if err != nil {
		return nil, fmt.Errorf("knowledge prompts: %w", err)
	}
	p := &Prompts{t: t}
	if overrideDir == "" {
		return p, nil
	}
	names, _ := fs.Glob(promptFS, "prompts/*.tmpl")
	var files []string
	for _, n := range names {
		f := filepath.Join(overrideDir, filepath.Base(n))
		if st, err := os.Stat(f); err == nil && !st.IsDir() {
			files = append(files, f)
		}
	}
	if len(files) == 0 {
		return p, nil
	}
	sort.Strings(files)
	clone, err := t.Clone()
	if err != nil {
		return p, err
	}
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			return p, fmt.Errorf("knowledge prompt %s: %w", f, err)
		}
		if _, err := clone.New(filepath.Base(f)).Parse(string(b)); err != nil {
			return p, fmt.Errorf("knowledge prompt %s: %w", f, err)
		}
	}
	return &Prompts{t: clone, Overrides: files}, nil
}

// MustDefaultPrompts returns the embedded prompts (they are tested to
// parse; a failure yields nil and is caught by Render).
func defaultPrompts() *Prompts {
	p, _ := LoadPrompts("")
	return p
}

// Render executes a template ("overview.tmpl", …) with d.
func (p *Prompts) Render(name string, d PromptData) (string, error) {
	if p == nil || p.t == nil {
		return "", errors.New("knowledge prompts are not loaded")
	}
	var b bytes.Buffer
	if err := p.t.ExecuteTemplate(&b, name, d); err != nil {
		return "", fmt.Errorf("knowledge prompt %s: %w", name, err)
	}
	return strings.TrimSpace(b.String()) + "\n", nil
}

// TemplateFor is the template of a level's describe step.
func TemplateFor(level string) string { return level + ".tmpl" }

// listFiles fills Files and MoreFiles.
func (d *PromptData) listFiles(files []string) {
	d.Files = files
	if len(files) > maxListedFiles {
		d.Files, d.MoreFiles = files[:maxListedFiles], len(files)-maxListedFiles
	}
}

// CompactEntries renders entry points as prompt lines.
func CompactEntries(eps []EntryPoint) string {
	var b strings.Builder
	for _, e := range eps {
		fmt.Fprintf(&b, "- %s", e.Name)
		if e.Handler != "" {
			fmt.Fprintf(&b, " → %s", e.Handler)
		}
		loc := e.File
		if e.Line > 0 {
			loc = fmt.Sprintf("%s:%d", e.File, e.Line)
		}
		if e.Source == "agent" {
			loc += ", added by the entry-point page"
		}
		fmt.Fprintf(&b, " (%s)\n", loc)
	}
	return b.String()
}

// CompactOverview renders the overview page for prompts.
func CompactOverview(d *OverviewDoc) string {
	if d == nil {
		return ""
	}
	var b strings.Builder
	b.WriteString(strings.TrimSpace(d.Summary) + "\n")
	if d.Architecture != "" {
		b.WriteString("Architecture: " + d.Architecture + "\n")
	}
	if len(d.Containers) > 0 {
		var l []string
		for _, c := range d.Containers {
			s := c.Name
			if c.Tech != "" {
				s += " (" + c.Tech + ")"
			}
			l = append(l, s)
		}
		b.WriteString("Containers: " + strings.Join(l, ", ") + "\n")
	}
	for _, r := range d.Relations {
		fmt.Fprintf(&b, "- %s → %s: %s\n", r.From, r.To, r.Label)
	}
	return b.String()
}

func refsText(refs []Ref) string {
	var l []string
	for _, r := range refs {
		s := r.File
		if r.Line > 0 {
			s = fmt.Sprintf("%s:%d", r.File, r.Line)
		}
		if r.Symbol != "" {
			if s != "" {
				s += " "
			}
			s += r.Symbol
		}
		if s != "" {
			l = append(l, s)
		}
	}
	return strings.Join(l, ", ")
}

func promptQuestions(qs []*QuestionRecord) []PromptQuestion {
	var out []PromptQuestion
	for _, q := range qs {
		out = append(out, PromptQuestion{ID: q.ID, Text: q.Text, Item: q.Item, Refs: refsText(q.Refs)})
	}
	return out
}
