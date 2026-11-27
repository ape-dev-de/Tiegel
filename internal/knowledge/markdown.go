package knowledge

import (
	"fmt"
	"net/url"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/ape-dev-de/tiegel/internal/mermaid"
)

// Meta is the front matter of a page.
type Meta struct {
	ID          string
	Level       string
	Title       string
	Sources     []string
	Commit      string
	GeneratedAt time.Time
	Confidence  float64
	Model       string
}

// FrontMatter renders the YAML front matter.
func (m Meta) FrontMatter() string {
	var b strings.Builder
	b.WriteString("---\n")
	fmt.Fprintf(&b, "id: %s\n", yamlString(m.ID))
	if m.Level != "" {
		fmt.Fprintf(&b, "level: %s\n", yamlString(m.Level))
	}
	fmt.Fprintf(&b, "title: %s\n", yamlString(m.Title))
	b.WriteString("sources:\n")
	for _, s := range m.Sources {
		fmt.Fprintf(&b, "  - %s\n", yamlString(s))
	}
	if m.Commit != "" {
		fmt.Fprintf(&b, "commit: %s\n", yamlString(m.Commit))
	}
	fmt.Fprintf(&b, "generated-at: %s\n", m.GeneratedAt.UTC().Format(time.RFC3339))
	fmt.Fprintf(&b, "confidence: %s\n", strconv.FormatFloat(m.Confidence, 'f', 2, 64))
	if m.Model != "" {
		fmt.Fprintf(&b, "model: %s\n", yamlString(m.Model))
	}
	b.WriteString("---\n")
	return b.String()
}

func yamlString(s string) string {
	if s == "" || strings.ContainsAny(s, ":#{}[],&*!|>'\"%@`\\") || strings.TrimSpace(s) != s {
		return strconv.Quote(s)
	}
	return s
}

// SplitFrontMatter returns the front matter fields (simple key: value
// lines, lists joined by ",") and the body of a document.
func SplitFrontMatter(doc string) (map[string]string, string) {
	if !strings.HasPrefix(doc, "---\n") {
		return nil, doc
	}
	end := strings.Index(doc[4:], "\n---\n")
	if end < 0 {
		return nil, doc
	}
	fm := map[string]string{}
	key := ""
	for _, l := range strings.Split(doc[4:4+end], "\n") {
		if strings.HasPrefix(l, "  - ") && key != "" {
			v := unquote(strings.TrimPrefix(l, "  - "))
			if fm[key] != "" {
				v = fm[key] + "," + v
			}
			fm[key] = v
			continue
		}
		k, v, ok := strings.Cut(l, ":")
		if !ok {
			continue
		}
		key = strings.TrimSpace(k)
		fm[key] = unquote(strings.TrimSpace(v))
	}
	return fm, doc[4+end+5:]
}

func unquote(s string) string {
	if u, err := strconv.Unquote(s); err == nil {
		return u
	}
	return s
}

// Linker turns project relative source references into links.
type Linker struct {
	// Root is the project root (links point to absolute file URLs so
	// that the document works from outside the project).
	Root string
}

// Link renders "[path:line](file:///abs/path#Lline)".
func (l Linker) Link(file string, line int) string {
	label := file
	if line > 0 {
		label = fmt.Sprintf("%s:%d", file, line)
	}
	return "[" + escapeLabel(label) + "](" + l.URL(file, line) + ")"
}

// URL is the file URL of a project relative file and line.
func (l Linker) URL(file string, line int) string {
	abs := filepath.ToSlash(filepath.Join(l.Root, filepath.FromSlash(file)))
	if !strings.HasPrefix(abs, "/") {
		abs = "/" + abs // Windows drive letters
	}
	u := (&url.URL{Scheme: "file", Path: abs}).String()
	if line > 0 {
		u += "#L" + strconv.Itoa(line)
	}
	return u
}

// Ref renders a code reference: link and symbol, marked when the
// mechanical check failed.
func (l Linker) Ref(r Ref) string {
	var parts []string
	if r.File != "" {
		parts = append(parts, l.Link(r.File, r.Line))
	}
	if r.Symbol != "" {
		parts = append(parts, "`"+r.Symbol+"`")
	}
	s := strings.Join(parts, " ")
	if r.Unverified() {
		s += " *(unverified)*"
	}
	return s
}

func escapeLabel(s string) string {
	return strings.NewReplacer("[", `\[`, "]", `\]`).Replace(s)
}

// cell makes text safe for a Markdown table cell.
func cell(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	return strings.ReplaceAll(s, "|", `\|`)
}

// ValidMermaid reports whether Tiegel's Mermaid renderer accepts src
// (only flowchart, sequence, state, class, ER and pie diagrams).
func ValidMermaid(src string) error {
	if strings.TrimSpace(src) == "" {
		return fmt.Errorf("empty diagram")
	}
	_, err := mermaid.Render(src, mermaid.Options{})
	return err
}

// mermaidBlock returns a fenced Mermaid block, or "" when the diagram
// does not parse (a broken diagram is dropped rather than written).
func mermaidBlock(src string) string {
	src = strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(strings.TrimSpace(src), "```mermaid"), "```"))
	if ValidMermaid(src) != nil {
		return ""
	}
	return "```mermaid\n" + src + "\n```\n"
}

var nodeIDRE = regexp.MustCompile(`[^A-Za-z0-9_]+`)

// mermaidID derives a node ID.
func mermaidID(prefix, s string) string {
	id := strings.Trim(nodeIDRE.ReplaceAllString(s, "_"), "_")
	if id == "" {
		id = "n"
	}
	return prefix + id
}

// mermaidLabel makes text safe inside ["…"] and messages.
func mermaidLabel(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	s = strings.NewReplacer(`"`, "'", "[", "(", "]", ")", "{", "(", "}", ")", ";", ",", "#", "", "|", "/", "<", "‹", ">", "›", "`", "'").Replace(s)
	if r := []rune(s); len(r) > 60 {
		s = string(r[:59]) + "…"
	}
	return s
}

// ContextDiagram draws the C4-style context and container diagram of
// the overview as a flowchart: the system's own containers in a
// subgraph, people and external systems outside, data flows as edges.
func ContextDiagram(project string, d *OverviewDoc) string {
	var b strings.Builder
	b.WriteString("flowchart LR\n")
	ids := map[string]string{}
	inside := func(k string) bool {
		switch strings.ToLower(k) {
		case "person", "user", "external", "external system", "saas", "mail":
			return false
		}
		return true
	}
	node := func(c Container) string {
		id := mermaidID("c_", c.Name)
		ids[strings.ToLower(c.Name)] = id
		label := mermaidLabel(c.Name)
		if c.Tech != "" {
			label += " · " + mermaidLabel(c.Tech)
		}
		switch strings.ToLower(c.Kind) {
		case "database", "storage", "search":
			return fmt.Sprintf("%s[(\"%s\")]", id, label)
		case "queue":
			return fmt.Sprintf("%s>\"%s\"]", id, label)
		case "person", "user":
			return fmt.Sprintf("%s((\"%s\"))", id, label)
		case "cache":
			return fmt.Sprintf("%s{{\"%s\"}}", id, label)
		}
		return fmt.Sprintf("%s[\"%s\"]", id, label)
	}
	var in, out []string
	for _, c := range d.Containers {
		if inside(c.Kind) {
			in = append(in, node(c))
		} else {
			out = append(out, node(c))
		}
	}
	for _, n := range out {
		b.WriteString("    " + n + "\n")
	}
	if len(in) > 0 {
		fmt.Fprintf(&b, "    subgraph sys [\"%s\"]\n", mermaidLabel(project))
		for _, n := range in {
			b.WriteString("        " + n + "\n")
		}
		b.WriteString("    end\n")
	}
	for _, r := range d.Relations {
		from, to := ids[strings.ToLower(r.From)], ids[strings.ToLower(r.To)]
		if from == "" || to == "" {
			continue
		}
		if r.Label != "" {
			fmt.Fprintf(&b, "    %s -- \"%s\" --> %s\n", from, mermaidLabel(r.Label), to)
		} else {
			fmt.Fprintf(&b, "    %s --> %s\n", from, to)
		}
	}
	return b.String()
}

// SequenceDiagram draws a process as a sequence diagram: participants
// are the classes of the steps in order, each step a message from the
// previous participant.
func SequenceDiagram(d *ProcessDoc) string {
	var b strings.Builder
	b.WriteString("sequenceDiagram\n")
	var parts []string
	pid := map[string]string{}
	add := func(name string) string {
		if id, ok := pid[name]; ok {
			return id
		}
		id := mermaidID("p", name)
		pid[name] = id
		parts = append(parts, fmt.Sprintf("    participant %s as %s\n", id, mermaidLabel(name)))
		return id
	}
	trigger := "Trigger"
	if len(d.Triggers) > 0 {
		trigger = d.Triggers[0]
	}
	var msgs []string
	prev := add(trigger)
	for _, s := range d.Steps {
		who := shortClass(SymbolClass(s.Symbol))
		if who == "" {
			who = filepath.Base(s.File)
		}
		cur := add(who)
		text := mermaidLabel(s.What)
		if text == "" {
			text = "call"
		}
		msgs = append(msgs, fmt.Sprintf("    %s->>%s: %s\n", prev, cur, text))
		prev = cur
	}
	for _, w := range d.Writes {
		cur := add(w.Name)
		msgs = append(msgs, fmt.Sprintf("    %s->>%s: write\n", prev, cur))
	}
	for _, p := range parts {
		b.WriteString(p)
	}
	for _, m := range msgs {
		b.WriteString(m)
	}
	return b.String()
}

func dataList(b *strings.Builder, title string, l []Data, lk Linker) {
	if len(l) == 0 {
		return
	}
	fmt.Fprintf(b, "\n## %s\n\n", title)
	for _, x := range l {
		fmt.Fprintf(b, "- **%s**", x.Name)
		if x.Kind != "" {
			fmt.Fprintf(b, " (%s)", x.Kind)
		}
		if x.Description != "" {
			b.WriteString(": " + x.Description)
		}
		if x.Ref != nil && (x.Ref.File != "" || x.Ref.Symbol != "") {
			b.WriteString(" – " + lk.Ref(*x.Ref))
		}
		b.WriteString("\n")
	}
}

func questionsSection(b *strings.Builder, qa *QA, lk Linker, up string) {
	if len(qa.OpenQuestions) == 0 && len(qa.Answered) == 0 {
		return
	}
	b.WriteString("\n## Open questions\n\n")
	for _, q := range qa.OpenQuestions {
		b.WriteString("- " + strings.TrimSpace(q.Question))
		if len(q.Refs) > 0 {
			var l []string
			for _, r := range q.Refs {
				l = append(l, lk.Ref(r))
			}
			b.WriteString(" – " + strings.Join(l, ", "))
		}
		b.WriteString("\n")
	}
	if len(qa.Answered) > 0 {
		b.WriteString("\nAnswered here (see [questions](" + up + "questions.md)):\n\n")
		for _, a := range qa.Answered {
			fmt.Fprintf(b, "- **%s**: %s\n", a.ID, strings.TrimSpace(a.Answer))
		}
	}
}

// RenderOverview renders overview.md: the agent's summary, the C4-style
// diagram and the mechanical facts.
func RenderOverview(project string, d *OverviewDoc, f Facts, lk Linker) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# Overview: %s\n", project)
	if d != nil {
		fmt.Fprintf(&b, "\n## Summary\n\n%s\n", strings.TrimSpace(d.Summary))
		if d.Architecture != "" {
			fmt.Fprintf(&b, "\n## Architecture\n\n%s\n", strings.TrimSpace(d.Architecture))
		}
		if len(d.Containers) > 0 {
			b.WriteString("\n## System context and containers\n\n")
			if blk := mermaidBlock(ContextDiagram(project, d)); blk != "" {
				b.WriteString(blk + "\n")
			}
			b.WriteString("| Container | Kind | Technology | Role |\n| --- | --- | --- | --- |\n")
			for _, c := range d.Containers {
				fmt.Fprintf(&b, "| %s | %s | %s | %s |\n", cell(c.Name), cell(c.Kind), cell(c.Tech), cell(c.Description))
			}
			if len(d.Relations) > 0 {
				b.WriteString("\nData flows between them:\n\n")
				for _, r := range d.Relations {
					fmt.Fprintf(&b, "- %s → %s", r.From, r.To)
					if r.Label != "" {
						b.WriteString(": " + r.Label)
					}
					b.WriteString("\n")
				}
			}
		}
	} else {
		b.WriteString("\n## Summary\n\nThe agent's analysis is not available yet; the facts below were collected by Tiegel.\n")
	}
	b.WriteString("\n## Tech stack\n\n")
	fw := f.Framework
	if fw == "" {
		fw = "none detected"
	}
	fmt.Fprintf(&b, "- **Framework:** %s", fw)
	if len(f.Frameworks) > 0 {
		fmt.Fprintf(&b, " (%s)", strings.Join(f.Frameworks, ", "))
	}
	b.WriteString("\n")
	if f.PHP != "" {
		fmt.Fprintf(&b, "- **PHP:** %s\n", f.PHP)
	}
	if f.Style != "" {
		fmt.Fprintf(&b, "- **Style (mechanical guess):** %s\n", f.Style)
	}
	if len(f.Packages) > 0 {
		fmt.Fprintf(&b, "- **Packages:** %s\n", strings.Join(f.Packages, ", "))
	}
	if f.PHPRoot != "" && f.PHPRoot != "." {
		fmt.Fprintf(&b, "- **PHP root:** %s\n", f.PHPRoot)
	}
	if len(f.PSR4) > 0 {
		var l []string
		for _, ns := range sortedKeys(f.PSR4) {
			l = append(l, "`"+ns+"` → "+strings.Join(f.PSR4[ns], ", "))
		}
		fmt.Fprintf(&b, "- **PSR-4:** %s\n", strings.Join(l, "; "))
	}
	roles := map[string]string{}
	if d != nil {
		for _, fo := range d.Folders {
			roles[strings.Trim(fo.Path, "/")] = fo.Role
		}
	}
	if len(f.Folders) > 0 {
		b.WriteString("\n## Folders\n\n| Folder | PHP files | Role |\n| --- | --- | --- |\n")
		for _, fo := range f.Folders {
			fmt.Fprintf(&b, "| %s | %d | %s |\n", cell(fo.Path), fo.PHPFiles, cell(roles[fo.Path]))
		}
	}
	if len(f.Scripts)+len(f.Services)+len(f.Tooling)+len(f.DevTools) > 0 {
		b.WriteString("\n## Development tooling\n\n")
		if len(f.Services) > 0 {
			fmt.Fprintf(&b, "- **Docker Compose services:** %s\n", strings.Join(f.Services, ", "))
		}
		if len(f.DevTools) > 0 {
			fmt.Fprintf(&b, "- **QA and tests:** %s\n", strings.Join(f.DevTools, ", "))
		}
		for _, t := range f.Tooling {
			fmt.Fprintf(&b, "- %s: %s\n", t.Name, lk.Link(t.File, 0))
		}
		if len(f.Scripts) > 0 {
			b.WriteString("\nComposer scripts:\n\n")
			for _, s := range f.Scripts {
				fmt.Fprintf(&b, "- `%s`: `%s`\n", s.Name, strings.ReplaceAll(s.Command, "`", "'"))
			}
		}
	}
	if d != nil {
		questionsSection(&b, &d.QA, lk, "")
	}
	return b.String()
}

// RenderEntryPoints renders entrypoints/<kind>.md (with d nil: the
// mechanical inventory only).
func RenderEntryPoints(it Item, d *EntryPointsDoc, lk Linker) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# Entry points: %s\n", it.Title)
	if d != nil {
		fmt.Fprintf(&b, "\n%s\n", strings.TrimSpace(d.Summary))
		b.WriteString("\n## Inventory\n\n| Entry point | Handler | Input | Output | Data |\n| --- | --- | --- | --- | --- |\n")
		for _, e := range d.Entries {
			name := cell(e.Name)
			if !e.Found {
				name += " *(found by the agent)*"
			}
			fmt.Fprintf(&b, "| %s | %s | %s | %s | %s |\n", name, cell(lk.Ref(e.Handler)), cell(e.Input), cell(e.Output), cell(e.Purpose))
		}
		groups := map[string][]string{}
		var order []string
		for _, e := range d.Entries {
			if e.Process == "" {
				continue
			}
			if _, ok := groups[e.Process]; !ok {
				order = append(order, e.Process)
			}
			groups[e.Process] = append(groups[e.Process], e.Name)
		}
		if len(order) > 0 {
			b.WriteString("\n## Grouped by process\n\n")
			for _, g := range order {
				fmt.Fprintf(&b, "- **%s:** %s\n", g, strings.Join(groups[g], ", "))
			}
		}
		questionsSection(&b, &d.QA, lk, "../")
		return b.String()
	}
	b.WriteString("\nFound by Tiegel in the code (the agent's description is not available yet).\n\n| Entry point | Handler | Declared in |\n| --- | --- | --- |\n")
	for _, e := range it.Entries {
		h := e.Handler
		if h != "" {
			h = "`" + h + "`"
		}
		fmt.Fprintf(&b, "| %s | %s | %s |\n", cell(e.Name), cell(h), cell(lk.Link(e.File, e.Line)))
	}
	return b.String()
}

// RenderProcess renders processes/<id>.md.
func RenderProcess(it Item, d *ProcessDoc, lk Linker) string {
	var b strings.Builder
	name := d.Name
	if name == "" {
		name = it.Title
	}
	fmt.Fprintf(&b, "# Process: %s\n\n%s\n", name, strings.TrimSpace(d.Summary))
	if len(d.Triggers) > 0 {
		fmt.Fprintf(&b, "\n**Triggers:** %s\n", strings.Join(d.Triggers, ", "))
	}
	dataList(&b, "Inputs", d.Inputs, lk)
	b.WriteString("\n## Data flow\n\n")
	diag := ""
	if d.Diagram != "" {
		diag = mermaidBlock(d.Diagram)
	}
	if diag == "" && len(d.Steps) > 0 {
		diag = mermaidBlock(SequenceDiagram(d))
	}
	if diag != "" {
		b.WriteString(diag + "\n")
	}
	for i, s := range d.Steps {
		fmt.Fprintf(&b, "%d. %s – %s", i+1, lk.Ref(s.Ref), strings.TrimSpace(s.What))
		if s.Data != "" {
			fmt.Fprintf(&b, " (data: %s)", s.Data)
		}
		b.WriteString("\n")
	}
	dataList(&b, "Reads", d.Reads, lk)
	dataList(&b, "Writes", d.Writes, lk)
	dataList(&b, "Outputs", d.Outputs, lk)
	dataList(&b, "External systems", d.External, lk)
	questionsSection(&b, &d.QA, lk, "../")
	return b.String()
}

// RenderComponent renders components/<id>.md; usedBy links the process
// pages (title → relative MD).
func RenderComponent(it Item, d *ComponentDoc, usedBy map[string]string, lk Linker) string {
	var b strings.Builder
	name := d.Name
	if name == "" {
		name = it.Group
	}
	fmt.Fprintf(&b, "# Component: %s\n\n`%s`", shortClass(name), name)
	if len(it.Files) > 0 {
		b.WriteString(" · " + lk.Link(it.Files[0], 0))
	}
	fmt.Fprintf(&b, "\n\n%s\n", strings.TrimSpace(d.Summary))
	if len(d.Methods) > 0 {
		b.WriteString("\n## Data contract\n")
		for _, m := range d.Methods {
			fmt.Fprintf(&b, "\n### `%s`\n\n", ShortMethod(m.Symbol))
			if m.File != "" {
				b.WriteString(lk.Ref(Ref{File: m.File, Line: m.Line, Verified: m.Verified}) + "\n\n")
			}
			line := func(k, v string) {
				if strings.TrimSpace(v) != "" {
					fmt.Fprintf(&b, "- **%s:** %s\n", k, v)
				}
			}
			line("In", m.Inputs)
			line("Out", m.Outputs)
			line("Reads", strings.Join(m.Reads, ", "))
			line("Writes", strings.Join(m.Writes, ", "))
			line("Side effects", strings.Join(m.SideEffects, ", "))
			line("Throws", strings.Join(m.Throws, ", "))
		}
	}
	if d.State != "" {
		fmt.Fprintf(&b, "\n## State\n\n%s\n", d.State)
	}
	if len(it.UsedBy) > 0 {
		b.WriteString("\n## Used by\n\n")
		for _, p := range it.UsedBy {
			if md := usedBy[p]; md != "" {
				fmt.Fprintf(&b, "- [%s](../%s)\n", escapeLabel(p), md)
			} else {
				fmt.Fprintf(&b, "- %s\n", p)
			}
		}
	}
	if len(d.Collaborators) > 0 {
		b.WriteString("\n## Collaborators\n\n")
		for _, c := range d.Collaborators {
			fmt.Fprintf(&b, "- `%s`\n", c)
		}
	}
	questionsSection(&b, &d.QA, lk, "../")
	return b.String()
}

// ShortMethod shortens "App\X\Order::place" to "Order::place".
func ShortMethod(sym string) string {
	if i := strings.Index(sym, "::"); i > 0 {
		return shortClass(sym[:i]) + sym[i:]
	}
	return shortClass(sym)
}

// RenderQuestions renders questions.md from the store; titles maps item
// IDs to their pages (for "asked in" links).
func RenderQuestions(s *QuestionStore, pages map[string]string, lk Linker) string {
	var b strings.Builder
	open, answered := s.Counts()
	fmt.Fprintf(&b, "# Open questions\n\nQuestions the analysis could not settle from the code, and the answers later levels found. %d open, %d answered.\n", open, answered)
	link := func(item string) string {
		if md := pages[item]; md != "" {
			return "[" + escapeLabel(item) + "](" + md + ")"
		}
		return item
	}
	refs := func(l []Ref) string {
		var out []string
		for _, r := range l {
			out = append(out, lk.Ref(r))
		}
		return strings.Join(out, ", ")
	}
	if open > 0 {
		fmt.Fprintf(&b, "\n## Open (%d)\n\n", open)
		for _, q := range s.Questions {
			if q.Status == QuestionAnswered {
				continue
			}
			fmt.Fprintf(&b, "- **%s** %s (asked in %s", q.ID, q.Text, link(q.Item))
			if r := refs(q.Refs); r != "" {
				b.WriteString("; see " + r)
			}
			b.WriteString(")\n")
		}
	}
	if answered > 0 {
		fmt.Fprintf(&b, "\n## Answered (%d)\n\n", answered)
		for _, q := range s.Questions {
			if q.Status != QuestionAnswered {
				continue
			}
			fmt.Fprintf(&b, "- **%s** %s (asked in %s)\n  **Answer:** %s (by %s", q.ID, q.Text, link(q.Item), q.Answer, link(q.AnsweredBy))
			if r := refs(q.AnswerRefs); r != "" {
				b.WriteString("; " + r)
			}
			b.WriteString(")\n")
		}
	}
	return b.String()
}

// RenderIndex renders index.md, the table of contents: overview → entry
// points → processes → components → questions.
func RenderIndex(project string, generated time.Time, m *Manifest, open, answered int) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# Knowledge base: %s\n\nGenerated by Tiegel from the source code, top-down: overview → entry points → processes → components. Agents describe and validate every page; references are checked against the code. Last run: %s.\n",
		project, generated.UTC().Format("2006-01-02 15:04 UTC"))
	sum := func(s string, n int) string {
		s = cell(s)
		if r := []rune(s); len(r) > n {
			s = string(r[:n-1]) + "…"
		}
		return s
	}
	conf := func(st *ItemState) string {
		if st.Mechanical {
			return "facts only"
		}
		return fmt.Sprintf("%.2f", st.Confidence)
	}
	for i, level := range Levels[:4] {
		var l []*ItemState
		for _, st := range m.Level(level) {
			if st.MD != "" {
				l = append(l, st)
			}
		}
		if len(l) == 0 {
			continue
		}
		fmt.Fprintf(&b, "\n## %d. %s\n\n", i+1, LevelTitle(level))
		switch level {
		case LevelOverview, LevelEntryPoints:
			for _, st := range l {
				fmt.Fprintf(&b, "- [%s](%s) (%s) – %s\n", escapeLabel(st.Title), st.MD, conf(st), sum(st.Summary, 200))
			}
		default:
			fmt.Fprintf(&b, "| %s | Confidence | Summary |\n| --- | --- | --- |\n", map[string]string{LevelProcesses: "Process", LevelComponents: "Component"}[level])
			for _, st := range l {
				fmt.Fprintf(&b, "| [%s](%s) | %s | %s |\n", escapeLabel(st.Title), st.MD, conf(st), sum(st.Summary, 160))
			}
		}
	}
	if open+answered > 0 {
		fmt.Fprintf(&b, "\n## %s\n\n[questions.md](questions.md): %d open, %d answered.\n", LevelTitle(LevelQuestions), open, answered)
	}
	return b.String()
}

var (
	mdLinkRE  = regexp.MustCompile(`\[([^\]]*)\]\([^)]*\)`)
	mdMarkRE  = regexp.MustCompile("\\*\\*|__|`")
	mdBlankRE = regexp.MustCompile(`\s+`)
)

// Snippet returns the plain text of a section for result lists: without
// heading lines, code fences, link targets and emphasis, at most n runes.
func Snippet(md string, n int) string {
	var lines []string
	fence := false
	for _, l := range strings.Split(md, "\n") {
		t := strings.TrimSpace(l)
		if strings.HasPrefix(t, "```") {
			fence = !fence
			continue
		}
		if fence || strings.HasPrefix(t, "#") {
			continue
		}
		lines = append(lines, l)
	}
	s := mdLinkRE.ReplaceAllString(strings.Join(lines, " "), "$1")
	s = mdMarkRE.ReplaceAllString(s, "")
	s = strings.TrimSpace(mdBlankRE.ReplaceAllString(s, " "))
	if r := []rune(s); n > 0 && len(r) > n {
		s = string(r[:n-1]) + "…"
	}
	return s
}
