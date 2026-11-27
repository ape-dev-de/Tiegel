package knowledge_test

import (
	"context"
	"errors"
	"flag"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	kn "github.com/ape-dev-de/tiegel/internal/knowledge"
	"github.com/ape-dev-de/tiegel/internal/knowledge/knowledgetest"
)

var update = flag.Bool("update", false, "rewrite the golden prompt files")

func write(t *testing.T, root string, files map[string]string) {
	t.Helper()
	for p, c := range files {
		f := filepath.Join(root, filepath.FromSlash(p))
		if err := os.MkdirAll(filepath.Dir(f), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(f, []byte(c), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func read(t *testing.T, file string) string {
	t.Helper()
	b, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// shop is a small Symfony project: an HTTP controller and a console
// command that both use the OrderService.
var shop = map[string]string{
	"composer.json": `{"name":"acme/shop","require":{"php":">=8.2","symfony/framework-bundle":"^7.1","doctrine/orm":"^3"},"autoload":{"psr-4":{"App\\":"src/"}}}`,
	"src/Controller/OrderController.php": `<?php
namespace App\Controller;

use App\Service\OrderService;

class OrderController
{
    #[Route('/orders', methods: ['POST'])]
    public function create(OrderService $s) { return $s->save([]); }

    #[Route('/orders/{id}', methods: ['GET'])]
    public function show(int $id) {}
}
`,
	"src/Command/ImportCommand.php": `<?php
namespace App\Command;

#[AsCommand(name: 'app:import')]
class ImportCommand extends Command
{
    protected function execute($in, $out): int { return 0; }
}
`,
	"src/Service/OrderService.php": `<?php
namespace App\Service;

class OrderService
{
    public function save(array $data) { return $data; }
}
`,
	"docker-compose.yml": "services:\n  db:\n    image: mysql:8\n",
}

type fixture struct {
	root, dir string
	fake      *knowledgetest.Fake
	pipe      *kn.Pipeline
	events    []kn.Event
	mu        sync.Mutex
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	f := &fixture{root: t.TempDir(), dir: t.TempDir()}
	write(t, f.root, shop)
	f.fake = &knowledgetest.Fake{Service: kn.Ref{File: "src/Service/OrderService.php", Symbol: `App\Service\OrderService::save`}}
	f.pipe = &kn.Pipeline{Planner: f.planner(), Project: "shop", Agent: f.fake.Agent(), Commit: "abc", Model: "fake/large",
		Progress: func(e kn.Event) { f.mu.Lock(); f.events = append(f.events, e); f.mu.Unlock() },
		Now:      func() time.Time { return time.Date(2026, 11, 27, 0, 0, 0, 0, time.UTC) }}
	return f
}

func (f *fixture) planner() kn.Planner {
	files := kn.FilterFiles([]string{"src/Controller/OrderController.php", "src/Command/ImportCommand.php", "src/Service/OrderService.php"}, nil, nil)
	facts := kn.GatherFacts(f.root, f.root, files)
	eps := kn.ExtractEntryPoints(f.root, ".", files)
	facts.DeriveStyle(eps)
	return kn.Planner{Root: f.root, Dir: f.dir, Facts: facts, Files: files, Entries: eps}
}

func (f *fixture) run(t *testing.T, w kn.Work) (kn.Summary, *kn.Manifest) {
	t.Helper()
	m, err := kn.LoadManifest(f.dir)
	if err != nil {
		t.Fatal(err)
	}
	f.pipe.Planner = f.planner()
	return f.pipe.Run(context.Background(), w, m), m
}

// describeLevels returns the level of every describe prompt in order.
func describeLevels(prompts []string) []string {
	var out []string
	for _, p := range prompts {
		if l, step, _ := knowledgetest.Level(p); step == kn.StepDescribe {
			out = append(out, l)
		}
	}
	return out
}

func TestPipelineTopDown(t *testing.T) {
	f := newFixture(t)
	sum, m := f.run(t, kn.Work{Levels: kn.Levels})
	if sum.Failed != 0 || sum.Cancelled || len(sum.Errors) != 0 {
		t.Fatalf("%+v", sum)
	}
	// Ordering: overview → entry points → processes → components →
	// questions; every page described and validated.
	levels := describeLevels(f.fake.Prompts())
	if !slices.IsSortedFunc(levels, func(a, b string) int { return kn.LevelRank(a) - kn.LevelRank(b) }) {
		t.Fatal(levels)
	}
	counts := map[string]int{}
	for _, l := range levels {
		counts[l]++
	}
	if counts[kn.LevelOverview] != 1 || counts[kn.LevelEntryPoints] != 2 || counts[kn.LevelProcesses] != 2 || counts[kn.LevelComponents] != 1 || counts[kn.LevelQuestions] != 1 {
		t.Fatal(counts)
	}
	if len(f.fake.Prompts()) != 2*len(levels) || sum.Done != len(levels) {
		t.Fatal(len(f.fake.Prompts()), sum.Done)
	}
	// Pages per level with front matter, diagrams and links.
	ov := read(t, filepath.Join(f.dir, "overview.md"))
	for _, want := range []string{"---\nid: overview\nlevel: overview\n", "confidence: 0.85", "# Overview: shop", "```mermaid\nflowchart LR", "db (mysql:8)", "symfony/framework-bundle ^7.1"} {
		if !strings.Contains(ov, want) {
			t.Fatal(want, ov)
		}
	}
	ep := read(t, filepath.Join(f.dir, "entrypoints", "http.md"))
	if !strings.Contains(ep, "| POST /orders |") || !strings.Contains(ep, "**Orders:**") || strings.Contains(ep, "found by the agent") {
		t.Fatal(ep)
	}
	proc := read(t, filepath.Join(f.dir, "processes", "app-controller-ordercontroller.md"))
	for _, want := range []string{"# Process: OrderController", "```mermaid\nsequenceDiagram", "`App\\Service\\OrderService::save` – stores the order", "## Writes", "orders (table)"} {
		if !strings.Contains(proc, want) {
			t.Fatal(want, proc)
		}
	}
	comp := read(t, filepath.Join(f.dir, "components", "app-service-orderservice.md"))
	if !strings.Contains(comp, "# Component: OrderService") || !strings.Contains(comp, "## Used by") || !strings.Contains(comp, "(../processes/app-controller-ordercontroller.md)") {
		t.Fatal(comp)
	}
	idx := read(t, filepath.Join(f.dir, "index.md"))
	pos := func(s string) int { return strings.Index(idx, s) }
	if pos("## 1. Overview") < 0 || !(pos("## 1. Overview") < pos("## 2. Entry points") && pos("## 2. Entry points") < pos("## 3. Processes") && pos("## 3. Processes") < pos("## 4. Components") && pos("## 4. Components") < pos("questions.md")) {
		t.Fatal(idx)
	}
	// Context injection: later levels see the overview, the entry point
	// descriptions and the open questions.
	var procPrompt, compPrompt, firstProc string
	for _, p := range f.fake.Prompts() {
		l, step, title := knowledgetest.Level(p)
		if step != kn.StepDescribe {
			continue
		}
		if l == kn.LevelProcesses && firstProc == "" {
			firstProc = p
		}
		if l == kn.LevelProcesses && strings.HasPrefix(title, "OrderController") {
			procPrompt = p
		}
		if l == kn.LevelComponents {
			compPrompt = p
		}
	}
	if !strings.Contains(procPrompt, "Orders come in over HTTP") || !strings.Contains(procPrompt, "handles POST /orders") || !strings.Contains(procPrompt, "src/Controller/OrderController.php") {
		t.Fatal(procPrompt)
	}
	if !strings.Contains(firstProc, "Which table keeps the order lines?") {
		t.Fatal("the overview's open question was not passed on", firstProc)
	}
	if !strings.Contains(compPrompt, "OrderService::save – stores the order") || !strings.Contains(compPrompt, "src/Service/OrderService.php") {
		t.Fatal(compPrompt)
	}
	// Questions: the overview's question was answered by a process, the
	// component's by the "answer questions" iteration.
	qs, _ := kn.LoadQuestions(f.dir)
	open, answered := qs.Counts()
	if open != 0 || answered != 2 || sum.Answered != 2 {
		t.Fatal(open, answered, sum.Answered)
	}
	for _, q := range qs.Questions {
		if q.Level == kn.LevelOverview && !strings.HasPrefix(q.AnsweredBy, "process-") || q.Level == kn.LevelComponents && !strings.HasPrefix(q.AnsweredBy, "questions-") {
			t.Fatalf("%+v", q)
		}
	}
	qmd := read(t, filepath.Join(f.dir, "questions.md"))
	if !strings.Contains(qmd, "## Answered (2)") || !strings.Contains(qmd, "**Answer:** order_lines") {
		t.Fatal(qmd)
	}
	// Manifest: sources include the cited service file.
	st := m.Items["process-app-controller-ordercontroller"]
	if st.Status != kn.StatusDone || st.Files["src/Service/OrderService.php"] == "" || st.Files["@entries"] == "" || st.Confidence != 0.85 || st.Summary == "" || st.Data == "" {
		t.Fatalf("%+v", st)
	}
	if m.Items["questions-1"] != nil {
		t.Fatal("question batches are not pages")
	}
	// Events carry the level; queued first.
	f.mu.Lock()
	first := f.events[0]
	f.mu.Unlock()
	if first.State != kn.RunQueued || first.Level != kn.LevelOverview {
		t.Fatal(first)
	}

	// Incremental: nothing changed → nothing runs.
	n := len(f.fake.Prompts())
	sum, _ = f.run(t, kn.Work{Levels: kn.Levels})
	if sum.Done != 0 || len(f.fake.Prompts()) != n {
		t.Fatalf("%+v %d", sum, len(f.fake.Prompts())-n)
	}
	// The service changed: the processes citing it and its component are
	// stale, the overview and entry points are not.
	write(t, f.root, map[string]string{"src/Service/OrderService.php": strings.Replace(shop["src/Service/OrderService.php"], "return $data;", "return $data + ['saved' => true];", 1)})
	sum, _ = f.run(t, kn.Work{Levels: kn.Levels})
	got := describeLevels(f.fake.Prompts()[n:])
	if strings.Join(got, ",") != "processes,processes,components" {
		t.Fatal(got)
	}
	// A single level with all: only that level.
	n = len(f.fake.Prompts())
	sum, _ = f.run(t, kn.Work{Levels: []string{kn.LevelOverview}, All: true})
	if got := describeLevels(f.fake.Prompts()[n:]); strings.Join(got, ",") != "overview" || sum.Done != 1 {
		t.Fatal(got)
	}
	// Only one page.
	n = len(f.fake.Prompts())
	f.run(t, kn.Work{Levels: kn.Levels, Only: []string{"entrypoints-cli"}})
	if got := describeLevels(f.fake.Prompts()[n:]); strings.Join(got, ",") != "entrypoints" {
		t.Fatal(got)
	}
}

func TestPipelineFailuresAndDiagrams(t *testing.T) {
	f := newFixture(t)
	// The agent fails the overview; an invalid diagram for processes.
	f.fake.Hook = func(_ context.Context, level, _ string) error {
		if level == kn.LevelOverview {
			return errors.New("tired")
		}
		return nil
	}
	f.fake.Diagram = "sequenceDiagram\n    A->>"
	sum, m := f.run(t, kn.Work{Levels: []string{kn.LevelOverview, kn.LevelEntryPoints, kn.LevelProcesses}})
	if sum.Failed != 1 || !strings.Contains(sum.Errors[0], "overview: describe") {
		t.Fatalf("%+v", sum)
	}
	st := m.Items["overview"]
	if st.Status != kn.StatusFailed || !st.Mechanical || st.MD != "overview.md" {
		t.Fatalf("%+v", st)
	}
	ov := read(t, filepath.Join(f.dir, "overview.md"))
	if !strings.Contains(ov, "not available yet") || !strings.Contains(ov, "## Tech stack") {
		t.Fatal(ov)
	}
	if !strings.Contains(read(t, filepath.Join(f.dir, "index.md")), "(facts only)") {
		t.Fatal("index")
	}
	proc := read(t, filepath.Join(f.dir, "processes", "app-controller-ordercontroller.md"))
	if strings.Contains(proc, "A->>\n") || !strings.Contains(proc, "participant") {
		t.Fatal("invalid diagram kept or no replacement", proc)
	}
	// The overview is retried (failed), the rest is current.
	f.fake.Hook = nil
	n := len(f.fake.Prompts())
	sum, m = f.run(t, kn.Work{Levels: []string{kn.LevelOverview, kn.LevelEntryPoints, kn.LevelProcesses}})
	if got := describeLevels(f.fake.Prompts()[n:]); strings.Join(got, ",") != "overview" || m.Items["overview"].Mechanical {
		t.Fatal(got)
	}
	// A failing page keeps its old page.
	f.fake.Hook = func(_ context.Context, level, _ string) error {
		if level == kn.LevelEntryPoints {
			return errors.New("nope")
		}
		return nil
	}
	sum, m = f.run(t, kn.Work{Levels: []string{kn.LevelEntryPoints}, All: true})
	if sum.Failed != 2 || m.Items["entrypoints-http"].Status != kn.StatusFailed || m.Items["entrypoints-http"].MD == "" || m.Items["entrypoints-http"].Mechanical {
		t.Fatalf("%+v %+v", sum, m.Items["entrypoints-http"])
	}
	// Cancelled before the start.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	m, _ = kn.LoadManifest(f.dir)
	if s := f.pipe.Run(ctx, kn.Work{Levels: kn.Levels, All: true}, m); !s.Cancelled || s.Done+s.Failed != 0 {
		t.Fatal(s)
	}
}

func TestPipelineMigratesLegacy(t *testing.T) {
	f := newFixture(t)
	write(t, f.dir, map[string]string{
		"manifest.json": `{"version":1,"project":"shop","units":{"src":{"id":"src","title":"App","dir":"src","md":"units/src.md","status":"done","files":{"src/A.php":"x"}}}}`,
		"units/src.md":  "---\nunit: src\n---\n# App\n",
		"index.md":      "old",
	})
	m, err := kn.LoadManifest(f.dir)
	if err != nil || !m.Legacy || len(m.Items) != 0 {
		t.Fatal(m, err)
	}
	sum := f.pipe.Run(context.Background(), kn.Work{Levels: []string{kn.LevelOverview}}, m)
	if sum.Done != 1 {
		t.Fatal(sum)
	}
	if _, err := os.Stat(filepath.Join(f.dir, "units")); !os.IsNotExist(err) {
		t.Fatal("old pages kept")
	}
	m2, _ := kn.LoadManifest(f.dir)
	if m2.Legacy || m2.Items["overview"] == nil || m2.Version != kn.ManifestVersion {
		t.Fatalf("%+v", m2)
	}
	if strings.Contains(read(t, filepath.Join(f.dir, "index.md")), "old") {
		t.Fatal("index not rewritten")
	}
}

// TestReadDuringBuild reads pages, the manifest and the questions while
// a build writes them (run with -race).
func TestReadDuringBuild(t *testing.T) {
	f := newFixture(t)
	release := make(chan struct{})
	reached := make(chan struct{})
	var once sync.Once
	f.fake.Hook = func(ctx context.Context, level, _ string) error {
		if level == kn.LevelProcesses {
			once.Do(func() { close(reached) })
			select {
			case <-release:
			case <-ctx.Done():
			}
		}
		return nil
	}
	done := make(chan kn.Summary)
	go func() {
		m, _ := kn.LoadManifest(f.dir)
		done <- f.pipe.Run(context.Background(), kn.Work{Levels: kn.Levels}, m)
	}()
	select {
	case <-reached:
	case <-time.After(20 * time.Second):
		t.Fatal("processes not reached")
	}
	// The pages of the finished levels are there, listed and searchable.
	m, err := kn.LoadManifest(f.dir)
	if err != nil || m.Items["overview"] == nil || m.Items["entrypoints-http"] == nil {
		t.Fatal(m, err)
	}
	if !strings.Contains(read(t, filepath.Join(f.dir, "index.md")), "overview.md") {
		t.Fatal("index.md not written progressively")
	}
	chunks := kn.AllChunks(f.dir, m, 300)
	store, _ := kn.OpenFileStore(t.TempDir())
	res, err := kn.Search(context.Background(), store, "POST orders", kn.SearchOptions{Extra: chunks})
	if err != nil || len(res) == 0 || res[0].Chunk.Level != kn.LevelEntryPoints {
		t.Fatal(res, err)
	}
	var wg sync.WaitGroup
	for range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 20; i++ {
				m, err := kn.LoadManifest(f.dir)
				if err != nil {
					t.Error(err)
					return
				}
				for _, st := range m.Items {
					if st.MD != "" {
						if _, err := os.ReadFile(filepath.Join(f.dir, st.MD)); err != nil {
							t.Error(err)
							return
						}
					}
				}
				if _, err := kn.LoadQuestions(f.dir); err != nil {
					t.Error(err)
					return
				}
			}
		}()
	}
	close(release)
	wg.Wait()
	if sum := <-done; sum.Failed != 0 {
		t.Fatal(sum)
	}
}

func TestPromptsGoldenAndOverride(t *testing.T) {
	p, err := kn.LoadPrompts("")
	if err != nil {
		t.Fatal(err)
	}
	data := kn.PromptData{Project: "shop", Title: "OrderController (HTTP)", ID: "process-x", Group: `App\Controller\OrderController`,
		Facts:    "- framework: symfony\n- PHP files analysed: 3\n",
		Overview: "Orders come in over HTTP.\nContainers: Web app, MySQL\n",
		Entries:  "- POST /orders → App\\Controller\\OrderController::create (src/Controller/OrderController.php:8)\n",
		Context:  "- POST /orders: creates an order; in: JSON body\n",
		Files:    []string{"src/Controller/OrderController.php", "src/Service/OrderService.php"}, MoreFiles: 3,
		Questions: []kn.PromptQuestion{{ID: "q-0011aabb", Text: "Which table keeps the order lines?", Item: "overview", Refs: "src/Entity/Order.php"}},
		Doc:       `{"name":"x"}`, Notes: []string{"X::y: file src/X.php does not exist"}, MoreNotes: 2, Error: "no JSON block in the answer"}
	for _, name := range []string{"overview.tmpl", "entrypoints.tmpl", "processes.tmpl", "components.tmpl", "questions.tmpl", "validate.tmpl", "repair.tmpl"} {
		d := data
		d.Level = strings.TrimSuffix(name, ".tmpl")
		d.LevelTitle = kn.LevelTitle(d.Level)
		d.Schema = kn.Schema(d.Level)
		if name == "entrypoints.tmpl" {
			d.Title = "HTTP routes"
		}
		got, err := p.Render(name, d)
		if err != nil {
			t.Fatal(name, err)
		}
		golden := filepath.Join("testdata", "prompts", strings.TrimSuffix(name, ".tmpl")+".golden")
		if *update {
			write(t, ".", map[string]string{golden: got})
			continue
		}
		want, err := os.ReadFile(golden)
		if err != nil {
			t.Fatal(err, "(run go test -run Golden -update)")
		}
		if got != string(want) {
			t.Errorf("%s differs from %s (go test -run Golden -update):\n%s", name, golden, got)
		}
		if name != "repair.tmpl" && (!strings.Contains(got, "data-flow analyst") || !strings.Contains(got, "Never explain what a framework")) {
			t.Fatal("base prompt missing", name)
		}
	}
	// The base prompt forbids generic explanations and asks for
	// references and open questions.
	if out, _ := p.Render("processes.tmpl", data); !strings.Contains(out, "file:") && !strings.Contains(out, "names the file") {
		t.Fatal(out)
	}
	if _, err := p.Render("nope.tmpl", data); err == nil {
		t.Fatal("unknown template")
	}
	var nilP *kn.Prompts
	if _, err := nilP.Render("overview.tmpl", data); err == nil {
		t.Fatal("nil prompts")
	}
	// Overrides replace single templates; broken ones are reported.
	dir := t.TempDir()
	write(t, dir, map[string]string{"overview.tmpl": "{{template \"base\" .}}\nCUSTOM {{.Project}}\n{{template \"answer\" .}}"})
	po, err := kn.LoadPrompts(dir)
	if err != nil || len(po.Overrides) != 1 {
		t.Fatal(err)
	}
	if out, _ := po.Render("overview.tmpl", data); !strings.Contains(out, "CUSTOM shop") || !strings.Contains(out, "data-flow analyst") {
		t.Fatal(out)
	}
	if out, _ := po.Render("processes.tmpl", data); strings.Contains(out, "CUSTOM") {
		t.Fatal("override leaked")
	}
	write(t, dir, map[string]string{"base.tmpl": "{{define \"base\"}}broken{{"})
	if _, err := kn.LoadPrompts(dir); err == nil {
		t.Fatal("broken override accepted")
	}
	if po, err := kn.LoadPrompts(t.TempDir()); err != nil || len(po.Overrides) != 0 {
		t.Fatal(err)
	}
	if kn.Budget(2, "line one\nline two\nline three") != "line one\n… (shortened)" || kn.Budget(100, "short") != "short" {
		t.Fatal(kn.Budget(2, "line one\nline two\nline three"))
	}
	if kn.CompactOverview(nil) != "" || !strings.Contains(kn.CompactOverview(&kn.OverviewDoc{Summary: "s", Architecture: "a", Containers: []kn.Container{{Name: "c", Tech: "t"}}, Relations: []kn.Relation{{From: "a", To: "b", Label: "l"}}}), "- a → b: l") {
		t.Fatal("compact overview")
	}
	if !strings.Contains(kn.CompactEntries([]kn.EntryPoint{{Name: "x", File: "f.php", Source: "agent"}}), "added by the entry-point page") {
		t.Fatal("compact entries")
	}
}
