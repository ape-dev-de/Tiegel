package gitcli

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
)

// ErrEmptyMessage is returned when a commit message is empty.
var ErrEmptyMessage = errors.New("gitcli: empty commit message")

// ErrOperationInProgress is returned by [Repo.CommitSelection] while a
// merge, rebase, cherry-pick, revert or am is in progress: a selection
// commit starts from HEAD and would silently drop the changes of the
// operation (while git still records e.g. the second merge parent).
var ErrOperationInProgress = errors.New("gitcli: operation in progress")

// CommitOptions configures a commit.
type CommitOptions struct {
	// Message is the full commit message.
	Message string
	// Amend replaces the HEAD commit.
	Amend bool
	// Author overrides the author ("Name <email>"); empty uses the
	// configured user.name/user.email.
	Author string
	// NoVerify skips the pre-commit and commit-msg hooks.
	NoVerify bool
	// AllowEmpty permits a commit without changes.
	AllowEmpty bool
	// SignOff adds a Signed-off-by trailer.
	SignOff bool
}

func (o CommitOptions) args() []string {
	args := []string{"commit", "-q", "--cleanup=strip", "-F", "-"}
	if o.Amend {
		args = append(args, "--amend")
	}
	if o.Author != "" {
		args = append(args, "--author="+o.Author)
	}
	if o.NoVerify {
		args = append(args, "--no-verify")
	}
	if o.AllowEmpty {
		args = append(args, "--allow-empty")
	}
	if o.SignOff {
		args = append(args, "--signoff")
	}
	return args
}

// Commit commits the current index and returns the new HEAD hash.
func (r *Repo) Commit(ctx context.Context, opts CommitOptions) (string, error) {
	return r.commit(ctx, "", opts)
}

func (r *Repo) commit(ctx context.Context, indexFile string, opts CommitOptions) (string, error) {
	if strings.TrimSpace(opts.Message) == "" {
		return "", ErrEmptyMessage
	}
	o := runOpts{stdin: []byte(opts.Message)}
	if indexFile != "" {
		o.env = []string{"GIT_INDEX_FILE=" + indexFile}
	}
	if _, err := r.run(ctx, o, opts.args()...); err != nil {
		return "", fmt.Errorf("gitcli: commit: %w", err)
	}
	return r.resolve(ctx, "HEAD")
}

// CommitSelection describes what [Repo.CommitSelection] commits.
type CommitSelection struct {
	// Paths are committed with their complete work tree state (new,
	// modified, deleted). For renames list both the old and the new path.
	Paths []string
	// Patches are applied on top of HEAD, typically built with
	// [FileDiff.Patch] from a diff of HEAD against the work tree
	// (DiffOptions{Base: "HEAD"}).
	Patches [][]byte
}

// CommitSelection commits exactly HEAD plus the selection, independent of
// what is currently staged, using a temporary index (see package
// documentation). Afterwards the real index entries of the touched paths
// that matched the old HEAD are reset to the new HEAD. Touched paths with
// staged-only content (index differs from the old HEAD) keep it: the
// committed change is applied on top of the index entry, or, if that does
// not apply cleanly, the entry is left untouched. All other index entries
// stay untouched.
//
// It returns [ErrOperationInProgress] while a merge, rebase, cherry-pick,
// revert or am is in progress (bisecting is allowed).
func (r *Repo) CommitSelection(ctx context.Context, opts CommitOptions, sel CommitSelection) (string, error) {
	if strings.TrimSpace(opts.Message) == "" {
		return "", ErrEmptyMessage
	}
	if st := r.state(); st != StateClean && st != StateBisecting {
		return "", fmt.Errorf("%w: %s; finish or abort it first, or commit the whole index", ErrOperationInProgress, st)
	}
	if len(sel.Paths) == 0 && len(sel.Patches) == 0 && !opts.AllowEmpty && !opts.Amend {
		return "", ErrEmptySelection
	}
	tmp, err := os.CreateTemp(r.GitDir, "tiegel-index-*")
	if err != nil {
		return "", fmt.Errorf("gitcli: temporary index: %w", err)
	}
	idx := tmp.Name()
	_ = tmp.Close()
	// git refuses an existing empty index file; only reserve the name.
	_ = os.Remove(idx)
	defer os.Remove(idx)
	defer os.Remove(idx + ".lock")

	env := runOpts{env: []string{"GIT_INDEX_FILE=" + idx}}
	head, err := r.headOrEmptyTree(ctx)
	if err != nil {
		return "", err
	}
	if _, err := r.run(ctx, env, "read-tree", head); err != nil {
		return "", fmt.Errorf("gitcli: read-tree: %w", err)
	}
	if err := r.addPaths(ctx, idx, sel.Paths); err != nil {
		return "", err
	}
	touched := append([]string(nil), sel.Paths...)
	for _, p := range sel.Patches {
		if err := r.ApplyPatch(ctx, p, ApplyOptions{Cached: true, IndexFile: idx}); err != nil {
			return "", err
		}
		touched = append(touched, patchPaths(p)...)
	}
	touched = dedupe(touched)
	oldTree, err := r.treeOf(ctx, head)
	if err != nil {
		return "", err
	}
	var oldEntries, indexEntries map[string]string
	if len(touched) > 0 {
		if oldEntries, err = r.treeEntries(ctx, oldTree, touched); err != nil {
			return "", err
		}
		if indexEntries, err = r.indexEntries(ctx, touched); err != nil {
			return "", err
		}
	}
	hash, err := r.commit(ctx, idx, opts)
	if err != nil {
		return "", err
	}
	if err := r.syncIndex(ctx, oldTree, touched, oldEntries, indexEntries); err != nil {
		return hash, fmt.Errorf("gitcli: commit succeeded, index update failed: %w", err)
	}
	return hash, nil
}

// syncIndex updates the real index after a selection commit. Entries that
// matched the old HEAD are reset to the new HEAD. Entries with staged-only
// content get the committed change applied on top; if that fails they stay
// as they are, so staged content is never lost.
func (r *Repo) syncIndex(ctx context.Context, oldTree string, touched []string, oldEntries, indexEntries map[string]string) error {
	var reset []string
	for _, p := range touched {
		if oldEntries[p] == indexEntries[p] {
			reset = append(reset, p)
			continue
		}
		patch, err := r.run(ctx, runOpts{}, "diff", "--binary", "--no-renames", "--no-ext-diff", oldTree, "HEAD", "--", p)
		if err != nil || len(patch) == 0 {
			continue
		}
		// Best effort: on failure the staged content stays untouched.
		_ = r.ApplyPatch(ctx, patch, ApplyOptions{Cached: true})
	}
	if len(reset) == 0 {
		return nil
	}
	args := append([]string{"reset", "-q", "HEAD", "--"}, reset...)
	if _, err := r.run(ctx, runOpts{}, args...); err != nil {
		return err
	}
	return nil
}

// treeOf returns the tree hash of a tree-ish.
func (r *Repo) treeOf(ctx context.Context, treeish string) (string, error) {
	out, err := r.run(ctx, runOpts{}, "rev-parse", "--verify", treeish+"^{tree}")
	if err != nil {
		return "", fmt.Errorf("gitcli: resolve tree %s: %w", treeish, err)
	}
	return strings.TrimSpace(string(out)), nil
}

// treeEntries returns "mode hash" per path of the given tree.
func (r *Repo) treeEntries(ctx context.Context, tree string, paths []string) (map[string]string, error) {
	args := append([]string{"ls-tree", "-r", "-z", "--full-tree", tree, "--"}, paths...)
	out, err := r.run(ctx, runOpts{}, args...)
	if err != nil {
		return nil, fmt.Errorf("gitcli: ls-tree: %w", err)
	}
	m := map[string]string{}
	for _, rec := range bytes.Split(out, []byte{0}) {
		// <mode> SP <type> SP <hash> TAB <path>
		meta, path, ok := strings.Cut(string(rec), "\t")
		if !ok {
			continue
		}
		f := strings.Fields(meta)
		if len(f) == 3 {
			m[path] = f[0] + " " + f[2]
		}
	}
	return m, nil
}

// indexEntries returns "mode hash" per path of the real index. Paths with
// conflict stages get a marker that never equals a tree entry.
func (r *Repo) indexEntries(ctx context.Context, paths []string) (map[string]string, error) {
	args := append([]string{"ls-files", "-s", "-z", "--"}, paths...)
	out, err := r.run(ctx, runOpts{}, args...)
	if err != nil {
		return nil, fmt.Errorf("gitcli: ls-files: %w", err)
	}
	m := map[string]string{}
	for _, rec := range bytes.Split(out, []byte{0}) {
		// <mode> SP <hash> SP <stage> TAB <path>
		meta, path, ok := strings.Cut(string(rec), "\t")
		if !ok {
			continue
		}
		f := strings.Fields(meta)
		if len(f) != 3 {
			continue
		}
		if f[2] != "0" {
			m[path] = "conflict"
			continue
		}
		if m[path] != "conflict" {
			m[path] = f[0] + " " + f[1]
		}
	}
	return m, nil
}

// patchPaths returns all file paths a patch touches.
func patchPaths(p []byte) []string {
	files, err := ParseDiff(p)
	if err != nil {
		return nil
	}
	var out []string
	for _, f := range files {
		if f.OldPath != "" {
			out = append(out, f.OldPath)
		}
		if f.NewPath != "" && f.NewPath != f.OldPath {
			out = append(out, f.NewPath)
		}
	}
	return out
}

func dedupe(s []string) []string {
	seen := make(map[string]bool, len(s))
	out := s[:0:0]
	for _, v := range s {
		if !seen[v] {
			seen[v] = true
			out = append(out, v)
		}
	}
	return out
}

// Identity is a configured committer identity.
type Identity struct {
	Name, Email string
}

// String formats the identity as "Name <email>".
func (i Identity) String() string { return fmt.Sprintf("%s <%s>", i.Name, i.Email) }

// ConfigIdentity reads user.name and user.email from the git
// configuration. Missing values are returned empty.
func (r *Repo) ConfigIdentity(ctx context.Context) (Identity, error) {
	var id Identity
	var err error
	if id.Name, err = r.ConfigGet(ctx, "user.name"); err != nil {
		return id, err
	}
	if id.Email, err = r.ConfigGet(ctx, "user.email"); err != nil {
		return id, err
	}
	return id, nil
}

// ConfigGet returns a configuration value or "" if it is not set.
func (r *Repo) ConfigGet(ctx context.Context, key string) (string, error) {
	out, err := r.run(ctx, runOpts{}, "config", "--get", key)
	if err != nil && exitCode(err) == 1 && stderrOf(err) == "" {
		return "", nil // not set
	}
	if err != nil {
		return "", fmt.Errorf("gitcli: config %s: %w", key, err)
	}
	return strings.TrimSpace(string(out)), nil
}

// RecentMessages returns the full messages of the last n commits reachable
// from HEAD, newest first, without duplicates. It returns nil for an
// unborn branch.
func (r *Repo) RecentMessages(ctx context.Context, n int) ([]string, error) {
	if n <= 0 {
		return nil, nil
	}
	if _, err := r.resolve(ctx, "HEAD"); errors.Is(err, ErrNotFound) {
		return nil, nil
	}
	// Look a bit further back so duplicates do not shrink the result.
	out, err := r.run(ctx, runOpts{}, "log", "-z", "--no-show-signature", fmt.Sprintf("-n%d", n*2), "--format=%B", "HEAD")
	if err != nil {
		return nil, fmt.Errorf("gitcli: log messages: %w", err)
	}
	var msgs []string
	seen := map[string]bool{}
	for _, m := range bytes.Split(out, []byte{0}) {
		s := strings.TrimSpace(string(m))
		if s == "" || seen[s] {
			continue
		}
		seen[s] = true
		msgs = append(msgs, s)
		if len(msgs) == n {
			break
		}
	}
	return msgs, nil
}
