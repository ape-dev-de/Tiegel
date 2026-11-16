package interp

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"
)

// fakePHP writes a shell script that mimics the php CLI for the calls made
// by this package.
func fakePHP(t *testing.T, dir, name, version string) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("fake php scripts need a POSIX shell")
	}
	script := `#!/bin/sh
case "$1" in
  -m)
    printf '[PHP Modules]\nCore\nxdebug\njson\nZend OPcache\n\n[Zend Modules]\nXdebug\nZend OPcache\n'
    ;;
  -r)
    case "$2" in
      *PHP_VERSION*) printf '` + version + `' ;;
      *ini_get*) printf 'xdebug.mode=debug,develop\nxdebug.client_host=host.docker.internal\nxdebug.client_port=9003\nxdebug.start_with_request\n' ;;
      *) echo "unexpected code" >&2; exit 3 ;;
    esac
    ;;
  *) echo "unexpected args: $*" >&2; exit 2 ;;
esac
`
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestLocalQueries(t *testing.T) {
	dir := t.TempDir()
	l := NewLocal(fakePHP(t, dir, "php", "8.3.4"))
	ctx := context.Background()

	if l.Kind() != KindLocal {
		t.Errorf("Kind = %q", l.Kind())
	}
	if !strings.HasPrefix(l.ID(), "local:") {
		t.Errorf("ID = %q", l.ID())
	}
	if l.Label() != "PHP ("+l.Found+")" {
		t.Errorf("Label before version = %q", l.Label())
	}
	v, err := l.Version(ctx)
	if err != nil || v != "8.3.4" {
		t.Fatalf("Version = %q, %v", v, err)
	}
	if !strings.HasPrefix(l.Label(), "PHP 8.3.4 (") {
		t.Errorf("Label = %q", l.Label())
	}
	mods, err := l.Modules(ctx)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"Core", "json", "xdebug", "Zend OPcache"}
	if !reflect.DeepEqual(mods, want) {
		t.Errorf("Modules = %v, want %v", mods, want)
	}
	ini, err := l.Ini(ctx)
	if err != nil {
		t.Fatal(err)
	}
	wantIni := IniValues{
		"xdebug.mode":        "debug,develop",
		"xdebug.client_host": "host.docker.internal",
		"xdebug.client_port": "9003",
	}
	if !reflect.DeepEqual(ini, wantIni) {
		t.Errorf("Ini = %v, want %v", ini, wantIni)
	}
}

func TestRunErrorIncludesStderr(t *testing.T) {
	dir := t.TempDir()
	l := NewLocal(fakePHP(t, dir, "php", "8.2.0"))
	_, err := Run(context.Background(), l, "--bogus")
	if err == nil || !strings.Contains(err.Error(), "unexpected args") {
		t.Fatalf("err = %v", err)
	}
}

func TestRunErrorWithoutStderr(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX only")
	}
	dir := t.TempDir()
	p := filepath.Join(dir, "php")
	if err := os.WriteFile(p, []byte("#!/bin/sh\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	l := NewLocal(p)
	if _, err := Run(context.Background(), l, "-v"); err == nil {
		t.Fatal("want error")
	}
	if _, err := l.Version(context.Background()); err == nil {
		t.Fatal("want version error")
	}
	if _, err := l.Modules(context.Background()); err == nil {
		t.Fatal("want modules error")
	}
	if _, err := l.Ini(context.Background()); err == nil {
		t.Fatal("want ini error")
	}
}

func TestEmptyVersion(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX only")
	}
	dir := t.TempDir()
	p := filepath.Join(dir, "php")
	if err := os.WriteFile(p, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := NewLocal(p).Version(context.Background()); err == nil {
		t.Fatal("want error for empty version")
	}
}

func TestParseIniSkipsGarbage(t *testing.T) {
	got := ParseIni([]byte("a=1\r\n=x\nnovalue\nb=\n"))
	want := IniValues{"a": "1", "b": ""}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("ParseIni = %v", got)
	}
}

func TestIsPHPName(t *testing.T) {
	cases := map[string]bool{
		"php": true, "php.exe": true, "php8.3": true, "php83": true,
		"php-config": false, "phpize": false, "php8.3-fpm": false, "phpx": false, "pear": false,
	}
	for name, want := range cases {
		if got := isPHPName(name); got != want {
			t.Errorf("isPHPName(%q) = %v", name, got)
		}
	}
}

func TestFindBinariesDedupAndFilter(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX only")
	}
	a := t.TempDir()
	b := t.TempDir()
	real := fakePHP(t, a, "php", "8.3.4")
	fakePHP(t, a, "php8.1", "8.1.2")
	// Symlink to the same binary in another directory: must be deduplicated.
	if err := os.Symlink(real, filepath.Join(b, "php")); err != nil {
		t.Fatal(err)
	}
	// Not executable, not a php name.
	if err := os.WriteFile(filepath.Join(b, "php8.4"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	fakePHP(t, b, "php-config", "0")
	if err := os.Mkdir(filepath.Join(b, "php8.5"), 0o755); err != nil {
		t.Fatal(err)
	}

	got := FindBinaries([]string{b, a, filepath.Join(a, "missing")})
	if len(got) != 2 {
		for _, l := range got {
			t.Logf("found %s (%s)", l.Path, l.Found)
		}
		t.Fatalf("found %d binaries, want 2", len(got))
	}
	if got[0].Found != filepath.Join(b, "php") {
		t.Errorf("first found = %q, want symlink in b", got[0].Found)
	}
	if filepath.Base(got[1].Path) != "php8.1" {
		t.Errorf("second = %q", got[1].Path)
	}
}

func TestDiscover(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX only")
	}
	dir := t.TempDir()
	fakePHP(t, dir, "php", "8.4.1")
	broken := filepath.Join(dir, "php7")
	if err := os.WriteFile(broken, []byte("#!/bin/sh\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	got := Discover(context.Background(), DiscoverOptions{Dirs: []string{dir}, Timeout: 10 * time.Second})
	if len(got) != 1 || got[0].PHPVersion != "8.4.1" {
		t.Fatalf("Discover = %+v", got)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if got := Discover(ctx, DiscoverOptions{Dirs: []string{dir}}); len(got) != 0 {
		t.Errorf("cancelled Discover returned %d", len(got))
	}
}

func TestDiscoverDefaultDirs(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX only")
	}
	home := t.TempDir()
	herd := filepath.Join(home, ".config", "herd-lite", "bin")
	if err := os.MkdirAll(herd, 0o755); err != nil {
		t.Fatal(err)
	}
	fakePHP(t, herd, "php", "8.3.99")
	// Restrict PATH to an empty directory so the result only depends on the
	// fake Herd directory plus whatever system directories exist.
	got := Discover(context.Background(), DiscoverOptions{PathEnv: t.TempDir(), Home: home})
	found := false
	for _, l := range got {
		if l.PHPVersion == "8.3.99" {
			found = true
		}
	}
	if !found {
		t.Errorf("fake herd php not discovered: %+v", got)
	}
}

func TestDefaultSearchDirs(t *testing.T) {
	home := t.TempDir()
	pathDir := t.TempDir()
	mk := func(parts ...string) string {
		p := filepath.Join(append([]string{home}, parts...)...)
		if err := os.MkdirAll(p, 0o755); err != nil {
			t.Fatal(err)
		}
		return p
	}
	herd := mk(".config", "herd", "bin")
	phpenv := mk(".phpenv", "versions", "8.2.10", "bin")
	asdf := mk(".asdf", "shims")
	asdfData := t.TempDir()
	asdfInstall := filepath.Join(asdfData, "installs", "php", "8.1.0", "bin")
	if err := os.MkdirAll(asdfInstall, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("ASDF_DATA_DIR", asdfData)

	dirs := DefaultSearchDirs(pathDir+string(os.PathListSeparator)+pathDir+string(os.PathListSeparator)+filepath.Join(home, "nope"), home)
	if len(dirs) == 0 || dirs[0] != pathDir {
		t.Fatalf("PATH entry must come first: %v", dirs)
	}
	count := 0
	for _, d := range dirs {
		if d == pathDir {
			count++
		}
	}
	if count != 1 {
		t.Errorf("PATH dir listed %d times", count)
	}
	for _, want := range []string{herd, phpenv, asdf, asdfInstall} {
		if !contains(dirs, want) {
			t.Errorf("missing %s in %v", want, dirs)
		}
	}
	if contains(dirs, filepath.Join(home, "nope")) {
		t.Error("non-existing dir listed")
	}
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

// TestRealPHP exercises an installed php if there is one; it never fails
// because php is missing.
func TestRealPHP(t *testing.T) {
	got := Discover(context.Background(), DiscoverOptions{})
	if len(got) == 0 {
		t.Skip("no php installed")
	}
	if _, err := got[0].Modules(context.Background()); err != nil {
		t.Errorf("Modules: %v", err)
	}
	if _, err := got[0].Ini(context.Background()); err != nil {
		t.Errorf("Ini: %v", err)
	}
}
