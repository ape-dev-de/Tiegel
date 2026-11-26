package knowledge

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ape-dev-de/tiegel/internal/acp"
	"github.com/ape-dev-de/tiegel/internal/acp/acptest"
	"github.com/ape-dev-de/tiegel/internal/api"
	"github.com/ape-dev-de/tiegel/internal/core/bus"
	"github.com/ape-dev-de/tiegel/internal/core/module"
	kn "github.com/ape-dev-de/tiegel/internal/knowledge"
	"github.com/ape-dev-de/tiegel/internal/modules/modtest"
)

// project is a small PHP project with two namespaces.
var project = map[string]string{
	"composer.json":               `{"autoload":{"psr-4":{"App\\":"src/"}}}`,
	"src/Billing/Invoice.php":     "<?php\nnamespace App\\Billing;\nclass Invoice {\n  public function create(array $data) { return $data; }\n}\n",
	"src/Billing/Payment.php":     "<?php\nnamespace App\\Billing;\nclass Payment {\n  public function charge() {}\n}\n",
	"src/Shop/CartController.php": "<?php\nnamespace App\\Shop;\nclass CartController {\n  public function add() {}\n}\n",
	"tests/InvoiceTest.php":       "<?php\n",
	"vendor/x/y.php":              "<?php\n",
}

// unitOf returns the unit directory named in a prompt.
func unitOf(prompt string) string {
	i := strings.Index(prompt, "(directory ")
	if i < 0 {
		return ""
	}
	rest := prompt[i+len("(directory "):]
	if j := strings.IndexAny(rest, ",)"); j >= 0 {
		return rest[:j]
	}
	return rest
}

// script answers the three pipeline prompts like a real agent: it reads
// a file (allowed), tries an edit (rejected) and answers with JSON or
// Markdown.
func script(mu *sync.Mutex, perms *[]string, broken *bool) acptest.Script {
	return func(ctx context.Context, t *acptest.Turn) string {
		p := t.Prompt
		dir := unitOf(p)
		file, sym := "src/Billing/Invoice.php", "Invoice::create"
		if dir == "src/Shop" || strings.Contains(p, `Unit: App\Shop`) {
			file, sym = "src/Shop/CartController.php", "CartController::add"
		}
		switch {
		case strings.Contains(p, "You document the data flows"):
			read := t.Permission(ctx, acp.ToolCall{ToolCallID: "r1", Name: "read", Kind: acp.KindRead, Title: "read " + file})
			edit := t.Permission(ctx, acp.ToolCall{ToolCallID: "e1", Name: "edit", Kind: acp.KindEdit, Title: "edit " + file})
			shell := t.Permission(ctx, acp.ToolCall{ToolCallID: "s1", Kind: acp.KindExecute, Title: "rm -rf", RawInput: json.RawMessage(`{"command":"rm -rf src"}`)})
			mu.Lock()
			*perms = append(*perms, read, edit, shell)
			bad := *broken
			mu.Unlock()
			if bad {
				t.Text("I could not finish.")
				return ""
			}
			d := kn.Description{Summary: "Unit " + dir + " handles data.", Flows: []kn.Flow{{Name: "Main flow", Trigger: "HTTP POST",
				Steps: []kn.Step{{File: file, Symbol: sym, What: "receives and stores the data"}}, SideEffects: []string{"DB write"}}}}
			b, _ := json.Marshal(d)
			t.Text("Here is the description:\n```json\n")
			t.Text(string(b) + "\n```\n")
		case strings.Contains(p, "You review a description"):
			i := strings.Index(p, "```json\n")
			j := strings.Index(p[i+8:], "\n```")
			var d kn.Description
			_ = json.Unmarshal([]byte(p[i+8:i+8+j]), &d)
			v, _ := json.Marshal(kn.Validation{Confidence: 0.85, Issues: []string{"checked"}, Description: &d})
			t.Text("```json\n" + string(v) + "\n```")
		case strings.Contains(p, "Write the knowledge base page"):
			t.Text("## Overview\nThe unit stores invoices and payments.\n\n## Data flows\n### Main flow\n**Trigger:** HTTP POST\n1. [" + file + ":4](" + file + "#L4) `" + sym + "` – stores\n")
		default:
			t.Text("?")
		}
		return ""
	}
}

type fixture struct {
	t     *testing.T
	app   *api.App
	root  string
	fake  *acptest.Agent
	mod   *Module
	ol    *fakeOllama
	mu    sync.Mutex
	cmds  []acp.Command
	perms []string
	bad   bool
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	f := &fixture{t: t, ol: &fakeOllama{models: map[string]bool{}}}
	f.fake = &acptest.Agent{Script: script(&f.mu, &f.perms, &f.bad), Models: []string{"fake/small", "fake/large"}}
	srv := httptest.NewServer(f.ol.handler())
	t.Cleanup(srv.Close)
	f.mod = New(Options{
		LookPath:   func(name string) (string, error) { return "/fake/bin/" + name, nil },
		Executable: func() (string, error) { return "/opt/tiegel/tiegel", nil },
		Start: func(ctx context.Context, c acp.Command, h acp.Handler) (*acp.Process, error) {
			f.mu.Lock()
			f.cmds = append(f.cmds, c)
			f.mu.Unlock()
			return f.fake.Start(ctx, c, h)
		},
	})
	f.app = modtest.NewApp(t, []module.Module{f.mod}, nil)
	f.root = modtest.Project(t, f.app, project)
	f.set(SettingEndpoint, srv.URL)
	f.set(SettingEmbedModel, "emb")
	return f
}

func (f *fixture) call(method string, params, out any) {
	f.t.Helper()
	modtest.Call(f.t, f.app, method, params, out)
}

func (f *fixture) set(key string, v any) {
	f.t.Helper()
	raw, _ := json.Marshal(v)
	f.call("settings.set", api.SettingsSetParams{Key: key, Scope: "global", Value: raw}, nil)
}

// fakeOllama answers /api/embed, /api/show and /api/pull.
type fakeOllama struct {
	mu     sync.Mutex
	models map[string]bool
}

func vec(s string) []float32 {
	s = strings.ToLower(s)
	v := []float32{0.01, 0.01, 0.01}
	for i, w := range []string{"invoice", "cart", "payment"} {
		if strings.Contains(s, w) {
			v[i] = 1
		}
	}
	return v
}

func (o *fakeOllama) handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/embed", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Model string   `json:"model"`
			Input []string `json:"input"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		o.mu.Lock()
		ok := o.models[req.Model]
		o.mu.Unlock()
		if !ok {
			w.WriteHeader(404)
			fmt.Fprintf(w, `{"error":"model %q not found, try pulling it first"}`, req.Model)
			return
		}
		var out [][]float32
		for _, in := range req.Input {
			out = append(out, vec(in))
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"embeddings": out})
	})
	mux.HandleFunc("/api/show", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Model string `json:"model"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		o.mu.Lock()
		defer o.mu.Unlock()
		if !o.models[req.Model] {
			w.WriteHeader(404)
			_, _ = w.Write([]byte(`{"error":"model not found"}`))
			return
		}
		_, _ = w.Write([]byte(`{}`))
	})
	mux.HandleFunc("/api/pull", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Model string `json:"model"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		fmt.Fprintln(w, `{"status":"pulling","total":10,"completed":5}`)
		fmt.Fprintln(w, `{"status":"success"}`)
		o.mu.Lock()
		o.models[req.Model] = true
		o.mu.Unlock()
	})
	return mux
}

func TestPlanAndEstimate(t *testing.T) {
	f := newFixture(t)
	var pl api.KnowledgePlan
	f.call("knowledge.plan", api.KnowledgeScopeParams{}, &pl)
	if pl.Total != 2 || len(pl.Units) != 2 || pl.Units[0].ID != "src-billing" || pl.Units[0].Title != `App\Billing` || pl.Units[0].Stale != kn.StaleNew {
		t.Fatalf("%+v", pl)
	}
	e := pl.Estimate
	if e.Units != 2 || e.Files != 3 || e.Calls != 6 || e.InputTokens < 8000 || e.Agent != "opencode acp" || !strings.Contains(e.Note, "billed by your agent's provider") {
		t.Fatalf("%+v", e)
	}
	f.call("knowledge.plan", api.KnowledgeScopeParams{Scope: []string{"src/Shop"}}, &pl)
	if pl.Total != 1 || pl.Units[0].ID != "src-shop" {
		t.Fatalf("%+v", pl)
	}
	f.call("knowledge.plan", api.KnowledgeScopeParams{Scope: []string{filepath.Join(f.root, "tests")}}, &pl)
	if pl.Total != 1 || pl.Units[0].ID != "tests" {
		t.Fatalf("explicit excluded folder: %+v", pl)
	}
	if err := modtest.WantCode(t, f.app, "knowledge.plan", api.KnowledgeScopeParams{Scope: []string{"/elsewhere"}}, api.CodeInvalidParams); err == nil {
		t.Fatal("outside scope")
	}
	f.set(SettingModel, "fake/small")
	f.call("knowledge.plan", api.KnowledgeScopeParams{}, &pl)
	if pl.Estimate.Model != "fake/small" {
		t.Fatal(pl.Estimate)
	}
	var st api.KnowledgeStatus
	f.call("knowledge.status", nil, &st)
	if st.Exists || st.Stale != 2 || st.Units != 0 || !strings.HasSuffix(st.Dir, "knowledge") || strings.HasPrefix(st.Dir, f.root) || !st.Embedding.Local || !st.GiveAgent {
		t.Fatalf("%+v", st)
	}
	// Building requires an agent.
	f.mod.opts.LookPath = func(string) (string, error) { return "", os.ErrNotExist }
	if err := modtest.WantCode(t, f.app, "knowledge.build", api.KnowledgeBuildParams{Wait: true}, api.CodeNotFound); err == nil {
		t.Fatal("no agent")
	}
}

func TestBuildSearchAndIncremental(t *testing.T) {
	f := newFixture(t)
	f.set(SettingModel, "fake/large")
	rec := modtest.Record(t, f.app, "knowledge.*")
	// The embedding model is missing: the build works, embedding fails,
	// keyword search works.
	var run api.KnowledgeRun
	f.call("knowledge.build", api.KnowledgeBuildParams{Wait: true}, &run)
	if run.Running || run.Done != 2 || run.Failed != 0 || run.EmbedError == "" || !strings.Contains(run.EmbedError, "pull") {
		t.Fatalf("%+v", run)
	}
	// Read-only: read allowed, edit and shell rejected; opencode-style
	// inline config not set for a non-opencode command name? (opencode).
	f.mu.Lock()
	perms, cmds := append([]string(nil), f.perms...), append([]acp.Command(nil), f.cmds...)
	f.mu.Unlock()
	if perms[0] != "once" || perms[1] != "reject" || perms[2] != "reject" {
		t.Fatal(perms)
	}
	if len(cmds) != 1 || cmds[0].Path != "/fake/bin/opencode" || cmds[0].Dir != f.root || !containsEnv(cmds[0].Env, "OPENCODE_CONFIG_CONTENT="+readOnlyConfig) {
		t.Fatalf("%+v", cmds)
	}
	if f.fake.Model() != "fake/large" {
		t.Fatal("model not applied", f.fake.Model())
	}
	if len(f.fake.Prompts()) != 6 {
		t.Fatal(len(f.fake.Prompts()))
	}
	if strings.Contains(strings.Join(run.Errors, " "), "rejected") == false {
		t.Fatal("rejections not reported", run.Errors)
	}
	// Documents outside the project with front matter.
	var st api.KnowledgeStatus
	f.call("knowledge.status", nil, &st)
	if !st.Exists || st.Units != 2 || st.Stale != 0 || st.Embedding.Chunks == 0 || st.Embedding.Embedded != 0 {
		t.Fatalf("%+v", st)
	}
	var doc api.KnowledgeDoc
	f.call("knowledge.show", api.KnowledgeUnitParams{Unit: "src-billing"}, &doc)
	if !strings.HasPrefix(doc.Content, "---\nunit: src-billing\n") || !strings.Contains(doc.Content, "# App\\Billing") || !strings.Contains(doc.Content, "confidence: 0.85") || !strings.HasPrefix(doc.File, st.Dir) {
		t.Fatal(doc)
	}
	f.call("knowledge.show", api.KnowledgeUnitParams{Unit: `App\Shop`}, &doc)
	if doc.Path != "units/src-shop.md" {
		t.Fatal(doc.Path)
	}
	f.call("knowledge.read", api.KnowledgeReadParams{Path: "index.md"}, &doc)
	if !strings.Contains(doc.Content, "[App\\\\Billing](units/src-billing.md)") && !strings.Contains(doc.Content, "(units/src-billing.md)") {
		t.Fatal(doc.Content)
	}
	f.call("knowledge.read", api.KnowledgeReadParams{Path: "src-shop"}, &doc)
	if doc.Path != "units/src-shop.md" {
		t.Fatal(doc)
	}
	f.call("knowledge.show", api.KnowledgeUnitParams{}, &doc)
	if doc.Path != "index.md" {
		t.Fatal(doc.Path)
	}
	for _, bad := range []string{"../../settings.json", "../x.md", "manifest.json"} {
		if err := modtest.TryCall(f.app, "knowledge.read", api.KnowledgeReadParams{Path: bad}, &doc); err == nil {
			t.Fatal("read", bad)
		}
	}
	if err := modtest.WantCode(t, f.app, "knowledge.show", api.KnowledgeUnitParams{Unit: "nope"}, api.CodeNotFound); err == nil {
		t.Fatal("unknown unit")
	}
	var res api.KnowledgeSearchResult
	f.call("knowledge.search", api.KnowledgeSearchParams{Query: "cart controller"}, &res)
	if res.Mode != "keyword" || len(res.Hits) == 0 || res.Hits[0].Unit != "src-shop" || !strings.Contains(res.Note, "no embeddings") {
		t.Fatalf("%+v", res)
	}
	// Pull the model (explicit), then embed: hybrid search.
	var ms api.KnowledgeModelStatus
	f.call("knowledge.modelStatus", nil, &ms)
	if !ms.Reachable || ms.Available || ms.PullCommand != "ollama pull emb" {
		t.Fatalf("%+v", ms)
	}
	f.call("knowledge.pullModel", api.KnowledgePullParams{Wait: true}, &ms)
	if !ms.Available || ms.PullStatus != "success" {
		t.Fatalf("%+v", ms)
	}
	var er api.KnowledgeEmbedResult
	f.call("knowledge.embed", api.KnowledgeEmbedParams{}, &er)
	if er.Error != "" || er.Embedded == 0 || er.Pending != 0 || er.Model != "ollama:emb" {
		t.Fatalf("%+v", er)
	}
	f.call("knowledge.search", api.KnowledgeSearchParams{Query: "where are invoices created", K: 3}, &res)
	if res.Mode != "hybrid" || len(res.Hits) == 0 || res.Hits[0].Unit != "src-billing" || res.Hits[0].Vector <= 0 || res.Hits[0].Anchor == "" || !strings.HasPrefix(res.Hits[0].File, st.Dir) {
		t.Fatalf("%+v", res)
	}
	f.call("knowledge.search", api.KnowledgeSearchParams{Query: "invoice", KeywordOnly: true}, &res)
	if res.Mode != "keyword" {
		t.Fatal(res.Mode)
	}
	// Another embedding model in the settings: search falls back with a
	// note until re-embedded.
	f.set(SettingEmbedModel, "other")
	f.call("knowledge.search", api.KnowledgeSearchParams{Query: "invoice"}, &res)
	if res.Mode != "keyword" || !strings.Contains(res.Note, "knowledge embed") {
		t.Fatalf("%+v", res)
	}
	f.set(SettingEmbedModel, "emb")
	if err := modtest.WantCode(t, f.app, "knowledge.search", api.KnowledgeSearchParams{}, api.CodeInvalidParams); err == nil {
		t.Fatal("empty query")
	}
	// Incremental: nothing to do; a changed file makes its unit stale.
	var pl api.KnowledgePlan
	f.call("knowledge.plan", api.KnowledgeScopeParams{}, &pl)
	if len(pl.Units) != 0 || pl.Total != 2 {
		t.Fatalf("%+v", pl)
	}
	modtest.WriteFile(t, filepath.Join(f.root, "src/Shop/CartController.php"), "<?php\nclass CartController { public function add() { /* changed */ } }\n")
	f.mod.invalidate()
	f.call("knowledge.plan", api.KnowledgeScopeParams{}, &pl)
	if len(pl.Units) != 1 || pl.Units[0].ID != "src-shop" || pl.Units[0].Stale != kn.StaleChanged {
		t.Fatalf("%+v", pl)
	}
	var units []api.KnowledgeUnit
	f.call("knowledge.list", nil, &units)
	if len(units) != 2 || units[1].Stale != kn.StaleChanged || units[0].MD == "" || units[0].Confidence != 0.85 {
		t.Fatalf("%+v", units)
	}
	n := len(f.fake.Prompts())
	f.call("knowledge.build", api.KnowledgeBuildParams{Wait: true}, &run)
	if run.Done != 1 || len(f.fake.Prompts()) != n+3 || run.EmbedError != "" {
		t.Fatalf("%+v", run)
	}
	// --all rebuilds everything; a unit list builds exactly those.
	f.call("knowledge.plan", api.KnowledgeScopeParams{All: true}, &pl)
	if len(pl.Units) != 2 {
		t.Fatal(pl)
	}
	f.call("knowledge.build", api.KnowledgeBuildParams{Units: []string{"src-billing"}, Wait: true, NoEmbed: true}, &run)
	if run.Done != 1 {
		t.Fatal(run)
	}
	// Failure keeps the old document and marks the unit.
	f.mu.Lock()
	f.bad = true
	f.mu.Unlock()
	f.call("knowledge.build", api.KnowledgeBuildParams{Units: []string{"src-shop"}, Wait: true}, &run)
	if run.Failed != 1 || !strings.Contains(strings.Join(run.Errors, " "), "describe") {
		t.Fatalf("%+v", run)
	}
	f.call("knowledge.status", nil, &st)
	if st.Failed != 1 || st.Stale != 1 || st.Units != 2 {
		t.Fatalf("%+v", st)
	}
	// Progress events were published.
	rec.Wait(t, api.TopicKnowledgeProgress, func(e bus.Event) bool {
		p, ok := e.Payload.(api.KnowledgeProgress)
		return ok && p.State == kn.RunDone && p.Step == kn.StepWrite
	})
}

func containsEnv(env []string, kv string) bool {
	for _, e := range env {
		if e == kv {
			return true
		}
	}
	return false
}

func TestAgentGetsKnowledge(t *testing.T) {
	f := newFixture(t)
	servers := func() []api.AgentMCPServer {
		l, err := api.HookAgentMCPServers.Apply(context.Background(), f.app.Hooks(), nil)
		if err != nil {
			t.Fatal(err)
		}
		return l
	}
	if len(servers()) != 0 {
		t.Fatal("server without knowledge")
	}
	var run api.KnowledgeRun
	f.call("knowledge.build", api.KnowledgeBuildParams{Wait: true, NoEmbed: true}, &run)
	l := servers()
	if len(l) != 1 || l[0].Name != "tiegel-knowledge" || l[0].Command != "/opt/tiegel/tiegel" ||
		strings.Join(l[0].Args[:5], " ") != "--headless mcp knowledge --project "+f.root || l[0].Args[5] != "--config-dir" {
		t.Fatalf("%+v", l)
	}
	f.set(SettingGiveAgent, false)
	if len(servers()) != 0 {
		t.Fatal("setting off")
	}
	var mc api.KnowledgeMCPConfig
	f.call("knowledge.mcpConfig", nil, &mc)
	if mc.Command != "/opt/tiegel/tiegel" || !strings.Contains(mc.Opencode, `"type": "local"`) || !strings.Contains(mc.Claude, `"mcpServers"`) {
		t.Fatalf("%+v", mc)
	}
	// Search chunks documents built without embedding on demand.
	var res api.KnowledgeSearchResult
	f.call("knowledge.search", api.KnowledgeSearchParams{Query: "invoices"}, &res)
	if len(res.Hits) == 0 {
		t.Fatal("no hits")
	}
}

func TestCancelAndConflict(t *testing.T) {
	f := newFixture(t)
	started := make(chan struct{}, 4)
	f.fake.Script = func(ctx context.Context, tt *acptest.Turn) string {
		started <- struct{}{}
		<-ctx.Done()
		return acp.StopCancelled
	}
	var run api.KnowledgeRun
	f.call("knowledge.build", api.KnowledgeBuildParams{}, &run)
	if !run.Running || len(run.Units) != 2 {
		t.Fatalf("%+v", run)
	}
	select {
	case <-started:
	case <-time.After(10 * time.Second):
		t.Fatal("agent not prompted")
	}
	if err := modtest.WantCode(t, f.app, "knowledge.build", api.KnowledgeBuildParams{}, api.CodeConflict); err == nil {
		t.Fatal("second build")
	}
	var st api.KnowledgeStatus
	f.call("knowledge.status", nil, &st)
	if !st.Run.Running || st.Run.Phase != "agents" {
		t.Fatalf("%+v", st.Run)
	}
	f.call("knowledge.cancel", nil, &run)
	if run.Running || !run.Cancelled || run.Done != 0 {
		t.Fatalf("%+v", run)
	}
	var pl api.KnowledgePlan
	f.call("knowledge.plan", api.KnowledgeScopeParams{}, &pl)
	if len(pl.Units) != 2 {
		t.Fatal("cancelled units must stay stale")
	}
	// The agent cannot start.
	f.mod.opts.Start = func(context.Context, acp.Command, acp.Handler) (*acp.Process, error) {
		return nil, fmt.Errorf("exec failed")
	}
	f.call("knowledge.build", api.KnowledgeBuildParams{Wait: true}, &run)
	if !strings.Contains(strings.Join(run.Errors, " "), "exec failed") {
		t.Fatal(run.Errors)
	}
}

func TestModelStatusUnreachableAndOpenAI(t *testing.T) {
	f := newFixture(t)
	f.set(SettingEndpoint, "http://127.0.0.1:1")
	var ms api.KnowledgeModelStatus
	f.call("knowledge.modelStatus", nil, &ms)
	if ms.Reachable || !strings.Contains(ms.Error, "not reachable") {
		t.Fatalf("%+v", ms)
	}
	f.set(SettingProvider, kn.ProviderOpenAI)
	f.set(SettingEndpoint, "https://api.example.com/v1")
	f.call("knowledge.modelStatus", nil, &ms)
	if ms.PullCommand != "" || !ms.Available {
		t.Fatalf("%+v", ms)
	}
	if err := modtest.WantCode(t, f.app, "knowledge.pullModel", api.KnowledgePullParams{}, api.CodeInvalidParams); err == nil {
		t.Fatal("pull on openai")
	}
	var st api.KnowledgeStatus
	f.call("knowledge.status", nil, &st)
	if st.Embedding.Local || st.Embedding.Provider != kn.ProviderOpenAI {
		t.Fatalf("%+v", st.Embedding)
	}
	t.Setenv(APIKeyEnv, "from-env")
	if cfg := f.mod.embedConfig(""); cfg.APIKey != "from-env" {
		t.Fatal(cfg.APIKey)
	}
	f.set(SettingProvider, kn.ProviderOllama)
	if cfg := f.mod.embedConfig("x"); cfg.Provider != kn.ProviderOllama || cfg.APIKey != "" || cfg.Model != "x" {
		t.Fatalf("%+v", cfg)
	}
	if !localEndpoint("http://[::1]:11434") || localEndpoint("https://x.example") || localEndpoint("%%") {
		t.Fatal("localEndpoint")
	}
}

func TestReadOnlyHandler(t *testing.T) {
	root := t.TempDir()
	modtest.WriteFile(t, filepath.Join(root, "a.txt"), "1\n2\n3\n")
	h := acpHandler{a: &acpAgent{root: root, sinks: map[string]*strings.Builder{}}}
	line, limit := 2, 1
	r, err := h.ReadTextFile(context.Background(), acp.ReadTextFileRequest{Path: "a.txt", Line: &line, Limit: &limit})
	if err != nil || r.Content != "2\n" {
		t.Fatal(r, err)
	}
	if _, err := h.ReadTextFile(context.Background(), acp.ReadTextFileRequest{Path: "/etc/hosts"}); err == nil {
		t.Fatal("outside")
	}
	if _, err := h.ReadTextFile(context.Background(), acp.ReadTextFileRequest{Path: "gone.txt"}); err == nil {
		t.Fatal("missing")
	}
	if err := h.WriteTextFile(context.Background(), acp.WriteTextFileRequest{Path: "a.txt"}); err == nil {
		t.Fatal("write allowed")
	}
	res, _ := h.RequestPermission(context.Background(), acp.RequestPermissionRequest{ToolCall: acp.ToolCall{Name: "grep"}})
	if res.Outcome.Outcome != acp.OutcomeCancelled {
		t.Fatal("no options must cancel")
	}
	for tc, want := range map[*acp.ToolCall]bool{
		{Kind: acp.KindSearch}: true,
		{Name: "glob"}:         true,
		{Name: "write"}:        false,
		{Kind: acp.KindFetch}:  false,
		{Kind: acp.KindRead, Content: []acp.ToolCallContent{{Type: "diff"}}}: false,
	} {
		if readOnlyTool(*tc) != want {
			t.Fatal(tc)
		}
	}
	if n := h.a.Notes(); len(n) != 1 {
		t.Fatal(n)
	}
}
