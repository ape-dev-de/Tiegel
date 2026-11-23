package projectroles

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestDetectFromNested(t *testing.T) {
	root := t.TempDir()
	for _, d := range []string{"project/app/Lib", "project/ModulesTest", "project/Vendor", "docs", "public"} {
		if err := os.MkdirAll(filepath.Join(root, filepath.FromSlash(d)), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	cj := `{"autoload":{"psr-4":{"Lib\\":"app/Lib"}},"autoload-dev":{"psr-4":{"T\\":"ModulesTest"}},"config":{"vendor-dir":"Vendor/"}}`
	if err := os.WriteFile(filepath.Join(root, "project", "composer.json"), []byte(cj), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := DetectFrom(root, filepath.Join(root, "project"))
	if err != nil {
		t.Fatal(err)
	}
	want := []Root{
		{Path: "project/ModulesTest", Role: Test, Origin: FromComposer},
		{Path: "project/Vendor", Role: Library, Origin: FromComposer},
		{Path: "project/app/Lib", Role: Source, Origin: FromComposer},
		{Path: "public", Role: Resource, Origin: FromName},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v", got)
	}
	// A PHP root outside the project falls back to the project root.
	if got, _ := DetectFrom(root, t.TempDir()); len(got) != 1 || got[0].Path != "public" {
		t.Fatalf("outside %+v", got)
	}
	if got, _ := DetectFrom(root, ""); len(got) != 1 {
		t.Fatalf("empty php root %+v", got)
	}
}
