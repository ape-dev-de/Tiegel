package gui

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ape-dev-de/tiegel/internal/acp/acptest"
	"github.com/ape-dev-de/tiegel/internal/api"
	"github.com/ape-dev-de/tiegel/internal/app/gui/controller"
	kn "github.com/ape-dev-de/tiegel/internal/knowledge"
	"github.com/ape-dev-de/tiegel/internal/modules"
	knmod "github.com/ape-dev-de/tiegel/internal/modules/knowledge"
)

// knowledgeAgent answers the three pipeline prompts.
func knowledgeAgent(_ context.Context, t *acptest.Turn) string {
	p := t.Prompt
	file, sym, name := "src/Billing/Invoice.php", "Invoice::create", "Create invoice"
	if strings.Contains(p, `App\Shop`) || strings.Contains(p, "src/Shop.php") && !strings.Contains(p, "Billing") {
		file, sym, name = "src/Shop.php", "greet", "Greeting"
	}
	switch {
	case strings.Contains(p, "You document the data flows"):
		d := kn.Description{Summary: "Creates invoices from orders and stores them.", Flows: []kn.Flow{{Name: name, Trigger: "POST /invoices",
			Steps: []kn.Step{{File: file, Symbol: sym, What: "validates the order data and stores the invoice"}}, SideEffects: []string{"INSERT invoices"}}},
			Entities: []kn.Entity{{Name: "Invoice", File: file, Description: "a bill"}}, External: []kn.External{{Name: "MySQL", Kind: "database"}}}
		b, _ := json.Marshal(d)
		t.Text("```json\n" + string(b) + "\n```")
	case strings.Contains(p, "You review a description"):
		i := strings.Index(p, "```json\n")
		j := strings.Index(p[i+8:], "\n```")
		var d kn.Description
		_ = json.Unmarshal([]byte(p[i+8:i+8+j]), &d)
		v, _ := json.Marshal(kn.Validation{Confidence: 0.82, Description: &d})
		t.Text("```json\n" + string(v) + "\n```")
	default:
		t.Text("## Overview\nCreates invoices from orders and stores them in the database.\n\n## Data flows\n### " + name + "\n**Trigger:** POST /invoices\n1. [" + file + ":4](" + file + "#L4) `" + sym + "` – validates and stores\n\n## Entities\n- **Invoice** ([" + file + "](" + file + ")): a bill\n\n## External\n- **MySQL** (database): invoices table\n")
	}
	return ""
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
				if strings.Contains(strings.ToLower(in), "greet") {
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
	fake := &acptest.Agent{Script: knowledgeAgent}
	envModules = modules.Options{Knowledge: knmod.Options{Start: fake.Start, LookPath: func(n string) (string, error) { return "/bin/" + n, nil },
		Executable: func() (string, error) { return "/usr/local/bin/tiegel", nil }}}
	t.Cleanup(func() { envModules = modules.Options{} })
	e := newEnvWith(t, true, map[string]string{
		"composer.json":           `{"autoload":{"psr-4":{"App\\":"src/"}}}`,
		"src/Billing/Invoice.php": "<?php\nnamespace App\\Billing;\nclass Invoice {\n  public function create(array $order) { return $order; }\n}\n",
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
	e.waitFor("knowledge status", func() bool { return k.Loaded && k.Status.Dir != "" && len(k.Units) == 3 })
	if k.Status.Run.Running || len(fake.Prompts()) != 0 {
		t.Fatal("a build started without a user action")
	}
	if !strings.HasPrefix(e.v.knwd.view.Summary, "No knowledge base yet") || e.v.knwd.view.Stale != 3 {
		t.Fatalf("%q %d", e.v.knwd.view.Summary, e.v.knwd.view.Stale)
	}
	e.shot("knowledge-empty")

	// Update shows the estimate first; nothing runs before Build.
	k.AskBuild(false)
	e.waitFor("estimate", func() bool { return k.Plan != nil })
	e.frame()
	if !strings.Contains(e.v.knwd.view.Confirm, "billed by your agent's provider") || len(fake.Prompts()) != 0 {
		t.Fatalf("%q", e.v.knwd.view.Confirm)
	}
	e.shot("knowledge-confirm")
	k.Build()
	e.waitFor("build", func() bool {
		return !k.Status.Run.Running && k.Status.Units == 3 && len(k.Units) == 3 && k.Units[0].MD != ""
	})
	e.waitFor("embedded", func() bool {
		k.Load()
		e.ctl.Settle(200e6)
		return k.Status.Embedding.Embedded > 0 && k.Status.Embedding.Embedded == k.Status.Embedding.Chunks
	})
	e.frame()
	if len(fake.Prompts()) != 9 {
		t.Fatal(len(fake.Prompts()))
	}
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
