package servers

import (
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
	phpantom := touch(t, filepath.Join(home, ".cargo", "bin"), "phpantom_lsp", 0o755)
	intelephense := touch(t, pathDir, "intelephense", 0o755)
	if err := os.MkdirAll(filepath.Join(extra, "phpactor"), 0o755); err != nil { // directory, ignored
		t.Fatal(err)
	}
	// Symlink to an already found binary is reported once.
	if err := os.Symlink(projPhpactor, filepath.Join(extra, "phpactor-link")); err != nil {
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
	got := Detect(opts)
	var paths []string
	for _, s := range got {
		paths = append(paths, s.ID+"="+s.Path)
	}
	want := []string{"phpactor=" + projPhpactor, "phpactor=" + homePhpactor, "phpantom=" + phpantom}
	if !reflect.DeepEqual(paths, want) {
		t.Fatalf("got %v\nwant %v", paths, want)
	}

	opts.IncludeProprietary = true
	got = Detect(opts)
	last := got[len(got)-1]
	if last.ID != Intelephense || !last.Proprietary || last.Path != intelephense {
		t.Fatalf("intelephense: %+v", last)
	}
	cmd, args := last.Command()
	if cmd != intelephense || !reflect.DeepEqual(args, []string{"--stdio"}) {
		t.Fatalf("command %s %v", cmd, args)
	}

	opts.IncludeProprietary = false
	s, ok := Find(Intelephense, opts)
	if !ok || s.Path != intelephense {
		t.Fatalf("find intelephense: %+v %v", s, ok)
	}
	s, ok = Find(Phpactor, opts)
	if !ok || s.Path != projPhpactor {
		t.Fatalf("find phpactor: %+v", s)
	}
	cmd, args = s.Command()
	if cmd != projPhpactor || !reflect.DeepEqual(args, []string{"language-server"}) {
		t.Fatalf("phpactor command %s %v", cmd, args)
	}
	if _, ok := Find("nope", opts); ok {
		t.Fatal("unknown server found")
	}
}

func TestSearchDirs(t *testing.T) {
	t.Setenv("PATH", "/a/bin")
	t.Setenv("COMPOSER_HOME", "/composer")
	t.Setenv("HOME", "/h")
	t.Setenv("USERPROFILE", "/h")
	dirs := SearchDirs(DetectOptions{})
	has := func(d string) bool {
		for _, x := range dirs {
			if x == filepath.Clean(d) {
				return true
			}
		}
		return false
	}
	for _, d := range []string{"/a/bin", "/composer/vendor/bin", "/h/.composer/vendor/bin", "/h/.config/composer/vendor/bin"} {
		if !has(d) {
			t.Errorf("missing %s in %v", d, dirs)
		}
	}
	if runtime.GOOS == "darwin" && !has("/opt/homebrew/bin") {
		t.Errorf("missing homebrew in %v", dirs)
	}
	if dirs[0] != filepath.Clean("/a/bin") {
		t.Errorf("PATH should come first without project: %v", dirs)
	}
}

func TestOptions(t *testing.T) {
	defs := map[string]Definition{}
	for _, d := range Known() {
		defs[d.ID] = d
	}
	init, settings := defs[Phpactor].Options("8.3")
	if init["php.version"] != "8.3" || len(settings) != 0 {
		t.Fatalf("phpactor %v %v", init, settings)
	}
	init, settings = defs[Intelephense].Options("8.2")
	want := map[string]any{"intelephense": map[string]any{"environment": map[string]any{"phpVersion": "8.2"}}}
	if len(init) != 0 || !reflect.DeepEqual(settings, want) {
		t.Fatalf("intelephense %v %v", init, settings)
	}
	init, settings = defs[PHPantom].Options("8.4")
	if len(init) != 0 || len(settings) != 0 {
		t.Fatalf("phpantom %v %v", init, settings)
	}
	init, settings = defs[Phpactor].Options("")
	if init == nil || settings == nil || len(init) != 0 {
		t.Fatal("empty version")
	}
	m := map[string]any{"a": "scalar"}
	setNested(m, []string{"a", "b"}, 1)
	if !reflect.DeepEqual(m, map[string]any{"a": map[string]any{"b": 1}}) {
		t.Fatalf("setNested %v", m)
	}
}
