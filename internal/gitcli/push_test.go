package gitcli

import (
	"errors"
	"path/filepath"
	"testing"
)

func TestPush(t *testing.T) {
	tr := newTestRepo(t)
	ctx := ctxT(t)
	tr.write("a", "1\n")
	tr.commitAll("1")

	if _, err := tr.r.Push(ctx, PushOptions{}); !errors.Is(err, ErrNoRemote) {
		t.Fatalf("no remote: %v", err)
	}

	remote := filepath.Join(t.TempDir(), "remote.git")
	tr.git("init", "-q", "--bare", "-b", "main", remote)
	tr.git("remote", "add", "origin", remote)
	if rs, err := tr.r.Remotes(ctx); err != nil || len(rs) != 1 || rs[0] != "origin" {
		t.Fatalf("remotes %v %v", rs, err)
	}

	// First push: no upstream yet, so it is set.
	res, err := tr.r.Push(ctx, PushOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if res.Remote != "origin" || res.Branch != "main" || res.RemoteBranch != "main" || !res.SetUpstream {
		t.Errorf("first push: %+v", res)
	}
	rm, rb, ok, err := tr.r.Upstream(ctx, "main")
	if err != nil || !ok || rm != "origin" || rb != "main" {
		t.Errorf("upstream %q %q %v %v", rm, rb, ok, err)
	}

	// Second push uses the upstream and does not set it again.
	tr.write("a", "2\n")
	tr.commitAll("2")
	res, err = tr.r.Push(ctx, PushOptions{Tags: true})
	if err != nil {
		t.Fatal(err)
	}
	if res.SetUpstream {
		t.Errorf("second push set upstream: %+v", res)
	}
	if got := tr.git("--git-dir="+remote, "rev-parse", "main"); got != tr.git("rev-parse", "HEAD") {
		t.Error("remote not updated")
	}

	// Someone else pushes: our push is rejected.
	other := filepath.Join(t.TempDir(), "other")
	tr.git("clone", "-q", "-b", "main", remote, other)
	ot := &testRepo{t: t, dir: other}
	ot.git("config", "user.name", "O")
	ot.git("config", "user.email", "o@example.com")
	ot.write("b", "theirs\n")
	ot.commitAll("theirs")
	ot.git("push", "-q", "origin", "main")

	tr.write("a", "3\n")
	tr.commitAll("3")
	_, err = tr.r.Push(ctx, PushOptions{})
	var pe *PushError
	if !errors.As(err, &pe) || pe.Kind != PushRejected || pe.Message == "" || pe.Unwrap() == nil {
		t.Fatalf("rejected push: %#v", err)
	}
	if pe.Error() == "" {
		t.Error("empty error text")
	}
	// Force with lease works after fetching.
	tr.git("fetch", "-q", "origin")
	if _, err := tr.r.Push(ctx, PushOptions{ForceWithLease: true}); err != nil {
		t.Fatalf("force with lease: %v", err)
	}

	// Explicit remote and branch to a new remote branch name.
	res, err = tr.r.Push(ctx, PushOptions{Remote: "origin", Branch: "main", RemoteBranch: "copy"})
	if err != nil || res.RemoteBranch != "copy" {
		t.Fatalf("explicit push: %+v %v", res, err)
	}

	// Remote that does not exist.
	tr.git("remote", "add", "broken", filepath.Join(t.TempDir(), "nothing"))
	_, err = tr.r.Push(ctx, PushOptions{Remote: "broken"})
	if !errors.As(err, &pe) || pe.Kind != PushRepoNotFound {
		t.Fatalf("broken remote: %#v", err)
	}

	// Several remotes: origin wins; pushDefault overrides.
	tr.git("config", "remote.pushDefault", "broken")
	if _, err := tr.r.Push(ctx, PushOptions{Branch: "main"}); !errors.As(err, &pe) {
		t.Errorf("pushDefault ignored: %v", err)
	}

	tr.git("checkout", "-q", "--detach")
	if _, err := tr.r.Push(ctx, PushOptions{}); !errors.Is(err, ErrDetachedHead) {
		t.Errorf("detached: %v", err)
	}
}

func TestPushRemoteChoice(t *testing.T) {
	tr := newTestRepo(t)
	ctx := ctxT(t)
	tr.write("a", "1\n")
	tr.commitAll("1")
	tr.git("remote", "add", "one", "/nonexistent/one")
	tr.git("remote", "add", "two", "/nonexistent/two")
	if _, err := tr.r.pushRemote(ctx, "main", ""); !errors.Is(err, ErrNoRemote) {
		t.Errorf("ambiguous: %v", err)
	}
	tr.git("remote", "add", "origin", "/nonexistent/origin")
	if r, err := tr.r.pushRemote(ctx, "main", ""); err != nil || r != "origin" {
		t.Errorf("origin: %q %v", r, err)
	}
	if r, _ := tr.r.pushRemote(ctx, "main", "two"); r != "two" {
		t.Errorf("upstream remote: %q", r)
	}
	tr.git("config", "branch.main.pushRemote", "one")
	if r, _ := tr.r.pushRemote(ctx, "main", "two"); r != "one" {
		t.Errorf("pushRemote: %q", r)
	}
}

func TestClassifyPushError(t *testing.T) {
	cases := map[string]PushErrorKind{
		"! [rejected] main -> main (fetch first)":                                   PushRejected,
		"! [rejected] main -> main (stale info)":                                    PushRejected,
		" ! [remote rejected] main -> main (pre-receive hook declined)":             PushRemoteRejected,
		"fatal: Authentication failed for 'https://example.com/'":                   PushAuthFailed,
		"git@example.com: Permission denied (publickey).":                           PushAuthFailed,
		"fatal: could not read Username for 'https://x': terminal prompts disabled": PushAuthFailed,
		"ssh: Could not resolve hostname example.invalid":                           PushNetwork,
		"fatal: unable to access 'https://x/': Could not resolve host: x":           PushNetwork,
		"fatal: 'nope' does not appear to be a git repository":                      PushRepoNotFound,
		"remote: Repository not found.":                                             PushRepoNotFound,
		"error: something unexpected":                                               PushFailed,
	}
	for msg, want := range cases {
		pe := classifyPushError(&Error{Args: []string{"push"}, ExitCode: 1, Stderr: msg, Err: errors.New("exit 1")}, "")
		if pe.Kind != want || pe.Message == "" {
			t.Errorf("%q: kind %d, want %d (%q)", msg, pe.Kind, want, pe.Message)
		}
	}
	pe := classifyPushError(&Error{Args: []string{"push"}, Err: errors.New("boom")}, "")
	if pe.Message != "git push: boom" {
		t.Errorf("fallback message %q", pe.Message)
	}
	if got := remoteReason(" ! [remote rejected] main -> main (protected)"); got != "protected" {
		t.Errorf("remoteReason = %q", got)
	}
	if got := remoteReason("nothing"); got != "see details" {
		t.Errorf("remoteReason = %q", got)
	}
}
