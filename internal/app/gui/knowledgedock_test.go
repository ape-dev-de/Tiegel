package gui

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/ape-dev-de/tiegel/internal/acp"
	"github.com/ape-dev-de/tiegel/internal/acp/acptest"
	"github.com/ape-dev-de/tiegel/internal/api"
	"github.com/ape-dev-de/tiegel/internal/app/gui/controller"
	kn "github.com/ape-dev-de/tiegel/internal/knowledge"
	"github.com/ape-dev-de/tiegel/internal/knowledge/knowledgetest"
	"github.com/ape-dev-de/tiegel/internal/modules"
	knmod "github.com/ape-dev-de/tiegel/internal/modules/knowledge"
)

// knowledgeScript answers like an agent: thoughts and tool calls, then
// the fake's JSON.
func knowledgeScript(f *knowledgetest.Fake) acptest.Script {
	return func(ctx context.Context, t *acptest.Turn) string {
		if _, step, title := knowledgetest.Level(t.Prompt); step == kn.StepDescribe {
			t.Thought("The route posts to InvoiceController::create; following the call into InvoiceService.")
			t.Tool(false, acp.ToolCall{ToolCallID: "r1", Title: "read src/Controller/InvoiceController.php", Kind: acp.KindRead, Status: "pending"})
			t.Tool(true, acp.ToolCall{ToolCallID: "r1", Status: "completed"})
			t.Tool(false, acp.ToolCall{ToolCallID: "g1", Title: "grep InvoiceService (" + title + ")", Kind: acp.KindSearch, Status: "completed"})
		}
		t.Text(f.Answer(ctx, t.Prompt))
		return ""
	}
}

func TestKnowledgeWindow(t *testing.T) {
	// A fake Ollama with the model already pulled.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/show":
			_, _ = w.Write([]byte(`{}`))
		case "/api/embed":
			var req struct{ Input []string }
			_ = json.NewDecoder(r.Body).Decode(&req)
			var out [][]float32
			for _, in := range req.Input {
				v := []float32{0.01, 0.01}
				if strings.Contains(strings.ToLower(in), "invoice") {
					v[0] = 1
				}
				if strings.Contains(strings.ToLower(in), "order") {
					v[1] = 1
				}
				out = append(out, v)
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"embeddings": out})
		default:
			w.WriteHeader(404)
		}
	}))
	defer srv.Close()
	// The build waits in the processes level until released.
	release := make(chan struct{})
	var once sync.Once
	reached := make(chan struct{})
	kf := &knowledgetest.Fake{Service: kn.Ref{File: "src/Billing/InvoiceService.php", Symbol: `App\Billing\InvoiceService::save`},
		Hook: func(ctx context.Context, level, _ string) error {
			if level == kn.LevelProcesses {
				once.Do(func() { close(reached) })
				select {
				case <-release:
				case <-ctx.Done():
				}
			}
			return nil
		}}
	fake := &acptest.Agent{Script: knowledgeScript(kf)}
	envModules = modules.Options{Knowledge: knmod.Options{Start: fake.Start, LookPath: func(n string) (string, error) { return "/bin/" + n, nil },
		Executable: func() (string, error) { return "/usr/local/bin/tiegel", nil }}}
	t.Cleanup(func() { envModules = modules.Options{} })
	e := newEnvWith(t, true, map[string]string{
		"composer.json":                        `{"require":{"symfony/framework-bundle":"^7"},"autoload":{"psr-4":{"App\\":"src/"}}}`,
		"src/Controller/InvoiceController.php": "<?php\nnamespace App\\Controller;\nclass InvoiceController {\n  #[Route('/invoices', methods: ['POST'])]\n  public function create(array $order) { return $order; }\n}\n",
		"src/Billing/InvoiceService.php":       "<?php\nnamespace App\\Billing;\nclass InvoiceService {\n  public function save(array $order) { return $order; }\n}\n",
	}, nil)
	for key, val := range map[string]string{knmod.SettingEndpoint: srv.URL, knmod.SettingEmbedModel: "embeddinggemma-2:270m"} {
		raw, _ := json.Marshal(val)
		if err := e.app.CallLocal(context.Background(), "settings.set", api.SettingsSetParams{Key: key, Scope: "global", Value: raw}, nil); err != nil {
			t.Fatal(err)
		}
	}
	e.v.ShowTool(ToolKnowledge)
	e.settle()
	k := e.ctl.Knowledge
	e.waitFor("knowledge status", func() bool { return k.Loaded && k.Status.Dir != "" && k.Status.Stale > 0 })
	if k.Status.Run.Running || len(fake.Prompts()) != 0 {
		t.Fatal("a build started without a user action")
	}
	if !strings.HasPrefix(e.v.knwd.view.Summary, "No knowledge base yet") || e.v.knwd.view.Stale != k.Status.Stale {
		t.Fatalf("%q %d", e.v.knwd.view.Summary, e.v.knwd.view.Stale)
	}
	stale0 := k.Status.Stale
	e.shot("knowledge-empty")

	// Update shows the estimate per level first; nothing runs before Build.
	k.AskBuild(false, "")
	e.waitFor("estimate", func() bool { return k.Plan != nil })
	e.frame()
	if !strings.Contains(e.v.knwd.view.Confirm, "billed by your agent's provider") || !strings.Contains(e.v.knwd.view.Confirm, "Processes") || len(fake.Prompts()) != 0 {
		t.Fatalf("%q", e.v.knwd.view.Confirm)
	}
	e.shot("knowledge-confirm")
	k.Build()
	// While the processes level runs: the finished pages are listed and
	// searchable, the running page shows its step and counters, a click
	// opens its live transcript.
	<-reached
	e.waitFor("progressive pages", func() bool {
		return k.Status.Run.Running && k.Status.Exists && countDocs(k.Units) >= 2
	})
	var running string
	e.waitFor("running page", func() bool {
		for _, u := range k.Status.Run.Units {
			if u.State == kn.RunRunning && u.Level == kn.LevelProcesses && u.Tools > 0 {
				running = u.Unit
				return true
			}
		}
		return false
	})
	k.PeekAt(running)
	e.waitFor("transcript", func() bool { return strings.Contains(strings.Join(k.Transcript, "\n"), "grep InvoiceService") })
	e.frame()
	if v := e.v.knwd.view; v.Peek != running || len(v.Transcript) == 0 || !v.Running || v.Steps[0].ID == "" {
		t.Fatalf("%+v", v.Steps)
	}
	k.Search("invoices")
	e.waitFor("hits while building", func() bool { return len(k.Hits) > 0 })
	e.frame()
	e.shot("knowledge-running")
	k.Search("")
	close(release)
	e.waitFor("build", func() bool {
		return !k.Status.Run.Running && k.Status.Units >= stale0 && countDocs(k.Units) == k.Status.Units
	})
	e.waitFor("embedded", func() bool {
		k.Load()
		e.ctl.Settle(200e6)
		return k.Status.Embedding.Embedded > 0 && k.Status.Embedding.Embedded == k.Status.Embedding.Chunks
	})
	e.frame()
	if len(fake.Prompts()) != 2*k.Status.Run.Done || k.Status.Run.Failed != 0 || !e.v.knwd.view.LastRun {
		t.Fatal(len(fake.Prompts()), e.v.knwd.view.LastRun)
	}
	k.PeekAt("")
	e.frame()
	e.shot("knowledge-built")

	k.Search("where are invoices stored")
	e.waitFor("hits", func() bool { return len(k.Hits) > 0 })
	e.frame()
	if k.Hits[0].Anchor == "" || k.Hits[0].Score <= 0 {
		t.Fatalf("%+v", k.Hits[0])
	}
	e.shot("knowledge-search")

	// A hit opens the document (outside the project) in the preview.
	e.v.mdd.openAt(k.Hits[0].File, k.Hits[0].Anchor)
	e.settle()
	tab := e.ctl.ActiveTab()
	if tab == nil || tab.Virtual != controller.TabMarkdown || tab.Data != k.Hits[0].File || strings.HasPrefix(k.Hits[0].File, e.root) {
		t.Fatalf("%+v", tab)
	}
	e.waitFor("preview", func() bool { return e.ctl.Markdown.Preview(k.Hits[0].File).Doc != nil })
	e.frame()
	e.shot("knowledge-preview")
	if loc := lineAnchor("/x.php", "L4"); loc == nil || loc.Range.Start.Line != 4 || loc.Range.Start.Offset != -1 {
		t.Fatal(loc)
	}
	if lineAnchor("/x.php", "heading") != nil || lineAnchor("/x.php", "L0") != nil {
		t.Fatal("not a line anchor")
	}
}
