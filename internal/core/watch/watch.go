// Package watch is Tiegel's project-wide, recursive file watcher (D140).
//
// A Watcher keeps a snapshot of the project tree (size, mtime, file ID and
// type of every entry) and turns raw notifications of an OS backend into
// classified changes: created, modified, deleted and renamed (renames are
// paired by file ID within one batch). Backends only report "something
// happened at this path" (or "rescan this subtree"); the classification is
// always done by comparing the snapshot with the disk, so all backends
// produce the same events:
//
//   - FSEvents (macOS, cgo): one stream for the whole tree, no file
//     descriptor per file,
//   - fsnotify: inotify (Linux), kqueue (BSD); one watch per directory.
//     On Windows the same backend name is one recursive
//     ReadDirectoryChangesW on the root (per-directory handles would block
//     renaming directories),
//   - polling: a full rescan every PollInterval (fallback, and the backend
//     used when the others fail).
//
// Notifications are debounced (Debounce, at most MaxDelay after the first
// one) and delivered as one Batch to Options.OnBatch on the watcher
// goroutine. The .git directory is not part of the snapshot; changes of its
// HEAD, index, packed-refs, refs/ and the *_HEAD files of merges, rebases
// and cherry-picks are reported in Batch.Git. Excluded directories
// (default node_modules) are not watched at all.
//
// The package has no UI or API dependency; internal/api turns batches into
// the bus events files.changed, git.changed and the document reconciliation.
package watch

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// Kind classifies a change.
type Kind string

// Change kinds.
const (
	Created  Kind = "created"
	Modified Kind = "modified"
	Deleted  Kind = "deleted"
	Renamed  Kind = "renamed"
)

// Change is one classified change. Paths are absolute.
type Change struct {
	Path string `json:"path"`
	Kind Kind   `json:"kind"`
	// OldPath is the previous path of a renamed entry.
	OldPath string `json:"oldPath,omitempty"`
	// Dir is true for directories.
	Dir bool `json:"dir,omitempty"`
}

// Batch is the result of one debounce interval.
type Batch struct {
	// Changes are sorted by path.
	Changes []Change
	// Git lists the changed Git metadata files (absolute paths below
	// .git: HEAD, index, packed-refs, refs/..., MERGE_HEAD, ...).
	Git []string
}

// Empty reports whether b has neither changes nor Git changes.
func (b Batch) Empty() bool { return len(b.Changes) == 0 && len(b.Git) == 0 }

// Backend names a notification source.
type Backend string

// Backends.
const (
	Auto     Backend = "auto"
	FSEvents Backend = "fsevents"
	FSNotify Backend = "fsnotify"
	Poll     Backend = "poll"
)

// ParseBackend parses a backend name ("" means Auto).
func ParseBackend(s string) (Backend, error) {
	switch b := Backend(s); b {
	case "":
		return Auto, nil
	case Auto, FSEvents, FSNotify, Poll:
		return b, nil
	}
	return "", fmt.Errorf("watch: unknown backend %q (auto, fsevents, fsnotify, poll)", s)
}

// DefaultExclude are the directory names excluded when Options.Exclude is
// nil.
var DefaultExclude = []string{"node_modules"}

// Defaults.
const (
	DefaultDebounce     = 100 * time.Millisecond
	DefaultMaxDelay     = time.Second
	DefaultPollInterval = 2 * time.Second
	// maxPending turns many single notifications into one full rescan.
	maxPending = 20000
	// racyWindow: an entry whose mtime is younger than this may have been
	// changed again within the timestamp granularity without a visible
	// stat change (the "racy git" problem).
	racyWindow = 2 * time.Second
)

// Options configures a Watcher.
type Options struct {
	// Backend selects the notification source (default Auto: FSEvents on
	// macOS with cgo, fsnotify elsewhere, polling as fallback).
	Backend Backend
	// Debounce is the quiet time before a batch is delivered.
	Debounce time.Duration
	// MaxDelay bounds the delay after the first notification, so a
	// continuous stream of changes still produces batches.
	MaxDelay time.Duration
	// PollInterval is the rescan interval of the polling backend.
	PollInterval time.Duration
	// Exclude lists directory names (matched at any depth, e.g.
	// "node_modules", "vendor") or slash-separated paths relative to the
	// root (containing a "/", e.g. "var/cache") that are not watched. nil
	// means DefaultExclude; an empty non-nil slice excludes nothing.
	Exclude []string
	// OnBatch receives the batches on the watcher goroutine; the next
	// batch is collected while it runs.
	OnBatch func(Batch)
	// OnError receives non-fatal problems (backend failures that switched
	// to polling, unreadable directories).
	OnError func(error)
}

// Stats describes a running watcher.
type Stats struct {
	Root    string  `json:"root"`
	Backend Backend `json:"backend"`
	Ready   bool    `json:"ready"`
	Dirs    int     `json:"dirs"`
	Files   int     `json:"files"`
	// Startup is the time from Start until the initial scan was done.
	Startup time.Duration `json:"startup"`
	// Batches counts delivered batches, Rescans full rescans.
	Batches int `json:"batches"`
	Rescans int `json:"rescans"`
	// Degraded is set when the backend failed and polling took over.
	Degraded string `json:"degraded,omitempty"`
}

// entry is the snapshot of one file or directory.
type entry struct {
	size  int64
	mtime int64 // UnixNano
	id    uint64
	dir   bool
}

func entryOf(fi fs.FileInfo) entry {
	return entry{size: fi.Size(), mtime: fi.ModTime().UnixNano(), id: fileID(fi), dir: fi.IsDir()}
}

func (e entry) sameFile(o entry) bool {
	return e.dir == o.dir && e.size == o.size && e.mtime == o.mtime && e.id == o.id
}

// backend is a notification source. Notifications go to Watcher.notify.
type backend interface {
	// addDir starts watching a directory (no-op for recursive backends).
	addDir(abs string) error
	close()
}

// Watcher watches a directory tree. Create it with Start.
type Watcher struct {
	root     string
	opts     Options
	excNames map[string]bool
	excPaths []string // OS separators

	// pending notifications, guarded by mu
	mu      sync.Mutex
	pending map[string]bool // rel path → rescan subtree
	full    bool
	stats   Stats

	wake    chan struct{}
	refresh chan chan struct{}
	quit    chan struct{}
	done    chan struct{}
	ready   chan struct{}
	once    sync.Once

	// owned by the run goroutine
	be      backend
	state   map[string]entry
	git     map[string]entry
	pollC   <-chan time.Time
	pollT   *time.Ticker
	isPoll  bool
	degrade error
}

// Start resolves root (symlinks are evaluated, as notification backends
// report real paths) and starts watching it in the background. The initial
// scan runs on the watcher goroutine; Ready is closed when it is done.
// Changes made before that are part of the initial snapshot, not events.
func Start(root string, opts Options) (*Watcher, error) {
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, fmt.Errorf("watch: %w", err)
	}
	if r, err := filepath.EvalSymlinks(abs); err == nil {
		abs = r
	}
	st, err := os.Stat(abs)
	if err != nil {
		return nil, fmt.Errorf("watch: %w", err)
	}
	if !st.IsDir() {
		return nil, fmt.Errorf("watch: %s is not a directory", abs)
	}
	if opts.Backend == "" {
		opts.Backend = Auto
	}
	if _, err := ParseBackend(string(opts.Backend)); err != nil {
		return nil, err
	}
	if opts.Debounce <= 0 {
		opts.Debounce = DefaultDebounce
	}
	if opts.MaxDelay < opts.Debounce {
		opts.MaxDelay = max(DefaultMaxDelay, opts.Debounce)
	}
	if opts.PollInterval <= 0 {
		opts.PollInterval = DefaultPollInterval
	}
	if opts.Exclude == nil {
		opts.Exclude = DefaultExclude
	}
	if opts.OnBatch == nil {
		opts.OnBatch = func(Batch) {}
	}
	if opts.OnError == nil {
		opts.OnError = func(error) {}
	}
	w := &Watcher{
		root:     abs,
		opts:     opts,
		excNames: map[string]bool{},
		pending:  map[string]bool{},
		wake:     make(chan struct{}, 1),
		refresh:  make(chan chan struct{}),
		quit:     make(chan struct{}),
		done:     make(chan struct{}),
		ready:    make(chan struct{}),
	}
	for _, e := range opts.Exclude {
		e = strings.Trim(filepath.ToSlash(e), "/")
		switch {
		case e == "":
		case strings.Contains(e, "/"):
			w.excPaths = append(w.excPaths, filepath.FromSlash(e))
		default:
			w.excNames[e] = true
		}
	}
	if w.opts.Backend == Auto {
		w.opts.Backend = autoBackend()
	}
	w.stats.Root, w.stats.Backend = abs, w.opts.Backend
	go w.run()
	return w, nil
}

// Root returns the watched (symlink-resolved) root.
func (w *Watcher) Root() string { return w.root }

// Ready is closed once the initial scan is done.
func (w *Watcher) Ready() <-chan struct{} { return w.ready }

// Stats returns the current statistics.
func (w *Watcher) Stats() Stats {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.stats
}

// Refresh rescans the whole tree now (e.g. when the window gets the focus
// again) and returns after the resulting batch was delivered to OnBatch.
func (w *Watcher) Refresh(ctx context.Context) error {
	done := make(chan struct{})
	select {
	case w.refresh <- done:
	case <-w.done:
		return errors.New("watch: watcher closed")
	case <-ctx.Done():
		return ctx.Err()
	}
	select {
	case <-done:
		return nil
	case <-w.done:
		return errors.New("watch: watcher closed")
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Close stops the watcher and waits until its goroutine ended (a batch
// being delivered finishes first). It is safe to call more than once.
func (w *Watcher) Close() error {
	w.once.Do(func() { close(w.quit) })
	<-w.done
	return nil
}

// notify records a raw notification for an absolute path. subtree asks
// for a rescan of everything below path. Safe for concurrent use.
func (w *Watcher) notify(abs string, subtree bool) {
	rel, ok := w.rel(abs)
	if !ok {
		return
	}
	if g, ok := gitRel(rel); ok && !isGitMeta(g) {
		return // objects, logs, lock files: activity, not state
	}
	w.mu.Lock()
	if rel == "" {
		w.full = true
	} else if !w.full {
		w.pending[rel] = w.pending[rel] || subtree
		if len(w.pending) > maxPending {
			w.full = true
		}
	}
	if w.full {
		clear(w.pending)
	}
	w.mu.Unlock()
	w.poke()
}

// notifyFull requests a full rescan (events were dropped).
func (w *Watcher) notifyFull() {
	w.mu.Lock()
	w.full = true
	clear(w.pending)
	w.mu.Unlock()
	w.poke()
}

func (w *Watcher) poke() {
	select {
	case w.wake <- struct{}{}:
	default:
	}
}

// rel returns abs relative to the root ("" for the root itself).
func (w *Watcher) rel(abs string) (string, bool) {
	abs = filepath.Clean(abs)
	if abs == w.root || strings.EqualFold(abs, w.root) {
		return "", true
	}
	prefix := w.root
	if !strings.HasSuffix(prefix, string(filepath.Separator)) {
		prefix += string(filepath.Separator)
	}
	if !strings.HasPrefix(abs, prefix) {
		// Case-insensitive file systems (macOS, Windows): the backend
		// may report the on-disk spelling of a root opened differently.
		if len(abs) < len(prefix) || !strings.EqualFold(abs[:len(prefix)], prefix) {
			return "", false
		}
	}
	return abs[len(prefix):], true
}

func (w *Watcher) abs(rel string) string {
	if rel == "" {
		return w.root
	}
	return filepath.Join(w.root, rel)
}

// excluded reports whether rel (not below .git) is not watched.
func (w *Watcher) excluded(rel string) bool {
	base := filepath.Base(rel)
	if w.excNames[base] || base == ".git" {
		return true
	}
	// Temporary files of Tiegel's own atomic writes.
	if strings.HasPrefix(base, ".") && strings.Contains(base, ".tiegel-") {
		return true
	}
	for _, p := range w.excPaths {
		if rel == p || strings.HasPrefix(rel, p+string(filepath.Separator)) {
			return true
		}
	}
	// An excluded directory name anywhere in the path.
	if len(w.excNames) > 0 {
		for dir := filepath.Dir(rel); dir != "." && dir != ""; dir = filepath.Dir(dir) {
			if w.excNames[filepath.Base(dir)] {
				return true
			}
		}
	}
	return false
}

const gitDir = ".git"

// gitRel returns the path below .git ("HEAD", "refs/heads/main") of rel,
// or ok=false if rel is not inside the root's .git directory.
func gitRel(rel string) (string, bool) {
	if rel == gitDir {
		return "", true
	}
	if r, ok := strings.CutPrefix(rel, gitDir+string(filepath.Separator)); ok {
		return filepath.ToSlash(r), true
	}
	return "", false
}

// gitMeta lists the files below .git whose changes are reported.
var gitMeta = []string{"HEAD", "index", "packed-refs", "MERGE_HEAD", "CHERRY_PICK_HEAD", "REVERT_HEAD", "REBASE_HEAD"}

// isGitMeta reports whether g (relative to .git, slash separated) is a
// reported Git metadata path (or a directory below refs).
func isGitMeta(g string) bool {
	if g == "" || strings.HasSuffix(g, ".lock") {
		return g == ""
	}
	for _, m := range gitMeta {
		if g == m {
			return true
		}
	}
	return g == "refs" || strings.HasPrefix(g, "refs/")
}

func (w *Watcher) run() {
	defer close(w.done)
	start := time.Now()
	w.be = w.startBackend()
	w.state = make(map[string]entry, 1024)
	w.walk("", w.state)
	w.git = w.scanGit()
	w.mu.Lock()
	w.stats.Ready = true
	w.stats.Startup = time.Since(start)
	w.countLocked()
	w.mu.Unlock()
	close(w.ready)
	defer func() {
		if w.be != nil {
			w.be.close()
		}
		if w.pollT != nil {
			w.pollT.Stop()
		}
	}()

	var timer *time.Timer
	var timerC <-chan time.Time
	var first time.Time
	for {
		select {
		case <-w.quit:
			return
		case <-w.wake:
			now := time.Now()
			if timer == nil {
				first = now
				timer = time.NewTimer(w.opts.Debounce)
				timerC = timer.C
				continue
			}
			d := min(w.opts.Debounce, w.opts.MaxDelay-now.Sub(first))
			timer.Reset(max(d, 0))
		case <-timerC:
			timer, timerC = nil, nil
			w.flush(false)
		case <-w.pollC:
			w.flush(true)
		case done := <-w.refresh:
			if timer != nil {
				timer.Stop()
				timer, timerC = nil, nil
			}
			w.flush(true)
			close(done)
		}
	}
}

// startBackend creates the configured backend; on failure it falls back
// to polling (reported through OnError).
func (w *Watcher) startBackend() backend {
	kind := w.opts.Backend
	var be backend
	var err error
	switch kind {
	case FSEvents:
		be, err = newFSEvents(w)
	case FSNotify:
		be, err = newFSNotify(w)
	case Poll:
		w.setBackend(Poll)
		w.startPolling()
		return nil
	}
	if err != nil {
		w.setBackend(Poll)
		w.fallback(fmt.Errorf("watch: %s backend: %w (polling instead)", kind, err))
		return nil
	}
	w.setBackend(kind)
	return be
}

func (w *Watcher) setBackend(b Backend) {
	w.mu.Lock()
	w.stats.Backend = b
	w.mu.Unlock()
}

// fallback switches to polling after a backend failure.
func (w *Watcher) fallback(err error) {
	w.mu.Lock()
	if w.stats.Degraded == "" {
		w.stats.Degraded = err.Error()
	}
	w.mu.Unlock()
	w.opts.OnError(err)
	w.startPolling()
}

func (w *Watcher) startPolling() {
	if w.pollT == nil {
		w.pollT = time.NewTicker(w.opts.PollInterval)
		w.pollC = w.pollT.C
	}
}

func (w *Watcher) addWatch(abs string) {
	if w.be == nil {
		return
	}
	if err := w.be.addDir(abs); err != nil {
		// E.g. the inotify watch limit: keep the backend for what it
		// watches and poll for the rest.
		if w.pollT == nil {
			w.fallback(fmt.Errorf("watch: %s: %w (polling in addition)", abs, err))
		}
	}
}

func (w *Watcher) countLocked() {
	files, dirs := 0, 0
	for _, e := range w.state {
		if e.dir {
			dirs++
		} else {
			files++
		}
	}
	w.stats.Files, w.stats.Dirs = files, dirs
}

// walk records every entry below rel (not rel itself) in out and adds
// backend watches for the directories.
func (w *Watcher) walk(rel string, out map[string]entry) {
	dir := w.abs(rel)
	w.addWatch(dir)
	ents, err := os.ReadDir(dir)
	if err != nil {
		if !errors.Is(err, fs.ErrNotExist) && !errors.Is(err, fs.ErrPermission) {
			w.opts.OnError(fmt.Errorf("watch: %w", err))
		}
	}
	for _, de := range ents {
		crel := de.Name()
		if rel != "" {
			crel = filepath.Join(rel, crel)
		}
		if w.excluded(crel) {
			continue
		}
		fi, err := de.Info()
		if err != nil {
			continue // vanished
		}
		e := entryOf(fi)
		out[crel] = e
		if e.dir {
			w.walk(crel, out)
		}
	}
}

// scanGit snapshots the reported Git metadata (and watches .git and the
// refs directories for directory-based backends).
func (w *Watcher) scanGit() map[string]entry {
	out := map[string]entry{}
	gd := filepath.Join(w.root, gitDir)
	st, err := os.Lstat(gd)
	if err != nil || !st.IsDir() {
		return out // no repository, or a worktree/submodule (.git file)
	}
	w.addWatch(gd)
	for _, m := range gitMeta {
		if fi, err := os.Lstat(filepath.Join(gd, m)); err == nil {
			out[filepath.Join(gitDir, m)] = entryOf(fi)
		}
	}
	var walkRefs func(rel string)
	walkRefs = func(rel string) {
		w.addWatch(w.abs(rel))
		ents, _ := os.ReadDir(w.abs(rel))
		for _, de := range ents {
			crel := filepath.Join(rel, de.Name())
			if strings.HasSuffix(de.Name(), ".lock") {
				continue
			}
			if de.IsDir() {
				walkRefs(crel)
				continue
			}
			if fi, err := de.Info(); err == nil {
				out[crel] = entryOf(fi)
			}
		}
	}
	walkRefs(filepath.Join(gitDir, "refs"))
	return out
}

// flush processes the pending notifications (all = full rescan) and
// delivers the batch.
func (w *Watcher) flush(all bool) {
	w.mu.Lock()
	pending := w.pending
	full := w.full || all
	w.pending = map[string]bool{}
	w.full = false
	w.mu.Unlock()

	b := w.process(pending, full)
	w.mu.Lock()
	if full {
		w.stats.Rescans++
	}
	w.countLocked()
	if !b.Empty() {
		w.stats.Batches++
	}
	w.mu.Unlock()
	if !b.Empty() {
		w.opts.OnBatch(b)
	}
}

// process compares the notified paths with the disk and updates the
// snapshot.
func (w *Watcher) process(pending map[string]bool, full bool) Batch {
	roots := map[string]bool{} // subtrees to rescan ("" = everything)
	var singles []string
	gitTouched := full
	if full {
		roots[""] = true
	}
	for rel, sub := range pending {
		if _, ok := gitRel(rel); ok {
			gitTouched = true
			continue
		}
		if w.excluded(rel) {
			continue
		}
		if sub {
			roots[rel] = true
		} else {
			singles = append(singles, rel)
		}
	}
	// A notified path that is (or was) a directory is rescanned as a
	// subtree: its children appeared, vanished or moved with it.
	var files []string
	for _, rel := range singles {
		fi, err := os.Lstat(w.abs(rel))
		old, had := w.state[rel]
		if (err == nil && fi.IsDir()) || (had && old.dir) {
			roots[rel] = true
		} else {
			files = append(files, rel)
		}
	}
	roots = pruneRoots(roots)
	covered := func(rel string) bool {
		if roots[""] {
			return true
		}
		for p := rel; p != "." && p != ""; p = filepath.Dir(p) {
			if roots[p] {
				return true
			}
		}
		return false
	}

	oldE := map[string]entry{}
	newE := map[string]entry{}
	if len(roots) > 0 {
		for rel, e := range w.state {
			if covered(rel) {
				oldE[rel] = e
			}
		}
		for root := range roots {
			if root != "" {
				fi, err := os.Lstat(w.abs(root))
				if err != nil {
					continue // the subtree is gone
				}
				newE[root] = entryOf(fi)
				if !fi.IsDir() {
					continue
				}
			}
			w.walk(root, newE)
		}
	}
	var racy []string
	now := time.Now()
	for _, rel := range files {
		if covered(rel) {
			continue
		}
		old, had := w.state[rel]
		if had {
			oldE[rel] = old
		}
		fi, err := os.Lstat(w.abs(rel))
		if err != nil {
			continue
		}
		e := entryOf(fi)
		newE[rel] = e
		if had && old.sameFile(e) && now.Sub(fi.ModTime()) < racyWindow {
			// Same stat, but young enough to have changed within the
			// timestamp granularity: report it, consumers compare content.
			racy = append(racy, rel)
		}
	}

	changes := w.diff(oldE, newE)
	for _, rel := range racy {
		changes = append(changes, Change{Path: w.abs(rel), Kind: Modified})
	}
	for rel := range oldE {
		delete(w.state, rel)
	}
	for rel, e := range newE {
		w.state[rel] = e
	}
	sort.Slice(changes, func(i, j int) bool { return changes[i].Path < changes[j].Path })

	b := Batch{Changes: changes}
	if gitTouched {
		b.Git = w.diffGit()
	}
	return b
}

// pruneRoots drops roots below other roots.
func pruneRoots(roots map[string]bool) map[string]bool {
	if roots[""] {
		return map[string]bool{"": true}
	}
	out := map[string]bool{}
	for r := range roots {
		nested := false
		for p := filepath.Dir(r); p != "." && p != ""; p = filepath.Dir(p) {
			if roots[p] {
				nested = true
				break
			}
		}
		if !nested {
			out[r] = true
		}
	}
	return out
}

// diff classifies the differences between two snapshots of the same set
// of paths and pairs deletions with creations of the same file as renames.
func (w *Watcher) diff(oldE, newE map[string]entry) []Change {
	var changes []Change
	var deleted, created []string
	for rel, o := range oldE {
		n, ok := newE[rel]
		switch {
		case !ok:
			deleted = append(deleted, rel)
		case o.dir != n.dir:
			deleted = append(deleted, rel)
			created = append(created, rel)
		case !o.dir && !o.sameFile(n):
			changes = append(changes, Change{Path: w.abs(rel), Kind: Modified})
		}
	}
	for rel := range newE {
		if _, ok := oldE[rel]; !ok {
			created = append(created, rel)
		}
	}
	// Pair renames by file ID (or, without IDs, by a unique size+mtime).
	type key struct {
		id          uint64
		size, mtime int64
		dir         bool
	}
	keyOf := func(e entry) key {
		if e.id != 0 {
			return key{id: e.id, dir: e.dir}
		}
		return key{size: e.size, mtime: e.mtime, dir: e.dir}
	}
	byKey := map[key][]string{}
	for _, rel := range deleted {
		k := keyOf(oldE[rel])
		byKey[k] = append(byKey[k], rel)
	}
	createdBy := map[key]int{}
	for _, rel := range created {
		createdBy[keyOf(newE[rel])]++
	}
	renamedTo := map[string]string{} // new rel → old rel
	renamedFrom := map[string]bool{}
	for _, rel := range created {
		e := newE[rel]
		k := keyOf(e)
		olds := byKey[k]
		if len(olds) == 1 && createdBy[k] == 1 && (e.id != 0 || e.size > 0) && olds[0] != rel {
			renamedTo[rel] = olds[0]
			renamedFrom[olds[0]] = true
		}
	}
	pairDirsByContent(oldE, newE, created, deleted, renamedTo, renamedFrom)
	for _, rel := range created {
		if old, ok := renamedTo[rel]; ok {
			changes = append(changes, Change{Path: w.abs(rel), Kind: Renamed, OldPath: w.abs(old), Dir: newE[rel].dir})
			continue
		}
		changes = append(changes, Change{Path: w.abs(rel), Kind: Created, Dir: newE[rel].dir})
	}
	for _, rel := range deleted {
		if !renamedFrom[rel] {
			changes = append(changes, Change{Path: w.abs(rel), Kind: Deleted, Dir: oldE[rel].dir})
		}
	}
	return changes
}

// pairDirsByContent pairs directories that have no file ID (Windows:
// FileInfo carries none, and a directory's size and mtime say nothing):
// a created and a deleted directory are a rename when entries below them
// were renamed from one to the other under the same relative path, and
// the pairing is unique in both directions. Pairs are added to renamedTo
// and renamedFrom.
func pairDirsByContent(oldE, newE map[string]entry, created, deleted []string, renamedTo map[string]string, renamedFrom map[string]bool) {
	sep := string(filepath.Separator)
	gone := map[string]bool{}
	for _, rel := range deleted {
		if e := oldE[rel]; e.dir && e.id == 0 && !renamedFrom[rel] {
			gone[rel] = true
		}
	}
	if len(gone) == 0 {
		return
	}
	cands := map[string]map[string]bool{} // created dir → deleted dirs
	users := map[string]int{}             // deleted dir → number of created dirs naming it
	for _, rel := range created {
		if e := newE[rel]; !e.dir || e.id != 0 {
			continue
		}
		if _, ok := renamedTo[rel]; ok {
			continue
		}
		for n, o := range renamedTo {
			rest, ok := strings.CutPrefix(n, rel+sep)
			if !ok || !strings.HasSuffix(o, sep+rest) {
				continue
			}
			if from := strings.TrimSuffix(o, sep+rest); gone[from] && from != rel {
				if cands[rel] == nil {
					cands[rel] = map[string]bool{}
				}
				if !cands[rel][from] {
					cands[rel][from] = true
					users[from]++
				}
			}
		}
	}
	for rel, froms := range cands {
		if len(froms) != 1 {
			continue
		}
		for from := range froms {
			if users[from] == 1 {
				renamedTo[rel] = from
				renamedFrom[from] = true
			}
		}
	}
}

// diffGit rescans the Git metadata and returns the changed paths.
func (w *Watcher) diffGit() []string {
	cur := w.scanGit()
	var out []string
	for rel, e := range cur {
		if o, ok := w.git[rel]; !ok || !o.sameFile(e) {
			out = append(out, w.abs(rel))
		}
	}
	for rel := range w.git {
		if _, ok := cur[rel]; !ok {
			out = append(out, w.abs(rel))
		}
	}
	w.git = cur
	sort.Strings(out)
	return out
}
