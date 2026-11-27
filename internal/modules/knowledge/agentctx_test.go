package knowledge

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ape-dev-de/tiegel/internal/api"
	"github.com/ape-dev-de/tiegel/internal/core/settings"
	kn "github.com/ape-dev-de/tiegel/internal/knowledge"
	"github.com/ape-dev-de/tiegel/internal/modules/modtest"
)

// fixturePages writes a small knowledge base without building it: an
// invoice process page (current) and a cart command page whose source
// changes afterwards (outdated).
func fixturePages(t *testing.T, f *fixture) string {
	t.Helper()
	dir, err := f.mod.dir()
	if err != nil {
		t.Fatal(err)
	}
	gen := time.Now().Add(-time.Hour).UTC().Truncate(time.Second)
	lk := kn.Linker{Root: f.root}
	invoice := "---\nid: process-invoice\ntitle: Create invoice\n---\n# Create invoice\n\n## Data flow\n\nThe invoice data of POST /invoices goes from " + lk.Link("src/Controller/InvoiceController.php", 5) +
		" to the invoice service " + lk.Link("src/Billing/InvoiceService.php", 4) + " which saves the invoice rows.\n\n## Errors\n\nInvalid invoice data is rejected by the invoice validation.\n"
	cart := "---\nid: process-cart\ntitle: Clear cart\n---\n# Clear cart\n\n## Command\n\nThe cart:clear command empties the cart of every invoice customer in " + lk.Link("src/Command/CartCommand.php", 5) + ".\n"
	modtest.WriteFile(t, filepath.Join(dir, "index.md"), "# Knowledge\n")
	modtest.WriteFile(t, filepath.Join(dir, "processes", "invoice.md"), invoice)
	modtest.WriteFile(t, filepath.Join(dir, "processes", "cart.md"), cart)
	hash := func(rel string) string { return kn.HashFile(filepath.Join(f.root, rel)) }
	man := &kn.Manifest{Items: map[string]*kn.ItemState{
		"process-invoice": {ID: "process-invoice", Level: kn.LevelProcesses, Title: "Create invoice", Status: kn.StatusDone, GeneratedAt: gen, MD: "processes/invoice.md",
			Files: map[string]string{"src/Controller/InvoiceController.php": hash("src/Controller/InvoiceController.php"), "src/Billing/InvoiceService.php": hash("src/Billing/InvoiceService.php"), "@facts": "x"}},
		"process-cart": {ID: "process-cart", Level: kn.LevelProcesses, Title: "Clear cart", Status: kn.StatusDone, GeneratedAt: gen, MD: "processes/cart.md",
			Files: map[string]string{"src/Command/CartCommand.php": hash("src/Command/CartCommand.php")}},
	}}
	if err := man.Save(dir); err != nil {
		t.Fatal(err)
	}
	// The cart command changes after the page was generated; the invoice
	// sources are older than the page (not even hashed).
	modtest.WriteFile(t, filepath.Join(f.root, "src/Command/CartCommand.php"), "<?php\n// changed\n")
	old := gen.Add(-time.Hour)
	for _, rel := range []string{"src/Controller/InvoiceController.php", "src/Billing/InvoiceService.php"} {
		_ = os.Chtimes(filepath.Join(f.root, rel), old, old)
	}
	return dir
}

func (f *fixture) promptCtx(ctx context.Context, prompt string, first bool) api.AgentPromptContext {
	f.t.Helper()
	pc, err := api.HookAgentPromptContext.Apply(ctx, f.app.Hooks(), api.AgentPromptContext{Prompt: prompt, First: first})
	if err != nil {
		f.t.Fatal(err)
	}
	return pc
}

func (f *fixture) servers(ctx context.Context) []api.AgentMCPServer {
	f.t.Helper()
	l, err := api.HookAgentMCPServers.Apply(ctx, f.app.Hooks(), nil)
	if err != nil {
		f.t.Fatal(err)
	}
	return l
}

// knowledge.giveAgent becomes knowledge.agentMode: true → mcp, false →
// off, in the store that had it.
func TestAgentModeMigration(t *testing.T) {
	f := newFixture(t)
	if err := settings.Set(f.app.Settings(), legacyGiveAgent, false); err != nil {
		t.Fatal(err)
	}
	ps := f.app.ProjectSettings()
	if err := settings.Set(ps, legacyGiveAgent, true); err != nil {
		t.Fatal(err)
	}
	var st api.KnowledgeStatus
	f.call("knowledge.status", nil, &st)
	if st.AgentMode != api.AgentContextMCP || !st.GiveAgent {
		t.Fatalf("%+v", st)
	}
	if f.app.Settings().Has(legacyGiveAgent) || ps.Has(legacyGiveAgent) {
		t.Fatal("old key kept")
	}
	if raw, _ := f.app.Settings().Raw(SettingAgentMode); string(raw) != `"off"` {
		t.Fatal(string(raw))
	}
	if raw, _ := ps.Raw(SettingAgentMode); string(raw) != `"mcp"` {
		t.Fatal(string(raw))
	}
	// An explicit mode is kept; the old key is dropped.
	_ = settings.Set(ps, legacyGiveAgent, false)
	f.call("knowledge.status", nil, &st)
	if st.AgentMode != api.AgentContextMCP || ps.Has(legacyGiveAgent) {
		t.Fatalf("%+v", st)
	}
	_ = ps.Delete(SettingAgentMode)
	f.call("knowledge.status", nil, &st)
	if st.AgentMode != api.AgentContextOff || st.GiveAgent {
		t.Fatalf("%+v", st)
	}
	// Default: mcp.
	_ = f.app.Settings().Delete(SettingAgentMode)
	f.call("knowledge.status", nil, &st)
	if st.AgentMode != api.AgentContextMCP {
		t.Fatalf("%+v", st)
	}
}

func TestAgentModesAndRAG(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	// No knowledge base: nothing in any mode.
	f.set(SettingAgentMode, api.AgentContextBoth)
	if pc := f.promptCtx(ctx, "invoice", true); len(pc.Instructions)+len(pc.Blocks) != 0 || len(f.servers(ctx)) != 0 {
		t.Fatalf("%+v", pc)
	}
	fixturePages(t, f)

	// mcp (default): the read-only server and the instruction once.
	f.set(SettingAgentMode, api.AgentContextMCP)
	l := f.servers(ctx)
	if len(l) != 1 || l[0].Name != MCPServerName || !l[0].ReadOnly {
		t.Fatalf("%+v", l)
	}
	pc := f.promptCtx(ctx, "invoice", true)
	if len(pc.Blocks) != 0 || len(pc.Instructions) != 1 || !strings.Contains(pc.Instructions[0], "generated summary and may be outdated") ||
		!strings.Contains(pc.Instructions[0], "file:line") || !strings.Contains(pc.Instructions[0], "the code wins") {
		t.Fatalf("%+v", pc)
	}
	if pc := f.promptCtx(ctx, "invoice", false); len(pc.Instructions)+len(pc.Blocks) != 0 {
		t.Fatalf("%+v", pc)
	}

	// rag: the block with every message, no MCP server.
	f.set(SettingAgentMode, api.AgentContextRAG)
	if len(f.servers(ctx)) != 0 {
		t.Fatal("server in rag mode")
	}
	pc = f.promptCtx(ctx, "invoice cart", false)
	if len(pc.Instructions) != 0 || len(pc.Blocks) != 1 {
		t.Fatalf("%+v", pc)
	}
	b := pc.Blocks[0]
	if b.Source != ID || b.Mode != "keyword" || len(b.Hits) < 2 || b.Tokens > DefaultRagBudget || b.Tokens != kn.EstimateTokens(b.Text) {
		t.Fatalf("%+v", b)
	}
	t.Log(b.Text)
	if !strings.HasPrefix(b.Text, "## Context from the project knowledge\n") || !strings.Contains(b.Text, "verify against the code") {
		t.Fatal(b.Text)
	}
	var inv, cart *api.AgentContextHit
	for i := range b.Hits {
		h := &b.Hits[i]
		switch h.Unit {
		case "process-invoice":
			if inv == nil {
				inv = h
			}
		case "process-cart":
			cart = h
		}
	}
	if inv == nil || cart == nil {
		t.Fatalf("%+v", b.Hits)
	}
	if inv.Title != "Create invoice" || inv.Outdated || inv.GeneratedAt.IsZero() || !strings.HasSuffix(inv.File, filepath.Join("processes", "invoice.md")) || inv.Anchor == "" {
		t.Fatalf("%+v", inv)
	}
	if strings.Join(inv.Sources, ",") != "src/Controller/InvoiceController.php:5,src/Billing/InvoiceService.php:4" && !strings.Contains(inv.Heading, "Errors") {
		t.Fatalf("%+v", inv)
	}
	if !cart.Outdated || strings.Join(cart.Changed, ",") != "src/Command/CartCommand.php" || cart.Heading != "Command" {
		t.Fatalf("%+v", cart)
	}
	for _, want := range []string{"### 1. ", "Create invoice › Data flow", "Sources: src/Controller/InvoiceController.php:5, src/Billing/InvoiceService.php:4",
		"generated " + inv.GeneratedAt.Format("2006-01-02"), "Clear cart › Command", "POSSIBLY OUTDATED: changed since: src/Command/CartCommand.php", "The invoice data of POST /invoices"} {
		if !strings.Contains(b.Text, want) {
			t.Errorf("missing %q in\n%s", want, b.Text)
		}
	}
	if strings.Contains(b.Text, "file://") {
		t.Fatal("link targets in the block")
	}

	// Budget and k bound the block.
	f.set(SettingRagBudget, 200)
	pc = f.promptCtx(ctx, "invoice cart", false)
	if len(pc.Blocks) != 1 || pc.Blocks[0].Tokens > 200 {
		t.Fatalf("%d tokens\n%s", pc.Blocks[0].Tokens, pc.Blocks[0].Text)
	}
	f.set(SettingRagBudget, DefaultRagBudget)
	f.set(SettingRagK, 1)
	if pc = f.promptCtx(ctx, "invoice cart", false); len(pc.Blocks[0].Hits) != 1 {
		t.Fatalf("%+v", pc.Blocks[0].Hits)
	}
	// Nothing found: no block.
	if pc = f.promptCtx(ctx, "zzzqqq", false); len(pc.Blocks) != 0 {
		t.Fatalf("%+v", pc)
	}

	// both: block, instruction with the first message and the server.
	f.set(SettingAgentMode, api.AgentContextBoth)
	pc = f.promptCtx(ctx, "invoice", true)
	if len(pc.Blocks) != 1 || len(pc.Instructions) != 1 || len(f.servers(ctx)) != 1 {
		t.Fatalf("%+v", pc)
	}

	// off: nothing; an evaluation's mode in the context wins.
	f.set(SettingAgentMode, api.AgentContextOff)
	if pc = f.promptCtx(ctx, "invoice", true); len(pc.Blocks)+len(pc.Instructions) != 0 || len(f.servers(ctx)) != 0 {
		t.Fatalf("%+v", pc)
	}
	ectx := api.WithAgentContextMode(ctx, api.AgentContextRAG)
	if pc = f.promptCtx(ectx, "invoice", true); len(pc.Blocks) != 1 || len(pc.Instructions) != 1 || len(f.servers(ectx)) != 0 {
		t.Fatalf("%+v", pc)
	}
	if l := f.servers(api.WithAgentContextMode(ctx, api.AgentContextMCP)); len(l) != 1 {
		t.Fatalf("%+v", l)
	}
	var st api.KnowledgeStatus
	f.call("knowledge.status", nil, &st)
	if st.AgentMode != api.AgentContextOff || st.GiveAgent {
		t.Fatalf("%+v", st)
	}
}

// The instruction comes from agent.tmpl, which the user may override.
func TestInstructionOverride(t *testing.T) {
	f := newFixture(t)
	fixturePages(t, f)
	dir := f.mod.promptDir()
	if dir == "" {
		t.Skip("no config dir")
	}
	modtest.WriteFile(t, filepath.Join(dir, "agent.tmpl"), "CUSTOM RULES for {{.Project}}")
	pc := f.promptCtx(context.Background(), "invoice", true)
	if len(pc.Instructions) != 1 || pc.Instructions[0] != "CUSTOM RULES for "+filepath.Base(f.root) {
		t.Fatalf("%+v", pc.Instructions)
	}
	modtest.WriteFile(t, filepath.Join(dir, "agent.tmpl"), "{{broken")
	pc = f.promptCtx(context.Background(), "invoice", true)
	if len(pc.Instructions) != 1 || !strings.Contains(pc.Instructions[0], "the code wins") {
		t.Fatalf("%+v", pc.Instructions)
	}
}

func TestRenderRAGHelpers(t *testing.T) {
	if trimHeading("Page › A › B", "Page") != "A › B" || trimHeading("Page", "Page") != "" || trimHeading("Other", "Page") != "Other" {
		t.Fatal("trimHeading")
	}
	h := api.KnowledgeHit{Text: "see [a.php:1](file:///x#L1), [a.php:1](file:///x#L1) and [b\\[x\\].php:2](file:///y)", Sources: []string{"c.php"}}
	if got := strings.Join(hitSources(h), ","); got != "a.php:1,b[x].php:2" {
		t.Fatal(got)
	}
	h = api.KnowledgeHit{Sources: []string{"@facts", "a", "b", "c", "d", "e", "f", "g", "h"}}
	if got := hitSources(h); len(got) != maxSources+1 || got[maxSources] != "+2 more" {
		t.Fatal(got)
	}
	long := strings.Repeat("ünïcödé text ", 200)
	for _, n := range []int{0, 1, 10, 100, 1000} {
		if s := fitSnippet(long, n); len(s) > n {
			t.Fatalf("%d: %d", n, len(s))
		}
	}
	if _, ok := renderRAG(api.KnowledgeSearchResult{}, nil, "", 100); ok {
		t.Fatal("block without hits")
	}
	res := api.KnowledgeSearchResult{Mode: "hybrid", Hits: []api.KnowledgeHit{
		{Unit: "u", Path: "processes/u.md", Heading: "U › A", Anchor: "a", Text: long},
		{Unit: "u", Path: "processes/u.md", Heading: "U › A", Anchor: "a", Text: long},
		{Unit: "v", Path: "processes/v.md", Heading: "B", Anchor: "b", Text: long},
	}}
	b, ok := renderRAG(res, nil, "", 300)
	if !ok || len(b.Hits) != 2 || b.Tokens > 300 || b.Hits[0].Title != "u" || !strings.Contains(b.Text, "hybrid search") {
		t.Fatalf("%+v", b)
	}
	// Changed files of a page: missing files count, inputs do not.
	root := t.TempDir()
	modtest.WriteFile(t, filepath.Join(root, "a.php"), "a")
	c := newChangeCheck(root)
	st := &kn.ItemState{Files: map[string]string{"a.php": kn.HashFile(filepath.Join(root, "a.php")), "gone.php": "x", "@in": "y"}}
	if got := c.files(st); strings.Join(got, ",") != "gone.php" {
		t.Fatal(got)
	}
	st.Files["a.php"] = "other"
	if got := c.files(st); strings.Join(got, ",") != "a.php,gone.php" {
		t.Fatal(got)
	}
	if newChangeCheck("").files(st) != nil {
		t.Fatal("no root")
	}
}
