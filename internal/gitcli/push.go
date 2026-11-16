package gitcli

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

// ErrDetachedHead is returned when an operation needs a branch.
var ErrDetachedHead = errors.New("gitcli: HEAD is detached")

// ErrNoRemote is returned by [Repo.Push] when no remote is configured or
// none can be chosen.
var ErrNoRemote = errors.New("gitcli: no remote to push to")

// PushErrorKind classifies a failed push.
type PushErrorKind int

// Push failure categories.
const (
	PushFailed         PushErrorKind = iota // anything else
	PushRejected                            // non fast-forward: remote has new commits
	PushRemoteRejected                      // a hook or protection rule on the remote declined
	PushAuthFailed                          // authentication / permission problem
	PushNetwork                             // host not reachable
	PushRepoNotFound                        // remote repository does not exist
)

// PushError is a push failure with a human readable explanation.
type PushError struct {
	Kind    PushErrorKind
	Message string // short explanation for the user
	Stderr  string // raw git output
	Err     error
}

// Error implements the error interface.
func (e *PushError) Error() string { return "push failed: " + e.Message }

// Unwrap returns the underlying git error.
func (e *PushError) Unwrap() error { return e.Err }

// PushOptions configures [Repo.Push].
type PushOptions struct {
	// Remote overrides the remote; empty uses the push remote of the branch,
	// its upstream remote, remote.pushDefault, the only remote or "origin".
	Remote string
	// Branch is the local branch; empty uses the current branch.
	Branch string
	// RemoteBranch is the target branch name; empty uses the upstream
	// branch or the local branch name.
	RemoteBranch string
	// SetUpstream configures the upstream (-u). It is set automatically when
	// the branch has no upstream yet.
	SetUpstream bool
	// ForceWithLease overwrites the remote branch if it is still at the
	// expected state.
	ForceWithLease bool
	// Tags also pushes annotated tags reachable from the pushed commits
	// (--follow-tags).
	Tags bool
}

// PushResult describes a successful push.
type PushResult struct {
	Remote       string
	Branch       string // local branch
	RemoteBranch string
	SetUpstream  bool   // upstream was configured by this push
	Output       string // git's porcelain output
}

// Upstream returns the configured upstream of branch as remote name and
// remote branch name (without refs/heads/). ok is false when the branch
// has no upstream.
func (r *Repo) Upstream(ctx context.Context, branch string) (remote, remoteBranch string, ok bool, err error) {
	remote, err = r.ConfigGet(ctx, "branch."+branch+".remote")
	if err != nil {
		return "", "", false, err
	}
	merge, err := r.ConfigGet(ctx, "branch."+branch+".merge")
	if err != nil {
		return "", "", false, err
	}
	if remote == "" || merge == "" {
		return "", "", false, nil
	}
	return remote, strings.TrimPrefix(merge, "refs/heads/"), true, nil
}

// Remotes returns the configured remote names.
func (r *Repo) Remotes(ctx context.Context) ([]string, error) {
	out, err := r.run(ctx, runOpts{}, "remote")
	if err != nil {
		return nil, fmt.Errorf("gitcli: remote: %w", err)
	}
	return strings.Fields(string(out)), nil
}

// Push pushes a branch. Network access only happens here, on explicit user
// request. Credential prompts are disabled; failures come back as
// [*PushError] with a readable message.
func (r *Repo) Push(ctx context.Context, opts PushOptions) (*PushResult, error) {
	branch := opts.Branch
	if branch == "" {
		info, err := r.Info(ctx)
		if err != nil {
			return nil, err
		}
		if info.Detached {
			return nil, ErrDetachedHead
		}
		branch = info.Branch
	}
	upRemote, upBranch, hasUp, err := r.Upstream(ctx, branch)
	if err != nil {
		return nil, err
	}
	remote := opts.Remote
	if remote == "" {
		remote, err = r.pushRemote(ctx, branch, upRemote)
		if err != nil {
			return nil, err
		}
	}
	res := &PushResult{Remote: remote, Branch: branch, RemoteBranch: opts.RemoteBranch}
	if res.RemoteBranch == "" {
		res.RemoteBranch = branch
		if hasUp && upRemote == remote {
			res.RemoteBranch = upBranch
		}
	}
	res.SetUpstream = opts.SetUpstream || !hasUp
	args := []string{"push", "--porcelain"}
	if res.SetUpstream {
		args = append(args, "--set-upstream")
	}
	if opts.ForceWithLease {
		args = append(args, "--force-with-lease")
	}
	if opts.Tags {
		args = append(args, "--follow-tags")
	}
	args = append(args, remote, "refs/heads/"+branch+":refs/heads/"+res.RemoteBranch)
	out, err := r.run(ctx, runOpts{}, args...)
	res.Output = string(out)
	if err != nil {
		if ctx.Err() != nil {
			return nil, fmt.Errorf("gitcli: push: %w", err)
		}
		return nil, classifyPushError(err, string(out))
	}
	return res, nil
}

func (r *Repo) pushRemote(ctx context.Context, branch, upRemote string) (string, error) {
	for _, key := range []string{"branch." + branch + ".pushRemote", "remote.pushDefault"} {
		v, err := r.ConfigGet(ctx, key)
		if err != nil {
			return "", err
		}
		if v != "" {
			return v, nil
		}
	}
	if upRemote != "" {
		return upRemote, nil
	}
	remotes, err := r.Remotes(ctx)
	if err != nil {
		return "", err
	}
	switch {
	case len(remotes) == 1:
		return remotes[0], nil
	case len(remotes) == 0:
		return "", ErrNoRemote
	}
	for _, rm := range remotes {
		if rm == "origin" {
			return rm, nil
		}
	}
	return "", fmt.Errorf("%w: several remotes and none is the default; choose one", ErrNoRemote)
}

// classifyPushError turns git's push output into a [*PushError].
func classifyPushError(err error, stdout string) *PushError {
	stderr := stderrOf(err)
	all := stdout + "\n" + stderr
	low := strings.ToLower(all)
	pe := &PushError{Kind: PushFailed, Stderr: stderr, Err: err}
	switch {
	case strings.Contains(low, "[rejected]") || strings.Contains(low, "non-fast-forward") || strings.Contains(low, "fetch first"):
		pe.Kind = PushRejected
		pe.Message = "the remote branch contains commits you do not have locally. Update (pull or rebase) first, then push again."
	case strings.Contains(low, "stale info"):
		pe.Kind = PushRejected
		pe.Message = "the remote branch changed since the last fetch, force push with lease was refused. Fetch first."
	case strings.Contains(low, "[remote rejected]") || strings.Contains(low, "hook declined") || strings.Contains(low, "protected branch"):
		pe.Kind = PushRemoteRejected
		pe.Message = "the remote declined the push (server hook or branch protection): " + remoteReason(all)
	case strings.Contains(low, "authentication failed") || strings.Contains(low, "could not read username") ||
		strings.Contains(low, "could not read password") || strings.Contains(low, "permission denied") ||
		strings.Contains(low, "terminal prompts disabled") || strings.Contains(low, "403"):
		pe.Kind = PushAuthFailed
		pe.Message = "authentication failed. Configure a credential helper or an SSH key (ssh-agent); interactive password prompts are not supported."
	case strings.Contains(low, "could not resolve host") || strings.Contains(low, "connection refused") ||
		strings.Contains(low, "timed out") || strings.Contains(low, "network is unreachable") ||
		strings.Contains(low, "unable to access") || strings.Contains(low, "connection reset"):
		pe.Kind = PushNetwork
		pe.Message = "the remote could not be reached. Check the network connection and the remote URL."
	case strings.Contains(low, "does not appear to be a git repository") || strings.Contains(low, "repository not found") ||
		strings.Contains(low, "no such remote"):
		pe.Kind = PushRepoNotFound
		pe.Message = "the remote repository does not exist or is not accessible. Check the remote URL."
	default:
		pe.Message = firstLines(strings.TrimSpace(stderr), 3)
		if pe.Message == "" {
			pe.Message = err.Error()
		}
	}
	return pe
}

// remoteReason extracts the reason of a "[remote rejected] ... (reason)".
func remoteReason(s string) string {
	for _, l := range strings.Split(s, "\n") {
		if i := strings.Index(l, "[remote rejected]"); i >= 0 {
			if a, b := strings.LastIndex(l, "("), strings.LastIndex(l, ")"); a >= 0 && b > a {
				return l[a+1 : b]
			}
		}
	}
	return "see details"
}
