package gitcli

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// Commit is one entry of [Repo.Log].
type Commit struct {
	Hash           string
	Parents        []string
	AuthorName     string
	AuthorEmail    string
	AuthorDate     time.Time
	CommitterName  string
	CommitterEmail string
	CommitDate     time.Time
	Subject        string
	// Refs are the decorations, e.g. "HEAD -> main", "origin/main",
	// "tag: v1.0".
	Refs []string
}

// IsMerge reports whether the commit has more than one parent.
func (c *Commit) IsMerge() bool { return len(c.Parents) > 1 }

// LogOptions configures [Repo.Log].
type LogOptions struct {
	// Rev is the start revision (default HEAD). Ignored with All.
	Rev string
	// All lists commits reachable from all refs.
	All bool
	// Skip and Max implement paging; Max 0 means no limit.
	Skip, Max int
	// Path limits the log to commits touching this file.
	Path string
	// Follow follows renames of Path.
	Follow bool
}

const logFormat = "%H%x1f%P%x1f%an%x1f%ae%x1f%at%x1f%cn%x1f%ce%x1f%ct%x1f%D%x1f%s%x00"

// Log lists commits, newest first. An unborn branch yields no commits.
func (r *Repo) Log(ctx context.Context, opts LogOptions) ([]Commit, error) {
	args := []string{"log", "--no-show-signature", "--no-color", "--decorate=short", "--format=" + logFormat}
	if opts.Skip > 0 {
		args = append(args, "--skip="+strconv.Itoa(opts.Skip))
	}
	if opts.Max > 0 {
		args = append(args, "--max-count="+strconv.Itoa(opts.Max))
	}
	if opts.Follow && opts.Path != "" {
		args = append(args, "--follow")
	}
	if opts.All {
		args = append(args, "--all")
	} else {
		rev := opts.Rev
		if rev == "" {
			rev = "HEAD"
			if _, err := r.resolve(ctx, "HEAD"); errors.Is(err, ErrNotFound) {
				return nil, nil
			}
		}
		args = append(args, rev)
	}
	args = append(args, "--")
	if opts.Path != "" {
		args = append(args, opts.Path)
	}
	out, err := r.run(ctx, runOpts{}, args...)
	if err != nil {
		return nil, fmt.Errorf("gitcli: log: %w", err)
	}
	return parseLog(out)
}

func parseLog(out []byte) ([]Commit, error) {
	var commits []Commit
	for _, rec := range bytes.Split(out, []byte{0}) {
		s := strings.TrimLeft(string(rec), "\n")
		if s == "" {
			continue
		}
		f := strings.Split(s, "\x1f")
		if len(f) != 10 {
			return nil, fmt.Errorf("gitcli: log: bad record %q", s)
		}
		c := Commit{
			Hash: f[0], Parents: strings.Fields(f[1]),
			AuthorName: f[2], AuthorEmail: f[3], AuthorDate: unixTime(f[4]),
			CommitterName: f[5], CommitterEmail: f[6], CommitDate: unixTime(f[7]),
			Subject: f[9],
		}
		if f[8] != "" {
			c.Refs = strings.Split(f[8], ", ")
		}
		commits = append(commits, c)
	}
	return commits, nil
}

func unixTime(s string) time.Time {
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return time.Time{}
	}
	return time.Unix(n, 0)
}

// ShowFile returns the content of path at revision rev
// (`git show rev:path`). It wraps [ErrNotFound] when the path or revision
// does not exist.
func (r *Repo) ShowFile(ctx context.Context, rev, path string) ([]byte, error) {
	return r.showObject(ctx, rev+":"+path)
}

// ShowIndexFile returns the content of path in the index at stage
// (0 = normal entry, 1 = base, 2 = ours, 3 = theirs during a conflict).
func (r *Repo) ShowIndexFile(ctx context.Context, stage int, path string) ([]byte, error) {
	if stage < 0 || stage > 3 {
		return nil, fmt.Errorf("gitcli: invalid index stage %d", stage)
	}
	return r.showObject(ctx, ":"+strconv.Itoa(stage)+":"+path)
}

func (r *Repo) showObject(ctx context.Context, obj string) ([]byte, error) {
	out, err := r.run(ctx, runOpts{}, "show", "--no-textconv", "--format=", obj)
	if err != nil {
		if isNotFound(stderrOf(err)) {
			return nil, fmt.Errorf("gitcli: show %s: %w", obj, ErrNotFound)
		}
		return nil, fmt.Errorf("gitcli: show %s: %w", obj, err)
	}
	return out, nil
}

func isNotFound(stderr string) bool {
	for _, s := range []string{"does not exist", "exists on disk, but not in", "invalid object name",
		"bad revision", "unknown revision", "not in the index", "is in the index, but not at stage"} {
		if strings.Contains(stderr, s) {
			return true
		}
	}
	return false
}

// Branch is a local or remote-tracking branch.
type Branch struct {
	// Name is the short name ("main", "origin/main").
	Name string
	// Ref is the full ref name.
	Ref    string
	Remote bool
	// Current marks the checked out branch.
	Current bool
	Hash    string
	// Upstream is the short upstream name of a local branch.
	Upstream string
	// UpstreamGone is set when the upstream branch no longer exists.
	UpstreamGone  bool
	Ahead, Behind int
	Subject       string
	CommitDate    time.Time
}

// Branches lists local and remote-tracking branches (remote HEAD symrefs
// are skipped).
func (r *Repo) Branches(ctx context.Context) ([]Branch, error) {
	format := "%(refname)%00%(refname:short)%00%(objectname)%00%(HEAD)%00%(upstream:short)%00%(upstream:track,nobracket)%00%(symref)%00%(committerdate:unix)%00%(contents:subject)"
	out, err := r.run(ctx, runOpts{}, "for-each-ref", "--format="+format, "refs/heads", "refs/remotes")
	if err != nil {
		return nil, fmt.Errorf("gitcli: branches: %w", err)
	}
	var res []Branch
	for _, line := range strings.Split(string(out), "\n") {
		if line == "" {
			continue
		}
		f := strings.Split(line, "\x00")
		if len(f) != 9 {
			return nil, fmt.Errorf("gitcli: branches: bad line %q", line)
		}
		if f[6] != "" {
			continue
		}
		b := Branch{
			Ref: f[0], Name: f[1], Hash: f[2], Current: f[3] == "*", Upstream: f[4],
			Remote: strings.HasPrefix(f[0], "refs/remotes/"), CommitDate: unixTime(f[7]), Subject: f[8],
		}
		parseTrack(&b, f[5])
		res = append(res, b)
	}
	return res, nil
}

// parseTrack parses "ahead 1, behind 2" or "gone".
func parseTrack(b *Branch, s string) {
	if s == "gone" {
		b.UpstreamGone = true
		return
	}
	for _, part := range strings.Split(s, ", ") {
		var n int
		if _, err := fmt.Sscanf(part, "ahead %d", &n); err == nil {
			b.Ahead = n
		} else if _, err := fmt.Sscanf(part, "behind %d", &n); err == nil {
			b.Behind = n
		}
	}
}

// Checkout switches to a local branch, or detaches at another revision. A
// remote-tracking branch name ("origin/feature") creates and checks out a
// local tracking branch "feature" if none exists.
func (r *Repo) Checkout(ctx context.Context, ref string) error {
	if _, err := r.resolve(ctx, "refs/heads/"+ref); err == nil {
		return r.checkout(ctx, ref)
	}
	if _, err := r.resolve(ctx, "refs/remotes/"+ref); err == nil {
		if _, local, ok := strings.Cut(ref, "/"); ok {
			if _, err := r.resolve(ctx, "refs/heads/"+local); errors.Is(err, ErrNotFound) {
				return r.checkout(ctx, "-b", local, "--track", ref)
			}
		}
	}
	return r.checkout(ctx, "--detach", ref)
}

func (r *Repo) checkout(ctx context.Context, args ...string) error {
	full := append([]string{"checkout", "-q"}, args...)
	full = append(full, "--")
	if _, err := r.run(ctx, runOpts{}, full...); err != nil {
		return fmt.Errorf("gitcli: checkout: %w", err)
	}
	return nil
}

// CreateBranch creates a branch at start (empty = HEAD) and optionally
// checks it out.
func (r *Repo) CreateBranch(ctx context.Context, name, start string, checkout bool) error {
	if _, err := r.run(ctx, runOpts{}, "check-ref-format", "--branch", name); err != nil {
		return fmt.Errorf("gitcli: invalid branch name %q: %w", name, err)
	}
	if checkout {
		args := []string{"-b", name}
		if start != "" {
			args = append(args, start)
		}
		return r.checkout(ctx, args...)
	}
	args := []string{"branch", "--", name}
	if start != "" {
		args = append(args, start)
	}
	if _, err := r.run(ctx, runOpts{}, args...); err != nil {
		return fmt.Errorf("gitcli: create branch: %w", err)
	}
	return nil
}

// DeleteBranch deletes a local branch; force deletes unmerged branches.
func (r *Repo) DeleteBranch(ctx context.Context, name string, force bool) error {
	flag := "-d"
	if force {
		flag = "-D"
	}
	if _, err := r.run(ctx, runOpts{}, "branch", flag, "--", name); err != nil {
		return fmt.Errorf("gitcli: delete branch: %w", err)
	}
	return nil
}

// ConflictVersions are the index stages of an unmerged path.
type ConflictVersions struct {
	Base, Ours, Theirs          []byte
	HasBase, HasOurs, HasTheirs bool
}

// ConflictVersions reads stages 1 (base), 2 (ours) and 3 (theirs) of path.
// Missing stages (e.g. added on both sides, deleted on one side) are
// reported through the Has flags.
func (r *Repo) ConflictVersions(ctx context.Context, path string) (*ConflictVersions, error) {
	cv := &ConflictVersions{}
	targets := []struct {
		data *[]byte
		has  *bool
	}{{&cv.Base, &cv.HasBase}, {&cv.Ours, &cv.HasOurs}, {&cv.Theirs, &cv.HasTheirs}}
	for i, t := range targets {
		b, err := r.ShowIndexFile(ctx, i+1, path)
		if errors.Is(err, ErrNotFound) {
			continue
		}
		if err != nil {
			return nil, err
		}
		*t.data, *t.has = b, true
	}
	if !cv.HasBase && !cv.HasOurs && !cv.HasTheirs {
		return nil, fmt.Errorf("gitcli: %s is not in conflict: %w", path, ErrNotFound)
	}
	return cv, nil
}

// ResolveConflict writes content to path in the work tree (keeping the
// file mode if the file exists) and marks it resolved with `git add`.
func (r *Repo) ResolveConflict(ctx context.Context, path string, content []byte) error {
	abs := r.abs(path)
	mode := os.FileMode(0o644)
	if st, err := os.Stat(abs); err == nil {
		mode = st.Mode().Perm()
	}
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		return fmt.Errorf("gitcli: resolve %s: %w", path, err)
	}
	if err := os.WriteFile(abs, content, mode); err != nil {
		return fmt.Errorf("gitcli: resolve %s: %w", path, err)
	}
	return r.Stage(ctx, path)
}
