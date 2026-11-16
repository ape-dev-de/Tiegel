package fspath

import (
	"path/filepath"
	"runtime"
	"testing"
)

func TestValidName(t *testing.T) {
	for _, n := range []string{"", ".", "..", "a/b", "a\x00"} {
		for _, goos := range []string{"linux", "darwin", "windows"} {
			if ValidName(n, goos) == nil {
				t.Errorf("ValidName(%q, %s) accepted", n, goos)
			}
		}
	}
	for _, n := range []string{"a.php", "Ünïcode.txt", ".env", "a:b", "x?", "con", "trailing.", `a\b`} {
		if err := ValidName(n, "linux"); err != nil {
			t.Errorf("linux rejects %q: %v", n, err)
		}
	}
	for _, n := range []string{"a:b", "x?", "a<b", `a\b`, "a|b", "con", "CON.txt", "nul.php", "com1", "LPT9.log", "aux ", "trailing.", "tab\tname"} {
		if ValidName(n, "windows") == nil {
			t.Errorf("windows accepts %q", n)
		}
	}
	for _, n := range []string{"a.php", "console.php", "com10", "lpt", "con_x", ".env"} {
		if err := ValidName(n, "windows"); err != nil {
			t.Errorf("windows rejects %q: %v", n, err)
		}
	}
}

func TestWithin(t *testing.T) {
	root := filepath.FromSlash("/proj")
	for p, want := range map[string]bool{
		"/proj": true, "/proj/a": true, "/proj/a/../b": true, "/proj/..x": true,
		"/proj2": false, "/": false, "/proj/../x": false,
	} {
		if got := Within(root, filepath.Clean(filepath.FromSlash(p))); got != want {
			t.Errorf("Within(%q) = %v", p, got)
		}
	}
}

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
