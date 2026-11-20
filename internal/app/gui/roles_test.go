package gui

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/ape-dev-de/tiegel/internal/ui/fileicons"
	"github.com/ape-dev-de/tiegel/internal/ui/organisms/filetree"
)

// Folder roles of the project tree come from composer.json (project.roles)
// and follow a saved composer.json.
func TestTreeFolderRolesFromComposer(t *testing.T) {
	e := newEnvWith(t, true, map[string]string{
		"composer.json":  `{"autoload": {"psr-4": {"App\\": "lib/"}}, "autoload-dev": {"psr-4": {"Spec\\": "spec/"}}}`,
		"lib/A.php":      "<?php\n",
		"spec/ATest.php": "<?php\n",
	}, nil)
	e.waitFor("roles", func() bool { return e.ctl.RoleOf("lib") == "source" && e.ctl.RoleOf("spec") == "test" })
	if r := e.ctl.RoleOf("src"); r != "" {
		t.Fatalf("src has role %q although composer.json declares the sources", r)
	}
	icon := func(p string) string {
		return e.v.treeIcon(&filetree.Node{Name: p, Path: p, Dir: true}, false).Name()
	}
	if got, want := icon("lib"), fileicons.GlyphFor("lib", true, fileicons.Hints{SourceRoot: true}).Name(); got != want {
		t.Fatalf("lib icon %q, want %q", got, want)
	}
	if got, want := icon("spec"), fileicons.GlyphFor("spec", true, fileicons.Hints{TestRoot: true}).Name(); got != want {
		t.Fatalf("spec icon %q, want %q", got, want)
	}
	composer := filepath.Join(e.root, "composer.json")
	if err := os.WriteFile(composer, []byte(`{"autoload": {"psr-4": {"App\\": "src/"}}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	e.app.Bus().Publish("file.saved", map[string]any{"path": "composer.json", "file": composer})
	e.waitFor("roles after save", func() bool { return e.ctl.RoleOf("src") == "source" && e.ctl.RoleOf("lib") == "" })
}
