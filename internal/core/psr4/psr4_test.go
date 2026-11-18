package psr4

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func write(t *testing.T, dir, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, "composer.json"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestRead(t *testing.T) {
	dir := t.TempDir()
	if m, err := Read(dir); m != nil || err != nil {
		t.Fatalf("missing file: %v %v", m, err)
	}
	write(t, dir, `{"autoload": {"psr-4": {"App\\": "src/", "Lib\\": ["lib", "./more/"], "": ".", "Bad\\": 3}},
		"autoload-dev": {"psr-4": {"App\\Tests\\": "tests"}}}`)
	got, err := Read(dir)
	if err != nil {
		t.Fatal(err)
	}
	want := []Mapping{{Prefix: "", Dir: ""}, {Prefix: "Lib", Dir: "lib"}, {Prefix: "Lib", Dir: "more"}, {Prefix: "App", Dir: "src"}, {Prefix: `App\Tests`, Dir: "tests", Dev: true}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v", got)
	}
	write(t, dir, `{`)
	if _, err := Read(dir); !errors.Is(err, ErrComposer) {
		t.Fatalf("invalid json: %v", err)
	}
}

func TestNamespaceFor(t *testing.T) {
	maps := []Mapping{{Prefix: "App", Dir: "src"}, {Prefix: `App\Domain`, Dir: "src/Domain"}, {Prefix: `App\Tests`, Dir: "tests", Dev: true}}
	for _, tc := range []struct {
		rel, ns string
		found   bool
	}{
		{"src", "App", true}, {"src/Http/Controller", `App\Http\Controller`, true}, {"src/Domain/Order", `App\Domain\Order`, true},
		{"tests/Unit", `App\Tests\Unit`, true}, {"docs", "", false}, {"srcx", "", false},
	} {
		ns, _, found, err := NamespaceFor(maps, tc.rel)
		if err != nil || ns != tc.ns || found != tc.found {
			t.Errorf("%s: %q %v %v", tc.rel, ns, found, err)
		}
	}
	_, _, _, err := NamespaceFor(maps, "src/my-dir")
	var se *SegmentError
	if !errors.As(err, &se) || se.Segment != "my-dir" || se.Error() == "" {
		t.Fatalf("bad segment: %v", err)
	}
	if ns, _, found, _ := NamespaceFor([]Mapping{{Prefix: "", Dir: ""}}, "a/b"); ns != `a\b` || !found {
		t.Fatalf("fallback %q", ns)
	}
	if !ValidNamespace(`A\B_c`) || !ValidNamespace("") || ValidNamespace(`A\\B`) || ValidNamespace("1A") {
		t.Fatal("ValidNamespace")
	}
	if NormDir("./a/b/") != "a/b" || NormDir("/") != "" || NormDir(".") != "" {
		t.Fatal("NormDir")
	}
}
