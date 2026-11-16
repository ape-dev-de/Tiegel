package syntax

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"math/rand"
	"strings"
	"sync"
	"testing"
)

func newDoc(t testing.TB, lang Language, src string) *Document {
	t.Helper()
	d, err := NewDocument(context.Background(), lang, []byte(src))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(d.Close)
	return d
}

// roleAt returns the role of the first occurrence of needle in src.
func roleAt(t *testing.T, d *Document, needle string) Role {
	t.Helper()
	src := d.Source()
	i := bytes.Index(src, []byte(needle))
	if i < 0 {
		t.Fatalf("needle %q not found", needle)
	}
	spans := d.Highlights(i, i+len(needle))
	if len(spans) == 0 {
		t.Fatalf("no spans for %q", needle)
	}
	r := spans[0].Role
	for _, s := range spans {
		if s.Role != r {
			t.Fatalf("needle %q has mixed roles: %v", needle, spans)
		}
	}
	return r
}

func checkTiling(t *testing.T, spans []Span, start, end int) {
	t.Helper()
	pos := start
	for _, s := range spans {
		if s.Start != pos || s.End <= s.Start {
			t.Fatalf("spans do not tile at %d: %+v", pos, s)
		}
		pos = s.End
	}
	if pos != end {
		t.Fatalf("spans end at %d, want %d", pos, end)
	}
}

const phpSample = `<?php
declare(strict_types=1);

namespace App\Service;

use App\Model\User;
use App\Model\Group;
use Psr\Log\{LoggerInterface, NullLogger};

/**
 * Doc block.
 */
#[Attribute]
final class UserService extends Base implements Countable
{
    public const int LIMIT = 10;
    private array $cache = [
        'a' => 1,
        'b' => 2.5,
    ];

    public function __construct(private readonly LoggerInterface $logger, public int $n = 0) {}

    public static function find(int $id): ?User
    {
        // line comment
        $name = "user {$id}";
        return User::load($id) ?? null;
    }
}

interface Countable { public function count(): int; }
trait T { protected $p; }
enum Suit: string { case Hearts = 'H'; case Spades = 'S'; }

function helper(string $s): string { return strtoupper($s); }
if (!function_exists('legacy')) { function legacy() {} }
`

func TestPHPHighlights(t *testing.T) {
	d := newDoc(t, LangPHP, phpSample)
	cases := map[string]Role{
		"<?php":           RoleTag,
		"declare":         RoleKeyword,
		"namespace":       RoleKeyword,
		"final":           RoleKeyword,
		"class":           RoleKeyword,
		"UserService":     RoleType,
		"Countable\n":     RoleType,
		"LIMIT":           RoleConstant,
		"$cache":          RoleProperty,
		"'a'":             RoleString,
		"2.5":             RoleNumber,
		"/**":             RoleComment,
		"// line":         RoleComment,
		"find":            RoleFunction,
		"$name":           RoleVariable,
		"strtoupper":      RoleFunction,
		"??":              RoleOperator,
		"=>":              RoleOperator,
		";":               RolePunctuation,
		"Attribute":       RoleAttribute,
		"Hearts":          RoleConstant,
		"null":            RoleConstant,
		"int $id":         RoleType,
		"__construct":     RoleFunction,
		"function_exists": RoleFunction,
	}
	for needle, want := range cases {
		// Only the first token of multi-token needles is checked.
		tok := strings.Fields(needle)[0]
		if needle == "int $id" {
			tok = "int"
			needle = "int $id"
		}
		src := d.Source()
		i := bytes.Index(src, []byte(needle))
		if i < 0 {
			t.Fatalf("needle %q not found", needle)
		}
		spans := d.Highlights(i, i+len(tok))
		if len(spans) != 1 || spans[0].Role != want {
			t.Errorf("%q: got %v, want %v", needle, spans, want)
		}
	}
	all := d.Highlights(0, len(phpSample)+100)
	checkTiling(t, all, 0, len(phpSample))
	if d.HasError() {
		t.Fatalf("sample has syntax errors: %s", d.SExpr())
	}
	if got := d.Highlights(10, 10); got != nil {
		t.Fatalf("empty range: %v", got)
	}
	if got := d.Highlights(-5, 3); len(got) == 0 || got[0].Start != 0 {
		t.Fatalf("negative start not clamped: %v", got)
	}
}

func TestOtherLanguageHighlights(t *testing.T) {
	j := newDoc(t, LangJSON, `{"name": "tiegel", "n": 12, "ok": true, "x": null}`)
	for needle, want := range map[string]Role{`"name"`: RoleProperty, `"tiegel"`: RoleString, "12": RoleNumber, "true": RoleConstant, "null": RoleConstant} {
		if got := roleAt(t, j, needle); got != want {
			t.Errorf("json %s: %v want %v", needle, got, want)
		}
	}
	y := newDoc(t, LangYAML, "# comment\nservices:\n  app:\n    image: \"php:8.3\"\n    port: 8080\n    debug: true\n")
	for needle, want := range map[string]Role{"# comment": RoleComment, "services": RoleProperty, `"php:8.3"`: RoleString, "8080": RoleNumber, "true": RoleConstant, ":": RolePunctuation} {
		if got := roleAt(t, y, needle); got != want {
			t.Errorf("yaml %s: %v want %v", needle, got, want)
		}
	}
	x := newDoc(t, LangXML, "<?xml version=\"1.0\"?>\n<!-- c -->\n<phpunit colors=\"true\">\n  <testsuite name=\"unit\">tests</testsuite>\n</phpunit>\n")
	for needle, want := range map[string]Role{"phpunit": RoleTag, "colors": RoleAttribute, "<!-- c -->": RoleComment, "xml": RoleKeyword, "version": RoleAttribute} {
		if got := roleAt(t, x, needle); got != want {
			t.Errorf("xml %s: %v want %v", needle, got, want)
		}
	}
	p := newDoc(t, LangPlain, "hello")
	if got := p.Highlights(0, 5); len(got) != 1 || got[0].Role != RolePlain {
		t.Fatalf("plain: %v", got)
	}
	if p.HasError() || p.SExpr() != "" || p.Folds() != nil || p.Outline() != nil {
		t.Fatal("plain document should have no tree")
	}
}

func TestEnvHighlights(t *testing.T) {
	src := "# comment\nexport APP_ENV=prod\nDEBUG=true\nPORT=8080\nURL=\"http://${HOST}:$PORT\" # trailing\nRAW='${NOPE}'\nPLAIN=abc $HOME #c\nEMPTY=\n  \nbroken line\n"
	d := newDoc(t, LangEnv, src)
	if d.Language() != LangEnv || LangEnv.HasTree() || !LangPHP.HasTree() {
		t.Fatal("language")
	}
	for needle, want := range map[string]Role{
		"# comment": RoleComment, "export": RoleKeyword, "APP_ENV": RoleProperty, "prod": RoleString,
		"true": RoleConstant, "8080": RoleNumber, "${HOST}": RoleVariable, "$PORT": RoleVariable,
		"# trailing": RoleComment, "'${NOPE}'": RoleString, "$HOME": RoleVariable, "#c": RoleComment,
		"broken": RoleProperty,
	} {
		if got := roleAt(t, d, needle); got != want {
			t.Errorf("env %q: %v want %v", needle, got, want)
		}
	}
	i := strings.Index(src, "=prod")
	if got := d.Highlights(i, i+1); len(got) != 1 || got[0].Role != RoleOperator {
		t.Errorf("env '=': %v", got)
	}
	checkTiling(t, d.Highlights(0, len(src)), 0, len(src))
	// Ranges starting mid-line work as well.
	checkTiling(t, d.Highlights(3, 25), 3, 25)
	// Reparse just replaces the text.
	if err := d.Reparse(context.Background(), []byte("A=1")); err != nil {
		t.Fatal(err)
	}
	if got := roleAt(t, d, "1"); got != RoleNumber {
		t.Fatal(got)
	}
}

func TestRolesAndCaptures(t *testing.T) {
	if len(Roles()) != int(roleCount) || Roles()[0] != RolePlain {
		t.Fatal("Roles")
	}
	for _, r := range Roles() {
		if r.String() == "" {
			t.Fatal("empty role name")
		}
	}
	if Role(200).String() != "plain" {
		t.Fatal("out of range role")
	}
	cases := map[string]Role{
		"function.method":       RoleFunction,
		"punctuation.bracket":   RolePunctuation,
		"string.special.key":    RoleProperty,
		"string.special":        RoleString,
		"variable.builtin":      RoleKeyword,
		"type.builtin":          RoleType,
		"markup":                RolePlain,
		"nonsense.thing":        RolePlain,
		"constant.builtin":      RoleConstant,
		"keyword.function.call": RoleKeyword,
	}
	for in, want := range cases {
		if got := RoleForCapture(in); got != want {
			t.Errorf("RoleForCapture(%q) = %v, want %v", in, got, want)
		}
	}
}

func TestDetectLanguage(t *testing.T) {
	cases := []struct {
		path, head string
		want       Language
	}{
		{"src/A.php", "", LangPHP},
		{"x.phtml", "", LangPHP},
		{"composer.json", "", LangJSON},
		{"composer.lock", "", LangJSON},
		{"yarn.lock", "", LangPlain},
		{"compose.yaml", "", LangYAML},
		{"a.yml", "", LangYAML},
		{"phpunit.xml", "", LangXML},
		{"phpunit.xml.dist", "", LangXML},
		{".env", "", LangEnv},
		{".env.local", "", LangEnv},
		{"prod.env", "", LangEnv},
		{"bin/console", "#!/usr/bin/env php\n<?php", LangPHP},
		{"bin/run", "#!/bin/sh\necho", LangPlain},
		{"script", "  <?php echo 1;", LangPHP},
		{"feed", "<?xml version=\"1.0\"?>", LangXML},
		{"README", "hello", LangPlain},
	}
	for _, c := range cases {
		if got := DetectLanguage(c.path, []byte(c.head)); got != c.want {
			t.Errorf("DetectLanguage(%q) = %q, want %q", c.path, got, c.want)
		}
	}
}

func TestPointsAndEdits(t *testing.T) {
	src := []byte("ab\ncd\nef")
	if p := PointAt(src, 4); p != (Point{Line: 1, Column: 1}) {
		t.Fatal(p)
	}
	if p := PointAt(src, 100); p != (Point{Line: 2, Column: 2}) {
		t.Fatal(p)
	}
	if p := PointAt(src, -1); p != (Point{}) {
		t.Fatal(p)
	}
	e := NewInputEdit(src, 1, 4, []byte("X\nYZ"))
	want := InputEdit{StartByte: 1, OldEndByte: 4, NewEndByte: 5,
		StartPoint: Point{0, 1}, OldEndPoint: Point{1, 1}, NewEndPoint: Point{1, 2}}
	if e != want {
		t.Fatalf("%+v", e)
	}
	e = NewInputEdit(src, 0, 0, []byte("Q"))
	if e.NewEndPoint != (Point{0, 1}) {
		t.Fatalf("%+v", e)
	}
}

// applyEdit replaces src[start:end] with ins, informs d and reparses.
func applyEdit(t testing.TB, d *Document, src []byte, start, end int, ins string) []byte {
	t.Helper()
	d.Edit(NewInputEdit(src, start, end, []byte(ins)))
	out := make([]byte, 0, len(src)-(end-start)+len(ins))
	out = append(out, src[:start]...)
	out = append(out, ins...)
	out = append(out, src[end:]...)
	if err := d.Reparse(context.Background(), out); err != nil {
		t.Fatal(err)
	}
	return out
}

func TestIncrementalReparseMatchesFreshParse(t *testing.T) {
	src := []byte(phpSample)
	d := newDoc(t, LangPHP, phpSample)
	rng := rand.New(rand.NewSource(1))
	inserts := []string{"x", "$a", " ", "\n", "{", "}", "'", "/*", "*/", "function f() {}", "class Z {}", ""}
	for i := 0; i < 200; i++ {
		start := rng.Intn(len(src) + 1)
		end := min(len(src), start+rng.Intn(6))
		src = applyEdit(t, d, src, start, end, inserts[rng.Intn(len(inserts))])
		fresh := newDoc(t, LangPHP, string(src))
		if d.SExpr() != fresh.SExpr() {
			t.Fatalf("step %d: incremental tree differs from fresh parse", i)
		}
		checkTiling(t, d.Highlights(0, len(src)), 0, len(src))
	}
}

func TestEditWithoutReparse(t *testing.T) {
	src := []byte("<?php\n$a = 'x';\n")
	d := newDoc(t, LangPHP, string(src))
	// Insert a lot of text at the end; tree now extends beyond old source.
	ins := strings.Repeat("$bbbbbbbb = 1;\n", 20)
	d.Edit(NewInputEdit(src, len(src), len(src), []byte(ins)))
	spans := d.Highlights(0, len(src)+len(ins))
	if len(spans) == 0 {
		t.Fatal("no spans")
	}
	_ = d.Folds()
	_ = d.Outline()
	s, err := d.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if len(s.Source()) < len(src) {
		t.Fatal("snapshot source too short")
	}
}

func bigPHP(lines int) []byte {
	var b strings.Builder
	b.WriteString("<?php\n\nnamespace Bench;\n\n")
	n := 5
	for i := 0; n < lines; i++ {
		fmt.Fprintf(&b, `/**
 * Class number %[1]d.
 */
final class Generated%[1]d extends Base implements \Countable
{
    private array $items = ['a' => 1, 'b' => 2, 'c' => 3];
    public const LIMIT = %[1]d;

    public function __construct(private readonly string $name = "gen%[1]d") {}

    public function count(): int
    {
        $total = 0;
        foreach ($this->items as $key => $value) {
            if ($value > self::LIMIT) {
                $total += $value * 2; // double
            } else {
                $total -= strlen("{$key}-{$this->name}");
            }
        }
        return $total ?? 0;
    }
}

`, i)
		n += 24
	}
	return []byte(b.String())
}

func TestReparseCancelled(t *testing.T) {
	src := bigPHP(3000)
	old := []byte("<?php echo 1;")
	d := newDoc(t, LangPHP, string(old))
	d.Edit(NewInputEdit(old, 0, len(old), src))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := d.Reparse(ctx, src)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("want cancellation, got %v", err)
	}
	if !bytes.Equal(d.Source(), []byte("<?php echo 1;")) {
		t.Fatal("old state must survive a cancelled parse")
	}
	// The parser must be usable again afterwards.
	if err := d.Reparse(context.Background(), src); err != nil {
		t.Fatal(err)
	}
	if d.HasError() {
		t.Fatal("unexpected syntax error")
	}
}

func TestCloseAndClosedDocument(t *testing.T) {
	d, err := NewDocument(context.Background(), LangPHP, []byte("<?php echo 1;"))
	if err != nil {
		t.Fatal(err)
	}
	d.Close()
	d.Close()
	if err := d.Reparse(context.Background(), []byte("x")); !errors.Is(err, ErrClosed) {
		t.Fatal(err)
	}
	d.Edit(InputEdit{})
	if d.Highlights(0, 5) != nil || d.Folds() != nil || d.Outline() != nil || d.HasError() || d.SExpr() != "" {
		t.Fatal("closed document should be empty")
	}
	if _, err := d.Snapshot(); !errors.Is(err, ErrClosed) {
		t.Fatal(err)
	}
	if _, err := d.Matches(nil); !errors.Is(err, ErrClosed) {
		t.Fatal(err)
	}
	e, _ := NewDocument(context.Background(), LangEnv, nil)
	e.Close()
	if err := e.Reparse(context.Background(), nil); !errors.Is(err, ErrClosed) {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := NewDocument(ctx, LangPHP, bigPHP(3000)); err == nil {
		t.Fatal("cancelled NewDocument should fail")
	}
}

func TestQueriesAndSnapshot(t *testing.T) {
	d := newDoc(t, LangPHP, phpSample)
	q, err := NewQuery(LangPHP, `(class_declaration name: (name) @name) (function_definition name: (name) @fn)`)
	if err != nil {
		t.Fatal(err)
	}
	defer q.Close()
	if q.Language() != LangPHP || len(q.CaptureNames()) != 2 || q.PatternCount() != 2 || q.PatternStart(1) <= q.PatternStart(0) {
		t.Fatal("query metadata")
	}
	ms, err := d.Matches(q)
	if err != nil || len(ms) != 3 {
		t.Fatalf("matches: %v %v", ms, err)
	}
	if c := ms[0].Captures[0]; c.Name != "name" || c.Kind != "name" || string(phpSample[c.StartByte:c.EndByte]) != "UserService" || c.Start.Line != 13 {
		t.Fatalf("capture: %+v", c)
	}
	s, err := d.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	if s.Language() != LangPHP {
		t.Fatal("snapshot lang")
	}
	// The snapshot is independent of later changes.
	if err := d.Reparse(context.Background(), []byte("<?php")); err != nil {
		t.Fatal(err)
	}
	sm, err := s.Matches(q)
	if err != nil || len(sm) != 3 {
		t.Fatalf("snapshot matches: %d %v", len(sm), err)
	}
	if len(s.Folds()) == 0 || len(s.Outline()) == 0 {
		t.Fatal("snapshot folds/outline")
	}
	s.Close()
	s.Close()
	if ms, _ := s.Matches(q); ms != nil {
		t.Fatal("closed snapshot")
	}

	jq, err := NewQuery(LangJSON, `(pair) @p`)
	if err != nil {
		t.Fatal(err)
	}
	defer jq.Close()
	if _, err := d.Matches(jq); err == nil {
		t.Fatal("language mismatch must fail")
	}
	if _, err := NewQuery(LangEnv, `(x)`); err == nil {
		t.Fatal("env has no grammar")
	}
	if _, err := NewQuery(LangPHP, `(no_such_node)`); err == nil {
		t.Fatal("invalid query")
	}
	closed, _ := NewQuery(LangPHP, `(name) @n`)
	closed.Close()
	closed.Close()
	if _, err := d.Matches(closed); err == nil {
		t.Fatal("closed query must fail")
	}
	env := newDoc(t, LangEnv, "A=1")
	if ms, err := env.Matches(q); err == nil || ms != nil {
		t.Fatal("env matches must fail on language mismatch")
	}
	es, err := env.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	defer es.Close()
	if string(es.Source()) != "A=1" || es.Folds() != nil || es.Outline() != nil {
		t.Fatal("env snapshot")
	}
}

func TestConcurrentUse(t *testing.T) {
	src := bigPHP(600)
	d := newDoc(t, LangPHP, string(src))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var wg sync.WaitGroup
	// Background parser goroutine.
	wg.Add(1)
	go func() {
		defer wg.Done()
		cur := bytes.Clone(src)
		for i := 0; i < 20; i++ {
			pos := len(cur) / 2
			d.Edit(NewInputEdit(cur, pos, pos, []byte(" ")))
			cur = append(cur[:pos:pos], append([]byte(" "), cur[pos:]...)...)
			if err := d.Reparse(ctx, cur); err != nil {
				t.Error(err)
				return
			}
		}
	}()
	// Readers on other goroutines.
	for g := 0; g < 4; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 20; i++ {
				_ = d.Highlights(0, 2000)
				_ = d.Folds()
				_ = d.Outline()
				if s, err := d.Snapshot(); err == nil {
					_ = s.Outline()
					s.Close()
				}
			}
		}()
	}
	wg.Wait()
}
