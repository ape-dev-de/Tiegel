//go:build cgo

package syntax

import (
	"context"
	"strings"
	"testing"
)

// lineOf returns the zero-based line of the first occurrence of needle.
func lineOf(t *testing.T, src, needle string) int {
	t.Helper()
	i := strings.Index(src, needle)
	if i < 0 {
		t.Fatalf("needle %q not found", needle)
	}
	return strings.Count(src[:i], "\n")
}

func hasFold(folds []Range, start, end int) bool {
	for _, f := range folds {
		if f.StartLine == start && f.EndLine == end {
			return true
		}
	}
	return false
}

func TestPHPFolds(t *testing.T) {
	d := newDoc(t, LangPHP, phpSample)
	folds := d.Folds()
	l := func(n string) int { return lineOf(t, phpSample, n) }
	want := []Range{
		{l("use App\\Model\\User;"), l("use Psr\\Log")}, // use run
		{l("/**"), l(" */")},                            // block comment
		{l("{\n    public const"), l("}\n\ninterface")}, // class body
		{l("private array"), l("    ];")},               // array
		{l("    {\n        // line"), l("        return User") + 1},
	}
	for _, w := range want {
		if !hasFold(folds, w.StartLine, w.EndLine) {
			t.Errorf("missing fold %+v in %+v", w, folds)
		}
	}
	for i, f := range folds {
		if f.EndLine <= f.StartLine {
			t.Errorf("single-line fold %+v", f)
		}
		if i > 0 && folds[i-1].StartLine >= f.StartLine {
			t.Errorf("folds not sorted/unique: %+v", folds)
		}
	}
	// Line comments never fold; one-line constructs never fold.
	if hasFold(folds, l("// line"), l("// line")) {
		t.Error("line comment folded")
	}

	grp := newDoc(t, LangPHP, "<?php\nuse Foo\\{\n    A,\n    B,\n};\n")
	if !hasFold(grp.Folds(), 1, 4) {
		t.Errorf("use group: %+v", grp.Folds())
	}
}

func TestOtherFolds(t *testing.T) {
	j := newDoc(t, LangJSON, "{\n  \"a\": [\n    1,\n    2\n  ],\n  \"b\": {}\n}\n")
	if f := j.Folds(); !hasFold(f, 0, 6) || !hasFold(f, 1, 4) || len(f) != 2 {
		t.Errorf("json folds: %+v", f)
	}
	y := newDoc(t, LangYAML, "services:\n  app:\n    image: php\n    ports:\n      - 80\n      - 443\nvolumes: {}\n")
	if f := y.Folds(); !hasFold(f, 0, 5) || !hasFold(f, 1, 5) || !hasFold(f, 3, 5) {
		t.Errorf("yaml folds: %+v", f)
	}
	x := newDoc(t, LangXML, "<a>\n  <b>\n    x\n  </b>\n  <!--\n  c\n  -->\n</a>\n")
	if f := x.Folds(); !hasFold(f, 0, 7) || !hasFold(f, 1, 3) || !hasFold(f, 4, 6) {
		t.Errorf("xml folds: %+v", f)
	}
	e := newDoc(t, LangEnv, "A=1\nB=2\n")
	if e.Folds() != nil {
		t.Error("env has no folds")
	}
}

func TestOutline(t *testing.T) {
	d := newDoc(t, LangPHP, phpSample)
	syms := d.Outline()
	if len(syms) != 1 || syms[0].Kind != SymbolNamespace || syms[0].Name != `App\Service` {
		t.Fatalf("top level: %+v", syms)
	}
	ns := syms[0]
	var names []string
	for _, s := range ns.Children {
		names = append(names, s.Kind.String()+" "+s.Name)
	}
	got := strings.Join(names, ", ")
	want := "class UserService, interface Countable, trait T, enum Suit, function helper, function legacy"
	if got != want {
		t.Fatalf("namespace children:\n got %s\nwant %s", got, want)
	}
	if ns.EndByte < strings.Index(phpSample, "legacy") {
		t.Error("statement namespace should extend to its last child")
	}

	cls := ns.Children[0]
	var members []string
	for _, m := range cls.Children {
		members = append(members, m.Kind.String()+" "+m.Name+" ["+strings.Join(m.Modifiers, " ")+"] "+m.Detail)
	}
	wantMembers := []string{
		"constant LIMIT [public] int",
		"property $cache [private] array",
		"method __construct [public] (private readonly LoggerInterface $logger, public int $n = 0)",
		"property $logger [private readonly] LoggerInterface",
		"property $n [public] int",
		"method find [public static] (int $id): ?User",
	}
	if strings.Join(members, "\n") != strings.Join(wantMembers, "\n") {
		t.Fatalf("class members:\n%s", strings.Join(members, "\n"))
	}
	if cls.Modifiers[0] != "final" || cls.NameStart.Line != lineOf(t, phpSample, "final class") || cls.NameStart.Column != len("final class ") {
		t.Errorf("class: %+v", cls)
	}
	enum := ns.Children[3]
	if enum.Detail != "string" || len(enum.Children) != 2 || enum.Children[0].Kind != SymbolEnumCase || enum.Children[1].Name != "Spades" {
		t.Errorf("enum: %+v", enum)
	}
	if ns.Children[2].Children[0].Kind != SymbolProperty || ns.Children[2].Children[0].Name != "$p" {
		t.Errorf("trait: %+v", ns.Children[2])
	}
	if ns.Children[4].Detail != "(string $s): string" {
		t.Errorf("function detail %q", ns.Children[4].Detail)
	}

	braced := newDoc(t, LangPHP, `<?php
namespace A { class X { public function __construct(int $plain) {} } const C = 1; }
namespace B { interface Y {} }
namespace { function g() {} }
`)
	bs := braced.Outline()
	if len(bs) != 3 || bs[0].Name != "A" || len(bs[0].Children) != 2 || bs[0].Children[1].Kind != SymbolConstant || bs[1].Children[0].Kind != SymbolInterface {
		t.Fatalf("braced namespaces: %+v", bs)
	}
	if bs[2].Name != "" || bs[2].Children[0].Name != "g" {
		t.Fatalf("global namespace: %+v", bs[2])
	}
	if len(bs[0].Children[0].Children) != 1 {
		t.Fatalf("plain constructor parameters are not properties: %+v", bs[0].Children[0].Children)
	}

	hooks := newDoc(t, LangPHP, "<?php class H { public private(set) int $a = 1; abstract protected function f(); }")
	h := hooks.Outline()[0]
	if strings.Join(h.Children[0].Modifiers, " ") != "public private(set)" || strings.Join(h.Children[1].Modifiers, " ") != "abstract protected" {
		t.Fatalf("modifiers: %+v", h.Children)
	}

	if newDoc(t, LangJSON, "{}").Outline() != nil {
		t.Error("json has no outline")
	}
	for k := SymbolNamespace; k <= SymbolConstant; k++ {
		if k.String() == "unknown" {
			t.Errorf("kind %d has no name", k)
		}
	}
	if SymbolKind(99).String() != "unknown" {
		t.Error("unknown kind")
	}
}

func TestOutlineAfterReparse(t *testing.T) {
	d := newDoc(t, LangPHP, "<?php class A {}")
	if err := d.Reparse(context.Background(), []byte("<?php class B {} class C {}")); err != nil {
		t.Fatal(err)
	}
	if o := d.Outline(); len(o) != 2 || o[1].Name != "C" {
		t.Fatalf("%+v", o)
	}
}
