package index

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/ape-dev-de/tiegel/internal/syntax"
)

func TestResolveNames(t *testing.T) {
	sc := Scope{Namespace: `App\X`, Uses: []Use{{UseClass, "Foo", `Lib\Foo`}, {UseFunction, "fn", `Lib\fn`}, {UseConst, "K", `Lib\K`}}}
	cases := map[string]string{
		`\A\B`: `A\B`, `foo`: `Lib\Foo`, `Foo\Bar`: `Lib\Foo\Bar`, `Baz`: `App\X\Baz`, `namespace\Q`: `App\X\Q`,
		`int`: "", `self`: "", `Sub\T`: `App\X\Sub\T`, "": "",
	}
	for in, want := range cases {
		if got := ResolveClass(in, sc); got != want {
			t.Errorf("ResolveClass(%q) = %q, want %q", in, got, want)
		}
	}
	if got := resolveCallable("fn", UseFunction, sc); !slices.Equal(got, []string{`Lib\fn`}) {
		t.Errorf("fn %q", got)
	}
	if got := resolveCallable("strlen", UseFunction, sc); !slices.Equal(got, []string{`App\X\strlen`, "strlen"}) {
		t.Errorf("strlen %q", got)
	}
	if got := resolveCallable("K", UseConst, sc); !slices.Equal(got, []string{`Lib\K`}) {
		t.Errorf("K %q", got)
	}
	if got := resolveCallable(`Foo\x`, UseFunction, sc); !slices.Equal(got, []string{`Lib\Foo\x`}) {
		t.Errorf("Foo\\x %q", got)
	}
	if got := resolveCallable(`Sub\x`, UseFunction, sc); !slices.Equal(got, []string{`App\X\Sub\x`}) {
		t.Errorf("Sub\\x %q", got)
	}
	if got := resolveCallable(`namespace\x`, UseFunction, sc); !slices.Equal(got, []string{`App\X\x`}) {
		t.Errorf("namespace\\x %q", got)
	}
	if got := resolveCallable(`\x`, UseFunction, sc); !slices.Equal(got, []string{`x`}) {
		t.Errorf("\\x %q", got)
	}
	if got := resolveCallable(`x`, UseFunction, Scope{}); !slices.Equal(got, []string{`x`}) {
		t.Errorf("global x %q", got)
	}
	if resolveCallable("", UseConst, sc) != nil {
		t.Error("empty")
	}
}

func TestKinds(t *testing.T) {
	for k := KindClass; k <= KindConst; k++ {
		back, ok := ParseKind(k.String())
		if !ok || back != k {
			t.Errorf("kind %d", k)
		}
	}
	if Kind(0).String() != "unknown" || Kind(99).String() != "unknown" {
		t.Error("unknown")
	}
	if _, ok := ParseKind("nope"); ok {
		t.Error("ParseKind nope")
	}
	if !KindEnum.IsType() || KindMethod.IsType() || !KindEnumCase.IsMember() || KindFunction.IsMember() {
		t.Error("predicates")
	}
}

func TestWithoutTreeSitter(t *testing.T) {
	if TreeSitter {
		t.Skip("cgo build")
	}
	ix := New(Options{Root: t.TempDir()})
	defer ix.Close()
	if _, err := ix.Sync(context.Background(), nil); !errors.Is(err, syntax.ErrNoTreeSitter) {
		t.Fatalf("Sync: %v", err)
	}
	if _, err := ix.Definition("a.php", []byte("<?php"), 0); !errors.Is(err, syntax.ErrNoTreeSitter) {
		t.Fatalf("Definition: %v", err)
	}
	if _, err := ix.UpdateText("a.php", []byte("<?php")); !errors.Is(err, syntax.ErrNoTreeSitter) {
		t.Fatalf("UpdateText: %v", err)
	}
}
