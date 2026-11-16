package gitcli

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"
)

// remoteSetup creates a repo "tr" with a pushed main branch and a second
// clone "ot" of the same bare remote.
func remoteSetup(t *testing.T) (tr, ot *testRepo, remote string) {
	t.Helper()
	tr = newTestRepo(t)
	tr.write("a", "1\n2\n3\n")
	tr.commitAll("1")
	remote = filepath.Join(t.TempDir(), "remote.git")
	tr.git("init", "-q", "--bare", "-b", "main", remote)
	tr.git("remote", "add", "origin", remote)
	tr.git("push", "-q", "-u", "origin", "main")
	other := filepath.Join(t.TempDir(), "other")
	tr.git("clone", "-q", "-b", "main", remote, other)
	ot = &testRepo{t: t, dir: other}
	ot.git("config", "user.name", "O")
	ot.git("config", "user.email", "o@example.com")
	ot.git("config", "commit.gpgsign", "false")
	return tr, ot, remote
}

func TestFetch(t *testing.T) {
	tr := newTestRepo(t)
	ctx := ctxT(t)
	tr.write("a", "1\n")
	tr.commitAll("1")
	_, err := tr.r.Fetch(ctx, FetchOptions{})
	var re *RemoteError
	if !errors.As(err, &re) || re.Kind != RemoteNoRemote || !errors.Is(err, ErrNoRemote) || re.Error() == "" {
		t.Fatalf("no remote: %v", err)
	}

	tr, ot, _ := remoteSetup(t)
	ot.write("b", "x\n")
	ot.commitAll("b")
	ot.git("push", "-q", "origin", "main")
	ot.git("push", "-q", "origin", "main:feature")
	// A remote HEAD symref (created by clone, and by every fetch since git
	// 2.48) follows main but is no update of its own.
	tr.git("symbolic-ref", "refs/remotes/origin/HEAD", "refs/remotes/origin/main")
	res, err := tr.r.Fetch(ctx, FetchOptions{Prune: true, Tags: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Remotes) != 1 || len(res.Updated) != 2 || res.Updated[0].Ref != "refs/remotes/origin/feature" || res.Updated[0].Old != "" {
		t.Fatalf("fetch %+v", res)
	}
	// Pruning a deleted remote branch.
	ot.git("push", "-q", "origin", ":feature")
	res, err = tr.r.Fetch(ctx, FetchOptions{Remote: "origin", Prune: true})
	if err != nil || len(res.Updated) != 1 || res.Updated[0].New != "" {
		t.Fatalf("prune %+v %v", res, err)
	}
	// A broken remote URL is classified.
	tr.git("remote", "add", "broken", filepath.Join(t.TempDir(), "missing.git"))
	_, err = tr.r.Fetch(ctx, FetchOptions{Remote: "broken"})
	if !errors.As(err, &re) || re.Kind != RemoteRepoNotFound || re.Unwrap() == nil {
		t.Fatalf("broken: %#v", err)
	}
}

func TestPullStrategies(t *testing.T) {
	ctx := ctxT(t)
	tr, ot, _ := remoteSetup(t)

	// Up to date.
	res, err := tr.r.Pull(ctx, PullOptions{})
	if err != nil || !res.UpToDate || res.Target.Ref != "refs/remotes/origin/main" {
		t.Fatalf("up to date %+v %v", res, err)
	}

	// Fast-forward.
	ot.write("b", "theirs\n")
	ot.commitAll("b")
	ot.git("push", "-q", "origin", "main")
	res, err = tr.r.Pull(ctx, PullOptions{Strategy: StrategyFFOnly})
	if err != nil || !res.FastForward || res.UpToDate || len(res.Changed) != 1 || res.Changed[0] != "b" || res.Fetch == nil {
		t.Fatalf("ff %+v %v", res, err)
	}

	// Diverged: ff-only refuses, merge creates a merge commit.
	ot.write("c", "c\n")
	ot.commitAll("c")
	ot.git("push", "-q", "origin", "main")
	tr.write("d", "d\n")
	tr.commitAll("d")
	_, err = tr.r.Pull(ctx, PullOptions{Strategy: StrategyFFOnly})
	var re *RemoteError
	if !errors.As(err, &re) || re.Kind != RemoteDiverged {
		t.Fatalf("diverged %#v", err)
	}
	res, err = tr.r.Pull(ctx, PullOptions{Strategy: StrategyMerge})
	if err != nil || res.FastForward || len(res.Conflicts) != 0 || tr.git("rev-list", "--parents", "-n1", "HEAD") == "" {
		t.Fatalf("merge %+v %v", res, err)
	}
	if parents := strings.Fields(tr.git("rev-list", "--parents", "-n1", "HEAD")); len(parents) != 3 {
		t.Fatalf("no merge commit: %v", parents)
	}

	// Rebase.
	ot.git("pull", "-q", "--no-rebase", "origin", "main")
	ot.write("e", "e\n")
	ot.commitAll("e")
	ot.git("push", "-q", "origin", "main")
	tr.write("f", "f\n")
	tr.commitAll("f")
	res, err = tr.r.Pull(ctx, PullOptions{Strategy: StrategyRebase, Remote: "origin", Branch: "main"})
	if err != nil || res.State != StateClean {
		t.Fatalf("rebase %+v %v", res, err)
	}
	if parents := strings.Fields(tr.git("rev-list", "--parents", "-n1", "HEAD")); len(parents) != 2 {
		t.Fatalf("rebase made a merge: %v", parents)
	}

	if _, err := ParseStrategy("octopus"); err == nil {
		t.Error("bad strategy accepted")
	}
	if _, err := tr.r.Integrate(ctx, IntegrateOptions{Strategy: "octopus"}); err == nil {
		t.Error("bad strategy accepted by Integrate")
	}
}

func TestPullConflictsAndLocalChanges(t *testing.T) {
	ctx := ctxT(t)
	tr, ot, _ := remoteSetup(t)
	ot.write("a", "1\ntheirs\n3\n")
	ot.commitAll("theirs")
	ot.git("push", "-q", "origin", "main")

	// Uncommitted local change in the same file: refused, autostash helps.
	tr.write("a", "1\n2\n3\nlocal\n")
	_, err := tr.r.Pull(ctx, PullOptions{})
	var re *RemoteError
	if !errors.As(err, &re) || re.Kind != RemoteLocalChanges {
		t.Fatalf("local changes %#v", err)
	}
	res, err := tr.r.Pull(ctx, PullOptions{Autostash: true})
	if err != nil || len(res.Conflicts) != 0 || tr.read("a") != "1\ntheirs\n3\nlocal\n" {
		t.Fatalf("autostash %+v %v %q", res, err, tr.read("a"))
	}
	tr.git("checkout", "--", "a")

	// Conflicting commits: the merge stops with conflicts, no error.
	ot.write("a", "1\nTHEIRS\n3\n")
	ot.commitAll("theirs2")
	ot.git("push", "-q", "origin", "main")
	tr.write("a", "1\nOURS\n3\n")
	tr.commitAll("ours")
	res, err = tr.r.Pull(ctx, PullOptions{})
	if err != nil || len(res.Conflicts) != 1 || res.Conflicts[0] != "a" || res.State != StateMerging {
		t.Fatalf("conflict %+v %v", res, err)
	}
	_, err = tr.r.Integrate(ctx, IntegrateOptions{})
	if !errors.Is(err, ErrOperationInProgress) {
		t.Fatalf("in progress %v", err)
	}
	tr.git("merge", "--abort")

	// Rebase conflict.
	res, err = tr.r.Pull(ctx, PullOptions{Strategy: StrategyRebase})
	if err != nil || len(res.Conflicts) != 1 || (res.State != StateRebasing && res.State != StateRebaseInter) {
		t.Fatalf("rebase conflict %+v %v", res, err)
	}
	tr.git("rebase", "--abort")

	// Detached HEAD and missing upstream.
	tr.git("checkout", "-q", "--detach")
	if _, err := tr.r.Pull(ctx, PullOptions{}); !errors.As(err, &re) || re.Kind != RemoteDetached {
		t.Fatalf("detached %v", err)
	}
	if _, err := tr.r.Integrate(ctx, IntegrateOptions{}); !errors.As(err, &re) || re.Kind != RemoteDetached {
		t.Fatalf("detached integrate %v", err)
	}
	tr.git("checkout", "-q", "-b", "topic")
	if _, err := tr.r.Pull(ctx, PullOptions{}); !errors.As(err, &re) || re.Kind != RemoteNoUpstream {
		t.Fatalf("no upstream %v", err)
	}
	if _, err := tr.r.Integrate(ctx, IntegrateOptions{}); !errors.As(err, &re) || re.Kind != RemoteNoUpstream {
		t.Fatalf("no upstream integrate %v", err)
	}
	// With an explicit remote the same-named remote branch is used.
	if _, err := tr.r.Pull(ctx, PullOptions{Remote: "origin"}); !errors.As(err, &re) || re.Kind != RemoteNoUpstream {
		t.Fatalf("missing remote branch %v", err)
	}
}

func TestClassifyRemoteError(t *testing.T) {
	for msg, kind := range map[string]RemoteErrorKind{
		"fatal: Authentication failed for 'x'":         RemoteAuth,
		"ssh: Could not resolve host: example.invalid": RemoteNetwork,
		"weird":                    RemoteFailed,
		"fatal: No such remote: x": RemoteRepoNotFound,
	} {
		e := classifyRemoteError("fetch", &Error{Args: []string{"fetch"}, Stderr: msg, Err: errors.New("exit 1")}, "")
		if e.Kind != kind || e.Message == "" {
			t.Errorf("%q: %v %q", msg, e.Kind, e.Message)
		}
	}
	if e := classifyRemoteError("x", errors.New("boom"), ""); e.Message != "boom" {
		t.Errorf("fallback %q", e.Message)
	}
}

func TestStash(t *testing.T) {
	ctx := ctxT(t)
	tr := newTestRepo(t)
	tr.write("a", "1\n")
	tr.commitAll("1")

	if list, err := tr.r.StashList(ctx); err != nil || len(list) != 0 {
		t.Fatalf("empty list %v %v", list, err)
	}
	if created, err := tr.r.StashPush(ctx, StashOptions{}); err != nil || created {
		t.Fatalf("nothing to stash %v %v", created, err)
	}
	tr.write("a", "2\n")
	tr.write("new", "n\n")
	if created, err := tr.r.StashPush(ctx, StashOptions{Message: "wip one", IncludeUntracked: true}); err != nil || !created {
		t.Fatalf("push %v %v", created, err)
	}
	if tr.read("a") != "1\n" || tr.git("status", "--short") != "" {
		t.Fatal("work tree not clean")
	}
	tr.write("a", "3\n")
	if created, err := tr.r.StashPush(ctx, StashOptions{Message: "wip two", KeepIndex: true, Paths: []string{"a"}}); err != nil || !created {
		t.Fatalf("push 2 %v %v", created, err)
	}
	list, err := tr.r.StashList(ctx)
	if err != nil || len(list) != 2 || list[0].Index != 0 || list[1].Ref != "stash@{1}" || !strings.Contains(list[1].Message, "wip one") || list[0].Date.IsZero() {
		t.Fatalf("list %+v %v", list, err)
	}
	if err := tr.r.CheckStash(ctx, 1, list[1].Hash[:10]); err != nil {
		t.Fatal(err)
	}
	if err := tr.r.CheckStash(ctx, 1, list[0].Hash); !errors.Is(err, ErrStaleStash) {
		t.Fatalf("stale %v", err)
	}
	if err := tr.r.CheckStash(ctx, 5, ""); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing %v", err)
	}
	if _, err := tr.r.StashHash(ctx, -1); !errors.Is(err, ErrNotFound) {
		t.Fatalf("negative %v", err)
	}

	// Apply keeps, pop drops.
	res, err := tr.r.StashApply(ctx, 0, false)
	if err != nil || len(res.Conflicts) != 0 || res.Dropped || tr.read("a") != "3\n" {
		t.Fatalf("apply %+v %v", res, err)
	}
	// Applying "wip one" over the modified a is refused.
	if _, err := tr.r.StashApply(ctx, 1, false); err == nil {
		t.Fatal("apply over local changes succeeded")
	}
	tr.git("checkout", "--", "a")
	tr.git("clean", "-fdq")
	if err := tr.r.StashDrop(ctx, 0); err != nil {
		t.Fatal(err)
	}
	if err := tr.r.StashDrop(ctx, 3); !errors.Is(err, ErrNotFound) {
		t.Fatalf("drop missing %v", err)
	}
	res, err = tr.r.StashPop(ctx, 0, true)
	if err != nil || !res.Dropped || tr.read("new") != "n\n" {
		t.Fatalf("pop %+v %v %s", res, err, tr.git("status", "--short"))
	}
	if _, err := tr.r.StashPop(ctx, 0, false); !errors.Is(err, ErrNotFound) {
		t.Fatalf("pop empty %v", err)
	}

	// Conflicting pop keeps the entry and reports the conflict.
	tr.git("checkout", "--", "a")
	tr.git("clean", "-fdq")
	tr.write("a", "stashed\n")
	if _, err := tr.r.StashPush(ctx, StashOptions{}); err != nil {
		t.Fatal(err)
	}
	tr.write("a", "committed\n")
	tr.commitAll("c")
	res, err = tr.r.StashPop(ctx, 0, false)
	if err != nil || len(res.Conflicts) != 1 || res.Dropped {
		t.Fatalf("conflicting pop %+v %v", res, err)
	}
	if list, _ := tr.r.StashList(ctx); len(list) != 1 {
		t.Fatalf("entry dropped despite conflict: %v", list)
	}
}
