package git

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ape-dev-de/tiegel/internal/api"
	"github.com/ape-dev-de/tiegel/internal/core/autosave"
	"github.com/ape-dev-de/tiegel/internal/core/bus"
	"github.com/ape-dev-de/tiegel/internal/core/module"
	"github.com/ape-dev-de/tiegel/internal/gitcli"
	"github.com/ape-dev-de/tiegel/internal/modules/modtest"
)

const ten = "one\ntwo\nthree\nfour\nfive\nsix\nseven\neight\nnine\nten\n"

// setup creates an App with the git module and a project that is a git
// repository with one commit on "main".
func setup(t *testing.T) (*api.App, string) {
	t.Helper()
	modtest.NeedGit(t)
	a := modtest.NewApp(t, []module.Module{New(Options{})}, nil)
	dir := modtest.Project(t, a, map[string]string{"a.txt": ten, "b.txt": "b\n"})
	initRepo(t, dir)
	modtest.Git(t, dir, "add", "-A")
	modtest.Git(t, dir, "commit", "-q", "-m", "init")
	return a, dir
}

func initRepo(t *testing.T, dir string) {
	t.Helper()
	modtest.Git(t, dir, "init", "-q", "-b", "main")
	modtest.Git(t, dir, "config", "user.name", "Test")
	modtest.Git(t, dir, "config", "user.email", "test@example.org")
	modtest.Git(t, dir, "config", "commit.gpgsign", "false")
}

func write(t *testing.T, dir, name, content string) {
	t.Helper()
	modtest.WriteFile(t, filepath.Join(dir, name), content)
}

func read(t *testing.T, dir, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(dir, name))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func twoHunks() string {
	return strings.Replace(strings.Replace(ten, "one\n", "ONE\n", 1), "ten\n", "TEN\n", 1)
}

func docText(t *testing.T, a *api.App, path string) string {
	t.Helper()
	var dt api.DocText
	modtest.Call(t, a, "doc.text", api.PathParams{Path: path}, &dt)
	return dt.Text
}

func TestStageUnstage(t *testing.T) {
	a, dir := setup(t)
	rec := modtest.Record(t, a, api.TopicGitChanged)
	write(t, dir, "a.txt", twoHunks())
	write(t, dir, "c.txt", "new\n")

	var d GitDiffResult
	modtest.Call(t, a, "git.diff", GitDiffParams{Paths: []string{"a.txt"}}, &d)
	if len(d.Files) != 1 || len(d.Files[0].Hunks) != 2 {
		t.Fatalf("diff %+v", d)
	}
	var st GitStatus
	modtest.Call(t, a, "git.stage", StageParams{Path: "a.txt", Hunks: []HunkSel{{Hunk: 0}}}, &st)
	modtest.Call(t, a, "git.diff", GitDiffParams{Staged: true}, &d)
	if len(d.Files) != 1 || len(d.Files[0].Hunks) != 1 || !strings.Contains(d.Files[0].Hunks[0].Header, "-1,") {
		t.Fatalf("staged %+v", d)
	}
	modtest.Call(t, a, "git.unstage", StageParams{Path: "a.txt", Hunks: []HunkSel{{Hunk: 0}}}, &st)
	modtest.Call(t, a, "git.diff", GitDiffParams{Staged: true}, &d)
	if len(d.Files) != 0 {
		t.Fatalf("still staged %+v", d)
	}
	// Untracked file staged by hunk.
	modtest.Call(t, a, "git.stage", StageParams{Path: "c.txt", Hunks: []HunkSel{{Hunk: 0}}}, &st)
	staged := map[string]bool{}
	for _, f := range st.Files {
		staged[f.Path] = f.Staged
	}
	if !staged["c.txt"] || staged["a.txt"] {
		t.Fatalf("status %+v", st)
	}
	modtest.Call(t, a, "git.stage", StageParams{Paths: []string{"a.txt"}, Path: filepath.Join(dir, "c.txt")}, &st)
	for _, f := range st.Files {
		if !f.Staged {
			t.Fatalf("not staged %+v", f)
		}
	}
	modtest.Call(t, a, "git.unstage", StageParams{Paths: []string{"a.txt", "c.txt"}}, &st)
	for _, f := range st.Files {
		if f.Staged {
			t.Fatalf("still staged %+v", f)
		}
	}
	modtest.WantCode(t, a, "git.stage", StageParams{Hunks: []HunkSel{{}}}, api.CodeInvalidParams)
	modtest.WantCode(t, a, "git.stage", StageParams{}, api.CodeInvalidParams)
	modtest.WantCode(t, a, "git.unstage", StageParams{Path: "b.txt", Hunks: []HunkSel{{}}}, api.CodeNotFound)
	modtest.WantCode(t, a, "git.stage", StageParams{Path: "a.txt", Hunks: []HunkSel{{Hunk: 9}}}, api.CodeFailed)
	modtest.WantCode(t, a, "git.stage", StageParams{Paths: []string{"/elsewhere/x"}}, api.CodeInvalidParams)
	rec.Wait(t, api.TopicGitChanged, func(e busEvent) bool { return e.Payload.(api.GitChangedEvent).Op == "unstage" })
}

func TestDiscard(t *testing.T) {
	a, dir := setup(t)
	write(t, dir, "a.txt", twoHunks())
	modtest.Call(t, a, "doc.open", api.DocOpenParams{Path: "a.txt"}, nil)
	var res DiscardResult
	modtest.Call(t, a, "git.discard", DiscardParams{Path: "a.txt", Hunks: []HunkSel{{Hunk: 0}}}, &res)
	want := strings.Replace(ten, "ten\n", "TEN\n", 1)
	if !res.Reloaded || read(t, dir, "a.txt") != want || docText(t, a, "a.txt") != want {
		t.Fatalf("discard %+v %q", res, read(t, dir, "a.txt"))
	}
	modtest.Call(t, a, "doc.undo", api.PathParams{Path: "a.txt"}, nil)
	if docText(t, a, "a.txt") != twoHunks() {
		t.Fatal("reload is not undoable")
	}
	// The document is modified now: discard leaves it alone.
	modtest.Call(t, a, "git.discard", DiscardParams{Path: "a.txt", All: true}, &res)
	if res.Reloaded || read(t, dir, "a.txt") != ten {
		t.Fatalf("discard all %+v", res)
	}
	modtest.WantCode(t, a, "git.discard", DiscardParams{Path: "a.txt"}, api.CodeInvalidParams)
	modtest.WantCode(t, a, "git.discard", DiscardParams{Path: "a.txt", All: true}, api.CodeNotFound)
}

func TestCommitSelection(t *testing.T) {
	a, dir := setup(t)
	write(t, dir, "a.txt", twoHunks())
	modtest.Call(t, a, "doc.open", api.DocOpenParams{Path: "b.txt"}, nil)
	modtest.Call(t, a, "doc.replace", api.DocReplaceParams{Path: "b.txt", Text: "B\n"}, nil)

	remove := api.HookCommitMessage.On(a.Hooks(), 0, func(_ context.Context, msg string) (string, error) {
		return msg + "\n\nRefs: #1", nil
	})
	var res CommitSelectionResult
	modtest.Call(t, a, "git.commitSelection", CommitSelectionParams{
		Message: "partial", Paths: []string{"b.txt"}, Hunks: []FileHunks{{Path: "a.txt", Hunks: []HunkSel{{Hunk: 0}}}},
	}, &res)
	remove()
	if res.Hash == "" || res.Pushed || !strings.HasSuffix(res.Message, "Refs: #1") {
		t.Fatalf("commit %+v", res)
	}
	var show ShowResult
	modtest.Call(t, a, "git.show", ShowParams{Path: "a.txt"}, &show)
	if show.Content != strings.Replace(ten, "one\n", "ONE\n", 1) {
		t.Fatalf("committed a.txt %q", show.Content)
	}
	modtest.Call(t, a, "git.show", ShowParams{Rev: "HEAD", Path: "b.txt"}, &show)
	if show.Content != "B\n" || read(t, dir, "b.txt") != "B\n" {
		t.Fatalf("autosave before commit: %q", show.Content)
	}
	var msgs []string
	modtest.Call(t, a, "git.recentMessages", RecentMessagesParams{}, &msgs)
	if len(msgs) != 2 || !strings.HasPrefix(msgs[0], "partial") {
		t.Fatalf("messages %q", msgs)
	}
	// Amend the message only.
	modtest.Call(t, a, "git.commitSelection", CommitSelectionParams{Message: "partial (amended)", Amend: true}, &res)
	modtest.Call(t, a, "git.recentMessages", RecentMessagesParams{Max: 1}, &msgs)
	if len(msgs) != 1 || !strings.HasPrefix(msgs[0], "partial (amended)") {
		t.Fatalf("amend %q", msgs)
	}
	// Guard aborts.
	removeGuard := api.HookCommitBefore.On(a.Hooks(), 0, func(context.Context, api.CommitRequest) error {
		return errors.New("tests are red")
	})
	modtest.WantCode(t, a, "git.commitSelection", CommitSelectionParams{Message: "x", Paths: []string{"a.txt"}}, api.CodeFailed)
	modtest.WantCode(t, a, "git.commit", GitCommitParams{Message: "x", Stage: []string{"a.txt"}}, api.CodeFailed)
	removeGuard()
	modtest.WantCode(t, a, "git.commitSelection", CommitSelectionParams{Message: "x"}, api.CodeInvalidParams)
	modtest.WantCode(t, a, "git.commitSelection", CommitSelectionParams{Message: "x", Hunks: []FileHunks{{Path: "a.txt", Hunks: []HunkSel{{Hunk: 5}}}}}, api.CodeInvalidParams)
	modtest.WantCode(t, a, "git.commitSelection", CommitSelectionParams{Message: "", Paths: []string{"a.txt"}}, api.CodeInvalidParams)
	var cr GitCommitResult
	modtest.Call(t, a, "git.commit", GitCommitParams{Message: "rest", Stage: []string{"a.txt"}}, &cr)
	if cr.Hash == "" {
		t.Fatal("no hash")
	}
}

func pushData(t *testing.T, err error) PushErrorInfo {
	t.Helper()
	var ae *api.Error
	if !errors.As(err, &ae) {
		t.Fatalf("not an api error: %v", err)
	}
	info, ok := ae.Data.(PushErrorInfo)
	if !ok {
		t.Fatalf("data %#v", ae.Data)
	}
	return info
}

func TestPush(t *testing.T) {
	a, dir := setup(t)
	err := modtest.WantCode(t, a, "git.push", PushParams{}, api.CodeFailed)
	if pushData(t, err).Kind != "noRemote" {
		t.Fatalf("no remote: %v", err)
	}
	remote := filepath.Join(t.TempDir(), "remote.git")
	modtest.Git(t, dir, "init", "-q", "--bare", "-b", "main", remote)
	modtest.Git(t, dir, "remote", "add", "origin", remote)

	write(t, dir, "a.txt", twoHunks())
	var res CommitSelectionResult
	modtest.Call(t, a, "git.commitSelection", CommitSelectionParams{Message: "one", Paths: []string{"a.txt"}, Push: true}, &res)
	if !res.Pushed || res.Push == nil || res.Push.Remote != "origin" || res.PushError != nil {
		t.Fatalf("push %+v", res)
	}
	// Somebody else pushes.
	other := filepath.Join(t.TempDir(), "other")
	modtest.Git(t, dir, "clone", "-q", remote, other)
	modtest.Git(t, other, "config", "user.name", "O")
	modtest.Git(t, other, "config", "user.email", "o@example.org")
	modtest.Git(t, other, "config", "commit.gpgsign", "false")
	write(t, other, "o.txt", "o\n")
	modtest.Git(t, other, "add", "o.txt")
	modtest.Git(t, other, "commit", "-q", "-m", "other")
	modtest.Git(t, other, "push", "-q", "origin", "main")

	write(t, dir, "b.txt", "changed\n")
	modtest.Call(t, a, "git.commitSelection", CommitSelectionParams{Message: "two", Paths: []string{"b.txt"}, Push: true}, &res)
	if res.Pushed || res.PushError == nil || res.PushError.Kind != "rejected" || res.Hash == "" {
		t.Fatalf("rejected push %+v", res)
	}
	err = modtest.WantCode(t, a, "git.push", PushParams{Remote: "origin"}, api.CodeFailed)
	if info := pushData(t, err); info.Kind != "rejected" || !strings.Contains(err.Error(), "rejected") {
		t.Fatalf("data %+v", info)
	}
	var pr PushResult
	modtest.Call(t, a, "git.push", PushParams{Remote: "origin", RemoteBranch: "side"}, &pr)
	if pr.RemoteBranch != "side" {
		t.Fatalf("push side %+v", pr)
	}
}

func TestGutterRollback(t *testing.T) {
	a, dir := setup(t)
	modtest.Call(t, a, "doc.open", api.DocOpenParams{Path: "a.txt"}, nil)
	modtest.Call(t, a, "doc.replace", api.DocReplaceParams{Path: "a.txt", Start: &api.Pos{Line: 2}, End: &api.Pos{Line: 3}, Text: "TWO\n"}, nil)
	modtest.Call(t, a, "doc.delete", api.DocDeleteParams{Path: "a.txt", Start: api.Pos{Line: 5}, End: api.Pos{Line: 6}}, nil)
	var g GutterResult
	modtest.Call(t, a, "git.gutter", GutterParams{Path: "a.txt"}, &g)
	if !g.Open || len(g.Ranges) != 2 || g.Ranges[0] != (GutterRange{Kind: "modified", Start: 2, End: 3, BaseStart: 2, BaseEnd: 3}) ||
		g.Ranges[1].Kind != "deleted" || g.Ranges[1].Start != 5 || g.Ranges[1].End != 5 {
		t.Fatalf("gutter %+v", g)
	}
	modtest.Call(t, a, "git.gutter", GutterParams{Path: "b.txt", Base: "index"}, &g)
	if g.Open || len(g.Ranges) != 0 {
		t.Fatalf("b gutter %+v", g)
	}
	var st api.DocState
	modtest.Call(t, a, "git.rollback", RollbackParams{Path: "a.txt", Line: 2}, &st)
	modtest.Call(t, a, "git.rollback", RollbackParams{Path: "a.txt", Line: 5}, &st)
	if docText(t, a, "a.txt") != ten {
		t.Fatalf("rollback %q %+v", docText(t, a, "a.txt"), st)
	}
	modtest.Call(t, a, "doc.undo", api.PathParams{Path: "a.txt"}, nil)
	modtest.Call(t, a, "doc.undo", api.PathParams{Path: "a.txt"}, nil)
	if !strings.Contains(docText(t, a, "a.txt"), "TWO") {
		t.Fatal("rollback not undoable")
	}
	modtest.WantCode(t, a, "git.rollback", RollbackParams{Path: "a.txt", Line: 9}, api.CodeNotFound)
	modtest.WantCode(t, a, "git.rollback", RollbackParams{Path: "b.txt", Line: 1}, api.CodeNotFound)
	modtest.WantCode(t, a, "git.gutter", GutterParams{Path: "a.txt", Base: "x"}, api.CodeInvalidParams)
	modtest.WantCode(t, a, "git.rollback", RollbackParams{Path: "a.txt", Line: 1, Base: "x"}, api.CodeInvalidParams)
	modtest.WantCode(t, a, "git.gutter", GutterParams{Path: "missing.txt"}, api.CodeNotFound)
	// A new file: everything is added; rollback empties it.
	write(t, dir, "n.txt", "x\ny\n")
	modtest.Call(t, a, "doc.open", api.DocOpenParams{Path: "n.txt"}, nil)
	modtest.Call(t, a, "git.gutter", GutterParams{Path: "n.txt"}, &g)
	if len(g.Ranges) != 1 || g.Ranges[0].Kind != "added" {
		t.Fatalf("new file %+v", g)
	}
	modtest.Call(t, a, "git.rollback", RollbackParams{Path: "n.txt", Line: 1}, &st)
	if docText(t, a, "n.txt") != "" {
		t.Fatal("rollback of added file")
	}
}

func TestBranchesShowLog(t *testing.T) {
	a, dir := setup(t)
	write(t, dir, "bin.dat", "\xff\x00")
	modtest.Git(t, dir, "add", "bin.dat")
	modtest.Git(t, dir, "commit", "-q", "-m", "bin")
	var show ShowResult
	modtest.Call(t, a, "git.show", ShowParams{Rev: "HEAD", Path: "bin.dat"}, &show)
	if show.Encoding != "base64" {
		t.Fatalf("show %+v", show)
	}
	modtest.WantCode(t, a, "git.show", ShowParams{Path: "nope.txt"}, api.CodeNotFound)

	modtest.Call(t, a, "doc.open", api.DocOpenParams{Path: "a.txt"}, nil)
	var branches []GitBranch
	modtest.Call(t, a, "git.createBranch", BranchParams{Name: "feature", Checkout: true}, &branches)
	cur := ""
	for _, b := range branches {
		if b.Current {
			cur = b.Name
		}
	}
	if len(branches) != 2 || cur != "feature" {
		t.Fatalf("branches %+v", branches)
	}
	write(t, dir, "a.txt", "feature\n")
	modtest.Git(t, dir, "commit", "-q", "-am", "feature work")
	var st GitStatus
	modtest.Call(t, a, "git.checkout", CheckoutParams{Ref: "main"}, &st)
	if st.Branch != "main" || docText(t, a, "a.txt") != ten {
		t.Fatalf("checkout %+v %q", st, docText(t, a, "a.txt"))
	}
	modtest.WantCode(t, a, "git.deleteBranch", BranchParams{Name: "feature"}, api.CodeFailed)
	modtest.Call(t, a, "git.deleteBranch", BranchParams{Name: "feature", Force: true}, &branches)
	if len(branches) != 1 {
		t.Fatalf("after delete %+v", branches)
	}
	var log []GitCommit
	modtest.Call(t, a, "git.log", GitLogParams{}, &log)
	if len(log) != 2 || log[0].Subject != "bin" {
		t.Fatalf("log %+v", log)
	}
	modtest.WantCode(t, a, "git.checkout", CheckoutParams{}, api.CodeInvalidParams)
	modtest.WantCode(t, a, "git.checkout", CheckoutParams{Ref: "nope"}, api.CodeFailed)
	modtest.WantCode(t, a, "git.createBranch", BranchParams{}, api.CodeInvalidParams)
	modtest.WantCode(t, a, "git.deleteBranch", BranchParams{}, api.CodeInvalidParams)
	modtest.Call(t, a, "git.createBranch", BranchParams{Name: "x", Start: "HEAD~1"}, &branches)
	if len(branches) != 2 {
		t.Fatalf("branch x %+v", branches)
	}
}

func TestConflicts(t *testing.T) {
	a, dir := setup(t)
	modtest.Git(t, dir, "checkout", "-q", "-b", "feature")
	write(t, dir, "a.txt", strings.Replace(ten, "one", "feature", 1))
	write(t, dir, "b.txt", "feature b\n")
	modtest.Git(t, dir, "commit", "-q", "-am", "feature")
	modtest.Git(t, dir, "checkout", "-q", "main")
	write(t, dir, "a.txt", strings.Replace(ten, "one", "main", 1))
	write(t, dir, "b.txt", "main b\n")
	modtest.Git(t, dir, "commit", "-q", "-am", "main")
	cmd := exec.Command("git", "merge", "feature")
	cmd.Dir = dir
	if err := cmd.Run(); err == nil {
		t.Fatal("merge should conflict")
	}
	var files []GitFileStatus
	modtest.Call(t, a, "git.conflicts", nil, &files)
	if len(files) != 2 {
		t.Fatalf("conflicts %+v", files)
	}
	var md MergeData
	modtest.Call(t, a, "git.mergeData", api.PathParams{Path: "a.txt"}, &md)
	if md.Conflicts != 1 || !md.HasBase || !strings.Contains(md.Merged, "<<<<<<< ours") || md.Blocks[0].Kind != "conflict" || md.Blocks[0].Ours != "main\n" {
		t.Fatalf("merge data %+v", md)
	}
	modtest.WantCode(t, a, "git.resolve", ResolveParams{Path: "a.txt"}, api.CodeInvalidParams)
	modtest.WantCode(t, a, "git.resolve", ResolveParams{Path: "a.txt", Resolutions: []Resolution{{Block: 99, Resolution: "ours"}}}, api.CodeInvalidParams)
	modtest.WantCode(t, a, "git.resolve", ResolveParams{Path: "a.txt", Resolutions: []Resolution{{Block: 0, Resolution: "mine"}}}, api.CodeInvalidParams)
	var rr ResolveResult
	modtest.Call(t, a, "git.resolve", ResolveParams{Path: "a.txt", Resolutions: []Resolution{{Block: 0, Resolution: "custom", Custom: "both\n"}}}, &rr)
	if !strings.HasPrefix(rr.Content, "both\ntwo\n") || read(t, dir, "a.txt") != rr.Content {
		t.Fatalf("resolve %+v", rr)
	}
	content := "resolved b\n"
	modtest.Call(t, a, "git.resolve", ResolveParams{Path: "b.txt", Content: &content}, &rr)
	modtest.Call(t, a, "git.conflicts", nil, &files)
	if len(files) != 0 {
		t.Fatalf("left %+v", files)
	}
	modtest.WantCode(t, a, "git.mergeData", api.PathParams{Path: "a.txt"}, api.CodeNotFound)
}

func TestErrorsAndModuleSwitch(t *testing.T) {
	modtest.NeedGit(t)
	a := modtest.NewApp(t, []module.Module{New(Options{})}, nil)
	modtest.WantCode(t, a, "git.status", nil, api.CodeNoProject)
	modtest.Project(t, a, map[string]string{"x": ""})
	modtest.WantCode(t, a, "git.status", nil, api.CodeNotFound)
	for _, m := range []string{"git.diff", "git.log", "git.branches", "git.recentMessages", "git.conflicts", "git.stage",
		"git.unstage", "git.discard", "git.commitSelection", "git.push", "git.gutter", "git.rollback", "git.show",
		"git.checkout", "git.createBranch", "git.deleteBranch", "git.mergeData", "git.resolve", "git.commit"} {
		if err := modtest.TryCall(a, m, nil, nil); err == nil {
			t.Errorf("%s outside a repository succeeded", m)
		}
	}
	modtest.Call(t, a, "modules.disable", api.ModuleParams{ID: ID}, nil)
	modtest.WantCode(t, a, "git.status", nil, api.CodeModuleDisabled)
	modtest.WantCode(t, a, "git.mergeData", nil, api.CodeModuleDisabled)
	modtest.Call(t, a, "modules.enable", api.ModuleParams{ID: ID}, nil)
	modtest.WantCode(t, a, "git.status", nil, api.CodeNotFound)
	if PushKind(99) != "failed" || pushErrorInfo(errors.New("x")).Kind != "failed" {
		t.Fatal("push kinds")
	}
	for k, want := range map[int]string{1: "rejected", 2: "remoteRejected", 3: "auth", 4: "network", 5: "repoNotFound"} {
		if got := PushKind(pushKind(k)); got != want {
			t.Errorf("kind %d = %s", k, got)
		}
	}
}

type busEvent = bus.Event

func pushKind(k int) gitcli.PushErrorKind { return gitcli.PushErrorKind(k) }

// Review 2026-11-16 #1: git.commitSelection during a merge (git merge
// --no-commit) is refused with CodeConflict; HEAD and the merge stay.
func TestCommitSelectionRefusedDuringMerge(t *testing.T) {
	a, dir := setup(t)
	modtest.Git(t, dir, "checkout", "-q", "-b", "feature")
	write(t, dir, "f.txt", "feature\n")
	modtest.Git(t, dir, "add", "f.txt")
	modtest.Git(t, dir, "commit", "-q", "-m", "feature")
	modtest.Git(t, dir, "checkout", "-q", "main")
	write(t, dir, "b.txt", "main\n")
	modtest.Git(t, dir, "commit", "-q", "-am", "main")
	head := strings.TrimSpace(modtest.Git(t, dir, "rev-parse", "HEAD"))
	modtest.Git(t, dir, "merge", "--no-commit", "--no-ff", "-q", "feature")
	write(t, dir, "a.txt", twoHunks())

	modtest.WantCode(t, a, "git.commitSelection", CommitSelectionParams{
		Message: "selection", Hunks: []FileHunks{{Path: "a.txt", Hunks: []HunkSel{{Hunk: 0}}}},
	}, api.CodeConflict)
	if got := strings.TrimSpace(modtest.Git(t, dir, "rev-parse", "HEAD")); got != head {
		t.Fatalf("HEAD moved: %s", got)
	}
	if _, err := os.Stat(filepath.Join(dir, ".git", "MERGE_HEAD")); err != nil {
		t.Fatalf("merge state lost: %v", err)
	}
}

// Review 2026-11-16 #6: a hunk selection echoing header/hash is refused
// when the diff changed in between (e.g. format-on-save added a hunk), so
// the index cannot silently point to a different change.
func TestHunkSelectionHashValidation(t *testing.T) {
	a, dir := setup(t)
	var long strings.Builder
	for i := 1; i <= 30; i++ {
		fmt.Fprintf(&long, "line %d\n", i)
	}
	orig := long.String()
	write(t, dir, "a.txt", orig)
	modtest.Git(t, dir, "commit", "-q", "-am", "long")
	edit := func(s string, n int) string {
		return strings.Replace(s, fmt.Sprintf("line %d\n", n), fmt.Sprintf("LINE %d\n", n), 1)
	}
	write(t, dir, "a.txt", edit(edit(orig, 1), 30))
	var d GitDiffResult
	modtest.Call(t, a, "git.diff", GitDiffParams{Base: "HEAD", Paths: []string{"a.txt"}}, &d)
	if len(d.Files) != 1 || len(d.Files[0].Hunks) != 2 {
		t.Fatalf("diff %+v", d)
	}
	want := d.Files[0].Hunks[1] // the line 30 change
	if want.Hash == "" {
		t.Fatal("hunk without hash")
	}
	sel := HunkSel{Hunk: 1, Header: want.Header, Hash: want.Hash}

	// A formatter changes line 15: hunk 1 is now that change.
	write(t, dir, "a.txt", edit(edit(edit(orig, 1), 15), 30))
	err := modtest.WantCode(t, a, "git.commitSelection", CommitSelectionParams{
		Message: "line 30 only", Hunks: []FileHunks{{Path: "a.txt", Hunks: []HunkSel{sel}}},
	}, api.CodeConflict)
	// The error data tells clients to reload the diff.
	var ae *api.Error
	if !errors.As(err, &ae) || ae.Data != (StaleSelection{Reason: ReasonStaleSelection, Path: "a.txt", Hunk: 1}) {
		t.Fatalf("stale selection data: %#v", err)
	}
	modtest.WantCode(t, a, "git.stage", StageParams{Path: "a.txt", Hunks: []HunkSel{{Hunk: 1, Hash: want.Hash}}}, api.CodeConflict)
	modtest.WantCode(t, a, "git.discard", DiscardParams{Path: "a.txt", Hunks: []HunkSel{{Hunk: 7, Hash: want.Hash}}}, api.CodeConflict)
	if got := strings.TrimSpace(modtest.Git(t, dir, "rev-list", "--count", "HEAD")); got != "2" {
		t.Fatalf("a commit was created: %s", got)
	}

	// With a fresh diff the selection works.
	modtest.Call(t, a, "git.diff", GitDiffParams{Base: "HEAD", Paths: []string{"a.txt"}}, &d)
	h := d.Files[0].Hunks[2]
	var res CommitSelectionResult
	modtest.Call(t, a, "git.commitSelection", CommitSelectionParams{
		Message: "line 30 only", Hunks: []FileHunks{{Path: "a.txt", Hunks: []HunkSel{{Hunk: 2, Header: h.Header, Hash: h.Hash}}}},
	}, &res)
	var show ShowResult
	modtest.Call(t, a, "git.show", ShowParams{Path: "a.txt"}, &show)
	if show.Content != edit(orig, 30) {
		t.Fatalf("committed %q", show.Content)
	}
}

// Review 2026-11-16 #12: git.discard wrote the file and reloaded the
// document outside the save lock; a save in flight then overwrote the
// discarded file while the buffer showed the reloaded content as saved.
func TestDiscardWaitsForSaveInFlight(t *testing.T) {
	a, dir := setup(t)
	write(t, dir, "a.txt", twoHunks())
	modtest.Call(t, a, "doc.open", api.DocOpenParams{Path: "a.txt"}, nil)

	started, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	autosave.BeforeSave.On(a.Hooks(), 0, func(_ context.Context, e autosave.SaveEvent) (autosave.SaveEvent, error) {
		if e.Trigger == autosave.TriggerManual {
			once.Do(func() { close(started); <-release })
		}
		return e, nil
	})
	saved := make(chan error, 1)
	go func() {
		_, err := a.CallJSON(context.Background(), "doc.save", api.PathParams{Path: "a.txt"})
		saved <- err
	}()
	<-started
	discarded := make(chan error, 1)
	go func() {
		_, err := a.CallJSON(context.Background(), "git.discard", DiscardParams{Path: "a.txt", All: true})
		discarded <- err
	}()
	time.Sleep(50 * time.Millisecond)
	close(release)
	for _, ch := range []chan error{saved, discarded} {
		select {
		case err := <-ch:
			if err != nil {
				t.Fatal(err)
			}
		case <-time.After(10 * time.Second):
			t.Fatal("timeout")
		}
	}
	if disk, buf := read(t, dir, "a.txt"), docText(t, a, "a.txt"); disk != ten || buf != ten {
		t.Fatalf("disk %q, buffer %q; want the discarded (HEAD) content in both", disk, buf)
	}
}
