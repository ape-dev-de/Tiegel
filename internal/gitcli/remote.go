package gitcli

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// ErrStaleStash is returned by [Repo.CheckStash] when a stash entry no
// longer has the expected commit (the stash stack changed).
var ErrStaleStash = errors.New("gitcli: stash entry changed")

// RemoteErrorKind classifies a failed fetch, pull or stash operation.
type RemoteErrorKind string

// Failure categories of [RemoteError].
const (
	RemoteFailed       RemoteErrorKind = "failed"
	RemoteAuth         RemoteErrorKind = "auth"
	RemoteNetwork      RemoteErrorKind = "network"
	RemoteRepoNotFound RemoteErrorKind = "repoNotFound"
	RemoteNoRemote     RemoteErrorKind = "noRemote"
	RemoteNoUpstream   RemoteErrorKind = "noUpstream"
	RemoteLocalChanges RemoteErrorKind = "localChanges"
	RemoteDiverged     RemoteErrorKind = "diverged"
	RemoteDetached     RemoteErrorKind = "detached"
)

// RemoteError is a failed fetch/pull/stash operation with a readable
// explanation (like [PushError]).
type RemoteError struct {
	Op      string // "fetch", "pull", "stash apply", ...
	Kind    RemoteErrorKind
	Message string // short explanation for the user
	Stderr  string // raw git output
	Err     error
}

// Error implements the error interface.
func (e *RemoteError) Error() string { return e.Op + " failed: " + e.Message }

// Unwrap returns the underlying error.
func (e *RemoteError) Unwrap() error { return e.Err }

// classifyRemoteError turns git output of a failed fetch/merge/rebase/stash
// into a [*RemoteError].
func classifyRemoteError(op string, err error, stdout string) *RemoteError {
	stderr := stderrOf(err)
	low := strings.ToLower(stdout + "\n" + stderr)
	re := &RemoteError{Op: op, Kind: RemoteFailed, Stderr: stderr, Err: err}
	switch {
	case strings.Contains(low, "would be overwritten") || strings.Contains(low, "please commit your changes or stash them") ||
		strings.Contains(low, "you have unstaged changes") || strings.Contains(low, "your index contains uncommitted changes") ||
		strings.Contains(low, "already exists, no checkout") || strings.Contains(low, "could not restore untracked files"):
		re.Kind = RemoteLocalChanges
		re.Message = "local changes would be overwritten. Commit or stash them first (or use autostash)."
	case strings.Contains(low, "not possible to fast-forward") || strings.Contains(low, "not possible to fast forward"):
		re.Kind = RemoteDiverged
		re.Message = "the branches have diverged and cannot be fast-forwarded. Use merge or rebase."
	case strings.Contains(low, "does not appear to be a git repository") || strings.Contains(low, "repository not found") ||
		strings.Contains(low, "no such remote"):
		re.Kind = RemoteRepoNotFound
		re.Message = "the remote repository does not exist or is not accessible. Check the remote URL."
	case strings.Contains(low, "authentication failed") || strings.Contains(low, "could not read username") ||
		strings.Contains(low, "could not read password") || strings.Contains(low, "permission denied") ||
		strings.Contains(low, "terminal prompts disabled") || httpForbidden.MatchString(low):
		re.Kind = RemoteAuth
		re.Message = "authentication failed. Configure a credential helper or an SSH key (ssh-agent); interactive password prompts are not supported."
	case strings.Contains(low, "could not resolve host") || strings.Contains(low, "connection refused") ||
		strings.Contains(low, "timed out") || strings.Contains(low, "network is unreachable") ||
		strings.Contains(low, "unable to access") || strings.Contains(low, "connection reset"):
		re.Kind = RemoteNetwork
		re.Message = "the remote could not be reached. Check the network connection and the remote URL."
	default:
		re.Message = firstLines(strings.TrimSpace(stderr), 3)
		if re.Message == "" {
			re.Message = firstLines(strings.TrimSpace(stdout), 3)
		}
		if re.Message == "" {
			re.Message = err.Error()
		}
	}
	return re
}

// httpForbidden matches an HTTP 403 status in git output, not any "403"
// inside a path or hash.
var httpForbidden = regexp.MustCompile(`error: 403\b|\bhttp[/0-9.]* 403\b|\b403 forbidden`)

// --- fetch -------------------------------------------------------------------

// FetchOptions configures [Repo.Fetch].
type FetchOptions struct {
	// Remote is the remote to fetch; empty fetches all remotes.
	Remote string
	// Prune removes remote-tracking branches that no longer exist.
	Prune bool
	// Tags fetches all tags.
	Tags bool
}

// RefUpdate is a remote-tracking ref or tag changed by a fetch. Old is
// empty for new refs, New is empty for pruned refs.
type RefUpdate struct {
	Ref string
	Old string
	New string
}

// FetchResult describes a successful fetch.
type FetchResult struct {
	Remotes []string // fetched remotes
	Updated []RefUpdate
}

// refSnapshot maps remote-tracking refs and tags to their object IDs.
// Symbolic refs (refs/remotes/<remote>/HEAD) are skipped: they only point
// at a branch that is listed itself, and git >= 2.48 creates or moves them
// on every fetch (remote.<name>.followRemoteHEAD), which would otherwise
// show up as a spurious update depending on the installed git version.
func (r *Repo) refSnapshot(ctx context.Context) (map[string]string, error) {
	out, err := r.run(ctx, runOpts{}, "for-each-ref", "--format=%(objectname) %(refname) %(symref)", "refs/remotes", "refs/tags")
	if err != nil {
		return nil, fmt.Errorf("gitcli: for-each-ref: %w", err)
	}
	m := map[string]string{}
	for _, l := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		f := strings.Fields(l)
		if len(f) == 2 {
			m[f[1]] = f[0]
		}
	}
	return m, nil
}

// Fetch downloads objects and refs. Network access only happens on explicit
// user request; credential prompts are disabled. Failures come back as
// [*RemoteError].
func (r *Repo) Fetch(ctx context.Context, opts FetchOptions) (*FetchResult, error) {
	remotes, err := r.Remotes(ctx)
	if err != nil {
		return nil, err
	}
	if len(remotes) == 0 {
		return nil, &RemoteError{Op: "fetch", Kind: RemoteNoRemote, Message: "no remote configured", Err: ErrNoRemote}
	}
	before, err := r.refSnapshot(ctx)
	if err != nil {
		return nil, err
	}
	args := []string{"fetch", "--quiet"}
	if opts.Prune {
		args = append(args, "--prune")
	}
	if opts.Tags {
		args = append(args, "--tags")
	}
	res := &FetchResult{Remotes: remotes}
	if opts.Remote == "" {
		args = append(args, "--all")
	} else {
		args = append(args, opts.Remote)
		res.Remotes = []string{opts.Remote}
	}
	out, err := r.run(ctx, runOpts{}, args...)
	if err != nil {
		if ctx.Err() != nil {
			return nil, fmt.Errorf("gitcli: fetch: %w", err)
		}
		return nil, classifyRemoteError("fetch", err, string(out))
	}
	after, err := r.refSnapshot(ctx)
	if err != nil {
		return nil, err
	}
	for ref, n := range after {
		if o := before[ref]; o != n {
			res.Updated = append(res.Updated, RefUpdate{Ref: ref, Old: o, New: n})
		}
	}
	for ref, o := range before {
		if _, ok := after[ref]; !ok {
			res.Updated = append(res.Updated, RefUpdate{Ref: ref, Old: o})
		}
	}
	sortRefUpdates(res.Updated)
	return res, nil
}

func sortRefUpdates(u []RefUpdate) {
	for i := 1; i < len(u); i++ {
		for j := i; j > 0 && u[j].Ref < u[j-1].Ref; j-- {
			u[j], u[j-1] = u[j-1], u[j]
		}
	}
}

// --- pull --------------------------------------------------------------------

// Strategy is how fetched changes are integrated into the current branch.
type Strategy string

// Integration strategies.
const (
	StrategyMerge  Strategy = "merge"
	StrategyRebase Strategy = "rebase"
	StrategyFFOnly Strategy = "ff-only"
)

// ParseStrategy validates a strategy name; "" means merge.
func ParseStrategy(s string) (Strategy, error) {
	switch Strategy(s) {
	case "", StrategyMerge:
		return StrategyMerge, nil
	case StrategyRebase, StrategyFFOnly:
		return Strategy(s), nil
	}
	return "", fmt.Errorf("gitcli: unknown strategy %q (merge, rebase, ff-only)", s)
}

// PullOptions configures [Repo.Pull] and [Repo.ResolvePull].
type PullOptions struct {
	// Remote overrides the remote; empty uses the upstream's remote.
	Remote string
	// Branch is the remote branch; empty uses the upstream branch (or the
	// current branch name when only Remote is given).
	Branch    string
	Strategy  Strategy
	Autostash bool
	// Prune is passed to the fetch.
	Prune bool
}

// PullTarget is what a pull fetches and integrates.
type PullTarget struct {
	Branch       string // local (current) branch
	Remote       string // "." for a local upstream
	RemoteBranch string
	Ref          string // ref to integrate, e.g. refs/remotes/origin/main
}

// ResolvePull determines remote and ref for a pull of the current branch.
func (r *Repo) ResolvePull(ctx context.Context, opts PullOptions) (*PullTarget, error) {
	info, err := r.Info(ctx)
	if err != nil {
		return nil, err
	}
	if info.Detached {
		return nil, &RemoteError{Op: "pull", Kind: RemoteDetached, Message: "HEAD is detached; check out a branch first", Err: ErrDetachedHead}
	}
	upRemote, upBranch, hasUp, err := r.Upstream(ctx, info.Branch)
	if err != nil {
		return nil, err
	}
	if !hasUp && opts.Remote == "" && opts.Branch == "" {
		return nil, &RemoteError{Op: "pull", Kind: RemoteNoUpstream,
			Message: fmt.Sprintf("branch %s has no upstream; push it with upstream first or choose remote and branch", info.Branch)}
	}
	t := &PullTarget{Branch: info.Branch, Remote: opts.Remote, RemoteBranch: opts.Branch}
	if t.Remote == "" {
		t.Remote = upRemote
	}
	if t.Remote == "" {
		if t.Remote, err = r.pushRemote(ctx, info.Branch, ""); err != nil {
			return nil, &RemoteError{Op: "pull", Kind: RemoteNoRemote, Message: err.Error(), Err: err}
		}
	}
	if t.RemoteBranch == "" {
		t.RemoteBranch = info.Branch
		if hasUp && upRemote == t.Remote {
			t.RemoteBranch = upBranch
		}
	}
	if t.Remote == "." {
		t.Ref = "refs/heads/" + t.RemoteBranch
	} else {
		t.Ref = "refs/remotes/" + t.Remote + "/" + t.RemoteBranch
	}
	return t, nil
}

// IntegrateOptions configures [Repo.Integrate].
type IntegrateOptions struct {
	// Target is the revision to integrate (default "@{upstream}").
	Target    string
	Strategy  Strategy
	Autostash bool
}

// PullResult describes a pull or integration. Conflicts are not an error:
// the operation stops with State merging or rebasing and Conflicts lists
// the unmerged paths.
type PullResult struct {
	Target      *PullTarget
	Fetch       *FetchResult
	Before      string
	After       string
	UpToDate    bool
	FastForward bool
	Changed     []string // paths changed between Before and After
	Conflicts   []string
	State       RepoState
}

// Integrate merges or rebases the current branch onto opts.Target (no
// network access).
func (r *Repo) Integrate(ctx context.Context, opts IntegrateOptions) (*PullResult, error) {
	info, err := r.Info(ctx)
	if err != nil {
		return nil, err
	}
	if info.Detached {
		return nil, &RemoteError{Op: "pull", Kind: RemoteDetached, Message: "HEAD is detached; check out a branch first", Err: ErrDetachedHead}
	}
	if info.State != StateClean && info.State != StateBisecting {
		return nil, fmt.Errorf("%w: %s", ErrOperationInProgress, info.State)
	}
	strategy, err := ParseStrategy(string(opts.Strategy))
	if err != nil {
		return nil, err
	}
	target := opts.Target
	if target == "" {
		target = "@{upstream}"
	}
	th, err := r.resolve(ctx, target)
	if err != nil {
		if errors.Is(err, ErrNotFound) || strings.Contains(stderrOf(err), "no upstream") {
			return nil, &RemoteError{Op: "pull", Kind: RemoteNoUpstream, Message: fmt.Sprintf("%s does not exist (fetch first or set an upstream)", target), Err: err}
		}
		return nil, err
	}
	res := &PullResult{Before: info.Head, After: info.Head}
	if info.Head != "" && r.isAncestor(ctx, th, info.Head) {
		res.UpToDate = true
		return res, nil
	}
	var args []string
	switch strategy {
	case StrategyMerge:
		args = []string{"merge", "--no-edit"}
	case StrategyFFOnly:
		args = []string{"merge", "--ff-only"}
	case StrategyRebase:
		args = []string{"rebase"}
	}
	if opts.Autostash {
		args = append(args, "--autostash")
	}
	args = append(args, th)
	out, runErr := r.run(ctx, runOpts{}, args...)
	if runErr != nil {
		if ctx.Err() != nil {
			return nil, fmt.Errorf("gitcli: pull: %w", runErr)
		}
		st, err := r.Status(ctx, StatusOptions{NoUntracked: true})
		if err != nil {
			return nil, err
		}
		res.State = r.state()
		for _, f := range st.Conflicts() {
			res.Conflicts = append(res.Conflicts, f.Path)
		}
		if len(res.Conflicts) == 0 && res.State == StateClean {
			return nil, classifyRemoteError("pull", runErr, string(out))
		}
	}
	if h, err := r.resolve(ctx, "HEAD"); err == nil {
		res.After = h
	}
	res.FastForward = res.State == StateClean && res.After == th && (info.Head == "" || r.isAncestor(ctx, info.Head, th))
	if info.Head != "" && res.After != info.Head {
		res.Changed, _ = r.changedBetween(ctx, info.Head, res.After)
	}
	return res, nil
}

// Pull fetches the target remote (network access, user-triggered only) and
// integrates the remote branch into the current branch.
func (r *Repo) Pull(ctx context.Context, opts PullOptions) (*PullResult, error) {
	t, err := r.ResolvePull(ctx, opts)
	if err != nil {
		return nil, err
	}
	var fr *FetchResult
	if t.Remote != "." {
		if fr, err = r.Fetch(ctx, FetchOptions{Remote: t.Remote, Prune: opts.Prune}); err != nil {
			return nil, err
		}
	}
	res, err := r.Integrate(ctx, IntegrateOptions{Target: t.Ref, Strategy: opts.Strategy, Autostash: opts.Autostash})
	if err != nil {
		return nil, err
	}
	res.Target, res.Fetch = t, fr
	return res, nil
}

func (r *Repo) isAncestor(ctx context.Context, a, b string) bool {
	_, err := r.run(ctx, runOpts{}, "merge-base", "--is-ancestor", a, b)
	return err == nil
}

func (r *Repo) changedBetween(ctx context.Context, a, b string) ([]string, error) {
	out, err := r.run(ctx, runOpts{}, "diff", "--name-only", "-z", "--no-renames", a, b)
	if err != nil {
		return nil, fmt.Errorf("gitcli: diff: %w", err)
	}
	var paths []string
	for _, p := range strings.Split(string(out), "\x00") {
		if p != "" {
			paths = append(paths, p)
		}
	}
	return paths, nil
}

// --- stash -------------------------------------------------------------------

// StashOptions configures [Repo.StashPush].
type StashOptions struct {
	Message          string
	IncludeUntracked bool
	KeepIndex        bool
	// Paths limits the stash to these paths.
	Paths []string
}

// StashEntry is one entry of the stash stack.
type StashEntry struct {
	Index   int
	Ref     string // stash@{n}
	Hash    string
	Message string
	Date    time.Time
}

// StashResult is the result of applying or popping a stash entry.
type StashResult struct {
	// Conflicts lists unmerged paths after a conflicting apply; the entry
	// is then kept even for pop.
	Conflicts []string
	// Dropped is true when pop removed the entry.
	Dropped bool
}

// StashPush stashes local changes. created is false if there was nothing
// to stash.
func (r *Repo) StashPush(ctx context.Context, opts StashOptions) (created bool, err error) {
	before, _ := r.resolve(ctx, "refs/stash")
	args := []string{"stash", "push", "--quiet"}
	if opts.IncludeUntracked {
		args = append(args, "--include-untracked")
	}
	if opts.KeepIndex {
		args = append(args, "--keep-index")
	}
	if opts.Message != "" {
		args = append(args, "-m", opts.Message)
	}
	// git stash push ignores --include-untracked with
	// GIT_LITERAL_PATHSPECS=1 (it builds magic pathspecs internally), so
	// literal paths are marked explicitly instead.
	if len(opts.Paths) > 0 {
		args = append(args, "--")
		for _, p := range opts.Paths {
			args = append(args, ":(literal)"+p)
		}
	}
	out, err := r.run(ctx, runOpts{env: []string{"GIT_LITERAL_PATHSPECS=0"}}, args...)
	if err != nil {
		return false, classifyRemoteError("stash", err, string(out))
	}
	after, _ := r.resolve(ctx, "refs/stash")
	return after != "" && after != before, nil
}

// StashList returns the stash entries, newest first.
func (r *Repo) StashList(ctx context.Context) ([]StashEntry, error) {
	out, err := r.run(ctx, runOpts{}, "stash", "list", "--format=%gd%x1f%H%x1f%ct%x1f%gs")
	if err != nil {
		return nil, fmt.Errorf("gitcli: stash list: %w", err)
	}
	var list []StashEntry
	for _, l := range strings.Split(strings.TrimRight(string(out), "\n"), "\n") {
		f := strings.SplitN(l, "\x1f", 4)
		if len(f) != 4 {
			continue
		}
		e := StashEntry{Ref: f[0], Hash: f[1], Message: f[3]}
		if i := strings.Index(f[0], "{"); i >= 0 {
			e.Index, _ = strconv.Atoi(strings.TrimSuffix(f[0][i+1:], "}"))
		}
		if sec, err := strconv.ParseInt(f[2], 10, 64); err == nil {
			e.Date = time.Unix(sec, 0)
		}
		list = append(list, e)
	}
	return list, nil
}

func stashRef(index int) string { return "stash@{" + strconv.Itoa(index) + "}" }

// StashHash returns the commit of stash@{index} or ErrNotFound.
func (r *Repo) StashHash(ctx context.Context, index int) (string, error) {
	if index < 0 {
		return "", fmt.Errorf("gitcli: stash index %d: %w", index, ErrNotFound)
	}
	out, err := r.run(ctx, runOpts{okCodes: []int{1, 128}}, "rev-parse", "-q", "--verify", stashRef(index))
	h := strings.TrimSpace(string(out))
	if err != nil || h == "" {
		return "", fmt.Errorf("gitcli: %s: %w", stashRef(index), ErrNotFound)
	}
	return h, nil
}

// CheckStash makes sure stash@{index} exists and, if hash is non-empty, is
// that commit ([ErrStaleStash] otherwise).
func (r *Repo) CheckStash(ctx context.Context, index int, hash string) error {
	h, err := r.StashHash(ctx, index)
	if err != nil {
		return err
	}
	if hash != "" && !strings.HasPrefix(h, hash) {
		return fmt.Errorf("%w: %s is %s, expected %s", ErrStaleStash, stashRef(index), h, hash)
	}
	return nil
}

// StashApply applies stash@{index}; restoreIndex also restores the index.
func (r *Repo) StashApply(ctx context.Context, index int, restoreIndex bool) (*StashResult, error) {
	return r.stashApply(ctx, "apply", index, restoreIndex)
}

// StashPop applies stash@{index} and drops it unless the apply conflicted.
func (r *Repo) StashPop(ctx context.Context, index int, restoreIndex bool) (*StashResult, error) {
	return r.stashApply(ctx, "pop", index, restoreIndex)
}

func (r *Repo) stashApply(ctx context.Context, verb string, index int, restoreIndex bool) (*StashResult, error) {
	hash, err := r.StashHash(ctx, index)
	if err != nil {
		return nil, err
	}
	args := []string{"stash", verb, "--quiet"}
	if restoreIndex {
		args = append(args, "--index")
	}
	args = append(args, stashRef(index))
	out, runErr := r.run(ctx, runOpts{}, args...)
	res := &StashResult{}
	if runErr != nil {
		st, err := r.Status(ctx, StatusOptions{NoUntracked: true})
		if err != nil {
			return nil, err
		}
		for _, f := range st.Conflicts() {
			res.Conflicts = append(res.Conflicts, f.Path)
		}
		if len(res.Conflicts) == 0 {
			return nil, classifyRemoteError("stash "+verb, runErr, string(out))
		}
		return res, nil
	}
	if verb == "pop" {
		h, err := r.StashHash(ctx, index)
		res.Dropped = err != nil || h != hash
	}
	return res, nil
}

// StashDrop removes stash@{index}.
func (r *Repo) StashDrop(ctx context.Context, index int) error {
	if _, err := r.StashHash(ctx, index); err != nil {
		return err
	}
	if _, err := r.run(ctx, runOpts{}, "stash", "drop", "--quiet", stashRef(index)); err != nil {
		return fmt.Errorf("gitcli: stash drop: %w", err)
	}
	return nil
}
