package syntax

import (
	"context"
	"strings"
	"testing"
)

// roleOf returns the role of the span covering the first occurrence of
// sub in src (RolePlain when uncovered).
func roleOf(spans []Span, src, sub string) Role {
	i := strings.Index(src, sub)
	if i < 0 {
		return 255
	}
	for _, s := range spans {
		if s.Start <= i && i < s.End {
			return s.Role
		}
	}
	return RolePlain
}

func TestSnippetLexers(t *testing.T) {
	cases := []struct {
		lang, src string
		want      map[string]Role
	}{
		{"js", "const x = foo(1.5); // hi\n/* block\n still */ let s = `t` + 'q'; class Foo {}",
			map[string]Role{"const": RoleKeyword, "foo": RoleFunction, "1.5": RoleNumber, "// hi": RoleComment,
				"still": RoleComment, "`t`": RoleString, "'q'": RoleString, "Foo": RoleType, "=": RoleOperator, "{": RolePunctuation}},
		{"ts", "let a: string = null", map[string]Role{"let": RoleKeyword, "string": RoleType, "null": RoleConstant}},
		{"go", "func main() { var err error = nil }", map[string]Role{"func": RoleKeyword, "main": RoleFunction, "error": RoleType, "nil": RoleConstant}},
		{"SQL", "SELECT id FROM users WHERE name = 'x' -- c", map[string]Role{"SELECT": RoleKeyword, "FROM": RoleKeyword, "'x'": RoleString, "-- c": RoleComment}},
		{"shell", "# setup\nexport PATH=$HOME/bin:${X}\napt-get install -y git # note\necho \"$1\"",
			map[string]Role{"# setup": RoleComment, "export": RoleKeyword, "$HOME": RoleVariable, "${X}": RoleVariable,
				"# note": RoleComment, "\"$1\"": RoleString, "apt-get": RolePlain}},
		{"css", ".a { color: #fff; margin: 10px; } /* c */", map[string]Role{"color": RoleProperty, "10px": RoleNumber, "/* c */": RoleComment}},
		{"py", "def f(x):\n    return None  # c", map[string]Role{"def": RoleKeyword, "None": RoleConstant, "# c": RoleComment}},
		{"rust", "fn main() { let v: Vec<i32> = Vec::new(); }", map[string]Role{"fn": RoleKeyword, "i32": RoleType}},
		{"toml", "[server]\nport = 8080\nname = \"x\"\ndebug = true\n; c\nhost = local", map[string]Role{"[server]": RoleType, "port": RoleProperty,
			"8080": RoleNumber, "\"x\"": RoleString, "true": RoleConstant, "; c": RoleComment, "local": RoleString}},
		{"dockerfile", "FROM php:8.3\nRUN apt-get update # x\nENV A=$B", map[string]Role{"FROM": RoleKeyword, "RUN": RoleKeyword, "$B": RoleVariable}},
		{"diff", "diff --git a b\n@@ -1 +1 @@\n-old\n+new\n ctx\n\\ No newline", map[string]Role{"diff": RoleKeyword, "@@": RoleFunction,
			"-old": RoleTag, "+new": RoleString, "\\ No": RoleComment}},
	}
	for _, c := range cases {
		spans := HighlightSnippet(context.Background(), c.lang, []byte(c.src))
		for sub, want := range c.want {
			if got := roleOf(spans, c.src, sub); got != want {
				t.Errorf("%s: %q is %s, want %s", c.lang, sub, got, want)
			}
		}
		for i := 1; i < len(spans); i++ {
			if spans[i].Start < spans[i-1].End {
				t.Errorf("%s: overlapping spans %v %v", c.lang, spans[i-1], spans[i])
			}
		}
	}
}

func TestSnippetTreeSitter(t *testing.T) {
	if !TreeSitter {
		t.Skip("no Tree-sitter in this build")
	}
	src := "echo \"hi\"; // c\n$x = 1;"
	spans := HighlightSnippet(context.Background(), "php", []byte(src))
	if roleOf(spans, src, `"hi"`) != RoleString || roleOf(spans, src, "// c") != RoleComment || roleOf(spans, src, "$x") != RoleVariable {
		t.Fatalf("php without open tag: %v", spans)
	}
	for _, s := range spans {
		if s.Start < 0 || s.End > len(src) {
			t.Fatalf("span %v outside the snippet", s)
		}
	}
	src = "a: 1\nb: \"x\""
	if roleOf(HighlightSnippet(context.Background(), "yml", []byte(src)), src, `"x"`) != RoleString {
		t.Fatal("yaml alias")
	}
}

func TestSnippetUnknown(t *testing.T) {
	if HighlightSnippet(context.Background(), "brainfuck", []byte("+++")) != nil || HighlightSnippet(context.Background(), "go", nil) != nil {
		t.Fatal("unknown language or empty source highlighted")
	}
	if l, ok := SnippetLanguage(" YML "); !ok || l != "yaml" {
		t.Fatalf("alias %q %v", l, ok)
	}
}
