package knowledge

import (
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Meta is the front matter of a unit document.
type Meta struct {
	Unit        string
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
	fmt.Fprintf(&b, "unit: %s\n", yamlString(m.Unit))
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

func escapeLabel(s string) string {
	return strings.NewReplacer("[", `\[`, "]", `\]`).Replace(s)
}

// RenderBody renders a description as Markdown (used when the writing
// agent fails or returns something unusable).
func RenderBody(u Unit, d Description, l Linker) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# %s\n\n## Overview\n\n%s\n", u.Title, strings.TrimSpace(d.Summary))
	b.WriteString("\n## Data flows\n")
	if len(d.Flows) == 0 {
		b.WriteString("\nNo data flows found.\n")
	}
	for _, f := range d.Flows {
		fmt.Fprintf(&b, "\n### %s\n\n**Trigger:** %s\n\n", f.Name, f.Trigger)
		for i, s := range f.Steps {
			sym := ""
			if s.Symbol != "" {
				sym = " `" + s.Symbol + "`"
			}
			mark := ""
			if s.Verified != nil && !*s.Verified {
				mark = " *(unverified)*"
			}
			fmt.Fprintf(&b, "%d. %s%s – %s%s\n", i+1, l.Link(s.File, s.Line), sym, s.What, mark)
		}
		if f.Data != "" {
			fmt.Fprintf(&b, "\n**Data:** %s\n", f.Data)
		}
		if len(f.SideEffects) > 0 {
			fmt.Fprintf(&b, "\n**Side effects:** %s\n", strings.Join(f.SideEffects, "; "))
		}
	}
	if len(d.Entities) > 0 {
		b.WriteString("\n## Entities\n\n")
		for _, e := range d.Entities {
			fmt.Fprintf(&b, "- **%s**", e.Name)
			if e.File != "" {
				b.WriteString(" (" + l.Link(e.File, 0) + ")")
			}
			if e.Description != "" {
				b.WriteString(": " + e.Description)
			}
			b.WriteString("\n")
		}
	}
	if len(d.External) > 0 {
		b.WriteString("\n## External\n\n")
		for _, e := range d.External {
			fmt.Fprintf(&b, "- **%s**", e.Name)
			if e.Kind != "" {
				b.WriteString(" (" + e.Kind + ")")
			}
			if e.Description != "" {
				b.WriteString(": " + e.Description)
			}
			if e.File != "" {
				b.WriteString(" – " + l.Link(e.File, 0))
			}
			b.WriteString("\n")
		}
	}
	if len(d.OpenQuestions) > 0 {
		b.WriteString("\n## Open questions\n\n")
		for _, q := range d.OpenQuestions {
			b.WriteString("- " + q + "\n")
		}
	}
	return b.String()
}

var (
	wholeFence = regexp.MustCompile("(?s)^```(?:markdown|md)?[ \t]*\n(.*)\n```\\s*$")
	h2RE       = regexp.MustCompile(`(?m)^##\s+(.+?)\s*$`)
	relLinkRE  = regexp.MustCompile(`\]\(([^)\s#:]+\.php)(?:#L?(\d+))?\)`)
)

// RequiredSections must be present in the writing agent's Markdown.
var RequiredSections = []string{"Overview", "Data flows"}

// NormalizeBody cleans the writing agent's Markdown: it drops a wrapping
// fence and front matter, puts "# title" first and turns project
// relative links to PHP files into file links. It fails when a required
// section is missing.
func NormalizeBody(u Unit, answer string, l Linker) (string, error) {
	s := strings.TrimSpace(strings.ReplaceAll(answer, "\r\n", "\n"))
	if m := wholeFence.FindStringSubmatch(s); m != nil {
		s = strings.TrimSpace(m[1])
	} else if i := strings.Index(s, "```markdown\n"); i >= 0 {
		if j := strings.LastIndex(s, "\n```"); j > i {
			s = strings.TrimSpace(s[i+len("```markdown\n") : j])
		}
	}
	if _, body := SplitFrontMatter(s + "\n"); body != s+"\n" {
		s = strings.TrimSpace(body)
	}
	// Text before the first heading is chatter ("Here is the document").
	if i := strings.Index(s, "\n#"); i >= 0 && !strings.HasPrefix(s, "#") {
		s = s[i+1:]
	}
	if strings.HasPrefix(s, "# ") {
		if i := strings.IndexByte(s, '\n'); i >= 0 {
			s = strings.TrimSpace(s[i+1:])
		} else {
			s = ""
		}
	}
	have := map[string]bool{}
	for _, m := range h2RE.FindAllStringSubmatch(s, -1) {
		have[strings.ToLower(m[1])] = true
	}
	var missing []string
	for _, r := range RequiredSections {
		if !have[strings.ToLower(r)] {
			missing = append(missing, r)
		}
	}
	if len(missing) > 0 {
		return "", fmt.Errorf("sections missing: %s", strings.Join(missing, ", "))
	}
	s = relLinkRE.ReplaceAllStringFunc(s, func(m string) string {
		sm := relLinkRE.FindStringSubmatch(m)
		f := strings.TrimPrefix(sm[1], "./")
		if _, err := os.Stat(filepath.Join(l.Root, filepath.FromSlash(f))); err != nil {
			return m
		}
		line, _ := strconv.Atoi(sm[2])
		return "](" + l.URL(f, line) + ")"
	})
	return "# " + u.Title + "\n\n" + s + "\n", nil
}

var (
	mdLinkRE  = regexp.MustCompile(`\[([^\]]*)\]\([^)]*\)`)
	mdMarkRE  = regexp.MustCompile("\\*\\*|__|`")
	mdBlankRE = regexp.MustCompile(`\s+`)
)

// Snippet returns the plain text of a section for result lists: without
// heading lines, link targets and emphasis, at most n runes.
func Snippet(md string, n int) string {
	var lines []string
	for _, l := range strings.Split(md, "\n") {
		if t := strings.TrimSpace(l); strings.HasPrefix(t, "#") || strings.HasPrefix(t, "```") {
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

// IndexEntry is one line of index.md.
type IndexEntry struct {
	ID, Title, Summary, MD string
	Confidence             float64
	Files                  int
}

// RenderIndex renders index.md, the table of contents.
func RenderIndex(project string, generated time.Time, entries []IndexEntry) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# Knowledge base: %s\n\nGenerated by Tiegel from the source code (agents describe, validate and write; claims are checked against the code). Last run: %s.\n\n", project, generated.UTC().Format("2006-01-02 15:04 UTC"))
	b.WriteString("| Unit | Files | Confidence | Summary |\n| --- | --- | --- | --- |\n")
	for _, e := range entries {
		sum := strings.ReplaceAll(strings.TrimSpace(e.Summary), "\n", " ")
		sum = strings.ReplaceAll(sum, "|", `\|`)
		if len([]rune(sum)) > 160 {
			sum = string([]rune(sum)[:157]) + "…"
		}
		fmt.Fprintf(&b, "| [%s](%s) | %d | %.2f | %s |\n", escapeLabel(e.Title), e.MD, e.Files, e.Confidence, sum)
	}
	return b.String()
}

// OverviewOf returns the first paragraph below "## Overview" of a document
// body.
func OverviewOf(body string) string {
	i := strings.Index(body, "## Overview")
	if i < 0 {
		return ""
	}
	rest := strings.TrimSpace(body[i+len("## Overview"):])
	if j := strings.Index(rest, "\n\n"); j >= 0 {
		rest = rest[:j]
	}
	if strings.HasPrefix(rest, "#") {
		return ""
	}
	return rest
}
