package controller

import (
	"encoding/json"
	"errors"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/ape-dev-de/tiegel/internal/api"
)

func TestLoadExtract(t *testing.T) {
	f := projectFake()
	c, n := navStarted(t, f)
	file := filepath.Join(root, "src/Mailer.php")
	n.LoadExtract(file, 1, 4, ExtractVariable)
	if !strings.Contains(c.Status.Notice, "needs a language server") {
		t.Fatalf("no server: %q", c.Status.Notice)
	}
	c.Status.LSPRunning = true
	f.answer("lsp.codeActions", []CodeAction{{ID: "1", Title: "Extract method"}, {ID: "2", Title: "Extract expression"}, {ID: "3", Title: "Extract constant", Preferred: true}})
	n.LoadExtract(file, 1, 4, ExtractVariable)
	c.Settle(time.Second)
	if n.Actions == nil || len(n.Actions.Items) != 2 || n.Actions.Items[0].ID != "3" {
		t.Fatalf("variable %+v", n.Actions)
	}
	p := navParamsOf(f, "lsp.codeActions")
	if only, _ := p["only"].([]any); len(only) != 1 || only[0] != "refactor.extract" || p["end"] == nil {
		t.Fatalf("params %v", p)
	}
	n.LoadExtract(file, 1, 1, ExtractMethod)
	c.Settle(time.Second)
	if len(n.Actions.Items) != 1 || n.Actions.Items[0].ID != "1" {
		t.Fatalf("method %+v", n.Actions.Items)
	}
	// Titles naming neither: all are offered.
	if got := filterExtract([]CodeAction{{Title: "Extract to file"}}, ExtractMethod); len(got) != 1 {
		t.Fatalf("fallback %+v", got)
	}
	f.answer("lsp.codeActions", []CodeAction{})
	n.LoadExtract(file, 1, 4, ExtractMethod)
	c.Settle(time.Second)
	if !strings.Contains(c.Status.Notice, "offers no extract method") {
		t.Fatalf("none: %q", c.Status.Notice)
	}
	f.on("lsp.codeActions", func(json.RawMessage) (any, error) { return nil, errors.New("boom") })
	n.LoadExtract(file, 1, 4, ExtractMethod)
	c.Settle(time.Second)
	if c.Status.Notice != "boom" {
		t.Fatalf("error: %q", c.Status.Notice)
	}
}

func TestTemplatesController(t *testing.T) {
	f := projectFake()
	c, _ := navStarted(t, f)
	f.answer("templates.list", map[string]any{"templates": []TemplateItem{{Abbreviation: "fore"}, {Abbreviation: "if", Surround: true}}})
	c.LoadTemplates()
	c.Settle(time.Second)
	if !c.Templates.IsAbbreviation("fore") || c.Templates.IsAbbreviation("nope") {
		t.Fatalf("abbreviations %+v", c.Templates)
	}
	var got []TemplateItem
	c.TemplatesAt("/p/a.php", 3, true, func(ts []TemplateItem) { got = ts })
	c.Settle(time.Second)
	if len(got) != 2 || navParamsOf(f, "templates.list")["surround"] != true {
		t.Fatalf("at %+v", got)
	}
	f.answer("templates.list", map[string]any{"templates": []TemplateItem{}})
	c.TemplatesAt("/p/a.php", 3, false, func([]TemplateItem) { t.Fatal("empty list chosen") })
	c.Settle(time.Second)
	if !strings.Contains(c.Status.Notice, "No live templates") {
		t.Fatalf("notice %q", c.Status.Notice)
	}
	f.answer("templates.expand", map[string]any{"abbreviation": "fore", "caret": 9, "version": 4, "applied": true})
	c.ExpandTemplate("/p/a.php", 4, 4, "", "clip", nil)
	c.Settle(time.Second)
	if ex := c.Templates.Expanded; ex == nil || ex.File != "/p/a.php" || ex.Caret != 9 || c.Templates.ExpandedVersion != 1 {
		t.Fatalf("expanded %+v", ex)
	}
	if p := navParamsOf(f, "templates.expand"); p["clipboard"] != "clip" || p["abbreviation"] != nil {
		t.Fatalf("params %v", p)
	}
	f.on("templates.expand", func(json.RawMessage) (any, error) { return nil, api.Errorf(api.CodeNotFound, "no template") })
	fell := false
	c.ExpandTemplate("/p/a.php", 4, 6, "if", "", func() { fell = true })
	c.Settle(time.Second)
	if !fell {
		t.Fatal("fallback not run")
	}
	c.ExpandTemplate("/p/a.php", 4, 6, "if", "", nil)
	c.Settle(time.Second)
	if c.Status.Notice != "no template" {
		t.Fatalf("notice %q", c.Status.Notice)
	}
}

// The New PHP Class kinds are the file templates of the backend.
func TestPHPClassKindsMatchTemplates(t *testing.T) {
	if !slices.Equal(PHPClassKinds, api.FileTemplateKinds()) {
		t.Fatalf("%v vs %v", PHPClassKinds, api.FileTemplateKinds())
	}
	if PHPClassKindLabel("test") != "Test" {
		t.Fatal(PHPClassKindLabel("test"))
	}
}

func TestGenerateController(t *testing.T) {
	f := projectFake()
	c, _ := navStarted(t, f)
	f.answer("generate.list", map[string]any{"class": "A", "actions": []GenerateAction{{Source: "lsp", ID: "7", Title: "Implement"}, {Source: "builtin", Kind: "getters", Title: "Getters", Properties: []string{"a"}}}})
	var menu GenerateMenu
	c.LoadGenerate("/p/A.php", 12, func(m GenerateMenu) { menu = m })
	c.Settle(time.Second)
	if menu.File != "/p/A.php" || menu.Offset != 12 || len(menu.Actions) != 2 || navParamsOf(f, "generate.list")["noLsp"] != true {
		t.Fatalf("menu %+v", menu)
	}
	f.answer("lsp.applyCodeAction", map[string]any{"applied": true})
	c.RunGenerate(menu, menu.Actions[0], nil)
	c.Settle(time.Second)
	if navParamsOf(f, "lsp.applyCodeAction")["id"] != "7" || c.Status.Notice != "Applied: Implement" {
		t.Fatalf("lsp %q", c.Status.Notice)
	}
	f.answer("generate.apply", map[string]any{"methods": []string{"getA"}, "caret": 40})
	c.RunGenerate(menu, menu.Actions[1], []string{"a"})
	c.Settle(time.Second)
	if c.Status.Notice != "Generated getA" || c.Reveal == nil || c.Reveal.Range.Start.Offset != 40 {
		t.Fatalf("builtin %q %+v", c.Status.Notice, c.Reveal)
	}
	if p := navParamsOf(f, "generate.apply"); p["kind"] != "getters" || len(p["properties"].([]any)) != 1 {
		t.Fatalf("params %v", p)
	}
	c.RunGenerate(menu, GenerateAction{Title: "Constructor", Disabled: "has one"}, nil)
	if c.Status.Notice != "Constructor: has one" {
		t.Fatalf("disabled %q", c.Status.Notice)
	}
	f.on("generate.list", func(json.RawMessage) (any, error) { return nil, errors.New("no class") })
	c.LoadGenerate("/p/A.php", 1, func(GenerateMenu) { t.Fatal("menu on error") })
	c.Settle(time.Second)
	if c.Status.Notice != "no class" {
		t.Fatalf("error %q", c.Status.Notice)
	}
}
