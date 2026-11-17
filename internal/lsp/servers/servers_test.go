package servers

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"testing"
)

func touch(t *testing.T, dir, name string, mode os.FileMode) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte("#!/bin/sh\n"), mode); err != nil {
		t.Fatal(err)
	}
	return p
}

var (
	phpactor     = Definition{ID: "phpactor", Name: "Phpactor", Binaries: []string{"phpactor"}, Args: []string{"language-server"}, PHPVersionOption: "php.version"}
	phpantom     = Definition{ID: "phpantom", Name: "PHPantom", Binaries: []string{"phpantom_lsp", "phpantom"}}
	intelephense = Definition{ID: "intelephense", Binaries: []string{"intelephense"}, Args: []string{"--stdio"}, Proprietary: true, PHPVersionSetting: "intelephense.environment.phpVersion"}
)

func TestDetect(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix executable bits")
	}
	root := t.TempDir()
	home := filepath.Join(root, "home")
	project := filepath.Join(root, "project")
	pathDir := filepath.Join(root, "path")
	extra := filepath.Join(root, "extra")

	projPhpactor := touch(t, filepath.Join(project, "vendor", "bin"), "phpactor", 0o755)
	homePhpactor := touch(t, filepath.Join(home, ".config", "composer", "vendor", "bin"), "phpactor", 0o755)
	touch(t, filepath.Join(home, ".composer", "vendor", "bin"), "phpactor", 0o644) // not executable
	antom := touch(t, filepath.Join(home, ".cargo", "bin"), "phpantom_lsp", 0o755)
	intel := touch(t, pathDir, "intelephense", 0o755)
	if err := os.MkdirAll(filepath.Join(extra, "phpactor"), 0o755); err != nil { // directory, ignored
		t.Fatal(err)
	}
	linkDir := filepath.Join(root, "linkdir")
	if err := os.MkdirAll(linkDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(projPhpactor, filepath.Join(linkDir, "phpactor")); err != nil {
		t.Fatal(err)
	}

	opts := DetectOptions{
		ProjectDir:   project,
		HomeDir:      home,
		PathEnv:      pathDir + string(os.PathListSeparator) + pathDir,
		ComposerHome: filepath.Join(root, "nocomposer"),
		ExtraDirs:    []string{extra, linkDir},
		NoSystemDirs: true,
	}
	got := Detect([]Definition{phpactor, phpantom}, opts)
	var paths []string
	for _, s := range got {
		paths = append(paths, s.ID+"="+s.Path)
	}
	want := []string{"phpactor=" + projPhpactor, "phpactor=" + homePhpactor, "phpantom=" + antom}
	if !reflect.DeepEqual(paths, want) {
		t.Fatalf("got %v\nwant %v", paths, want)
	}

	dirs := SearchDirs(opts)
	s, ok := Locate(intelephense, dirs)
	if !ok || s.Path != intel {
		t.Fatalf("locate intelephense: %+v %v", s, ok)
	}
	cmd, args := s.Command()
	if cmd != intel || !reflect.DeepEqual(args, []string{"--stdio"}) {
		t.Fatalf("command %s %v", cmd, args)
	}
	s, ok = Locate(phpactor, dirs)
	if !ok || s.Path != projPhpactor {
		t.Fatalf("locate phpactor: %+v", s)
	}
	if _, ok := Locate(Definition{ID: "nope", Binaries: []string{"nope"}}, dirs); ok {
		t.Fatal("unknown server found")
	}

	// CheckExecutable: bare names, paths, directories, missing x bit.
	if p, err := CheckExecutable("phpantom_lsp", dirs); err != nil || p != antom {
		t.Fatalf("bare name %q %v", p, err)
	}
	if _, err := CheckExecutable("nope", dirs); !errors.Is(err, ErrNotFound) {
		t.Fatalf("bare missing %v", err)
	}
	if p, err := CheckExecutable(intel, nil); err != nil || p != intel {
		t.Fatalf("path %q %v", p, err)
	}
	if _, err := CheckExecutable(filepath.Join(root, "missing"), nil); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing %v", err)
	}
	if _, err := CheckExecutable(extra, nil); !errors.Is(err, ErrNotExecutable) {
		t.Fatalf("dir %v", err)
	}
	if _, err := CheckExecutable(filepath.Join(home, ".composer", "vendor", "bin", "phpactor"), nil); !errors.Is(err, ErrNotExecutable) {
		t.Fatalf("no x bit %v", err)
	}
}

func TestSearchDirs(t *testing.T) {
	t.Setenv("PATH", "/a/bin")
	t.Setenv("COMPOSER_HOME", "/composer")
	t.Setenv("HOME", "/h")
	t.Setenv("USERPROFILE", "/h")
	dirs := SearchDirs(DetectOptions{LoginPath: func() string { return "/login/bin" + string(os.PathListSeparator) + "/a/bin" }})
	has := func(d string) bool {
		for _, x := range dirs {
			if x == filepath.Clean(d) {
				return true
			}
		}
		return false
	}
	for _, d := range []string{"/a/bin", "/composer/vendor/bin", "/h/.composer/vendor/bin", "/h/.config/composer/vendor/bin", "/login/bin"} {
		if !has(d) {
			t.Errorf("missing %s in %v", d, dirs)
		}
	}
	if runtime.GOOS == "darwin" && !has("/opt/homebrew/bin") {
		t.Errorf("missing homebrew in %v", dirs)
	}
	if dirs[0] != filepath.Clean("/a/bin") || dirs[1] != filepath.Clean("/login/bin") {
		t.Errorf("PATH, then login PATH should come first without project: %v", dirs)
	}
	// Without system dirs the login shell is not asked.
	dirs = SearchDirs(DetectOptions{NoSystemDirs: true, LoginPath: func() string { t.Fatal("login shell asked"); return "" }})
	if has("/login/bin") {
		t.Fatal("login path used")
	}
}

func TestOptions(t *testing.T) {
	init, settings := phpactor.Options("8.3")
	if init["php.version"] != "8.3" || len(settings) != 0 {
		t.Fatalf("phpactor %v %v", init, settings)
	}
	init, settings = intelephense.Options("8.2")
	want := map[string]any{"intelephense": map[string]any{"environment": map[string]any{"phpVersion": "8.2"}}}
	if len(init) != 0 || !reflect.DeepEqual(settings, want) {
		t.Fatalf("intelephense %v %v", init, settings)
	}
	init, settings = phpantom.Options("8.4")
	if len(init) != 0 || len(settings) != 0 {
		t.Fatalf("phpantom %v %v", init, settings)
	}
	init, settings = phpactor.Options("")
	if init == nil || settings == nil || len(init) != 0 {
		t.Fatal("empty version")
	}
	m := map[string]any{"a": "scalar"}
	setNested(m, []string{"a", "b"}, 1)
	if !reflect.DeepEqual(m, map[string]any{"a": map[string]any{"b": 1}}) {
		t.Fatalf("setNested %v", m)
	}
}
