package phproot

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func mk(t *testing.T, root string, files ...string) {
	t.Helper()
	for _, f := range files {
		p := filepath.Join(root, filepath.FromSlash(f))
		if strings.HasSuffix(f, "/.") { // a directory
			if err := os.MkdirAll(p, 0o755); err != nil {
				t.Fatal(err)
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("{}"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func paths(cs []Candidate) []string {
	var out []string
	for _, c := range cs {
		out = append(out, c.Path)
	}
	return out
}

func TestDetectNested(t *testing.T) {
	root := t.TempDir()
	mk(t, root,
		"project/composer.json", "project/phpunit.xml", "project/vendor/bin/.",
		"tools/composer.json", "tools/composer.lock",
		"a/b/composer.json",
		"a/b/c/composer.json",              // too deep
		"node_modules/x/composer.json",     // skipped
		".hidden/composer.json",            // skipped
		"project/vendor/pkg/composer.json", // vendor skipped
	)
	cs := Detect(root)
	if got := paths(cs); !reflect.DeepEqual(got, []string{"project", "tools", "a/b"}) {
		t.Fatalf("candidates %v", got)
	}
	if !cs[0].Recommended || cs[1].Recommended {
		t.Fatalf("recommended %+v", cs)
	}
	if want := []string{ReasonComposer, ReasonVendor, ReasonPHPUnit}; !reflect.DeepEqual(cs[0].Reasons, want) {
		t.Fatalf("reasons %v", cs[0].Reasons)
	}
	d, auto := Resolve(root, "")
	if d != filepath.Join(root, "project") || !auto {
		t.Fatalf("resolve %s %v", d, auto)
	}
	if d, auto = Resolve(root, "tools"); d != filepath.Join(root, "tools") || auto {
		t.Fatalf("setting %s %v", d, auto)
	}
	// Invalid settings fall back to the automatic choice.
	for _, s := range []string{"missing", "../x", "project/composer.json"} {
		if d, auto = Resolve(root, s); d != filepath.Join(root, "project") || !auto {
			t.Fatalf("%s: %s %v", s, d, auto)
		}
	}
	if d, ok := FromSetting(root, filepath.Join(root, "a")); !ok || d != filepath.Join(root, "a") {
		t.Fatalf("absolute %s %v", d, ok)
	}
	if Rel(root, filepath.Join(root, "a", "b")) != "a/b" || Rel(root, root) != "." || Abs(root, ".") != root {
		t.Fatal("Rel/Abs")
	}
}

func TestDetectRootWins(t *testing.T) {
	root := t.TempDir()
	mk(t, root, "composer.json", "sub/composer.json", "sub/vendor/x/.", "sub/phpunit.xml")
	cs := Detect(root)
	if got := paths(cs); !reflect.DeepEqual(got, []string{".", "sub"}) {
		t.Fatalf("candidates %v", got)
	}
	if d, _ := Resolve(root, ""); d != root {
		t.Fatalf("resolve %s", d)
	}
	// Without composer.json anywhere: the project root.
	empty := t.TempDir()
	if cs := Detect(empty); len(cs) != 0 {
		t.Fatalf("empty %v", cs)
	}
	if d, auto := Resolve(empty, ""); d != empty || !auto {
		t.Fatalf("empty resolve %s", d)
	}
	if d, _ := Resolve("", "x"); d != "" {
		t.Fatal("no project")
	}
}

func TestComposerDirs(t *testing.T) {
	dir := t.TempDir()
	if BinDir(dir) != filepath.Join(dir, "vendor", "bin") || Vendor(dir) != filepath.Join(dir, "vendor") {
		t.Fatal("defaults")
	}
	if err := os.WriteFile(filepath.Join(dir, "composer.json"), []byte(`{"config":{"vendor-dir":"Vendor/"}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if BinDir(dir) != filepath.Join(dir, "Vendor", "bin") || VendorDir([]byte("{")) != "vendor" {
		t.Fatalf("vendor-dir %s", BinDir(dir))
	}
	if err := os.WriteFile(filepath.Join(dir, "composer.json"), []byte(`{"config":{"bin-dir":"bin"}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if BinDir(dir) != filepath.Join(dir, "bin") {
		t.Fatalf("bin-dir %s", BinDir(dir))
	}
	abs := filepath.Join(dir, "elsewhere")
	if join(dir, abs) != abs {
		t.Fatal("join abs")
	}
}
