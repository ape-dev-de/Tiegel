package api

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestProjectRoles(t *testing.T) {
	a := newTestApp(t)
	wantCode(t, a, "project.roles", nil, CodeNoProject)
	root := t.TempDir()
	for _, d := range []string{"lib", "spec", "vendor", "public"} {
		if err := os.MkdirAll(filepath.Join(root, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	composer := filepath.Join(root, "composer.json")
	if err := os.WriteFile(composer, []byte(`{"autoload":{"psr-4":{"App\\":"lib/"}},"autoload-dev":{"psr-4":{"Tests\\":"spec/"}}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	call(t, a, "project.open", PathParams{Path: root}, nil)
	var res ProjectRolesResult
	call(t, a, "project.roles", nil, &res)
	got := ""
	for _, r := range res.Roots {
		got += r.Path + ":" + string(r.Role) + ":" + string(r.Origin) + " "
	}
	if got != "lib:source:composer public:resource:name spec:test:composer vendor:library:composer " || res.Warning != "" {
		t.Fatalf("roots %q warning %q", got, res.Warning)
	}
	if err := os.WriteFile(composer, []byte(`{`), 0o644); err != nil {
		t.Fatal(err)
	}
	call(t, a, "project.roles", nil, &res)
	if !strings.Contains(res.Warning, "composer.json") || len(res.Roots) != 2 || res.Roots[0].Origin != "name" {
		t.Fatalf("invalid composer.json: %+v", res)
	}
}
