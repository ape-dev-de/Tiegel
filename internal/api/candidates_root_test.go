package api

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/ape-dev-de/tiegel/internal/core/settings"
)

func TestToolRootFromPHPRootMethod(t *testing.T) {
	a := newTestApp(t)
	dir := t.TempDir()
	// sub has vendor/ (ranked first), other only composer.json.
	writeFile(t, filepath.Join(dir, "sub", "composer.json"), "{}")
	writeFile(t, filepath.Join(dir, "sub", "vendor", "autoload.php"), "<?php")
	writeFile(t, filepath.Join(dir, "other", "composer.json"), "{}")
	call(t, a, "project.open", PathParams{Path: dir}, nil)
	root := a.ProjectRoot()
	if got := a.ToolRoot(context.Background()); got != filepath.Join(root, "sub") {
		t.Fatalf("tool root %s", got)
	}
	var res CandidatesResult
	call(t, a, "settings.candidates", CandidatesParams{Key: keyPHPRoot}, &res)
	if len(res.Candidates) != 2 || res.Candidates[0].Value != "sub" || !res.Candidates[0].Recommended || res.Candidates[1].Value != "other" {
		t.Fatalf("php root candidates %+v", res)
	}
	if a.absProjectPath("") != root || a.absProjectPath("/x") != "/x" {
		t.Fatal("absProjectPath")
	}
}

func TestToolRootFallbacks(t *testing.T) {
	a := newTestApp(t)
	if a.ToolRoot(context.Background()) != "" {
		t.Fatal("no project")
	}
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "composer.json"), "{}")
	writeFile(t, filepath.Join(dir, "lib", "composer.json"), "{}")
	call(t, a, "project.open", PathParams{Path: dir}, nil)
	root := a.ProjectRoot()
	if got := a.ToolRoot(context.Background()); got != root {
		t.Fatalf("root composer %s", got)
	}
	if err := settings.Set(a.ProjectSettings(), keyPHPRoot, "lib"); err != nil {
		t.Fatal(err)
	}
	if got := a.ToolRoot(context.Background()); got != filepath.Join(root, "lib") {
		t.Fatalf("setting %s", got)
	}
	other := newTestApp(t)
	call(t, other, "project.open", PathParams{Path: t.TempDir()}, nil)
	if got := other.ToolRoot(context.Background()); got != other.ProjectRoot() {
		t.Fatalf("no composer %s", got)
	}
}
