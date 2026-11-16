package git

import (
	"strings"
	"testing"

	"github.com/ape-dev-de/tiegel/internal/api"
	"github.com/ape-dev-de/tiegel/internal/core/module"
	"github.com/ape-dev-de/tiegel/internal/gitcli"
	"github.com/ape-dev-de/tiegel/internal/modules/modtest"
)

func TestFileDiff(t *testing.T) {
	a, dir := setup(t)
	head := strings.TrimSpace(modtest.Git(t, dir, "rev-parse", "HEAD"))
	write(t, dir, "a.txt", strings.Replace(ten, "two\n", "TWO 2\n", 1)+"eleven\n")

	var res FileDiffResult
	modtest.Call(t, a, "git.fileDiff", FileDiffParams{Path: "a.txt"}, &res)
	if res.Path != "a.txt" || res.Left.Version != "HEAD" || res.Left.Hash != head || res.Left.Label != "HEAD ("+head[:8]+")" ||
		res.Right.Label != "Working tree" || !res.Left.Exists || !res.Right.Exists || res.Left.Text != ten || res.Identical {
		t.Fatalf("sides %+v / %+v", res.Left, res.Right)
	}
	if len(res.Blocks) != 2 {
		t.Fatalf("blocks %+v", res.Blocks)
	}
	b := res.Blocks[0]
	if b.Kind != "modified" || b.LeftStart != 2 || b.LeftEnd != 3 || b.RightStart != 2 || b.RightEnd != 3 {
		t.Fatalf("modified %+v", b)
	}
	if b2 := res.Blocks[1]; b2.Kind != "added" || b2.LeftStart != 11 || b2.LeftEnd != 11 || b2.RightStart != 11 || b2.RightEnd != 12 {
		t.Fatalf("added %+v", b2)
	}

	// Open document: the buffer content is the work tree side.
	modtest.Call(t, a, "doc.open", api.DocOpenParams{Path: "a.txt"}, nil)
	if _, err := a.EditDocument("a.txt", 0, 0, 3, "ONE"); err != nil {
		t.Fatal(err)
	}
	modtest.Call(t, a, "git.fileDiff", FileDiffParams{Path: "a.txt"}, &res)
	if !res.Right.Open || !strings.HasPrefix(res.Right.Text, "ONE\n") || len(res.Blocks) != 2 || res.Blocks[0].LeftStart != 1 {
		t.Fatalf("open doc %+v %+v", res.Right, res.Blocks)
	}

	// Index vs work tree after staging; identical.
	modtest.Git(t, dir, "add", "a.txt")
	modtest.Call(t, a, "git.fileDiff", FileDiffParams{Path: "b.txt", Base: "index", Target: "working"}, &res)
	if !res.Identical || len(res.Blocks) != 0 || res.Left.Label != "Index" {
		t.Fatalf("identical %+v", res)
	}
	// Revision against revision, with a rename (basePath).
	modtest.Git(t, dir, "commit", "-q", "-m", "second")
	modtest.Git(t, dir, "mv", "a.txt", "c.txt")
	modtest.Git(t, dir, "commit", "-q", "-m", "rename")
	modtest.Call(t, a, "git.fileDiff", FileDiffParams{Path: "c.txt", BasePath: "a.txt", Base: head, Target: "HEAD"}, &res)
	if res.Left.Path != "a.txt" || res.Right.Path != "c.txt" || res.Left.Label != head[:8] || len(res.Blocks) != 2 {
		t.Fatalf("rename %+v", res)
	}
	// Ignore white space and no inline ranges.
	write(t, dir, "c.txt", strings.Replace(read(t, dir, "c.txt"), "three\n", "  three  \n", 1))
	modtest.Call(t, a, "git.fileDiff", FileDiffParams{Path: "c.txt", IgnoreWhitespace: true, NoInline: true}, &res)
	if !res.Identical {
		t.Fatalf("ignore ws %+v", res.Blocks)
	}
	// New file: left side missing.
	write(t, dir, "new.txt", "x\n")
	modtest.Call(t, a, "git.fileDiff", FileDiffParams{Path: "new.txt"}, &res)
	if res.Left.Exists || len(res.Blocks) != 1 || res.Blocks[0].Kind != "added" {
		t.Fatalf("new %+v", res)
	}
	modtest.Call(t, a, "git.fileDiff", FileDiffParams{Path: "new.txt", Base: "empty", Target: "working"}, &res)
	if res.Left.Exists || res.Left.Label != "Empty" || len(res.Blocks) != 1 {
		t.Fatalf("empty base %+v", res)
	}
	// Binary.
	write(t, dir, "bin.dat", "\xff\xfe\x00")
	modtest.Call(t, a, "git.fileDiff", FileDiffParams{Path: "bin.dat"}, &res)
	if !res.Right.Binary || res.Right.Text != "" || len(res.Blocks) != 0 {
		t.Fatalf("binary %+v", res)
	}
	modtest.WantCode(t, a, "git.fileDiff", FileDiffParams{}, api.CodeInvalidParams)
	modtest.WantCode(t, a, "git.fileDiff", FileDiffParams{Path: "nope.txt"}, api.CodeNotFound)
	modtest.WantCode(t, a, "git.fileDiff", FileDiffParams{Path: "c.txt", Base: "no-such-rev"}, api.CodeNotFound)
}

func TestFileDiffUnborn(t *testing.T) {
	a2, dir := setupEmpty(t)
	write(t, dir, "x.txt", "x\n")
	var res FileDiffResult
	modtest.Call(t, a2, "git.fileDiff", FileDiffParams{Path: "x.txt"}, &res)
	if res.Left.Exists || res.Left.Label != "HEAD" || len(res.Blocks) != 1 {
		t.Fatalf("unborn %+v", res)
	}
	var h FileHistoryResult
	modtest.Call(t, a2, "git.fileHistory", FileHistoryParams{Path: "x.txt"}, &h)
	if len(h.Commits) != 0 || h.More {
		t.Fatalf("unborn history %+v", h)
	}
}

func setupEmpty(t *testing.T) (*api.App, string) {
	t.Helper()
	modtest.NeedGit(t)
	app := modtest.NewApp(t, []module.Module{New(Options{})}, nil)
	dir := modtest.Project(t, app, map[string]string{"keep": ""})
	initRepo(t, dir)
	return app, dir
}

func TestFileHistoryAndBlame(t *testing.T) {
	a, dir := setup(t)
	c1 := strings.TrimSpace(modtest.Git(t, dir, "rev-parse", "HEAD"))
	write(t, dir, "a.txt", strings.Replace(ten, "five\n", "FIVE\n", 1))
	modtest.Git(t, dir, "commit", "-q", "-am", "five")
	c2 := strings.TrimSpace(modtest.Git(t, dir, "rev-parse", "HEAD"))
	modtest.Git(t, dir, "mv", "a.txt", "moved.txt")
	modtest.Git(t, dir, "commit", "-q", "-m", "move")
	c3 := strings.TrimSpace(modtest.Git(t, dir, "rev-parse", "HEAD"))

	var h FileHistoryResult
	modtest.Call(t, a, "git.fileHistory", FileHistoryParams{Path: "moved.txt", Max: 2}, &h)
	if len(h.Commits) != 2 || !h.More || h.Commits[0].Hash != c3 || h.Commits[0].Status != "R" || h.Commits[0].OldPath != "a.txt" ||
		h.Commits[1].Hash != c2 || h.Commits[1].Path != "a.txt" {
		t.Fatalf("history %+v", h)
	}
	modtest.Call(t, a, "git.fileHistory", FileHistoryParams{Path: "moved.txt", Skip: 2}, &h)
	if len(h.Commits) != 1 || h.More || h.Commits[0].Hash != c1 || h.Commits[0].Status != "A" || len(h.Commits[0].Parents) != 0 {
		t.Fatalf("page 2 %+v", h)
	}
	modtest.WantCode(t, a, "git.fileHistory", FileHistoryParams{}, api.CodeInvalidParams)

	// Blame of the open, modified document.
	modtest.Call(t, a, "doc.open", api.DocOpenParams{Path: "moved.txt"}, nil)
	if _, err := a.EditDocument("moved.txt", 0, 0, 0, "new\n"); err != nil {
		t.Fatal(err)
	}
	var b BlameResult
	modtest.Call(t, a, "git.blame", BlameParams{Path: "moved.txt"}, &b)
	if !b.Open || len(b.Lines) != 11 || b.Lines[0].Hash != gitcli.UncommittedHash || b.Lines[5].Hash != c2 || b.Lines[1].Hash != c1 {
		t.Fatalf("blame %+v", b.Lines)
	}
	if len(b.Commits) != 3 || !b.Commits[0].Uncommitted || b.Lines[5].Line != 6 || b.Lines[5].OrigLine != 5 {
		t.Fatalf("blame commits %+v", b.Commits)
	}
	for _, c := range b.Commits {
		if c.Hash == c2 && (c.Summary != "five" || c.Author != "Test" || c.Path != "a.txt" || c.Previous != c1) {
			t.Fatalf("commit %+v", c)
		}
	}
	modtest.Call(t, a, "git.blame", BlameParams{Path: "a.txt", Rev: c1}, &b)
	if len(b.Lines) != 10 || b.Lines[4].Hash != c1 || b.Open {
		t.Fatalf("blame rev %+v", b)
	}
	modtest.WantCode(t, a, "git.blame", BlameParams{}, api.CodeInvalidParams)
	modtest.WantCode(t, a, "git.blame", BlameParams{Path: "a.txt", Rev: "HEAD"}, api.CodeNotFound)

	// Compare revisions.
	var cr CompareRevisionsResult
	modtest.Call(t, a, "git.compareRevisions", CompareRevisionsParams{Base: c1, Target: c3}, &cr)
	if len(cr.Files) != 1 || cr.Files[0].Status != "R" || cr.Files[0].OldPath != "a.txt" || cr.Files[0].Path != "moved.txt" {
		t.Fatalf("compare %+v", cr)
	}
	write(t, dir, "b.txt", "changed\n")
	modtest.Call(t, a, "git.compareRevisions", CompareRevisionsParams{Target: "working"}, &cr)
	if cr.Base != "HEAD" || cr.Target != "working" || len(cr.Files) != 1 || cr.Files[0].Status != "M" {
		t.Fatalf("work tree %+v", cr)
	}
	modtest.WantCode(t, a, "git.compareRevisions", CompareRevisionsParams{Base: "nope"}, api.CodeNotFound)
}
