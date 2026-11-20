package controller

import (
	"context"
	"path/filepath"
	"testing"

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
	if _, err := w.Open(ctx, dirB); err != nil {
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
