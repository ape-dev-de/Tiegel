package gui

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/ape-dev-de/tiegel/internal/api"
)

// TestTreeFollowsExternalChanges: files created, renamed and deleted by
// other programs (an editor, git, a container on a bind mount) show up in
// the project tree through the watcher.
func TestTreeFollowsExternalChanges(t *testing.T) {
	e := newEnv(t, true)
	// The backend of the app (FSEvents on macOS), not polling.
	if err := e.ctl.Backend().Call(context.Background(), "watch.start", map[string]any{"backend": "auto"}, nil); err != nil {
		t.Fatal(err)
	}
	e.waitFor("tree", func() bool { n := e.ctl.Tree.Node("src"); return n != nil })
	// Expand src so its children are loaded.
	e.ctl.LoadDir("src")
	e.waitFor("src loaded", func() bool { n := e.ctl.Tree.Node("src"); return n != nil && n.Loaded })

	if err := os.WriteFile(filepath.Join(e.root, "src", "Extern.php"), []byte("<?php\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(e.root, "generated", "deep"), 0o755); err != nil {
		t.Fatal(err)
	}
	e.waitFor("created file", func() bool { return e.ctl.Tree.Node("src/Extern.php") != nil })
	e.waitFor("created dir", func() bool { return e.ctl.Tree.Node("generated") != nil })
	if err := os.Rename(filepath.Join(e.root, "src", "Extern.php"), filepath.Join(e.root, "src", "Moved.php")); err != nil {
		t.Fatal(err)
	}
	e.waitFor("renamed", func() bool {
		return e.ctl.Tree.Node("src/Extern.php") == nil && e.ctl.Tree.Node("src/Moved.php") != nil
	})
	if err := os.Remove(filepath.Join(e.root, "src", "Moved.php")); err != nil {
		t.Fatal(err)
	}
	e.waitFor("deleted", func() bool { return e.ctl.Tree.Node("src/Moved.php") == nil })
}

// TestTreeFollowsExternalChangesInSessions: with several projects per
// window (each its own App and watcher, like the real app), the tree of
// the shown project and of a project shown again follow disk changes.
func TestTreeFollowsExternalChangesInSessions(t *testing.T) {
	e := newWsEnvWith(t, "background", func(o *api.Options) { o.Watch = true })
	alpha := e.cur()
	e.waitFor("alpha tree", func() bool { n := alpha.Tree.Node(""); return n != nil && n.Loaded })
	if err := os.WriteFile(filepath.Join(e.rootA, "fresh.php"), []byte("<?php\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	e.waitFor("alpha sees fresh.php", func() bool { return e.cur().Tree.Node("fresh.php") != nil })

	e.openBeta()
	e.waitFor("beta tree", func() bool { n := e.cur().Tree.Node(""); return n != nil && n.Loaded })
	if err := os.WriteFile(filepath.Join(e.rootB, "other.php"), []byte("<?php\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	e.waitFor("beta sees other.php", func() bool { return e.cur().Tree.Node("other.php") != nil })
}
