package fspath

import (
	"path/filepath"
	"runtime"
	"testing"
)

func TestIsAbs(t *testing.T) {
	cases := map[string]bool{
		"":           false,
		"a":          false,
		"a/b":        false,
		"./a":        false,
		"../a":       false,
		"/a":         true,
		"/":          true,
		`\a`:         runtime.GOOS == "windows",
		`C:\a`:       runtime.GOOS == "windows",
		`C:a`:        false,
		`\\srv\s\a`:  runtime.GOOS == "windows",
		"//srv/s/a":  true,
		"~/x":        false,
		"file:///a":  false,
		`a\b`:        false,
		"/elsewhere": true,
	}
	for p, want := range cases {
		if got := IsAbs(p); got != want {
			t.Errorf("IsAbs(%q) = %v, want %v", p, got, want)
		}
	}
}

func TestResolve(t *testing.T) {
	base := filepath.FromSlash("/proj")
	cases := []struct{ p, want string }{
		{"src/a.php", filepath.Join(base, "src", "a.php")},
		{".", base},
		{"/elsewhere/x.json", filepath.FromSlash("/elsewhere/x.json")},
		{"/a/../b", filepath.FromSlash("/b")},
	}
	if runtime.GOOS == "windows" {
		cases = append(cases,
			struct{ p, want string }{`\elsewhere\x`, `\elsewhere\x`},
			struct{ p, want string }{`D:\x\y`, `D:\x\y`},
			struct{ p, want string }{`src\a.php`, `\proj\src\a.php`},
		)
	}
	for _, c := range cases {
		if got := Resolve(base, c.p); got != c.want {
			t.Errorf("Resolve(%q, %q) = %q, want %q", base, c.p, got, c.want)
		}
	}
}
