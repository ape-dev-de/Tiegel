package docsync_test

import (
	"context"
	"testing"
	"time"

	"github.com/ape-dev-de/tiegel/internal/api"
	"github.com/ape-dev-de/tiegel/internal/api/apitest"
	"github.com/ape-dev-de/tiegel/internal/api/docsync"
)

// A rename on the server (files.rename) moves the mirror along instead of
// detaching it.
func TestMirrorFollowsRename(t *testing.T) {
	a := apitest.NewApp(t, map[string]string{"a.php": initial})
	ctx := context.Background()
	closed := make(chan struct{})
	m := open(t, apitest.NewPipe(t, a), docsync.Options{OnClosed: func() { close(closed) }})
	if err := m.Insert(ctx, 0, "x"); err != nil {
		t.Fatal(err)
	}
	if _, err := api.Call[api.FileOpResult](ctx, api.NewInProcess(a), "files.rename", api.RenameParams{Path: "a.php", NewName: "b.php"}); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for m.Path() != "b.php" {
		if time.Now().After(deadline) {
			t.Fatalf("mirror path %q", m.Path())
		}
		time.Sleep(5 * time.Millisecond)
	}
	if err := m.Insert(ctx, 0, "y"); err != nil {
		t.Fatal(err)
	}
	if res, err := m.Save(ctx); err != nil || res.Path != "b.php" {
		t.Fatalf("save %+v %v", res, err)
	}
	select {
	case <-closed:
		t.Fatal("mirror detached on rename")
	default:
	}
}
