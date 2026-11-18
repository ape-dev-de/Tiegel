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

// A constructor for a class written on one line (or with members sharing
// a line) goes inside the class body, before the methods.
func TestApplyConstructorOneLineClass(t *testing.T) {
	a := newApp(t)
	const ctorXY = "\n    public function __construct(int $x, int $y)\n    {\n        $this->x = $x;\n        $this->y = $y;\n    }\n"
	const ctorX = "\n    public function __construct(int $x)\n    {\n        $this->x = $x;\n    }\n"
	cases := []struct{ name, src, want string }{
		{"Point.php", "<?php\nfinal class Point { private int $x; private int $y; }\n",
			"<?php\nfinal class Point { private int $x; private int $y; " + ctorXY + "}\n"},
		{"Abs.php", "<?php\nabstract class Abs { protected int $x; }\n",
			"<?php\nabstract class Abs { protected int $x; " + ctorX + "}\n"},
		{"Ro.php", "<?php\nreadonly class Ro { public int $x; }\n",
			"<?php\nreadonly class Ro { public int $x; " + ctorX + "}\n"},
		{"Tr.php", "<?php\ntrait Tr { private int $x; }\n",
			"<?php\ntrait Tr { private int $x; " + ctorX + "}\n"},
		{"Ns.php", "<?php\nnamespace N {\n    class Ns { private int $x; }\n}\n",
			"<?php\nnamespace N {\n    class Ns { private int $x; \n        public function __construct(int $x)\n        {\n            $this->x = $x;\n        }\n    }\n}\n"},
		// A method on the same line: the constructor goes before it.
		{"M.php", "<?php\nclass M { private int $x; public function f(): int { return 1; } }\n",
			"<?php\nclass M { private int $x; " + ctorX + "public function f(): int { return 1; } }\n"},
		// A multi-line method starting on the property's line.
		{"ML.php", "<?php\nclass ML\n{\n    private int $x; public function f(): int {\n        return 1;\n    }\n}\n",
			"<?php\nclass ML\n{\n    private int $x; " + ctorX + "public function f(): int {\n        return 1;\n    }\n}\n"},
		// Properties only after a method on the class line.
		{"After.php", "<?php\nclass After { public function f() {} private int $x; }\n",
			"<?php\nclass After { " + ctorX + "public function f() {} private int $x; }\n"},
	}
	files := map[string]string{"En.php": "<?php\nenum En { case A; }\n"}
	for _, c := range cases {
		files[c.name] = c.src
	}
	root := modtest.Project(t, a, files)
	for _, c := range cases {
		line := 2
		if c.name == "Ns.php" {
			line = 3
		}
		var res ApplyResult
		modtest.Call(t, a, "generate.apply", ApplyParams{Path: c.name, Pos: api.Pos{Line: line, Col: 10}, Kind: KindConstructor}, &res)
		b, _ := os.ReadFile(filepath.Join(root, c.name))
		if string(b) != c.want {
			t.Errorf("%s:\n got %q\nwant %q", c.name, b, c.want)
			continue
		}
		// The index sees the constructor inside the class.
		err := modtest.WantCode(t, a, "generate.apply", ApplyParams{Path: c.name, Pos: api.Pos{Line: line, Col: 10}, Kind: KindConstructor}, api.CodeNotFound)
		if !strings.Contains(err.Error(), "has a constructor") {
			t.Errorf("%s: %v", c.name, err)
		}
	}
	modtest.WantCode(t, a, "generate.apply", ApplyParams{Path: "En.php", Pos: api.Pos{Line: 2, Col: 8}, Kind: KindConstructor}, api.CodeNotFound)
}

func TestUpperFirstInvalidUTF8(t *testing.T) {
	for in, want := range map[string]string{"": "", "x": "X", "äb": "Äb", "\xff": "\xff", "\xe4b": "\xe4b"} {
		if got := upperFirst(in); got != want {
			t.Errorf("upperFirst(%q) = %q, want %q", in, got, want)
		}
	}
	if got := getterName(Property{Name: "\xe4"}); got != "get\xe4" {
		t.Errorf("getterName = %q", got)
	}
}
