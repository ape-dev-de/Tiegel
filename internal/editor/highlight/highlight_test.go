package highlight

import (
	"strings"
	"testing"

	"github.com/ape-dev-de/tiegel/internal/editor/buffer"
	"github.com/ape-dev-de/tiegel/internal/editor/fold"
)

func TestRoleNames(t *testing.T) {
	for _, r := range Roles() {
		got, ok := RoleByName(r.String())
		if !ok || got != r {
			t.Errorf("RoleByName(%q) = %v, %v", r, got, ok)
		}
	}
	if _, ok := RoleByName("nope"); ok {
		t.Error("unknown name")
	}
	if Role(200).String() != "unknown" {
		t.Error("unknown role")
	}
	if len(Roles()) != 14 || Roles()[0] != Plain || Roles()[13] != Property {
		t.Error("role order")
	}
}

func TestNormalize(t *testing.T) {
	in := []Span{{5, 9, String}, {0, 3, Keyword}, {2, 4, Number}, {6, 7, Comment}, {8, 8, Tag}, {-5, 1, Plain}}
	got := Normalize(in, 1, 8)
	want := []Span{{1, 3, Keyword}, {3, 4, Number}, {5, 8, String}}
	if len(got) != len(want) {
		t.Fatalf("got %v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %v, want %v", got, want)
		}
	}
}

// roleAt returns the role of the span covering the first occurrence of
// needle in text.
func roleAt(t *testing.T, text, needle string) Role {
	t.Helper()
	s := buffer.NewString(text).Snapshot()
	off := strings.Index(text, needle)
	if off < 0 {
		t.Fatalf("%q not in text", needle)
	}
	for _, sp := range PHPDemo.Spans(s, 0, s.Len()) {
		if sp.Start <= off && off < sp.End {
			return sp.Role
		}
	}
	return Plain
}

func TestPHPDemo(t *testing.T) {
	src := `<?php
namespace App\Service;

#[Attr]
final class UserService extends Base implements Countable
{
    /** Doc block
     * continued */
    private const LIMIT = 10;

    public function find(int $id): ?User {
        $x = $this->repo->load($id, "a \" b", 'c', 0x1F, .5);
        return User::make(null) ?? MAX_SIZE; // trailing
        # hash comment
    }
}
?>`
	cases := map[string]Role{
		"<?php": Tag, "namespace": Keyword, "App\\Service": Type, "#[": Attribute,
		"final": Keyword, "UserService": Type, "Base": Type, "Countable": Type,
		"/** Doc": Comment, "* continued": Comment, "LIMIT": Constant, "10": Number,
		"find": Function, "int": Type, "$id": Variable, "User {": Type,
		"$this": Variable, "repo": Property, "load": Function, `"a \" b"`: String,
		"'c'": String, "0x1F": Number, ".5": Number, "User::": Type, "make": Function,
		"null": Constant, "??": Operator, "MAX_SIZE": Constant, "// trailing": Comment,
		"# hash": Comment, "?>": Tag, "{\n": Punctuation, "=": Operator, "Attr": Type,
	}
	for needle, want := range cases {
		if got := roleAt(t, src, needle); got != want {
			t.Errorf("%q: got %v, want %v", needle, got, want)
		}
	}
	// Ranges inside a line are clipped.
	s := buffer.NewString("$a = 1;").Snapshot()
	for _, sp := range PHPDemo.Spans(s, 1, 3) {
		if sp.Start < 1 || sp.End > 3 {
			t.Errorf("unclipped %v", sp)
		}
	}
	// Unterminated constructs end at the line end.
	s = buffer.NewString("/* open\n'str").Snapshot()
	if sp := PHPDemo.Spans(s, 0, s.Len()); len(sp) != 2 || sp[0].Role != Comment || sp[1].End != s.Len() {
		t.Errorf("unterminated: %v", sp)
	}
}

func TestBraceFolds(t *testing.T) {
	src := "class A {\n  function f() {\n    $s = '{\\'';\n    /* x\n     y */\n  }\n  // {\n}\n$a = [\n 1];"
	s := buffer.NewString(src).Snapshot()
	got := BraceFolds(s)
	want := map[fold.Range]bool{
		{StartLine: 0, EndLine: 6}: true, // class body, closing "}" stays visible
		{StartLine: 1, EndLine: 4}: true, // function body
		{StartLine: 3, EndLine: 4}: true, // block comment
		{StartLine: 8, EndLine: 9}: true, // array closed mid-line
	}
	if len(got) != len(want) {
		t.Fatalf("got %v", got)
	}
	for _, r := range got {
		if !want[r] {
			t.Errorf("unexpected range %v (all: %v)", r, got)
		}
	}
}
