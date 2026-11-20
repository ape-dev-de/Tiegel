package controller

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ape-dev-de/tiegel/internal/gitcli"
)

func TestChangesDiffTab(t *testing.T) {
	f := projectFake()
	f.answer("git.status", map[string]any{"branch": "main", "files": []map[string]any{
		{"path": "src/Mailer.php", "kind": "ordinary", "index": ".", "worktree": "M"},
		{"path": "src/New.php", "kind": "ordinary", "index": "A", "worktree": "."},
		{"path": "index.php", "kind": "renamed", "origPath": "old.php", "index": "R", "worktree": "."},
		{"path": "notes.md", "kind": "untracked", "index": "?", "worktree": "?"},
	}})
	f.answer("git.diff", map[string]any{"raw": ""})
	f.answer("git.recentMessages", []string{"Last commit"})
	f.on("git.fileDiff", func(raw json.RawMessage) (any, error) {
		var p map[string]any
		_ = json.Unmarshal(raw, &p)
		return map[string]any{"path": p["path"], "left": map[string]any{"version": p["base"], "text": "a\n", "exists": true},
			"right": map[string]any{"version": p["target"], "text": "b\n", "exists": true}, "blocks": []any{}}, nil
	})
	gens := 0
	f.on("doc.open", func(raw json.RawMessage) (any, error) {
		var p pathParams
		_ = json.Unmarshal(raw, &p)
		f.mu.Lock()
		gens++
		f.docs[p.Path] = &fakeDoc{path: p.Path}
		g := gens
		f.mu.Unlock()
		return map[string]any{"path": p.Path, "generation": g}, nil
	})
	f.answer("doc.save", map[string]any{})
	f.answer("doc.close", map[string]any{})
	c := started(t, f)
	g := c.Vcs
	g.LoadChanges()
	waitFor(t, c, "changes", func() bool { return len(g.Changes) == 4 })
	if got := strings.Join(g.ChangeOrder(), ","); got != "src/Mailer.php,src/New.php,index.php,notes.md" {
		t.Fatalf("order %s", got)
	}

	dt := c.Work.OpenChangeDiff("src/Mailer.php", gitcli.DiffLocal)
	waitFor(t, c, "diff", func() bool { return dt.Version > 0 && !dt.Loading })
	tab := c.ActiveTab()
	if tab == nil || tab.Virtual != TabDiff || tab.Data != dt || tab.Title() != "Mailer.php (HEAD ↔ local)" {
		t.Fatalf("tab %+v", tab)
	}
	if dt.Nav == nil || dt.Nav.Index != 0 || dt.Nav.Total() != 4 || dt.Nav.Neighbor("src/Mailer.php", -1) != "notes.md" || dt.Doc == nil {
		t.Fatalf("nav %+v doc %v", dt.Nav, dt.Doc)
	}
	if c.Work.ChangesDiff() != dt {
		t.Fatal("ChangesDiff")
	}

	// Next file: the same tab shows the added file against the empty one;
	// the document opened for the first file is closed.
	closes := f.called("doc.close")
	if !c.Work.StepChangeDiff(dt, 1) {
		t.Fatal("step")
	}
	waitFor(t, c, "next diff", func() bool { return dt.Spec.Path == "src/New.php" && !dt.Loading })
	c.Settle(200 * time.Millisecond)
	var p map[string]any
	lastParams(t, f, "git.fileDiff", &p)
	if p["base"] != "empty" || p["path"] != filepath.Join(root, "src/New.php") || dt.Nav.Index != 1 || len(c.Tabs) != 1 {
		t.Fatalf("next %v nav %+v tabs %d", p, dt.Nav, len(c.Tabs))
	}
	if f.called("doc.close") != closes+1 {
		t.Fatal("previous document not released")
	}
	// The rename compares with its source; staged mode keeps the index.
	c.Work.OpenChangeDiff("index.php", gitcli.DiffStaged)
	waitFor(t, c, "rename diff", func() bool { return dt.Spec.Path == "index.php" && !dt.Loading })
	lastParams(t, f, "git.fileDiff", &p)
	if p["basePath"] != "old.php" || p["target"] != "index" || dt.Doc != nil {
		t.Fatalf("rename %v", p)
	}
	// Stepping keeps the mode and wraps around.
	c.Work.StepChangeDiff(dt, 2)
	waitFor(t, c, "wrapped", func() bool { return dt.Spec.Path == "src/Mailer.php" && !dt.Loading })
	if dt.Nav.Mode != gitcli.DiffStaged || dt.Spec.Target != "index" {
		t.Fatalf("mode lost %+v", dt.Spec)
	}
	if c.Work.StepChangeDiff(nil, 1) || c.Work.StepChangeDiff(dt, 0) {
		t.Fatal("invalid steps")
	}
	// Grouping changes the order the tab follows.
	g.Grouping = gitcli.GroupFlat
	if got := strings.Join(g.ChangeOrder(), ","); got != "index.php,src/Mailer.php,src/New.php,notes.md" {
		t.Fatalf("flat order %s", got)
	}
	if tr := g.ChangeTree(); tr.Changes.Badge != "main" {
		t.Fatalf("badge %q", tr.Changes.Badge)
	}

	// Closing forgets the tab.
	c.CloseTab(c.Active)
	c.Work.CloseDiff(dt)
	if c.Work.ChangesDiff() != nil {
		t.Fatal("not forgotten")
	}
	// A path that is no longer a change still opens (as a modification).
	other := c.Work.OpenChangeDiff("gone.php", gitcli.DiffLocal)
	if other.Nav.Index != -1 || other.Spec.Base != "HEAD" {
		t.Fatalf("unknown path %+v", other.Nav)
	}
	c.Settle(200 * time.Millisecond)
}

func TestCommitParamsOptions(t *testing.T) {
	p := CommitParams(GitCommitRequest{Message: "m", SignOff: true, NoVerify: true, Files: []GitFileSelection{{Path: "a", Whole: true}}})
	if p["signOff"] != true || p["noVerify"] != true {
		t.Fatalf("%v", p)
	}
}
