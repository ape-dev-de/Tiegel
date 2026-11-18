package phpgenerate

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ape-dev-de/tiegel/internal/api"
	"github.com/ape-dev-de/tiegel/internal/core/module"
	"github.com/ape-dev-de/tiegel/internal/index"
	"github.com/ape-dev-de/tiegel/internal/modules/modtest"
)

// fakeLSP provides lsp.codeActions like lang-php with a running server.
type fakeLSP struct{ acts []map[string]any }

func (f *fakeLSP) ID() string          { return "fake-lsp" }
func (f *fakeLSP) DependsOn() []string { return nil }
func (f *fakeLSP) Stop() error         { return nil }
func (f *fakeLSP) Start(ctx module.Context) error {
	return api.Provide(ctx, "lsp.codeActions", "fake", func(context.Context, json.RawMessage) ([]map[string]any, error) {
		return f.acts, nil
	})
}

const shop = `<?php

namespace App;

class Shop
{
    private string $name;
    private ?int $age = null;
    private bool $active = false;
    private static int $count = 0;
    public readonly string $id;

    public function getName(): string
    {
        return $this->name;
    }
}
`

func newApp(t *testing.T, extra ...module.Module) *api.App {
	t.Helper()
	if !index.TreeSitter {
		t.Skip("needs Tree-sitter")
	}
	return modtest.NewApp(t, append([]module.Module{New()}, extra...), nil)
}

func TestList(t *testing.T) {
	lsp := &fakeLSP{acts: []map[string]any{
		{"id": "1", "title": "Implement contracts", "kind": "quickfix"},
		{"id": "2", "title": "Import class", "kind": "quickfix"},
		{"id": "3", "title": "Generate accessors", "kind": "refactor"},
		{"id": "4", "title": "Fill object", "kind": "source.generate.fill"},
		{"id": "5", "title": "Extract method", "kind": "refactor.extract"},
	}}
	a := newApp(t, lsp)
	modtest.Project(t, a, map[string]string{"src/Shop.php": shop})
	var res ListResult
	modtest.Call(t, a, "generate.list", ListParams{Path: "src/Shop.php", Pos: api.Pos{Line: 7, Col: 5}}, &res)
	if res.Class != `App\Shop` || len(res.Properties) != 5 {
		t.Fatalf("%+v", res)
	}
	var titles []string
	byKind := map[string]GenerateAction{}
	for _, act := range res.Actions {
		titles = append(titles, act.Source+":"+act.Title)
		byKind[act.Kind] = act
	}
	if got := strings.Join(titles, ","); got != "lsp:Implement contracts,lsp:Generate accessors,lsp:Fill object,builtin:Constructor,builtin:Getters,builtin:Setters,builtin:Getters and Setters" {
		t.Fatalf("actions %s", got)
	}
	if got := strings.Join(byKind[KindConstructor].Properties, ","); got != "name,age,active,id" {
		t.Fatalf("constructor %s", got)
	}
	if got := strings.Join(byKind[KindGetters].Properties, ","); got != "age,active,count,id" {
		t.Fatalf("getters %s", got)
	}
	if got := strings.Join(byKind[KindSetters].Properties, ","); got != "name,age,active,count" {
		t.Fatalf("setters %s", got)
	}
	if got := strings.Join(byKind[KindGettersSetters].Properties, ","); got != "age,active,count" {
		t.Fatalf("both %s", got)
	}
	modtest.Call(t, a, "generate.list", ListParams{Path: "src/Shop.php", Pos: api.Pos{Line: 7, Col: 5}, NoLSP: true}, &res)
	if res.Actions[0].Source != "builtin" {
		t.Fatal("noLsp")
	}
	modtest.WantCode(t, a, "generate.list", ListParams{Path: "src/Shop.php", Pos: api.Pos{Line: 1}}, api.CodeNotFound)
	modtest.WantCode(t, a, "generate.list", ListParams{}, api.CodeInvalidParams)
}

func TestListWithoutServer(t *testing.T) {
	a := newApp(t)
	modtest.Project(t, a, map[string]string{"I.php": "<?php\ninterface I\n{\n    public function x(): void;\n}\n"})
	var res ListResult
	modtest.Call(t, a, "generate.list", ListParams{Path: "I.php", Pos: api.Pos{Line: 3}}, &res)
	if res.LSP == "" || len(res.Actions) != 4 {
		t.Fatalf("%+v", res)
	}
	for _, act := range res.Actions {
		if act.Disabled == "" {
			t.Fatalf("enabled in an interface: %+v", act)
		}
	}
}

func TestApply(t *testing.T) {
	a := newApp(t)
	root := modtest.Project(t, a, map[string]string{"src/Shop.php": shop})
	file := filepath.Join(root, "src/Shop.php")
	modtest.Call(t, a, "doc.open", map[string]any{"path": file}, nil)
	var res ApplyResult
	modtest.Call(t, a, "generate.apply", ApplyParams{Path: file, Pos: api.Pos{Line: 7}, Kind: KindConstructor, Properties: []string{"name", "$id"}}, &res)
	wantCtor := "    public readonly string $id;\n\n    public function __construct(string $name, string $id)\n    {\n        $this->name = $name;\n        $this->id = $id;\n    }\n\n    public function getName()"
	var doc struct{ Text string }
	modtest.Call(t, a, "doc.text", map[string]any{"path": file}, &doc)
	if !strings.Contains(doc.Text, wantCtor) || !res.Applied || res.Version == 0 {
		t.Fatalf("constructor\n%s", doc.Text)
	}
	if !strings.HasPrefix(doc.Text[res.Caret:], "        $this->name") {
		t.Fatalf("caret %q", doc.Text[res.Caret:res.Caret+20])
	}
	// One undo step.
	modtest.Call(t, a, "doc.undo", map[string]any{"path": file}, nil)
	modtest.Call(t, a, "doc.text", map[string]any{"path": file}, &doc)
	if doc.Text != shop {
		t.Fatal("undo")
	}
	modtest.Call(t, a, "generate.apply", ApplyParams{Path: file, Pos: api.Pos{Line: 7}, Kind: KindGettersSetters}, &res)
	modtest.Call(t, a, "doc.text", map[string]any{"path": file}, &doc)
	for _, want := range []string{
		"        return $this->name;\n    }\n\n    public function getAge(): ?int\n    {\n        return $this->age;\n    }\n\n    public function setAge(?int $age): void\n    {\n        $this->age = $age;\n    }",
		"    public function isActive(): bool\n",
		"    public static function getCount(): int\n    {\n        return self::$count;\n    }\n\n    public static function setCount(int $count): void\n    {\n        self::$count = $count;\n    }\n}\n",
	} {
		if !strings.Contains(doc.Text, want) {
			t.Fatalf("accessors: missing %q in\n%s", want, doc.Text)
		}
	}
	if strings.Join(res.Methods, ",") != "getAge,setAge,isActive,setActive,getCount,setCount" {
		t.Fatalf("methods %v", res.Methods)
	}
	modtest.WantCode(t, a, "generate.apply", ApplyParams{Path: file, Pos: api.Pos{Line: 7}, Kind: KindGetters, Properties: []string{"name"}}, api.CodeInvalidParams)
	modtest.WantCode(t, a, "generate.apply", ApplyParams{Path: file, Pos: api.Pos{Line: 7}, Kind: "toString"}, api.CodeInvalidParams)
}

func TestApplyOneLineBodyAndTabs(t *testing.T) {
	a := newApp(t)
	root := modtest.Project(t, a, map[string]string{
		"P.php": "<?php\nfinal class P { public function __construct(private int $x) {} }\n",
		"E.php": "<?php\nclass E\n{\n}\n",
		"F.php": "<?php\nclass F\n{\n    const A = 1;\n}\n",
	})
	modtest.Call(t, a, "settings.set", map[string]any{"key": "editor.insertSpaces", "value": false}, nil)
	var res ApplyResult
	modtest.WantCode(t, a, "generate.apply", ApplyParams{Path: "P.php", Pos: api.Pos{Line: 2}, Kind: KindConstructor}, api.CodeNotFound)
	modtest.Call(t, a, "generate.apply", ApplyParams{Path: "P.php", Pos: api.Pos{Line: 2}, Kind: KindGetters}, &res)
	b, _ := os.ReadFile(filepath.Join(root, "P.php"))
	if string(b) != "<?php\nfinal class P { public function __construct(private int $x) {} \n\tpublic function getX(): int\n\t{\n\t\treturn $this->x;\n\t}\n}\n" {
		t.Fatalf("one line %q", b)
	}
	// An empty class gets no constructor (nothing to initialise); an
	// untyped property gets untyped accessors.
	modtest.WantCode(t, a, "generate.apply", ApplyParams{Path: "E.php", Pos: api.Pos{Line: 3}, Kind: KindConstructor}, api.CodeNotFound)
	modtest.WantCode(t, a, "generate.apply", ApplyParams{Path: "F.php", Pos: api.Pos{Line: 3}, Kind: KindGetters}, api.CodeNotFound)
	modtest.WriteFile(t, filepath.Join(root, "G.php"), "<?php\nclass G\n{\n    var $v;\n}\n")
	f := false
	modtest.Call(t, a, "generate.apply", ApplyParams{Path: "G.php", Pos: api.Pos{Line: 3}, Kind: KindGetters, Apply: &f}, &res)
	if res.Applied || !strings.Contains(res.Text, "public function getV()\n") {
		t.Fatalf("apply false %+v", res)
	}
	modtest.Call(t, a, "generate.apply", ApplyParams{Path: "G.php", Pos: api.Pos{Line: 3}, Kind: KindConstructor}, &res)
	b, _ = os.ReadFile(filepath.Join(root, "G.php"))
	if string(b) != "<?php\nclass G\n{\n    var $v;\n\n\tpublic function __construct($v)\n\t{\n\t\t$this->v = $v;\n\t}\n}\n" {
		t.Fatalf("untyped %q", b)
	}
}
