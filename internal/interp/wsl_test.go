package interp

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"unicode/utf16"
)

func utf16le(s string, bom bool) []byte {
	var b []byte
	if bom {
		b = append(b, 0xFF, 0xFE)
	}
	for _, u := range utf16.Encode([]rune(s)) {
		b = append(b, byte(u), byte(u>>8))
	}
	return b
}

func TestDecodeWSLOutput(t *testing.T) {
	want := "Ubuntu-24.04\nDebian\n"
	cases := map[string][]byte{
		"utf16 bom":    utf16le("Ubuntu-24.04\r\nDebian\r\n", true),
		"utf16 no bom": utf16le("Ubuntu-24.04\r\nDebian\r\n", false),
		"utf8":         []byte("Ubuntu-24.04\r\nDebian\n"),
		"utf8 bom":     append([]byte{0xEF, 0xBB, 0xBF}, []byte("Ubuntu-24.04\nDebian\n")...),
		"nul padded":   append(utf16le("Ubuntu-24.04\nDebian\n", false), 0, 0),
	}
	for name, in := range cases {
		if got := DecodeWSLOutput(in); got != want {
			t.Errorf("%s: %q", name, got)
		}
	}
	if got := DecodeWSLOutput(utf16le("Übung\n", false)); got != "Übung\n" {
		t.Errorf("non-ascii: %q", got)
	}
	if got := DecodeWSLOutput(nil); got != "" {
		t.Errorf("empty: %q", got)
	}
	if got := DecodeWSLOutput([]byte("x")); got != "x" {
		t.Errorf("single byte: %q", got)
	}
}

func TestParseDistros(t *testing.T) {
	out := "\ufeffUbuntu\n\n  Debian \ndocker-desktop\ndocker-desktop-data\n"
	if got := ParseDistros(out, false); !reflect.DeepEqual(got, []string{"Ubuntu", "Debian"}) {
		t.Errorf("without docker: %v", got)
	}
	if got := ParseDistros(out, true); len(got) != 4 {
		t.Errorf("with docker: %v", got)
	}
	if got := ParseDistros("", false); got != nil {
		t.Errorf("empty: %v", got)
	}
}

type call struct {
	name string
	args []string
}

func fakeRunner(calls *[]call, fn func(args []string) ([]byte, error)) CommandRunner {
	return func(_ context.Context, name string, args ...string) ([]byte, error) {
		*calls = append(*calls, call{name, args})
		return fn(args)
	}
}

func TestListAndDiscoverWSL(t *testing.T) {
	var calls []call
	run := fakeRunner(&calls, func(args []string) ([]byte, error) {
		if args[0] == "-l" {
			return utf16le("Ubuntu\r\nAlpine\r\ndocker-desktop\r\nBroken\r\n", true), nil
		}
		switch args[1] {
		case "Ubuntu":
			return []byte("/usr/bin/php\n"), nil
		case "Alpine":
			return []byte(""), errors.New("exit status 1")
		default:
			return []byte("sh: php: not found\n"), nil
		}
	})
	ctx := context.Background()
	distros, err := ListDistros(ctx, WSLOptions{Run: run})
	if err != nil || !reflect.DeepEqual(distros, []string{"Ubuntu", "Alpine", "Broken"}) {
		t.Fatalf("ListDistros: %v %v", distros, err)
	}
	if calls[0].name != "wsl.exe" || !reflect.DeepEqual(calls[0].args, []string{"-l", "-q"}) {
		t.Errorf("list call: %+v", calls[0])
	}
	calls = nil
	found, err := DiscoverWSL(ctx, WSLOptions{Run: run, Program: `C:\Windows\System32\wsl.exe`})
	if err != nil || len(found) != 1 {
		t.Fatalf("DiscoverWSL: %v %v", found, err)
	}
	w := found[0]
	if w.Distro != "Ubuntu" || w.Path != "/usr/bin/php" || w.Program != `C:\Windows\System32\wsl.exe` {
		t.Errorf("found %+v", w)
	}
	want := []string{"-d", "Ubuntu", "--", "sh", "-c", "command -v php"}
	if !reflect.DeepEqual(calls[1].args, want) {
		t.Errorf("probe args: %v", calls[1].args)
	}

	// Disabled off Windows without injected runner.
	if got, err := DiscoverWSL(ctx, WSLOptions{GOOS: "darwin"}); got != nil || err != nil {
		t.Errorf("non-windows: %v %v", got, err)
	}
	// List failure.
	bad := func(context.Context, string, ...string) ([]byte, error) { return nil, errors.New("no wsl") }
	if _, err := DiscoverWSL(ctx, WSLOptions{Run: bad}); err == nil || !strings.Contains(err.Error(), "no wsl") {
		t.Errorf("want list error, got %v", err)
	}
	if !(WSLOptions{GOOS: "windows"}).enabled() {
		t.Error("windows should be enabled")
	}
}

func TestExecRunner(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("needs a POSIX shell")
	}
	out, err := ExecRunner(context.Background(), "sh", "-c", "printf ok")
	if err != nil || string(out) != "ok" {
		t.Errorf("%q %v", out, err)
	}
	_, err = ExecRunner(context.Background(), "sh", "-c", "echo boom >&2; exit 3")
	if err == nil || !strings.Contains(err.Error(), "boom") {
		t.Errorf("stderr in error: %v", err)
	}
	_, err = ExecRunner(context.Background(), "sh", "-c", "exit 3")
	if err == nil {
		t.Error("want error")
	}
}

func TestWSLCommand(t *testing.T) {
	ctx := context.Background()
	w := NewWSL("Ubuntu", "")
	if w.Kind() != KindWSL || w.ID() != "wsl:Ubuntu:php" || !strings.Contains(w.Label(), "Ubuntu") {
		t.Errorf("metadata: %s %s %s", w.Kind(), w.ID(), w.Label())
	}
	cmd, err := w.Command(ctx, "-r", "echo 1;")
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"wsl.exe", "-d", "Ubuntu", "--", "php", "-r", "echo 1;"}
	if !reflect.DeepEqual(cmd.Args, want) {
		t.Errorf("args %v", cmd.Args)
	}
	if got := (&WSL{Distro: "D"}).Args("-v"); !reflect.DeepEqual(got, []string{"-d", "D", "--", "php", "-v"}) {
		t.Errorf("default php: %v", got)
	}
	if _, err := (&WSL{}).Command(ctx); err == nil {
		t.Error("want error without distro")
	}
}

// TestWSLQueries runs the interpreter against a fake wsl launcher that drops
// "-d D --" and executes the rest, with a fake php behind it.
func TestWSLQueries(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fake scripts need a POSIX shell")
	}
	dir := t.TempDir()
	php := fakePHP(t, dir, "php", "8.3.4")
	launcher := filepath.Join(dir, "wsl")
	script := "#!/bin/sh\n[ \"$1\" = -d ] && [ \"$3\" = -- ] || exit 9\nshift 3\nexec \"$@\"\n"
	if err := os.WriteFile(launcher, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	w := &WSL{Distro: "Ubuntu", Path: php, Program: launcher}
	ctx := context.Background()
	if v, err := w.Version(ctx); err != nil || v != "8.3.4" {
		t.Errorf("Version %q %v", v, err)
	}
	if m, err := w.Modules(ctx); err != nil || len(m) == 0 {
		t.Errorf("Modules %v %v", m, err)
	}
	if ini, err := w.Ini(ctx); err != nil || ini["xdebug.client_port"] != "9003" {
		t.Errorf("Ini %v %v", ini, err)
	}
}

func TestWindowsToWSL(t *testing.T) {
	cases := []struct {
		in, distro, want string
		ok               bool
	}{
		{`C:\x\y`, "", "/mnt/c/x/y", true},
		{`c:/x`, "", "/mnt/c/x", true},
		{`D:\Projects\My App\`, "", "/mnt/d/Projects/My App", true},
		{`C:\`, "", "/mnt/c", true},
		{`C:`, "", "/mnt/c", true},
		{`C:\\a\\\b`, "", "/mnt/c/a/b", true},
		{`\\?\C:\long\path`, "", "/mnt/c/long/path", true},
		{`\\wsl$\Ubuntu\home\u`, "Ubuntu", "/home/u", true},
		{`\\wsl.localhost\ubuntu\home\u\`, "Ubuntu", "/home/u", true},
		{`\\WSL.LOCALHOST\Debian\etc`, "", "/etc", true},
		{`\\wsl$\Ubuntu`, "Ubuntu", "/", true},
		{`\\wsl$\Ubuntu\`, "Ubuntu", "/", true},
		{`\\?\UNC\wsl$\Ubuntu\srv`, "Ubuntu", "/srv", true},
		{`\\wsl$\Debian\home`, "Ubuntu", "", false},
		{`\\wsl$\`, "", "", false},
		{`\\wsl$`, "", "", false},
		{`\\server\share\x`, "", "", false},
		{`relative\path`, "", "", false},
		{`C:relative`, "", "", false},
		{`/already/unix`, "", "", false},
		{``, "", "", false},
	}
	for _, c := range cases {
		got, ok := WindowsToWSL(c.in, c.distro)
		if got != c.want || ok != c.ok {
			t.Errorf("WindowsToWSL(%q, %q) = %q, %v; want %q, %v", c.in, c.distro, got, ok, c.want, c.ok)
		}
	}
}

func TestWSLToWindows(t *testing.T) {
	cases := []struct{ in, want string }{
		{"/mnt/c/x", `C:\x`},
		{"/mnt/c/x/y/", `C:\x\y`},
		{"/mnt/c", `C:\`},
		{"/mnt/c/", `C:\`},
		{"/mnt/d/My App/a.php", `D:\My App\a.php`},
		{"/home/u", `\\wsl.localhost\Ubuntu\home\u`},
		{"/", `\\wsl.localhost\Ubuntu`},
		{"/mnt/wsl/x", `\\wsl.localhost\Ubuntu\mnt\wsl\x`},
		{"/mnt", `\\wsl.localhost\Ubuntu\mnt`},
		{"rel/a", `rel\a`},
	}
	for _, c := range cases {
		if got := WSLToWindows(c.in, "Ubuntu"); got != c.want {
			t.Errorf("WSLToWindows(%q) = %q, want %q", c.in, got, c.want)
		}
	}
	if got := WSLToWindowsShare("/home/u", "Ubuntu"); got != `\\wsl$\Ubuntu\home\u` {
		t.Errorf("share: %q", got)
	}
	// Round trip.
	for _, p := range []string{"/mnt/c/x/y", "/home/u/project", "/"} {
		back, ok := WindowsToWSL(WSLToWindows(p, "Ubuntu"), "Ubuntu")
		if !ok || back != p {
			t.Errorf("round trip %q → %q %v", p, back, ok)
		}
	}
}
