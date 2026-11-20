package api

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"testing"

	"github.com/ape-dev-de/tiegel/internal/gitcli"
)

// fakeGit registers git.status and git.fileDiff stand-ins.
func fakeGit(status string, fileDiffs *[]map[string]any, statusErr error) func(*App) error {
	return func(a *App) error {
		if err := Handle(a, "git.status", "fake", func(_ context.Context, _ json.RawMessage) (json.RawMessage, error) {
			if statusErr != nil {
				return nil, statusErr
			}
			return json.RawMessage(status), nil
		}); err != nil {
			return err
		}
		return Handle(a, "git.fileDiff", "fake", func(_ context.Context, p map[string]any) (map[string]any, error) {
			*fileDiffs = append(*fileDiffs, p)
			return map[string]any{"path": p["path"], "identical": false}, nil
		})
	}
}

const fakeStatus = `{"branch":"main","files":[
 {"path":"src/b.php","kind":"ordinary","index":".","worktree":"M"},
 {"path":"src/a.php","kind":"ordinary","index":"A","worktree":"."},
 {"path":"src/new.php","origPath":"src/old.php","kind":"renamed","index":"R","worktree":"."},
 {"path":"notes.md","kind":"untracked","index":"?","worktree":"?"}]}`

func TestVcsChanges(t *testing.T) {
	var diffs []map[string]any
	a := newTestApp(t, fakeGit(fakeStatus, &diffs, nil))
	var r VcsChangesResult
	call(t, a, "vcs.changes", VcsChangesParams{}, &r)
	if r.Branch != "main" || r.Tree.Changes.Count != 3 || r.Tree.Unversioned.Count != 1 || r.Tree.Changes.Badge != "main" {
		t.Fatalf("%+v", r)
	}
	if want := []string{"src/a.php", "src/b.php", "src/new.php", "notes.md"}; !reflect.DeepEqual(r.Order, want) {
		t.Errorf("order %v", r.Order)
	}
	call(t, a, "vcs.changes", VcsChangesParams{GroupBy: "none"}, &r)
	if r.Tree.Grouping != gitcli.GroupFlat || len(r.Tree.Changes.Children) != 3 {
		t.Errorf("flat %+v", r.Tree)
	}
	if err := tryCall(a, "vcs.changes", VcsChangesParams{GroupBy: "bogus"}, nil); ErrorCode(err) != CodeInvalidParams {
		t.Errorf("bad grouping: %v", err)
	}
	empty := newTestApp(t, fakeGit(`{"files":[]}`, &diffs, nil))
	call(t, empty, "vcs.changes", nil, &r)
	if r.Order == nil || len(r.Order) != 0 {
		t.Errorf("empty order %v", r.Order)
	}
}

func TestVcsDiffTab(t *testing.T) {
	var diffs []map[string]any
	a := newTestApp(t, fakeGit(fakeStatus, &diffs, nil))
	var r VcsDiffTabResult
	call(t, a, "vcs.diffTab", VcsDiffTabParams{Path: "src/b.php"}, &r)
	if r.Path != "src/b.php" || r.Base != gitcli.VersionHead || r.Target != gitcli.VersionWorking || r.Index != 1 || r.Total != 4 ||
		r.Prev != "src/a.php" || r.Next != "src/new.php" || r.Title != "b.php (HEAD ↔ local)" || r.State != gitcli.StateModified || len(r.Diff) == 0 {
		t.Fatalf("%+v", r)
	}
	// Step to the next file: the rename compares with its source.
	call(t, a, "vcs.diffTab", VcsDiffTabParams{Path: "src/b.php", Step: 1, IgnoreWhitespace: true}, &r)
	if r.Path != "src/new.php" || r.BasePath != "src/old.php" {
		t.Fatalf("step %+v", r)
	}
	last := diffs[len(diffs)-1]
	if last["basePath"] != "src/old.php" || last["ignoreWhitespace"] != true {
		t.Errorf("fileDiff params %v", last)
	}
	// Wrapping backwards from the first file; staged mode of an added file.
	call(t, a, "vcs.diffTab", VcsDiffTabParams{Path: "src/a.php", Mode: "staged"}, &r)
	if r.Base != gitcli.VersionEmpty || r.Target != gitcli.VersionIndex || r.Prev != "notes.md" {
		t.Fatalf("staged %+v", r)
	}
	call(t, a, "vcs.diffTab", VcsDiffTabParams{Path: "src/a.php", Step: -1}, &r)
	if r.Path != "notes.md" || r.Base != gitcli.VersionEmpty || r.Next != "src/a.php" {
		t.Fatalf("wrap %+v", r)
	}
	// Review wave 9 B10: an untracked file has no index version.
	call(t, a, "vcs.diffTab", VcsDiffTabParams{Path: "notes.md", Mode: "staged"}, &r)
	if r.Base != gitcli.VersionEmpty || r.Target != gitcli.VersionEmpty {
		t.Fatalf("staged untracked %+v", r)
	}
	if last := diffs[len(diffs)-1]; last["target"] != gitcli.VersionEmpty {
		t.Fatalf("fileDiff params %v", last)
	}
	call(t, a, "vcs.diffTab", VcsDiffTabParams{Path: "notes.md", Mode: "unstaged"}, &r)
	if r.Base != gitcli.VersionEmpty || r.Target != gitcli.VersionWorking {
		t.Fatalf("unstaged untracked %+v", r)
	}
	for _, p := range []VcsDiffTabParams{{}, {Path: "x", Mode: "bogus"}, {Path: "x", GroupBy: "bogus"}} {
		if err := tryCall(a, "vcs.diffTab", p, nil); ErrorCode(err) != CodeInvalidParams {
			t.Errorf("%+v: %v", p, err)
		}
	}
	if err := tryCall(a, "vcs.diffTab", VcsDiffTabParams{Path: "clean.php"}, nil); ErrorCode(err) != CodeNotFound {
		t.Errorf("unchanged file: %v", err)
	}
	failing := newTestApp(t, fakeGit("", &diffs, errors.New("no repo")))
	if err := tryCall(failing, "vcs.diffTab", VcsDiffTabParams{Path: "a"}, nil); err == nil {
		t.Error("status error not reported")
	}
	if err := tryCall(failing, "vcs.changes", nil, nil); err == nil {
		t.Error("status error not reported")
	}
}
