package controller

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ape-dev-de/tiegel/internal/api"
)

func TestLocalInProcessStaysOnItsApp(t *testing.T) {
	a, err := api.New(api.Options{ConfigDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	ctx := context.Background()
	w, err := a.EnableWorkspace(api.WorkspaceOptions{})
	if err != nil {
		t.Fatal(err)
	}
	dirA, _ := filepath.EvalSymlinks(t.TempDir())
	dirB, _ := filepath.EvalSymlinks(t.TempDir())
	if _, err := w.Open(ctx, dirA); err != nil {
		t.Fatal(err)
	}
	if _, err := w.OpenWith(ctx, dirB, "background"); err != nil {
		t.Fatal(err)
	}
	var info ProjectInfo
	local := NewLocalInProcess(a)
	if err := local.Call(ctx, "project.info", nil, &info); err != nil || info.Root != dirA {
		t.Fatalf("local: %+v %v", info, err)
	}
	if err := NewInProcess(a).Call(ctx, "project.info", nil, &info); err != nil || info.Root != dirB {
		t.Fatalf("forwarded: %+v %v", info, err)
	}
	appB, err := w.App(dirB)
	if err != nil || appB == nil {
		t.Fatal(err)
	}
	if err := NewLocalInProcess(appB).Call(ctx, "project.info", nil, &info); err != nil || info.Root != dirB {
		t.Fatalf("member: %+v %v", info, err)
	}
}

func TestAdoptReleaseRestoreTabs(t *testing.T) {
	f := projectFake()
	c := started(t, f)
	// The App's project is released by the workspace: welcome state.
	c.ProjectReleased()
	if c.Project.Open {
		t.Fatal("still open")
	}
	// The workspace opened a project in the App: the controller shows it.
	f.answer("project.info", ProjectInfo{Open: true, Root: root, Name: "proj"})
	c.AdoptProject()
	c.Settle(2 * time.Second)
	if !c.Project.Open || c.Project.Root != root {
		t.Fatalf("not adopted: %+v", c.Project)
	}
	starts := f.called("lsp.start")
	c.Activated()
	c.Settle(2 * time.Second)
	if f.called("lsp.start") != starts+1 {
		t.Fatal("activation did not start the language server")
	}
	// Tabs come back in their order, the active one with its caret.
	files := []string{root + "/a.php", root + "/b.php", root + "/c.php"}
	c.RestoreTabs(files, files[1], 7)
	c.Settle(2 * time.Second)
	var got []string
	for _, tab := range c.Tabs {
		got = append(got, tab.Path)
	}
	if strings.Join(got, ",") != "a.php,b.php,c.php" || c.ActiveTab().File != files[1] || c.Reveal == nil || c.Reveal.Range.Start.Offset != 7 {
		t.Fatalf("tabs %v active %+v", got, c.ActiveTab())
	}
	// Without a project in the App, adopting returns to the welcome state.
	f.answer("project.info", ProjectInfo{})
	c.AdoptProject()
	c.Settle(2 * time.Second)
	if c.Project.Open {
		t.Fatal("still open after the App lost its project")
	}
}
