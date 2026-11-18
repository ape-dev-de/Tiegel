package livetemplates

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ape-dev-de/tiegel/internal/api"
	"github.com/ape-dev-de/tiegel/internal/core/module"
	"github.com/ape-dev-de/tiegel/internal/index"
	"github.com/ape-dev-de/tiegel/internal/modules/modtest"
)

func newApp(t *testing.T) (*api.App, *Module) {
	t.Helper()
	m := New()
	m.now = func() time.Time { return time.Date(2026, 11, 18, 9, 5, 7, 0, time.UTC) }
	a := modtest.NewApp(t, []module.Module{m}, nil)
	return a, m
}

func setSetting(t *testing.T, a *api.App, key string, v any) {
	t.Helper()
	raw, _ := json.Marshal(v)
	modtest.Call(t, a, "settings.set", map[string]any{"key": key, "value": json.RawMessage(raw)}, nil)
}

// at returns src without "|" and the offset of the marker.
func at(src string) (string, int) {
	i := strings.Index(src, "|")
	return strings.Replace(src, "|", "", 1), i
}

func TestBuiltinsValid(t *testing.T) {
	seen := map[string]bool{}
	for _, tp := range Builtin() {
		if err := tp.Validate(); err != nil {
			t.Error(err)
		}
		if seen[tp.Abbreviation] {
			t.Errorf("duplicate %s", tp.Abbreviation)
		}
		seen[tp.Abbreviation] = true
		// Round trip through the user line format.
		back, err := ParseUser(FormatUser(tp))
		if err != nil || back.Body != tp.Body || len(back.Vars) != len(tp.Vars) {
			t.Errorf("%s: round trip %v %q", tp.Abbreviation, err, FormatUser(tp))
		}
	}
	for _, want := range []string{"fore", "forek", "if", "try", "pubf", "prif", "construct", "test"} {
		if !seen[want] {
			t.Errorf("missing built-in %s", want)
		}
	}
}

func TestParseUserErrors(t *testing.T) {
	for _, line := range []string{
		"x | php | only three",
		"bad abbr | php |  | | body",
		"x | nowhere |  | | body",
		"x | php |  | | ",
		"x | php |  | A=unknown() | $A$",
		"x | php |  | A | $A$",
		`x | php |  | A=date(Y) | $A$`,
		`x | php |  | A=className(x) | $A$`,
		`x | php |  | A=camelCase() | $A$`,
		`x | php |  | A="unterminated | $A$`,
	} {
		if _, err := ParseUser(line); err == nil {
			t.Errorf("%q accepted", line)
		}
	}
	tp, err := ParseUser(`dd | php-statement, php-member | dump | A="x;y"; B=camelCase(A)! | dd($A$, $B$);\n\t$END$ \x`)
	if err != nil {
		t.Fatal(err)
	}
	if tp.Body != "dd($A$, $B$);\n\t$END$ \\x" || tp.Vars["A"].Expr != `"x;y"` || !tp.Vars["B"].Skip || len(tp.Contexts) != 2 {
		t.Fatalf("%+v", tp)
	}
}

func TestExpressions(t *testing.T) {
	p := position{file: "/p/src/Order.php", line: 3, class: "Order", classFQN: `App\Order`, method: "total", namespace: "App",
		clipboard: "clip", now: time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)}
	vars := map[string]string{"N": "order_item-name"}
	for _, tc := range []struct{ expr, want string }{
		{`"lit"`, "lit"}, {"className()", "Order"}, {"classFQN()", `App\Order`}, {"methodName()", "total"},
		{"functionName()", "total"}, {"namespace()", "App"}, {"fileName()", "Order.php"},
		{"fileNameWithoutExtension()", "Order"}, {"date()", "2026-01-02"}, {`date("d.m.y G:i:s \\Y n/j")`, "02.01.26 3:04:05 Y 1/2"},
		{"time()", "03:04"}, {"clipboard()", "clip"}, {"lineNumber()", "3"},
		{"capitalize(N)", "Order_item-name"}, {"camelCase(N)", "orderItemName"}, {"snakeCase(N)", "order_item_name"},
		{"decapitalize(X)", "x"}, {"snakeCase(X)", "x"}, {"camelCase(M)", "m"},
	} {
		e, err := parseExpr(tc.expr)
		if err != nil {
			t.Errorf("%s: %v", tc.expr, err)
			continue
		}
		if got, _ := e.eval(p, vars); got != tc.want {
			t.Errorf("%s = %q, want %q", tc.expr, got, tc.want)
		}
	}
	t.Setenv("USER", "dev")
	if e, _ := parseExpr("user()"); func() string { v, _ := e.eval(p, nil); return v }() != "dev" {
		t.Error("user()")
	}
	e, _ := parseExpr("functionName()")
	if v, _ := e.eval(position{function: "f"}, nil); v != "f" {
		t.Error("functionName() of a function")
	}
	if v, ok := (expr{}).eval(p, nil); ok || v != "" {
		t.Error("empty expression")
	}
	if got := words("HTTPServerName"); len(got) != 2 {
		t.Errorf("words %v", got)
	}
}

func TestApplies(t *testing.T) {
	tp := func(ctx ...string) Template { return Template{Contexts: ctx} }
	cases := []struct {
		t    Template
		pos  string
		want bool
	}{
		{tp(CtxAny), posHTML, true}, {tp(CtxOther), posOther, true}, {tp(CtxOther), posStatement, false},
		{tp(CtxPHP), posMember, true}, {tp(CtxPHP), posComment, false}, {tp(CtxPHPStatement), posExpression, false},
		{tp(CtxPHPExpression), posStatement, true}, {tp(CtxPHPMember), posUnknown, true}, {tp(CtxHTML), posHTML, true},
		{tp(CtxPHPComment), posComment, true}, {tp(CtxPHPExpression), posString, false},
	}
	for _, c := range cases {
		if got := applies(c.t, c.pos); got != c.want {
			t.Errorf("%v at %s = %v", c.t.Contexts, c.pos, got)
		}
	}
}

func TestList(t *testing.T) {
	a, _ := newApp(t)
	root := modtest.Project(t, a, map[string]string{"src/A.php": "<?php\nclass A {\n    \n}\n"})
	setSetting(t, a, SettingUser, []string{"dd | php-statement | dump and die |  | dd($SELECTION$$END$);", "broken line", "fore | php-statement | my fore |  | foreach ($END$)"})
	setSetting(t, a, SettingDisabled, []string{"prif"})
	var res ListResult
	modtest.Call(t, a, "templates.list", ListParams{}, &res)
	if len(res.Problems) != 1 || res.Problems[0].Line != 2 {
		t.Fatalf("problems %+v", res.Problems)
	}
	byAbbr := map[string]TemplateInfo{}
	for _, tp := range res.Templates {
		byAbbr[tp.Abbreviation] = tp
	}
	if byAbbr["dd"].Source != SourceUser || !byAbbr["dd"].Surround || byAbbr["fore"].Description != "my fore" {
		t.Fatalf("user templates %+v", byAbbr)
	}
	if _, ok := byAbbr["prif"]; ok {
		t.Fatal("disabled built-in listed")
	}
	modtest.Call(t, a, "templates.list", ListParams{All: true, Surround: true}, &res)
	for _, tp := range res.Templates {
		if !tp.Surround {
			t.Fatalf("not a surround template: %s", tp.Abbreviation)
		}
	}
	if !index.TreeSitter {
		return
	}
	modtest.Call(t, a, "templates.list", ListParams{Path: filepath.Join(root, "src/A.php"), Pos: &api.Pos{Line: 3, Col: 5}}, &res)
	if res.Context != posMember {
		t.Fatalf("context %q", res.Context)
	}
	for _, tp := range res.Templates {
		if tp.Abbreviation == "fore" || tp.Abbreviation == "prif" {
			t.Fatalf("%s offered in a class body", tp.Abbreviation)
		}
	}
	modtest.WantCode(t, a, "templates.list", ListParams{Path: "missing.php"}, api.CodeNotFound)
	modtest.WantCode(t, a, "templates.list", ListParams{Path: "src/A.php", Pos: &api.Pos{Offset: 999}}, api.CodeInvalidParams)
}

func TestExpandTab(t *testing.T) {
	if !index.TreeSitter {
		t.Skip("needs Tree-sitter")
	}
	a, _ := newApp(t)
	src, off := at("<?php\nnamespace App;\n\nclass Order\n{\n    public function total()\n    {\n        fore|\n    }\n}\n")
	root := modtest.Project(t, a, map[string]string{"src/Order.php": src})
	file := filepath.Join(root, "src/Order.php")
	modtest.Call(t, a, "doc.open", map[string]any{"path": file}, nil)
	var res ExpandResult
	modtest.Call(t, a, "templates.expand", ExpandParams{Path: file, Pos: api.Pos{Offset: off}}, &res)
	want := "foreach ($items as $item) {\n            \n        }"
	if res.Text != want || res.Abbreviation != "fore" || res.Context != posStatement || !res.Applied || res.Version == 0 {
		t.Fatalf("%+v", res)
	}
	var doc struct{ Text string }
	modtest.Call(t, a, "doc.text", map[string]any{"path": file}, &doc)
	if !strings.Contains(doc.Text, "        "+want+"\n") {
		t.Fatalf("doc %q", doc.Text)
	}
	if len(res.Fields) != 2 || doc.Text[res.Fields[0].Ranges[0].Start:res.Fields[0].Ranges[0].End] != "items" ||
		doc.Text[res.Fields[1].Ranges[0].Start:res.Fields[1].Ranges[0].End] != "item" {
		t.Fatalf("fields %+v", res.Fields)
	}
	if !strings.HasSuffix(doc.Text[:res.Caret], "{\n            ") {
		t.Fatalf("caret %d", res.Caret)
	}
	// One undo step removes the expansion and restores the abbreviation.
	modtest.Call(t, a, "doc.undo", map[string]any{"path": file}, nil)
	modtest.Call(t, a, "doc.text", map[string]any{"path": file}, &doc)
	if doc.Text != src {
		t.Fatalf("after undo %q", doc.Text)
	}
	// Class member context: methods with the class name expression.
	setSetting(t, a, SettingUser, []string{`mk | php-member | factory | C=className()!; N=camelCase(C) | public static function create$N$(): self { return new $C$(); }`})
	src2, off2 := at("<?php\nnamespace App;\nclass Order\n{\n    mk|\n}\n")
	modtest.WriteFile(t, filepath.Join(root, "src/B.php"), src2)
	modtest.Call(t, a, "templates.expand", ExpandParams{Path: "src/B.php", Pos: api.Pos{Offset: off2}}, &res)
	if res.Text != "public static function createorder(): self { return new Order(); }" || res.Context != posMember || len(res.Fields) != 1 {
		t.Fatalf("member %+v", res)
	}
	data, _ := os.ReadFile(filepath.Join(root, "src/B.php"))
	if !strings.Contains(string(data), "    public static function createorder()") {
		t.Fatalf("closed file not written: %q", data)
	}
	// Not applicable: fore in a class body.
	src3, off3 := at("<?php\nclass A {\n    fore|\n}\n")
	modtest.WriteFile(t, filepath.Join(root, "src/C.php"), src3)
	err := modtest.WantCode(t, a, "templates.expand", ExpandParams{Path: "src/C.php", Pos: api.Pos{Offset: off3}}, api.CodeNotFound)
	if !strings.Contains(err.Error(), "does not apply") {
		t.Fatal(err)
	}
	// Unknown abbreviation, no word, missing path.
	src4, off4 := at("<?php\nnope|\n")
	modtest.WriteFile(t, filepath.Join(root, "src/D.php"), src4)
	modtest.WantCode(t, a, "templates.expand", ExpandParams{Path: "src/D.php", Pos: api.Pos{Offset: off4}}, api.CodeNotFound)
	modtest.WantCode(t, a, "templates.expand", ExpandParams{Path: "src/D.php", Pos: api.Pos{Offset: 0}}, api.CodeNotFound)
	modtest.WantCode(t, a, "templates.expand", ExpandParams{}, api.CodeInvalidParams)
	modtest.WantCode(t, a, "templates.expand", ExpandParams{Path: "src/D.php", Pos: api.Pos{Offset: 0}, End: &api.Pos{Offset: 3}}, api.CodeInvalidParams)
}

func TestExpandSurroundAndInsert(t *testing.T) {
	if !index.TreeSitter {
		t.Skip("needs Tree-sitter")
	}
	a, _ := newApp(t)
	src := "<?php\nfunction f() {\n    a();\n    if ($x) {\n        b();\n    }\n}\n"
	root := modtest.Project(t, a, map[string]string{"f.php": src})
	start := strings.Index(src, "    a();")
	end := strings.Index(src, "}\n}") + 2
	f := false
	var res ExpandResult
	modtest.Call(t, a, "templates.expand", ExpandParams{Path: "f.php", Pos: api.Pos{Offset: start}, End: &api.Pos{Offset: end}, Abbreviation: "try", Apply: &f}, &res)
	want := "try {\n        a();\n        if ($x) {\n            b();\n        }\n    } catch (\\Throwable $e) {\n        \n    }"
	if res.Text != want || res.Start != start+4 || res.End != end-1 || res.Applied {
		t.Fatalf("surround %+v", res)
	}
	data, _ := os.ReadFile(filepath.Join(root, "f.php"))
	if string(data) != src {
		t.Fatal("apply:false wrote the file")
	}
	// Insert a chosen template at the caret with values and clipboard;
	// tabs as configured.
	setSetting(t, a, "editor.insertSpaces", false)
	setSetting(t, a, SettingUser, []string{`cl | any | clipboard |  | x($CLIP$);\n\t$END$`, `cl2 | any | clipboard | CLIP=clipboard()! | x($CLIP$);`})
	off := strings.Index(src, "a();")
	modtest.Call(t, a, "templates.expand", ExpandParams{Path: "f.php", Pos: api.Pos{Offset: off}, Abbreviation: "cl", Values: map[string]string{"CLIP": "v"}, Apply: &f}, &res)
	if res.Text != "x(v);\n    \t" || len(res.Fields) != 1 {
		t.Fatalf("insert %+v", res)
	}
	modtest.Call(t, a, "templates.expand", ExpandParams{Path: "f.php", Pos: api.Pos{Offset: off}, Abbreviation: "cl2", Clipboard: "pasted", Apply: &f}, &res)
	if res.Text != "x(pasted);" || len(res.Fields) != 0 || res.Caret != off+len(res.Text) {
		t.Fatalf("clipboard %+v", res)
	}
	// HTML context outside the PHP tags; other files.
	modtest.WriteFile(t, filepath.Join(root, "v.php"), "<div>php</div>")
	modtest.Call(t, a, "templates.expand", ExpandParams{Path: "v.php", Pos: api.Pos{Offset: 8}}, &res)
	if res.Text != "<?php  ?>" || res.Caret != 11 || res.Context != posHTML {
		t.Fatalf("html %+v", res)
	}
	modtest.WriteFile(t, filepath.Join(root, "notes.txt"), "cl")
	modtest.Call(t, a, "templates.expand", ExpandParams{Path: "notes.txt", Pos: api.Pos{Offset: 2}}, &res)
	if res.Context != posOther || !strings.HasPrefix(res.Text, "x(") {
		t.Fatalf("other %+v", res)
	}
	// A stale version is rejected.
	modtest.Call(t, a, "doc.open", map[string]any{"path": "f.php"}, nil)
	v := uint64(99)
	modtest.WantCode(t, a, "templates.expand", ExpandParams{Path: "f.php", Pos: api.Pos{Offset: off}, Abbreviation: "cl", Version: &v}, api.CodeConflict)
}

func TestModuleDisabled(t *testing.T) {
	a, _ := newApp(t)
	modtest.Call(t, a, "modules.disable", map[string]any{"id": ID}, nil)
	modtest.WantCode(t, a, "templates.list", nil, api.CodeModuleDisabled)
}

// Abbreviations that are PHP keywords parse as keyword tokens.
func TestExpandKeywordAbbreviation(t *testing.T) {
	if !index.TreeSitter {
		t.Skip("needs Tree-sitter")
	}
	a, _ := newApp(t)
	modtest.Project(t, a, map[string]string{"k.php": "<?php\nfunction f() {\n    if\n}\n"})
	f := false
	var res ExpandResult
	modtest.Call(t, a, "templates.expand", ExpandParams{Path: "k.php", Pos: api.Pos{Line: 3, Col: 7}, Apply: &f}, &res)
	if res.Abbreviation != "if" || res.Context != posStatement || !strings.HasPrefix(res.Text, "if (CONDITION) {") {
		t.Fatalf("%+v", res)
	}
}
