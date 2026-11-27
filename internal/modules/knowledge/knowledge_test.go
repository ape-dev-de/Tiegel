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
	"github.com/ape-dev-de/tiegel/internal/knowledge/knowledgetest"
	"github.com/ape-dev-de/tiegel/internal/modules/modtest"
)

// project is a small Symfony project: an invoice controller and a cart
// command that both use the InvoiceService.
var project = map[string]string{
	"composer.json":                        `{"require":{"symfony/framework-bundle":"^7"},"autoload":{"psr-4":{"App\\":"src/"}}}`,
	"src/Controller/InvoiceController.php": "<?php\nnamespace App\\Controller;\nclass InvoiceController {\n  #[Route('/invoices', methods: ['POST'])]\n  public function create(array $data) { return $data; }\n}\n",
	"src/Command/CartCommand.php":          "<?php\nnamespace App\\Command;\n#[AsCommand(name: 'cart:clear')]\nclass CartCommand {\n  public function execute() {}\n}\n",
	"src/Billing/InvoiceService.php":       "<?php\nnamespace App\\Billing;\nclass InvoiceService {\n  public function save(array $data) {}\n}\n",
	"tests/InvoiceTest.php":                "<?php\n",
	"vendor/x/y.php":                       "<?php\n",
}

// script answers like a real agent: on the first describe prompt it
// reads a file (allowed), tries an edit and a shell command (rejected),
// then answers with the fake's JSON.
func script(f *fixture) acptest.Script {
	return func(ctx context.Context, t *acptest.Turn) string {
		_, step, _ := knowledgetest.Level(t.Prompt)
		if step == kn.StepDescribe {
			// Live activity: a thought, a read tool call and its update.
			t.Thought("Looking at the controller first.\nThen the service.")
			t.Tool(false, acp.ToolCall{ToolCallID: "t1", Title: "read src/Billing/InvoiceService.php", Kind: acp.KindRead, Status: "pending"})
			t.Tool(true, acp.ToolCall{ToolCallID: "t1", Status: "completed"})
		}
		if step == kn.StepDescribe {
			f.mu.Lock()
			first := len(f.perms) == 0
			f.mu.Unlock()
			if first {
				read := t.Permission(ctx, acp.ToolCall{ToolCallID: "r1", Name: "read", Kind: acp.KindRead, Title: "read src/Billing/InvoiceService.php"})
				edit := t.Permission(ctx, acp.ToolCall{ToolCallID: "e1", Name: "edit", Kind: acp.KindEdit, Title: "edit"})
				shell := t.Permission(ctx, acp.ToolCall{ToolCallID: "s1", Kind: acp.KindExecute, Title: "rm -rf", RawInput: json.RawMessage(`{"command":"rm -rf src"}`)})
				f.mu.Lock()
				f.perms = append(f.perms, read, edit, shell)
				f.mu.Unlock()
			}
		}
		t.Text(f.kf.Answer(ctx, t.Prompt))
		return ""
	}
}

type fixture struct {
	t     *testing.T
	app   *api.App
	root  string
	fake  *acptest.Agent
	kf    *knowledgetest.Fake
	mod   *Module
	ol    *fakeOllama
	mu    sync.Mutex
	cmds  []acp.Command
	perms []string
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	f := &fixture{t: t, ol: &fakeOllama{models: map[string]bool{}}}
	f.kf = &knowledgetest.Fake{Service: kn.Ref{File: "src/Billing/InvoiceService.php", Symbol: `App\Billing\InvoiceService::save`}}
	f.fake = &acptest.Agent{Script: script(f), Models: []string{"fake/small", "fake/large"}}
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

func levelEstimate(pl api.KnowledgePlan, level string) api.KnowledgeLevelEstimate {
	for _, l := range pl.Estimate.Levels {
		if l.Level == level {
			return l
		}
	}
	return api.KnowledgeLevelEstimate{}
}

func TestPlanAndEstimate(t *testing.T) {
	f := newFixture(t)
	var pl api.KnowledgePlan
	f.call("knowledge.plan", api.KnowledgeScopeParams{}, &pl)
	// Overview 1, entry points 2 (http, cli), processes 2; components and
	// questions are planned when the run gets there.
	ids := []string{}
	for _, u := range pl.Units {
		ids = append(ids, u.ID)
	}
	if strings.Join(ids, ",") != "overview,entrypoints-http,entrypoints-cli,process-app-command-cartcommand,process-app-controller-invoicecontroller" || pl.Units[0].Level != kn.LevelOverview || pl.Units[0].Stale != kn.StaleNew {
		t.Fatalf("%+v", ids)
	}
	e := pl.Estimate
	if len(e.Levels) != 5 || e.Agent != "opencode acp" || !strings.Contains(e.Note, "billed by your agent's provider") || !strings.Contains(e.Note, "upper bound") {
		t.Fatalf("%+v", e)
	}
	if c := levelEstimate(pl, kn.LevelComponents); !c.Later || c.Pages == 0 || c.Calls != 2*c.Pages {
		t.Fatalf("%+v", c)
	}
	if p := levelEstimate(pl, kn.LevelProcesses); p.Pages != 2 || p.Calls != 4 || p.InputTokens < 8000 || p.Later {
		t.Fatalf("%+v", p)
	}
	if e.Units != 1+2+2+levelEstimate(pl, kn.LevelComponents).Pages+levelEstimate(pl, kn.LevelQuestions).Pages {
		t.Fatal(e.Units)
	}
	// A single level.
	f.call("knowledge.plan", api.KnowledgeScopeParams{Level: "overview"}, &pl)
	if len(pl.Units) != 1 || len(pl.Estimate.Levels) != 1 || pl.Estimate.Calls != 2 {
		t.Fatalf("%+v", pl)
	}
	f.call("knowledge.plan", api.KnowledgeScopeParams{Level: "components"}, &pl)
	if pl.Estimate.Units != 0 || pl.Estimate.Levels[0].Later {
		t.Fatalf("components without process pages: %+v", pl.Estimate)
	}
	if err := modtest.WantCode(t, f.app, "knowledge.plan", api.KnowledgeScopeParams{Level: "nope"}, api.CodeInvalidParams); err == nil {
		t.Fatal("unknown level")
	}
	// Scope narrows the analysed files; explicit excluded folders count.
	f.call("knowledge.plan", api.KnowledgeScopeParams{Scope: []string{"src/Command"}, Level: "processes"}, &pl)
	if len(pl.Units) != 1 || pl.Units[0].ID != "process-app-command-cartcommand" {
		t.Fatalf("%+v", pl.Units)
	}
	if err := modtest.WantCode(t, f.app, "knowledge.plan", api.KnowledgeScopeParams{Scope: []string{"/elsewhere"}}, api.CodeInvalidParams); err == nil {
		t.Fatal("outside scope")
	}
	f.set(SettingModel, "fake/small")
	f.call("knowledge.plan", api.KnowledgeScopeParams{}, &pl)
	if pl.Estimate.Model != "fake/small" {
		t.Fatal(pl.Estimate)
	}
	f.set(SettingMaxProcesses, 1)
	f.mod.invalidate()
	f.call("knowledge.plan", api.KnowledgeScopeParams{Level: "processes"}, &pl)
	if l := pl.Estimate.Levels[0]; l.Pages != 1 || l.Skipped != 1 {
		t.Fatalf("%+v", l)
	}
	var st api.KnowledgeStatus
	f.call("knowledge.status", nil, &st)
	if st.Exists || st.Units != 0 || st.Stale == 0 || len(st.Levels) != 4 || st.Levels[1].Planned != 2 || !strings.HasSuffix(st.Dir, "knowledge") || strings.HasPrefix(st.Dir, f.root) || !st.Embedding.Local || !st.GiveAgent {
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
	// overview, 2 entry point pages, 2 processes, 1 component, 1 batch.
	if run.Running || run.Done != 7 || run.Failed != 0 || run.Answered != 2 || run.EmbedError == "" || !strings.Contains(run.EmbedError, "pull") || len(run.Units) != 7 {
		t.Fatalf("%+v", run)
	}
	f.mu.Lock()
	perms, cmds := append([]string(nil), f.perms...), append([]acp.Command(nil), f.cmds...)
	f.mu.Unlock()
	if perms[0] != "once" || perms[1] != "reject" || perms[2] != "reject" {
		t.Fatal(perms)
	}
	if len(cmds) != 1 || cmds[0].Path != "/fake/bin/opencode" || cmds[0].Dir != f.root || !containsEnv(cmds[0].Env, "OPENCODE_CONFIG_CONTENT="+readOnlyConfig) {
		t.Fatalf("%+v", cmds)
	}
	if f.fake.Model() != "fake/large" || len(f.fake.Prompts()) != 14 {
		t.Fatal(f.fake.Model(), len(f.fake.Prompts()))
	}
	if !strings.Contains(strings.Join(run.Errors, " "), "rejected") {
		t.Fatal("rejections not reported", run.Errors)
	}
	var st api.KnowledgeStatus
	f.call("knowledge.status", nil, &st)
	if !st.Exists || st.Units != 6 || st.Stale != 0 || st.Questions.Answered != 2 || st.Questions.Open != 0 || st.Embedding.Chunks == 0 || st.Embedding.Embedded != 0 {
		t.Fatalf("%+v", st)
	}
	var doc api.KnowledgeDoc
	f.call("knowledge.show", api.KnowledgeUnitParams{Unit: "overview"}, &doc)
	if !strings.HasPrefix(doc.Content, "---\nid: overview\nlevel: overview\n") || !strings.Contains(doc.Content, "```mermaid") || !strings.HasPrefix(doc.File, st.Dir) {
		t.Fatal(doc)
	}
	f.call("knowledge.show", api.KnowledgeUnitParams{Unit: `App\Controller\InvoiceController`}, &doc)
	if doc.Path != "processes/app-controller-invoicecontroller.md" {
		t.Fatal(doc.Path)
	}
	f.call("knowledge.show", api.KnowledgeUnitParams{Unit: "InvoiceService"}, &doc)
	if doc.Path != "components/app-billing-invoiceservice.md" {
		t.Fatal(doc.Path)
	}
	f.call("knowledge.read", api.KnowledgeReadParams{Path: "index.md"}, &doc)
	if !strings.Contains(doc.Content, "(processes/app-controller-invoicecontroller.md)") || !strings.Contains(doc.Content, "## 4. Components") {
		t.Fatal(doc.Content)
	}
	f.call("knowledge.read", api.KnowledgeReadParams{Path: "entrypoints-http"}, &doc)
	if doc.Path != "entrypoints/http.md" {
		t.Fatal(doc)
	}
	f.call("knowledge.show", api.KnowledgeUnitParams{Unit: "questions"}, &doc)
	if doc.Path != "questions.md" {
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
		t.Fatal("unknown page")
	}
	var qs api.KnowledgeQuestionsResult
	f.call("knowledge.questions", api.KnowledgeQuestionsParams{Status: "answered"}, &qs)
	if len(qs.Questions) != 2 || qs.Counts.Answered != 2 || qs.Questions[0].Answer == "" || qs.Questions[0].AnsweredBy == "" {
		t.Fatalf("%+v", qs)
	}
	if err := modtest.WantCode(t, f.app, "knowledge.questions", api.KnowledgeQuestionsParams{Status: "x"}, api.CodeInvalidParams); err == nil {
		t.Fatal("bad status")
	}
	var units []api.KnowledgeUnit
	f.call("knowledge.list", nil, &units)
	levels := []string{}
	for _, u := range units {
		if len(levels) == 0 || levels[len(levels)-1] != u.Level {
			levels = append(levels, u.Level)
		}
	}
	if strings.Join(levels, ",") != "overview,entrypoints,processes,components,questions" || units[0].Path != "overview.md" || units[0].Confidence != 0.85 || units[0].Summary == "" {
		t.Fatalf("%v %+v", levels, units[0])
	}
	var res api.KnowledgeSearchResult
	f.call("knowledge.search", api.KnowledgeSearchParams{Query: "cart clear"}, &res)
	if res.Mode != "keyword" || len(res.Hits) == 0 || !strings.Contains(res.Note, "no embeddings") || res.Hits[0].Level == "" {
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
	if res.Mode != "hybrid" || len(res.Hits) == 0 || res.Hits[0].Vector <= 0 || res.Hits[0].Anchor == "" || !strings.HasPrefix(res.Hits[0].File, st.Dir) {
		t.Fatalf("%+v", res)
	}
	f.call("knowledge.search", api.KnowledgeSearchParams{Query: "invoice", KeywordOnly: true}, &res)
	if res.Mode != "keyword" {
		t.Fatal(res.Mode)
	}
	f.set(SettingEmbedModel, "other")
	f.call("knowledge.search", api.KnowledgeSearchParams{Query: "invoice"}, &res)
	if res.Mode != "keyword" || !strings.Contains(res.Note, "knowledge embed") {
		t.Fatalf("%+v", res)
	}
	f.set(SettingEmbedModel, "emb")
	if err := modtest.WantCode(t, f.app, "knowledge.search", api.KnowledgeSearchParams{}, api.CodeInvalidParams); err == nil {
		t.Fatal("empty query")
	}
	// Incremental: nothing to do; a changed service makes the processes
	// citing it and its component stale.
	var pl api.KnowledgePlan
	f.call("knowledge.plan", api.KnowledgeScopeParams{}, &pl)
	if pl.Estimate.Units != 0 || len(pl.Units) != 0 {
		t.Fatalf("%+v", pl)
	}
	modtest.WriteFile(t, filepath.Join(f.root, "src/Billing/InvoiceService.php"), "<?php\nnamespace App\\Billing;\nclass InvoiceService {\n  public function save(array $data) { /* changed */ }\n}\n")
	f.mod.invalidate()
	f.call("knowledge.plan", api.KnowledgeScopeParams{}, &pl)
	stale := []string{}
	for _, u := range pl.Units {
		stale = append(stale, u.ID+":"+u.Stale)
	}
	if strings.Join(stale, ",") != "process-app-command-cartcommand:changed,process-app-controller-invoicecontroller:changed,component-app-billing-invoiceservice:changed" {
		t.Fatal(stale)
	}
	// Only the processes level.
	n := len(f.fake.Prompts())
	f.call("knowledge.build", api.KnowledgeBuildParams{KnowledgeScopeParams: api.KnowledgeScopeParams{Level: "processes"}, Wait: true}, &run)
	if run.Done != 2 || len(f.fake.Prompts()) != n+4 || run.EmbedError != "" || run.Level != "processes" {
		t.Fatalf("%+v", run)
	}
	// A page list builds exactly those.
	f.call("knowledge.build", api.KnowledgeBuildParams{Units: []string{"overview"}, Wait: true, NoEmbed: true}, &run)
	if run.Done != 1 {
		t.Fatal(run)
	}
	// Failure keeps the old page and marks it.
	f.kf.Hook = func(_ context.Context, level, _ string) error {
		if level == kn.LevelEntryPoints {
			return fmt.Errorf("tired")
		}
		return nil
	}
	f.call("knowledge.build", api.KnowledgeBuildParams{Units: []string{"entrypoints-cli"}, Wait: true}, &run)
	if run.Failed != 1 || !strings.Contains(strings.Join(run.Errors, " "), "describe") {
		t.Fatalf("%+v", run)
	}
	f.call("knowledge.status", nil, &st)
	if st.Failed != 1 || st.Levels[1].Failed != 1 || st.Units != 6 {
		t.Fatalf("%+v", st)
	}
	rec.Wait(t, api.TopicKnowledgeProgress, func(e bus.Event) bool {
		p, ok := e.Payload.(api.KnowledgeProgress)
		return ok && p.State == kn.RunDone && p.Step == kn.StepWrite && p.Level != ""
	})
}

// TestReadWhileBuilding reads status, list, pages, questions and search
// from several goroutines while a scripted build runs (go test -race):
// nothing blocks or fails, finished pages are there at once, search
// finds them before the embedding phase.
func TestReadWhileBuilding(t *testing.T) {
	f := newFixture(t)
	release := make(chan struct{})
	reached := make(chan struct{})
	var once sync.Once
	f.kf.Hook = func(ctx context.Context, level, _ string) error {
		if level == kn.LevelProcesses {
			once.Do(func() { close(reached) })
			select {
			case <-release:
			case <-ctx.Done():
			}
		}
		return nil
	}
	var run api.KnowledgeRun
	f.call("knowledge.build", api.KnowledgeBuildParams{}, &run)
	if !run.Running {
		t.Fatal(run)
	}
	select {
	case <-reached:
	case <-time.After(20 * time.Second):
		t.Fatal("processes not reached")
	}
	// While the build waits in the processes level:
	var st api.KnowledgeStatus
	f.call("knowledge.status", nil, &st)
	if !st.Run.Running || !st.Exists || st.Levels[0].Pages != 1 || st.Levels[1].Pages != 2 || st.Questions.Open != 1 {
		t.Fatalf("%+v", st)
	}
	var units []api.KnowledgeUnit
	f.call("knowledge.list", nil, &units)
	if units[0].ID != "overview" || units[0].MD == "" || units[1].MD == "" {
		t.Fatalf("%+v", units)
	}
	var doc api.KnowledgeDoc
	f.call("knowledge.read", api.KnowledgeReadParams{Path: "overview.md"}, &doc)
	if !strings.Contains(doc.Content, "# Overview") {
		t.Fatal(doc.Content)
	}
	f.call("knowledge.read", api.KnowledgeReadParams{Path: "index.md"}, &doc)
	if !strings.Contains(doc.Content, "entrypoints/http.md") {
		t.Fatal(doc.Content)
	}
	var res api.KnowledgeSearchResult
	f.call("knowledge.search", api.KnowledgeSearchParams{Query: "cart:clear"}, &res)
	if len(res.Hits) == 0 || res.Hits[0].Path != "entrypoints/cli.md" {
		t.Fatalf("%+v", res)
	}
	if _, ok := api.HookAgentMCPServers.Apply(context.Background(), f.app.Hooks(), nil); ok != nil {
		t.Fatal(ok)
	}
	var wg sync.WaitGroup
	for i := range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 15; j++ {
				var st api.KnowledgeStatus
				var us []api.KnowledgeUnit
				var qs api.KnowledgeQuestionsResult
				var res api.KnowledgeSearchResult
				var doc api.KnowledgeDoc
				for _, c := range []struct {
					m    string
					p, o any
				}{{"knowledge.status", nil, &st}, {"knowledge.list", nil, &us}, {"knowledge.questions", nil, &qs},
					{"knowledge.search", api.KnowledgeSearchParams{Query: "invoices"}, &res}, {"knowledge.show", api.KnowledgeUnitParams{Unit: "overview"}, &doc}} {
					if err := modtest.TryCall(f.app, c.m, c.p, c.o); err != nil {
						t.Error(c.m, err)
						return
					}
				}
				if i == 0 && j == 3 {
					once := sync.OnceFunc(func() { close(release) })
					once()
				}
			}
		}()
	}
	wg.Wait()
	select {
	case <-release:
	default:
		close(release)
	}
	deadline := time.Now().Add(30 * time.Second)
	for {
		f.call("knowledge.status", nil, &st)
		if !st.Run.Running {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("build did not end")
		}
		time.Sleep(20 * time.Millisecond)
	}
	if st.Run.Failed != 0 || st.Units != 6 {
		t.Fatalf("%+v", st.Run)
	}
}

func TestActivityAndTranscript(t *testing.T) {
	f := newFixture(t)
	rec := modtest.Record(t, f.app, "knowledge.*")
	var run api.KnowledgeRun
	f.call("knowledge.build", api.KnowledgeBuildParams{KnowledgeScopeParams: api.KnowledgeScopeParams{Level: "overview"}, Wait: true, NoEmbed: true}, &run)
	if run.Done != 1 || run.ID == "" {
		t.Fatalf("%+v", run)
	}
	// Events: thought, tool and step activity of the page.
	kinds := map[string]bool{}
	rec.Wait(t, api.TopicKnowledgeActivity, func(e bus.Event) bool {
		a, ok := e.Payload.(api.KnowledgeActivity)
		if ok && a.Item == "overview" {
			kinds[a.Kind] = true
		}
		return kinds["thought"] && kinds["tool"] && kinds["step"] && kinds["message"]
	})
	// Counters and step positions in the progress.
	u := run.Units[0]
	if u.Tools != 1 || u.Reads != 1 || u.Chars == 0 || u.Steps != 3 || u.StepIndex != 3 || u.Started.IsZero() {
		t.Fatalf("%+v", u)
	}
	var tr api.KnowledgeTranscript
	f.call("knowledge.transcript", api.KnowledgeTranscriptParams{Item: "overview"}, &tr)
	all := strings.Join(tr.Lines, "\n")
	for _, want := range []string{"» describe (step 1 of 3)", "thought: Looking at the controller first.", "  Then the service.", "tool: read src/Billing/InvoiceService.php [pending]", "→ completed", "```json", "» validate (step 2 of 3)", "» done (confidence"} {
		if !strings.Contains(all, want) {
			t.Fatal(want, "\n", all)
		}
	}
	if tr.Running || tr.Run != run.ID || tr.Title == "" || !strings.HasSuffix(tr.File, filepath.Join("runs", run.ID, "overview.log")) {
		t.Fatalf("%+v", tr)
	}
	b, err := os.ReadFile(tr.File)
	if err != nil || !strings.Contains(string(b), "thought: Looking at the controller first.") {
		t.Fatal(err)
	}
	// From disk after a restart of the module state.
	f.mod.mu.Lock()
	f.mod.act = nil
	f.mod.mu.Unlock()
	var tr2 api.KnowledgeTranscript
	f.call("knowledge.transcript", api.KnowledgeTranscriptParams{Item: "overview"}, &tr2)
	if tr2.Run != run.ID || !strings.Contains(strings.Join(tr2.Lines, "\n"), "» done") {
		t.Fatalf("%+v", tr2)
	}
	if err := modtest.WantCode(t, f.app, "knowledge.transcript", api.KnowledgeTranscriptParams{Item: "nope"}, api.CodeNotFound); err == nil {
		t.Fatal("unknown item")
	}
	if err := modtest.WantCode(t, f.app, "knowledge.transcript", api.KnowledgeTranscriptParams{}, api.CodeInvalidParams); err == nil {
		t.Fatal("no item")
	}
}

func TestTranscriptCapAndPrune(t *testing.T) {
	base := t.TempDir()
	for i := range keepRuns + 2 {
		modtest.WriteFile(t, filepath.Join(base, "runs", fmt.Sprintf("2026010%d", i), "x.log"), "x")
	}
	var mu sync.Mutex
	var evs []api.KnowledgeActivity
	a := newActivityLog(base, "20261127", func(_ string, v any) { mu.Lock(); evs = append(evs, v.(api.KnowledgeActivity)); mu.Unlock() }, nil)
	runs, _ := os.ReadDir(filepath.Join(base, "runs"))
	if len(runs) != keepRuns {
		t.Fatal(len(runs))
	}
	info := kn.StepInfo{Item: "p", Level: kn.LevelProcesses, Step: kn.StepDescribe}
	for i := range maxTranscriptLines + 10 {
		a.chunk(info, api.KnowledgeActivityMessage, fmt.Sprintf("line %d\n", i))
	}
	a.tool(info, acp.ToolCall{ToolCallID: "x", Name: "grep"}, false)
	a.tool(info, acp.ToolCall{ToolCallID: "x", Status: "in_progress"}, true)
	a.close()
	tr, _ := a.transcriptOf("p")
	if len(tr.Lines) != maxTranscriptLines || tr.Truncated == 0 || tr.Lines[len(tr.Lines)-1] != "tool: grep" {
		t.Fatal(len(tr.Lines), tr.Truncated, tr.Lines[len(tr.Lines)-1])
	}
	mu.Lock()
	defer mu.Unlock()
	// Coalesced: a few message events for the burst of 2000+ chunks (a
	// slow machine may flush it more than once), then one tool event.
	msgs, tools := 0, 0
	for _, e := range evs {
		switch e.Kind {
		case "message":
			msgs++
		case "tool":
			tools++
		}
	}
	last := evs[len(evs)-1]
	if msgs < 1 || msgs > maxTranscriptLines/10 || tools != 1 || last.Kind != "tool" || evs[0].Level != kn.LevelProcesses {
		t.Fatalf("%d messages, %d tools, last %+v", msgs, tools, last)
	}
	if stepText(kn.Event{State: kn.RunRunning, Step: kn.StepValidate, Attempt: 1, Error: "bad"}) != "repair 1 of validate: bad" || stepText(kn.Event{State: kn.RunFailed, Error: "x"}) != "failed: x" || stepText(kn.Event{State: kn.RunQueued}) != "" {
		t.Fatal("stepText")
	}
}

func TestLegacyKnowledge(t *testing.T) {
	f := newFixture(t)
	var st api.KnowledgeStatus
	f.call("knowledge.status", nil, &st)
	modtest.WriteFile(t, filepath.Join(st.Dir, "manifest.json"), `{"version":1,"units":{"src":{"id":"src","title":"App","md":"units/src.md","status":"done"}}}`)
	modtest.WriteFile(t, filepath.Join(st.Dir, "units", "src.md"), "---\nunit: src\n---\n# App\n\n## Overview\n\nOld invoices page.\n")
	modtest.WriteFile(t, filepath.Join(st.Dir, "index.md"), "# old")
	f.call("knowledge.status", nil, &st)
	if !st.Legacy || st.Units != 0 || st.Stale == 0 {
		t.Fatalf("%+v", st)
	}
	var units []api.KnowledgeUnit
	f.call("knowledge.list", nil, &units)
	var res api.KnowledgeSearchResult
	f.call("knowledge.search", api.KnowledgeSearchParams{Query: "invoices"}, &res)
	var run api.KnowledgeRun
	f.call("knowledge.build", api.KnowledgeBuildParams{KnowledgeScopeParams: api.KnowledgeScopeParams{Level: "overview"}, Wait: true, NoEmbed: true}, &run)
	if run.Done != 1 {
		t.Fatal(run)
	}
	if _, err := os.Stat(filepath.Join(st.Dir, "units")); !os.IsNotExist(err) {
		t.Fatal("old pages kept")
	}
	f.call("knowledge.status", nil, &st)
	if st.Legacy || st.Units != 1 {
		t.Fatalf("%+v", st)
	}
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
	f.call("knowledge.build", api.KnowledgeBuildParams{KnowledgeScopeParams: api.KnowledgeScopeParams{Level: "overview"}, Wait: true, NoEmbed: true}, &run)
	l := servers()
	if len(l) != 1 || l[0].Name != "tiegel-knowledge" || l[0].Command != "/opt/tiegel/tiegel" ||
		strings.Join(l[0].Args[:5], " ") != "--headless mcp knowledge --project "+f.root || l[0].Args[5] != "--config-dir" {
		t.Fatalf("%+v", l)
	}
	f.set(SettingAgentMode, api.AgentContextOff)
	if len(servers()) != 0 {
		t.Fatal("setting off")
	}
	var mc api.KnowledgeMCPConfig
	f.call("knowledge.mcpConfig", nil, &mc)
	if mc.Command != "/opt/tiegel/tiegel" || !strings.Contains(mc.Opencode, `"type": "local"`) || !strings.Contains(mc.Claude, `"mcpServers"`) {
		t.Fatalf("%+v", mc)
	}
	// Pages built without embedding are searched by keyword; searching
	// does not write the store.
	var res api.KnowledgeSearchResult
	f.call("knowledge.search", api.KnowledgeSearchParams{Query: "mysql"}, &res)
	if len(res.Hits) == 0 {
		t.Fatal("no hits")
	}
	var st api.KnowledgeStatus
	f.call("knowledge.status", nil, &st)
	if st.Embedding.Chunks != 0 {
		t.Fatal("search wrote the store", st.Embedding)
	}
}

func TestPromptOverride(t *testing.T) {
	f := newFixture(t)
	dir := f.mod.promptDir()
	if dir == "" {
		t.Skip("no config dir")
	}
	modtest.WriteFile(t, filepath.Join(dir, "overview.tmpl"), "{{template \"base\" .}}\n\n## Task: overview (level 0)\nCUSTOM PROMPT\n\n{{template \"answer\" .}}")
	var run api.KnowledgeRun
	f.call("knowledge.build", api.KnowledgeBuildParams{KnowledgeScopeParams: api.KnowledgeScopeParams{Level: "overview"}, Wait: true, NoEmbed: true}, &run)
	if run.Done != 1 || f.kf.Count("CUSTOM PROMPT") != 1 {
		t.Fatalf("%+v", run)
	}
	modtest.WriteFile(t, filepath.Join(dir, "overview.tmpl"), "{{broken")
	f.call("knowledge.build", api.KnowledgeBuildParams{KnowledgeScopeParams: api.KnowledgeScopeParams{Level: "overview", All: true}, Wait: true, NoEmbed: true}, &run)
	if run.Done != 1 || !strings.Contains(strings.Join(run.Errors, " "), "using the built-in prompts") {
		t.Fatalf("%+v", run)
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
	if !run.Running || len(run.Units) == 0 {
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
	if levelEstimate(pl, kn.LevelOverview).Pages != 1 {
		t.Fatal("cancelled pages must stay stale")
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
