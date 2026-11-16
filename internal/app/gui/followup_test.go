package gui

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ape-dev-de/tiegel/internal/app/gui/controller"
	"github.com/ape-dev-de/tiegel/internal/gitcli"
	"github.com/ape-dev-de/tiegel/internal/ui/organisms/filetree"
	"github.com/ape-dev-de/tiegel/internal/ui/organisms/palette"
)

// Opening a file reveals it in the tree: its parent directories expand
// and must be loaded (before, "src loading…" stayed forever because the
// expansion emitted no ExpandEvent and nobody listed the directory).
func TestRevealedFileLoadsParents(t *testing.T) {
	e := newEnvWith(t, true, map[string]string{"src/Deep/Nested/Thing.php": "<?php\n"}, nil)
	if n := e.ctl.Tree.Node("src"); n == nil || n.Loaded {
		t.Fatalf("src should start unloaded: %+v", n)
	}
	e.ctl.OpenFile("src/Deep/Nested/Thing.php", nil)
	e.waitFor("tab", func() bool { return e.ctl.ActiveTab() != nil })
	e.waitFor("parents loaded", func() bool {
		n := e.ctl.Tree.Node("src/Deep/Nested")
		return n != nil && n.Loaded
	})
	e.frame()
	for _, d := range []string{"src", "src/Deep", "src/Deep/Nested"} {
		if !e.v.tree.Expanded(d) {
			t.Errorf("%s not expanded", d)
		}
	}
	var walk func(n *filetree.Node) bool
	walk = func(n *filetree.Node) bool {
		if n.Dir && e.v.tree.Expanded(n.Path) && !n.Loaded {
			t.Errorf("%q still loading", n.Path)
		}
		for _, ch := range n.Children {
			walk(ch)
		}
		return true
	}
	walk(e.v.tree.Root)
}

// newGitEnvWith is newGitEnv with extra committed files.
func newGitEnvWith(t *testing.T, extra map[string]string) *env {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	return newEnvWith(t, true, extra, func(root string) {
		runGit(t, root, "init", "-q", "-b", "main")
		runGit(t, root, "config", "user.name", "Tiegel Test")
		runGit(t, root, "config", "user.email", "test@example.invalid")
		runGit(t, root, "config", "commit.gpgsign", "false")
		runGit(t, root, "add", ".")
		runGit(t, root, "commit", "-q", "-m", "Initial commit")
	})
}

// Review 2026-11-16 #6 (GUI): the hunk hashes the GUI computes from the
// raw diff are exactly the module's (git.diff), a partial commit sends
// them, and a selection made on an outdated diff is refused: the diff is
// reloaded and the panel explains it.
func TestPartialCommitHunkIdentity(t *testing.T) {
	var b strings.Builder
	for i := 1; i <= 30; i++ {
		fmt.Fprintf(&b, "line %d\n", i)
	}
	orig := b.String()
	e := newGitEnvWith(t, map[string]string{"long.txt": orig})
	e.waitFor("git available", func() bool { return e.ctl.Vcs.Available })
	edit := func(s string, ns ...int) string {
		for _, n := range ns {
			s = strings.Replace(s, fmt.Sprintf("line %d\n", n), fmt.Sprintf("LINE %d\n", n), 1)
		}
		return s
	}
	file := filepath.Join(e.root, "long.txt")
	write := func(s string) {
		if err := os.WriteFile(file, []byte(s), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write(edit(orig, 1, 30))
	g := e.ctl.Vcs
	change := func() *gitcli.FileDiff {
		for _, c := range g.Changes {
			if c.Status.Path == "long.txt" {
				return c.Diff
			}
		}
		return nil
	}
	g.LoadChanges()
	e.waitFor("diff", func() bool { d := change(); return d != nil && len(d.Hunks) == 2 })

	var res struct {
		Files []struct {
			Path  string `json:"path"`
			Hunks []struct {
				Header string `json:"header"`
				Hash   string `json:"hash"`
			} `json:"hunks"`
		} `json:"files"`
	}
	if err := e.ctl.Backend().Call(context.Background(), "git.diff", map[string]any{"base": "HEAD", "paths": []string{"long.txt"}}, &res); err != nil {
		t.Fatal(err)
	}
	d := change()
	if len(res.Files) != 1 || len(res.Files[0].Hunks) != len(d.Hunks) {
		t.Fatalf("backend diff %+v", res)
	}
	for i, h := range res.Files[0].Hunks {
		if h.Hash == "" || h.Hash != d.Hunks[i].Hash() || h.Header != d.Hunks[i].Header() {
			t.Fatalf("hunk %d: backend %q/%q, GUI %q/%q", i, h.Header, h.Hash, d.Hunks[i].Header(), d.Hunks[i].Hash())
		}
	}

	// Commit only the line 30 change.
	g.Commit(controller.GitCommitRequest{Message: "line 30", Files: []controller.GitFileSelection{{Path: "long.txt", Hunks: []gitcli.HunkSelection{{Hunk: 1}}}}})
	e.waitFor("commit", func() bool { return g.Committed == 1 || g.Problem != nil })
	if g.Problem != nil {
		t.Fatalf("commit failed: %+v", g.Problem)
	}
	if got := runGit(t, e.root, "show", "HEAD:long.txt"); got != edit(orig, 30) {
		t.Fatalf("committed %q", got)
	}
	e.waitFor("reloaded", func() bool { d := change(); return d != nil && len(d.Hunks) == 1 })

	// A formatter rewrites the remaining change before the user commits
	// it: the selection no longer matches.
	write(edit(orig, 1, 2, 30))
	head := runGit(t, e.root, "rev-parse", "HEAD")
	g.Commit(controller.GitCommitRequest{Message: "line 1", Files: []controller.GitFileSelection{{Path: "long.txt", Hunks: []gitcli.HunkSelection{{Hunk: 0}}}}})
	e.waitFor("refusal", func() bool { return g.Problem != nil })
	if !g.Problem.Stale {
		t.Fatalf("problem %+v", g.Problem)
	}
	if p := problemOf(g.Problem); p == nil || !p.Warning || !strings.Contains(p.Title, "modified after the diff was loaded") {
		t.Fatalf("banner %+v", p)
	}
	if runGit(t, e.root, "rev-parse", "HEAD") != head {
		t.Fatal("a commit was created")
	}
	e.waitFor("diff reloaded", func() bool {
		d := change()
		return d != nil && len(d.Hunks) == 1 && len(d.Hunks[0].Lines) > 0 && strings.Count(fmt.Sprint(d.Hunks[0].Lines), "LINE") == 2
	})
}

// Review 2026-11-16 #5 (GUI): unsaved changes preserved when the last
// session ended are offered at the next project start; Show displays the
// diff, Restore writes them back.
func TestRecoveryBannerRestore(t *testing.T) {
	e := newEnv(t, true)
	be := e.ctl.Backend()
	ctx := context.Background()
	file := filepath.Join(e.root, "index.php")
	unsaved := "<?php\necho 'unsaved';\n"
	if err := be.Call(ctx, "doc.open", map[string]string{"path": file}, nil); err != nil {
		t.Fatal(err)
	}
	if err := be.Call(ctx, "doc.replace", map[string]any{"path": file, "text": unsaved}, nil); err != nil {
		t.Fatal(err)
	}
	if err := be.Call(ctx, "project.close", nil, nil); err != nil {
		t.Fatal(err)
	}
	if len(e.ctl.Recovery) != 0 {
		t.Fatalf("no recovery before: %+v", e.ctl.Recovery)
	}

	e.ctl.OpenProject(e.root)
	e.waitFor("recovery points", func() bool { return len(e.ctl.Recovery) == 1 })
	pt := e.ctl.Recovery[0]
	if pt.Path != "index.php" || !strings.Contains(recoveryDetail(e.ctl.Recovery), "index.php") {
		t.Fatalf("point %+v", pt)
	}
	e.shot("recovery")

	e.ctl.ShowRecovery(pt)
	e.waitFor("diff popup", func() bool { return e.v.popup != nil && e.v.popup.Title == "Recover unsaved changes" })
	if rd := e.ctl.RecoveryShown; rd == nil || rd.Recovered != unsaved || rd.Current != "<?php\necho 1;\n" {
		t.Fatalf("shown %+v", rd)
	}
	if e.ctl.ActiveTab() == nil || e.ctl.ActiveTab().Path != "index.php" {
		t.Fatal("Show should open the file")
	}
	e.shot("recovery-diff")
	e.v.popup.Buttons[0].OnClick() // Restore
	e.v.ClosePopup()
	e.waitFor("restored", func() bool { return len(e.ctl.Recovery) == 0 })
	if data, _ := os.ReadFile(file); string(data) != unsaved {
		t.Fatalf("file %q", data)
	}

	// A remaining hint can be dismissed without restoring.
	e.ctl.Recovery = []controller.RecoveryPoint{pt}
	e.ctl.DismissRecovery()
	e.waitFor("dismissed", func() bool { return len(e.ctl.Recovery) == 0 })
}

// Review 2026-11-16 #7 (GUI): a stopped compose container shows up as a
// badge and tooltip at the PHP version; the chooser offers the explicit
// "Start container & detect".
func TestPHPContainerHintInStatusbar(t *testing.T) {
	e := newEnv(t, true)
	e.ctl.Status.PHP.Hint = &controller.Hint{Severity: "info", Code: controller.HintContainerNotRunning, Message: "container not running: compose service \"php\" is stopped"}
	e.frame()
	if !strings.Contains(e.v.php.Tooltip(), "stopped") || !strings.Contains(e.v.php.Tooltip(), "Start container & detect") || e.v.php.HintBadge != "container stopped" {
		t.Fatalf("tooltip %q badge %q", e.v.php.Tooltip(), e.v.php.HintBadge)
	}
	e.v.openPHPChooser()
	if !e.v.phpStart || e.v.chooser.Items[0].Label != "Start container & detect" || e.v.chooser.Items[1].Label != "Detect automatically" {
		t.Fatalf("chooser %+v", e.v.chooser.Items)
	}
	e.shot("php-hint")
	e.v.choose(palette.ChooseEvent{Index: 0}) // the explicit start
	e.waitFor("detection", func() bool { return strings.Contains(e.ctl.Status.Notice, "detected") })
	if e.ctl.Status.PHP.Hint != nil {
		t.Fatalf("hint after detection %+v", e.ctl.Status.PHP)
	}
	e.ctl.Status.PHP.Hint = nil
	e.frame()
	e.v.openPHPChooser()
	if e.v.phpStart || e.v.chooser.Items[0].Label != "Detect automatically" || e.v.php.Hint != "" {
		t.Fatal("no start entry without hint")
	}
	e.v.closeOverlay()
}
