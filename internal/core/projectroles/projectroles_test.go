package projectroles

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func project(t *testing.T, composer string, dirs ...string) string {
	t.Helper()
	root := t.TempDir()
	for _, d := range dirs {
		if err := os.MkdirAll(filepath.Join(root, filepath.FromSlash(d)), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if composer != "" {
		if err := os.WriteFile(filepath.Join(root, "composer.json"), []byte(composer), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func TestDetectFromComposer(t *testing.T) {
	root := project(t, `{
		"autoload": {"psr-4": {"App\\": "lib/", "Domain\\": ["modules/domain", "missing/"]}, "psr-0": {"Legacy_": "legacy"}, "classmap": ["database/", "helpers.php"]},
		"autoload-dev": {"psr-4": {"Tests\\": "spec/"}, "classmap": ["lib"]},
		"config": {"vendor-dir": "deps"}
	}`, "lib", "modules/domain", "legacy", "database", "spec", "deps", "src", "tests", "public", "vendor")
	if err := os.WriteFile(filepath.Join(root, "helpers.php"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := Detect(root)
	if err != nil {
		t.Fatal(err)
	}
	want := []Root{
		{"database", Source, FromComposer},
		{"deps", Library, FromComposer},
		{"legacy", Source, FromComposer},
		{"lib", Source, FromComposer}, // autoload wins over autoload-dev
		{"modules/domain", Source, FromComposer},
		{"public", Resource, FromName},
		{"spec", Test, FromComposer},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("roots\n got %+v\nwant %+v", got, want)
	}
	if r, ok := Lookup(got, "modules/domain/"); !ok || r != Source {
		t.Fatalf("lookup %v %v", r, ok)
	}
	if _, ok := Lookup(got, "src"); ok {
		t.Fatal("src is no root when composer declares the sources")
	}
}

func TestDetectFallbackNames(t *testing.T) {
	root := project(t, "", "src", "tests", "assets", "vendor", "docs")
	got, err := Detect(root)
	if err != nil {
		t.Fatal(err)
	}
	want := []Root{{"assets", Resource, FromName}, {"src", Source, FromName}, {"tests", Test, FromName}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v", got)
	}
	// composer.json with only autoload: tests still by name, vendor by
	// default vendor-dir.
	root = project(t, `{"autoload": {"psr-4": {"A\\": "src"}}}`, "src", "tests", "vendor")
	got, _ = Detect(root)
	want = []Root{{"src", Source, FromComposer}, {"tests", Test, FromName}, {"vendor", Library, FromComposer}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v", got)
	}
}

func TestDetectInvalidComposer(t *testing.T) {
	root := project(t, `{"autoload": `, "src")
	got, err := Detect(root)
	if !errors.Is(err, ErrComposer) {
		t.Fatalf("err %v", err)
	}
	if len(got) != 1 || got[0].Path != "src" {
		t.Fatalf("fallback %+v", got)
	}
}

func TestNormRejectsOutsidePaths(t *testing.T) {
	for in, want := range map[string]string{"": "", ".": "", "./": "", "../x": "", "/abs": "", `src\Foo`: "src/Foo", "a/./b/": "a/b"} {
		if got := norm(in); got != want {
			t.Errorf("norm(%q) = %q, want %q", in, got, want)
		}
	}
}
