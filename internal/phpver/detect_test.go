package phpver

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestParseVersion(t *testing.T) {
	cases := map[string]Version{
		"8":         {8, 0, 0},
		"8.2":       {8, 2, 0},
		"8.3.12":    {8, 3, 12},
		"v8.1":      {8, 1, 0},
		" 8.4.0RC1": {8, 4, 0},
		"8.3.0-dev": {8, 3, 0},
		"7.4.33\n":  {7, 4, 33},
		"8.1.":      {8, 1, 0},
		"8.1.2.3":   {8, 1, 2},
	}
	for in, want := range cases {
		got, err := ParseVersion(in)
		if err != nil || got != want {
			t.Errorf("ParseVersion(%q) = %v, %v; want %v", in, got, err, want)
		}
	}
	for _, in := range []string{"", "x", "dev-main", "99999999999999999999"} {
		if _, err := ParseVersion(in); err == nil {
			t.Errorf("ParseVersion(%q): expected error", in)
		}
	}
	v := Version{8, 2, 1}
	if v.String() != "8.2.1" || v.Short() != "8.2" || v.IsZero() || !(Version{}).IsZero() {
		t.Fatal("formatting")
	}
	if !(Version{7, 4, 0}).Less(Version{8, 0, 0}) || !(Version{8, 0, 0}).Less(Version{8, 1, 0}) ||
		!(Version{8, 1, 0}).Less(Version{8, 1, 1}) || v.Compare(v) != 0 || (Version{9, 0, 0}).Compare(v) != 1 {
		t.Fatal("compare")
	}
}

func TestMinVersion(t *testing.T) {
	cases := map[string]Version{
		"^8.1":                  {8, 1, 0},
		"~7.4.0":                {7, 4, 0},
		"~8":                    {8, 0, 0},
		">=8.0 <8.4":            {8, 0, 0},
		">=8.0,<8.4":            {8, 0, 0},
		">= 8.2":                {8, 2, 0},
		"^7.4 || ^8.0":          {7, 4, 0},
		"^8.0|^7.3":             {7, 3, 0},
		"8.1 - 8.3":             {8, 1, 0},
		"8.2.*":                 {8, 2, 0},
		"8.x":                   {8, 0, 0},
		"8.1.0@dev":             {8, 1, 0},
		">8.1":                  {8, 1, 1},
		"=8.3.4":                {8, 3, 4},
		"==8.3":                 {8, 3, 0},
		"8.2":                   {8, 2, 0},
		">=7.2 >=8.1":           {8, 1, 0},
		"^8.1 <8.3 !=8.2.1":     {8, 1, 0},
		">=7.1 <8.0 || >=8.1.2": {7, 1, 0},
		"v8.0":                  {8, 0, 0},
	}
	for in, want := range cases {
		got, err := MinVersion(in)
		if err != nil || got != want {
			t.Errorf("MinVersion(%q) = %v, %v; want %v", in, got, err, want)
		}
	}
	for _, in := range []string{"*", "<8.0", "^8.0 || *", "!=8.0"} {
		if _, err := MinVersion(in); !errors.Is(err, ErrNoLowerBound) {
			t.Errorf("MinVersion(%q): want ErrNoLowerBound, got %v", in, err)
		}
	}
	for _, in := range []string{"", "^", "abc", "^8 || ", ">=foo"} {
		if _, err := MinVersion(in); err == nil {
			t.Errorf("MinVersion(%q): expected error", in)
		}
	}
}

type fakeRunner struct {
	out  string
	err  error
	args []string
}

func (f *fakeRunner) RunPHP(_ context.Context, args ...string) ([]byte, error) {
	f.args = args
	return []byte(f.out), f.err
}

const composer = `{
  "require": {"php": "^8.1", "ext-json": "*"},
  "config": {"platform": {"php": "8.2.10"}}
}`

func TestDetectOrder(t *testing.T) {
	ctx := context.Background()
	over := Version{7, 4, 0}

	r, err := Detect(ctx, Options{Override: &over, Runner: &fakeRunner{out: "8.3.1"}})
	if err != nil || r.Source != SourceOverride || r.Version != over {
		t.Fatalf("override: %+v %v", r, err)
	}

	fr := &fakeRunner{out: "8.3.1\n"}
	r, err = Detect(ctx, Options{Runner: fr, ComposerJSON: []byte(composer)})
	if err != nil || r.Source != SourceInterpreter || r.Version != (Version{8, 3, 1}) || r.Detail != "8.3.1" {
		t.Fatalf("interpreter: %+v %v", r, err)
	}
	if len(fr.args) != 2 || fr.args[0] != "-r" || fr.args[1] != "echo PHP_VERSION;" {
		t.Fatalf("runner args: %q", fr.args)
	}

	r, err = Detect(ctx, Options{Runner: &fakeRunner{err: errors.New("no php")}, ComposerJSON: []byte(composer)})
	if err != nil || r.Source != SourcePlatform || r.Version != (Version{8, 2, 10}) {
		t.Fatalf("platform: %+v %v", r, err)
	}

	r, err = Detect(ctx, Options{Runner: &fakeRunner{out: "garbage"}, ComposerJSON: []byte(`{"require":{"php":">=7.4 <9"}}`)})
	if err != nil || r.Source != SourceRequire || r.Version != (Version{7, 4, 0}) || r.Detail != ">=7.4 <9" {
		t.Fatalf("require: %+v %v", r, err)
	}

	// platform.php: false disables the platform override.
	r, err = Detect(ctx, Options{ComposerJSON: []byte(`{"require":{"php":"^8.0"},"config":{"platform":{"php":false}}}`)})
	if err != nil || r.Source != SourceRequire {
		t.Fatalf("platform false: %+v %v", r, err)
	}

	_, err = Detect(ctx, Options{ComposerJSON: []byte(`{"require":{"php":"*"}}`), Runner: &fakeRunner{err: errors.New("boom")}})
	if !errors.Is(err, ErrUnknown) || !errors.Is(err, ErrNoLowerBound) {
		t.Fatalf("unknown: %v", err)
	}
	_, err = Detect(ctx, Options{ComposerJSON: []byte(`{nope`)})
	if !errors.Is(err, ErrUnknown) {
		t.Fatalf("invalid json: %v", err)
	}
	_, err = Detect(ctx, Options{ComposerJSON: []byte(`{"config":{"platform":{"php":"abc"}}}`)})
	if !errors.Is(err, ErrUnknown) {
		t.Fatalf("bad platform: %v", err)
	}
	for _, s := range []Source{SourceUnknown, SourceOverride, SourceInterpreter, SourcePlatform, SourceRequire} {
		if s.String() == "" {
			t.Fatal("empty source name")
		}
	}
}

func TestDetectProjectDir(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "composer.json"), []byte(composer), 0o600); err != nil {
		t.Fatal(err)
	}
	r, err := Detect(context.Background(), Options{ProjectDir: dir})
	if err != nil || r.Source != SourcePlatform {
		t.Fatalf("%+v %v", r, err)
	}
	// Missing composer.json is not an error of its own.
	_, err = Detect(context.Background(), Options{ProjectDir: t.TempDir()})
	if !errors.Is(err, ErrUnknown) {
		t.Fatal(err)
	}
	// composer.json that is a directory: read error is reported.
	bad := t.TempDir()
	if err := os.Mkdir(filepath.Join(bad, "composer.json"), 0o700); err != nil {
		t.Fatal(err)
	}
	_, err = Detect(context.Background(), Options{ProjectDir: bad})
	if !errors.Is(err, ErrUnknown) {
		t.Fatal(err)
	}
}

func TestComposerHelpersErrors(t *testing.T) {
	if _, _, _, err := PlatformVersion([]byte("{")); err == nil {
		t.Fatal("platform: expected error")
	}
	if _, _, _, err := RequireVersion([]byte("{")); err == nil {
		t.Fatal("require: expected error")
	}
	if _, _, ok, err := RequireVersion([]byte(`{}`)); ok || err != nil {
		t.Fatal("require: absent")
	}
}

// fakePHP writes a shell script that behaves like `php -r 'echo PHP_VERSION;'`.
func fakePHP(t *testing.T, body string) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("shell script runner")
	}
	p := filepath.Join(t.TempDir(), "php")
	if err := os.WriteFile(p, []byte("#!/bin/sh\n"+body+"\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestLocalRunner(t *testing.T) {
	ctx := context.Background()
	v, raw, err := InterpreterVersion(ctx, LocalRunner{Binary: fakePHP(t, `printf '8.2.7'`)})
	if err != nil || v != (Version{8, 2, 7}) || raw != "8.2.7" {
		t.Fatalf("%v %q %v", v, raw, err)
	}
	_, _, err = InterpreterVersion(ctx, LocalRunner{Binary: fakePHP(t, `echo broken >&2; exit 3`)})
	if err == nil || !strings.Contains(err.Error(), "broken") {
		t.Fatalf("stderr not reported: %v", err)
	}
	_, _, err = InterpreterVersion(ctx, LocalRunner{Binary: fakePHP(t, `exit 1`)})
	if err == nil {
		t.Fatal("exit code ignored")
	}
	_, _, err = InterpreterVersion(ctx, LocalRunner{Binary: filepath.Join(t.TempDir(), "missing")})
	if err == nil {
		t.Fatal("missing binary")
	}
}

func TestLocalRunnerRealPHP(t *testing.T) {
	if _, err := exec.LookPath("php"); err != nil {
		t.Skip("php not installed")
	}
	v, _, err := InterpreterVersion(context.Background(), LocalRunner{})
	if err != nil {
		t.Fatal(err)
	}
	if v.Major < 5 {
		t.Fatalf("implausible version %v", v)
	}
}
