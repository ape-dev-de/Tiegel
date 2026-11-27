package knowledge

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/ape-dev-de/tiegel/internal/api"
	"github.com/ape-dev-de/tiegel/internal/core/settings"
	kn "github.com/ape-dev-de/tiegel/internal/knowledge"
)

// The knowledge for the coding agent of the Agent window (D571): the
// setting knowledge.agentMode decides how the agent gets it – off, mcp
// (Tiegel's MCP server in session/new, the tools are read-only and
// allowed without asking), rag (every message gets the hits of a hybrid
// knowledge search as a compact context block) or both. Whenever the
// knowledge is in play, the first message of a session carries the
// instruction of agent.tmpl (generated summary, verify against the
// code, cite file:line, the code wins).

// legacyGiveAgent is the bool setting replaced by knowledge.agentMode
// (true → mcp, false → off).
const legacyGiveAgent = ID + ".giveAgent"

// Defaults of the RAG block.
const (
	DefaultRagK      = 6
	DefaultRagBudget = 1500
	// ragSearchTimeout bounds the hybrid search; after it the keyword
	// search runs alone.
	ragSearchTimeout = 10 * time.Second
	// maxSources bounds the sources listed per hit.
	maxSources = 6
)

// fallbackInstruction is used when agent.tmpl cannot be rendered.
const fallbackInstruction = "The project knowledge is a generated summary and may be outdated or wrong. Use it as a guide, not as evidence. Verify every statement you rely on against the code and cite the source as file:line. If knowledge and code disagree, the code wins – mention the contradiction."

// migrateGiveAgent moves knowledge.giveAgent to knowledge.agentMode in
// the project and global settings.
func (m *Module) migrateGiveAgent() {
	app := m.application()
	if app == nil {
		return
	}
	for _, st := range []*settings.Store{app.ProjectSettings(), app.Settings()} {
		if st == nil {
			continue
		}
		raw, ok := st.Raw(legacyGiveAgent)
		if !ok {
			continue
		}
		if !st.Has(SettingAgentMode) {
			var on bool
			mode := api.AgentContextMCP
			if json.Unmarshal(raw, &on) == nil && !on {
				mode = api.AgentContextOff
			}
			if err := settings.Set(st, SettingAgentMode, mode); err != nil {
				app.Logger().Warn("knowledge: migrate setting", "err", err)
				continue
			}
		}
		if err := st.Delete(legacyGiveAgent); err != nil {
			app.Logger().Warn("knowledge: migrate setting", "err", err)
		}
	}
}

// agentMode is the context mode: the override of an evaluation run
// (api.WithAgentContextMode), else the setting.
func (m *Module) agentMode(ctx context.Context) string {
	if mode := api.AgentContextModeFrom(ctx); slices.Contains(api.AgentContextModes, mode) {
		return mode
	}
	m.migrateGiveAgent()
	mode := setting(m, SettingAgentMode, api.AgentContextMCP)
	if !slices.Contains(api.AgentContextModes, mode) {
		return api.AgentContextMCP
	}
	return mode
}

// ready returns the knowledge directory when the knowledge base exists.
func (m *Module) ready() (string, bool) {
	dir, err := m.dir()
	if err != nil {
		return "", false
	}
	if _, err := os.Stat(filepath.Join(dir, "index.md")); err != nil {
		return "", false
	}
	return dir, true
}

// instruction renders agent.tmpl (the user's override first).
func (m *Module) instruction() string {
	name := ""
	if app := m.application(); app != nil {
		name = filepath.Base(app.ProjectRoot())
	}
	p, err := kn.LoadPrompts(m.promptDir())
	if err != nil {
		p, _ = kn.LoadPrompts("")
	}
	if p != nil {
		if s, err := p.Render("agent.tmpl", kn.PromptData{Project: name}); err == nil && strings.TrimSpace(s) != "" {
			return strings.TrimSpace(s)
		}
	}
	return fallbackInstruction
}

// promptContext implements api.HookAgentPromptContext.
func (m *Module) promptContext(ctx context.Context, pc api.AgentPromptContext) (api.AgentPromptContext, error) {
	mode := m.agentMode(ctx)
	if mode == api.AgentContextOff {
		return pc, nil
	}
	dir, ok := m.ready()
	if !ok {
		return pc, nil
	}
	out := pc
	out.Instructions = slices.Clone(pc.Instructions)
	out.Blocks = slices.Clone(pc.Blocks)
	if pc.First {
		out.Instructions = append(out.Instructions, m.instruction())
	}
	if mode == api.AgentContextRAG || mode == api.AgentContextBoth {
		if b, ok := m.ragBlock(ctx, dir, pc.Prompt); ok {
			out.Blocks = append(out.Blocks, b)
		}
	}
	return out, nil
}

// ragBlock searches the knowledge for prompt and renders the hits as a
// compact block within the token budget.
func (m *Module) ragBlock(ctx context.Context, dir, prompt string) (api.AgentContextBlock, bool) {
	prompt = strings.TrimSpace(prompt)
	if prompt == "" {
		return api.AgentContextBlock{}, false
	}
	k := max(1, min(setting(m, SettingRagK, DefaultRagK), 50))
	budget := max(100, setting(m, SettingRagBudget, DefaultRagBudget))
	sctx, cancel := context.WithTimeout(ctx, ragSearchTimeout)
	res, err := m.search(sctx, api.KnowledgeSearchParams{Query: prompt, K: k})
	cancel()
	if err != nil {
		res, err = m.search(ctx, api.KnowledgeSearchParams{Query: prompt, K: k, KeywordOnly: true})
		if err != nil {
			return api.AgentContextBlock{}, false
		}
	}
	man, _ := kn.LoadManifest(dir)
	root := ""
	if app := m.application(); app != nil {
		root = app.ProjectRoot()
	}
	return renderRAG(res, man, root, budget)
}

// sourceLinkRE matches the labels of code links ("[src/A.php:12](…)").
var sourceLinkRE = regexp.MustCompile(`\[((?:\\.|[^\]\\\s])+:\d+)\]\(`)

// renderRAG renders the context block of search hits: a header, then per
// hit the page title › heading, its sources (file:line), when it was
// generated, a "possibly outdated" marker when a source changed since,
// and the snippet – at most budget tokens.
func renderRAG(res api.KnowledgeSearchResult, man *kn.Manifest, root string, budget int) (api.AgentContextBlock, bool) {
	var hits []api.KnowledgeHit
	seen := map[string]bool{}
	for _, h := range res.Hits {
		key := h.Path + "#" + h.Anchor + "#" + h.Heading
		if seen[key] {
			continue
		}
		seen[key] = true
		hits = append(hits, h)
	}
	if len(hits) == 0 {
		return api.AgentContextBlock{}, false
	}
	limit := budget * 4
	var b strings.Builder
	header := fmt.Sprintf("## Context from the project knowledge\nGenerated summaries matching this message (%s search). They may be outdated or wrong – verify against the code and cite file:line; the code wins.\n", searchMode(res.Mode))
	b.WriteString(header)
	share := (limit - len(header)) / len(hits)
	changed := newChangeCheck(root)
	block := api.AgentContextBlock{Source: ID, Title: "Context from project knowledge", Mode: res.Mode, Note: res.Note}
	for _, h := range hits {
		hit := api.AgentContextHit{Unit: h.Unit, Path: h.Path, File: h.File, Anchor: h.Anchor, Score: h.Score, Title: h.Unit}
		var st *kn.ItemState
		if man != nil {
			st = man.Items[h.Unit]
		}
		if st != nil {
			hit.Title = st.Title
			hit.GeneratedAt = st.GeneratedAt
			hit.Changed = changed.files(st)
			hit.Outdated = len(hit.Changed) > 0
		}
		if hit.Title == "" {
			hit.Title = strings.TrimSuffix(filepath.Base(h.Path), ".md")
		}
		hit.Heading = trimHeading(h.Heading, hit.Title)
		hit.Sources = hitSources(h)
		meta := hitMeta(len(block.Hits)+1, hit)
		left := limit - b.Len() - len(meta) - 2
		if left < 80 {
			break
		}
		hit.Snippet = fitSnippet(h.Text, min(left, max(share-len(meta), 160)))
		if hit.Snippet == "" {
			continue
		}
		b.WriteString("\n" + meta + hit.Snippet + "\n")
		block.Hits = append(block.Hits, hit)
	}
	if len(block.Hits) == 0 {
		return api.AgentContextBlock{}, false
	}
	block.Text = b.String()
	block.Tokens = kn.EstimateTokens(block.Text)
	return block, true
}

func searchMode(mode string) string {
	if mode == "" {
		return "keyword"
	}
	return mode
}

// hitMeta renders the lines above a snippet.
func hitMeta(n int, h api.AgentContextHit) string {
	var b strings.Builder
	title := h.Title
	if h.Heading != "" {
		title += " › " + h.Heading
	}
	fmt.Fprintf(&b, "### %d. %s\n", n, title)
	if len(h.Sources) > 0 {
		b.WriteString("Sources: " + strings.Join(h.Sources, ", ") + "\n")
	}
	var info []string
	if !h.GeneratedAt.IsZero() {
		info = append(info, "generated "+h.GeneratedAt.UTC().Format("2006-01-02"))
	}
	if h.Outdated {
		files := h.Changed
		more := ""
		if len(files) > 3 {
			files, more = files[:3], fmt.Sprintf(" (+%d more)", len(h.Changed)-3)
		}
		info = append(info, "POSSIBLY OUTDATED: changed since: "+strings.Join(files, ", ")+more)
	}
	if len(info) > 0 {
		b.WriteString(strings.Join(info, " · ") + "\n")
	}
	return b.String()
}

// trimHeading drops the page title from the front of a heading path.
func trimHeading(heading, title string) string {
	heading = strings.TrimSpace(heading)
	switch {
	case heading == title:
		return ""
	case strings.HasPrefix(heading, title+" › "):
		return strings.TrimPrefix(heading, title+" › ")
	}
	return heading
}

// hitSources returns the code locations a hit cites (file:line links of
// its text), else the page's source files.
func hitSources(h api.KnowledgeHit) []string {
	var out []string
	for _, mm := range sourceLinkRE.FindAllStringSubmatch(h.Text, -1) {
		s := strings.NewReplacer(`\[`, "[", `\]`, "]").Replace(mm[1])
		if !slices.Contains(out, s) {
			out = append(out, s)
		}
	}
	if len(out) == 0 {
		for _, s := range h.Sources {
			if !strings.HasPrefix(s, "@") && !slices.Contains(out, s) {
				out = append(out, s)
			}
		}
	}
	if len(out) > maxSources {
		rest := len(out) - maxSources
		out = append(out[:maxSources:maxSources], fmt.Sprintf("+%d more", rest))
	}
	return out
}

// fitSnippet returns the plain text of a section in at most n bytes.
func fitSnippet(text string, n int) string {
	if n <= 0 {
		return ""
	}
	runes := n
	for {
		s := kn.Snippet(text, runes)
		if len(s) <= n || runes <= 1 {
			if len(s) > n {
				return ""
			}
			return s
		}
		runes = runes*n/len(s) - 1
	}
}

// changeCheck tells which source files of a page changed since it was
// generated (cached per block: files are hashed once).
type changeCheck struct {
	root   string
	hashes map[string]string
}

func newChangeCheck(root string) *changeCheck {
	return &changeCheck{root: root, hashes: map[string]string{}}
}

// files returns the changed sources of st (sorted); files not modified
// after the generation are not hashed.
func (c *changeCheck) files(st *kn.ItemState) []string {
	if c.root == "" {
		return nil
	}
	var out []string
	for f, want := range st.Files {
		if strings.HasPrefix(f, "@") || want == "" {
			continue
		}
		abs := filepath.Join(c.root, filepath.FromSlash(f))
		info, err := os.Stat(abs)
		if err != nil {
			out = append(out, f)
			continue
		}
		if !st.GeneratedAt.IsZero() && info.ModTime().Before(st.GeneratedAt) {
			continue
		}
		got, ok := c.hashes[abs]
		if !ok {
			got = kn.HashFile(abs)
			c.hashes[abs] = got
		}
		if got != want {
			out = append(out, f)
		}
	}
	sort.Strings(out)
	return out
}
