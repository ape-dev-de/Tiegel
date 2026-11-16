package git

import (
	"errors"
	"path/filepath"
	"testing"

	"github.com/ape-dev-de/tiegel/internal/api"
	"github.com/ape-dev-de/tiegel/internal/core/bus"
	"github.com/ape-dev-de/tiegel/internal/modules/modtest"
)

// remoteSetup adds a bare remote with the project's main branch pushed and
// returns a second clone of it.
func remoteSetup(t *testing.T, dir string) (other string) {
	t.Helper()
	remote := filepath.Join(t.TempDir(), "remote.git")
	modtest.Git(t, dir, "init", "-q", "--bare", "-b", "main", remote)
	modtest.Git(t, dir, "remote", "add", "origin", remote)
	modtest.Git(t, dir, "push", "-q", "-u", "origin", "main")
	other = filepath.Join(t.TempDir(), "other")
	modtest.Git(t, dir, "clone", "-q", "-b", "main", remote, other)
	return other
}

func remoteKind(t *testing.T, err error) string {
	t.Helper()
	var ae *api.Error
	if !errors.As(err, &ae) {
		t.Fatalf("not an api error: %v", err)
	}
	info, ok := ae.Data.(RemoteErrorInfo)
	if !ok {
		t.Fatalf("no error data: %#v", ae)
	}
	return info.Kind
}

func TestFetchPullUpdate(t *testing.T) {
	a, dir := setup(t)
	rec := modtest.Record(t, a, api.TopicGitChanged)

	// No remote yet.
	err := modtest.WantCode(t, a, "git.fetch", FetchParams{}, api.CodeFailed)
	if remoteKind(t, err) != "noRemote" {
		t.Fatal(err)
	}
	err = modtest.WantCode(t, a, "git.updateProject", UpdateParams{}, api.CodeFailed)
	if remoteKind(t, err) != "noUpstream" {
		t.Fatal(err)
	}
	other := remoteSetup(t, dir)
	modtest.WantCode(t, a, "git.pull", PullParams{Strategy: "octopus"}, api.CodeInvalidParams)

	// Up to date.
	var pr PullResult
	modtest.Call(t, a, "git.updateProject", UpdateParams{}, &pr)
	if !pr.UpToDate || pr.Remote != "origin" || pr.RemoteBranch != "main" {
		t.Fatalf("up to date %+v", pr)
	}

	// Remote change; the open, modified document is saved first and reloaded.
	write(t, other, "b.txt", "theirs\n")
	modtest.Git(t, other, "commit", "-q", "-am", "theirs")
	modtest.Git(t, other, "push", "-q", "origin", "main")
	var fr FetchResult
	modtest.Call(t, a, "git.fetch", FetchParams{Prune: true}, &fr)
	if len(fr.Updated) != 1 || fr.Updated[0].Ref != "refs/remotes/origin/main" {
		t.Fatalf("fetch %+v", fr)
	}
	var gs GitStatus
	modtest.Call(t, a, "git.status", GitStatusParams{}, &gs)
	if gs.Behind != 1 {
		t.Fatalf("behind %+v", gs)
	}
	modtest.Call(t, a, "doc.open", api.DocOpenParams{Path: "b.txt"}, nil)
	modtest.Call(t, a, "doc.open", api.DocOpenParams{Path: "a.txt"}, nil)
	modtest.Call(t, a, "doc.insert", api.DocInsertParams{Path: "a.txt", At: api.Pos{Offset: 0}, Text: "local\n"}, nil)
	modtest.Call(t, a, "git.updateProject", UpdateParams{Strategy: "merge", Autostash: true}, &pr)
	if pr.UpToDate || !pr.FastForward || len(pr.Changed) != 1 || pr.Changed[0] != "b.txt" || pr.Behind != 0 || len(pr.Fetched) != 0 {
		t.Fatalf("update %+v", pr)
	}
	if docText(t, a, "b.txt") != "theirs\n" || read(t, dir, "a.txt") != "local\n"+ten {
		t.Fatalf("reload/save: %q %q", docText(t, a, "b.txt"), read(t, dir, "a.txt"))
	}
	rec.Wait(t, api.TopicGitChanged, func(e bus.Event) bool {
		ev, _ := e.Payload.(api.GitChangedEvent)
		return ev.Op == "update" && len(ev.Paths) == 1
	})

	// Conflict via git.pull: reported in the result, then visible in git.conflicts.
	modtest.Git(t, dir, "commit", "-q", "-am", "local")
	write(t, other, "a.txt", "remote\n"+ten)
	modtest.Git(t, other, "commit", "-q", "-am", "remote")
	modtest.Git(t, other, "push", "-q", "origin", "main")
	err = modtest.WantCode(t, a, "git.pull", PullParams{Strategy: "ff-only"}, api.CodeFailed)
	if remoteKind(t, err) != "diverged" {
		t.Fatal(err)
	}
	modtest.Call(t, a, "git.pull", PullParams{Remote: "origin", Branch: "main"}, &pr)
	if len(pr.Conflicts) != 1 || pr.Conflicts[0] != "a.txt" || pr.State != "merging" {
		t.Fatalf("conflict %+v", pr)
	}
	var conf []GitFileStatus
	modtest.Call(t, a, "git.conflicts", nil, &conf)
	if len(conf) != 1 {
		t.Fatalf("conflicts %+v", conf)
	}
	modtest.WantCode(t, a, "git.pull", PullParams{}, api.CodeConflict)
}

func TestStashMethods(t *testing.T) {
	a, dir := setup(t)
	var list []StashEntry
	modtest.Call(t, a, "git.stashList", nil, &list)
	if len(list) != 0 {
		t.Fatalf("list %+v", list)
	}
	var sr StashResult
	modtest.Call(t, a, "git.stash", StashParams{}, &sr)
	if sr.Created {
		t.Fatal("stashed nothing")
	}

	// An unsaved document change is saved and stashed; the document reloads.
	modtest.Call(t, a, "doc.open", api.DocOpenParams{Path: "a.txt"}, nil)
	modtest.Call(t, a, "doc.insert", api.DocInsertParams{Path: "a.txt", At: api.Pos{Offset: 0}, Text: "wip\n"}, nil)
	write(t, dir, "new.txt", "n\n")
	modtest.Call(t, a, "git.stash", StashParams{Message: "work", IncludeUntracked: true}, &sr)
	if !sr.Created || sr.Entry == nil || sr.Entry.Index != 0 || docText(t, a, "a.txt") != ten {
		t.Fatalf("stash %+v %q", sr, docText(t, a, "a.txt"))
	}
	modtest.WantCode(t, a, "git.stash", StashParams{Paths: []string{"/elsewhere/x"}}, api.CodeInvalidParams)

	var ar StashApplyResult
	modtest.WantCode(t, a, "git.stashApply", StashRefParams{Index: 3}, api.CodeNotFound)
	modtest.WantCode(t, a, "git.stashApply", StashRefParams{Hash: "0000000"}, api.CodeConflict)
	modtest.Call(t, a, "git.stashApply", StashRefParams{Hash: sr.Entry.Hash}, &ar)
	if ar.Dropped || len(ar.Conflicts) != 0 || docText(t, a, "a.txt") != "wip\n"+ten {
		t.Fatalf("apply %+v", ar)
	}
	modtest.Git(t, dir, "checkout", "--", "a.txt")
	modtest.Git(t, dir, "clean", "-fdq")

	// Pop with a conflict keeps the entry.
	write(t, dir, "a.txt", "committed\n")
	modtest.Git(t, dir, "commit", "-q", "-am", "c")
	modtest.Call(t, a, "git.stashPop", StashRefParams{}, &ar)
	if ar.Dropped || len(ar.Conflicts) != 1 {
		t.Fatalf("pop conflict %+v", ar)
	}
	modtest.Git(t, dir, "reset", "-q", "--hard")
	modtest.Git(t, dir, "clean", "-fdq")
	modtest.Call(t, a, "git.stashPop", StashRefParams{Index: 0}, &ar)
	if ar.Dropped {
		t.Fatalf("pop over committed change should conflict: %+v", ar)
	}
	modtest.Git(t, dir, "reset", "-q", "--hard")
	modtest.Git(t, dir, "clean", "-fdq")

	modtest.WantCode(t, a, "git.stashDrop", StashRefParams{Index: 0, Hash: "ffff"}, api.CodeConflict)
	modtest.Call(t, a, "git.stashDrop", StashRefParams{}, &list)
	if len(list) != 0 {
		t.Fatalf("drop %+v", list)
	}
	modtest.WantCode(t, a, "git.stashDrop", StashRefParams{}, api.CodeNotFound)
	modtest.WantCode(t, a, "git.stashPop", StashRefParams{}, api.CodeNotFound)

	// A clean pop drops the entry.
	write(t, dir, "b.txt", "changed\n")
	modtest.Call(t, a, "git.stash", StashParams{KeepIndex: true, Paths: []string{"b.txt"}}, &sr)
	modtest.Call(t, a, "git.stashPop", StashRefParams{RestoreIndex: true}, &ar)
	if !ar.Dropped || read(t, dir, "b.txt") != "changed\n" {
		t.Fatalf("pop %+v", ar)
	}
}
