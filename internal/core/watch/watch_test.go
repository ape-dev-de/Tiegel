package watch

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

// waitTimeout bounds every wait for an event; the tests poll with a
// deadline instead of sleeping.
const waitTimeout = 10 * time.Second

type collector struct {
	mu      sync.Mutex
	changes []Change
	git     []string
	batches int
}

func (c *collector) add(b Batch) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.changes = append(c.changes, b.Changes...)
	c.git = append(c.git, b.Git...)
	c.batches++
}

func (c *collector) reset() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.changes, c.git = nil, nil
}

func (c *collector) snapshot() ([]Change, []string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]Change(nil), c.changes...), append([]string(nil), c.git...)
}

// waitFor polls until cond holds for the collected changes.
func (c *collector) waitFor(t *testing.T, what string, cond func([]Change, []string) bool) {
	t.Helper()
	deadline := time.Now().Add(waitTimeout)
	for {
		ch, git := c.snapshot()
		if cond(ch, git) {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("timeout waiting for %s; got changes %+v git %v", what, ch, git)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func has(ch []Change, kind Kind, path string) bool {
	for _, c := range ch {
		if c.Kind == kind && c.Path == path {
			return true
		}
	}
	return false
}

func hasRename(ch []Change, from, to string) bool {
	for _, c := range ch {
		if c.Kind == Renamed && c.Path == to && c.OldPath == from {
			return true
		}
	}
	return false
}

func mkfile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func backends() []Backend {
	bs := []Backend{FSNotify, Poll}
	if fseventsAvailable {
		bs = append([]Backend{FSEvents}, bs...)
	}
	return bs
}

func startWatcher(t *testing.T, root string, opts Options) (*Watcher, *collector) {
	t.Helper()
	c := &collector{}
	opts.OnBatch = c.add
	if opts.PollInterval == 0 {
		opts.PollInterval = 50 * time.Millisecond
	}
	if opts.Debounce == 0 {
		opts.Debounce = 20 * time.Millisecond
	}
	w, err := Start(root, opts)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = w.Close() })
	select {
	case <-w.Ready():
	case <-time.After(waitTimeout):
		t.Fatal("watcher not ready")
	}
	if st := w.Stats(); st.Backend != opts.Backend || st.Degraded != "" {
		t.Fatalf("backend %+v, want %s", st, opts.Backend)
	}
	return w, c
}

func TestBackends(t *testing.T) {
	for _, be := range backends() {
		t.Run(string(be), func(t *testing.T) {
			dir := t.TempDir()
			mkfile(t, filepath.Join(dir, "a.php"), "a")
			mkfile(t, filepath.Join(dir, "src", "b.php"), "b")
			mkfile(t, filepath.Join(dir, "node_modules", "x", "y.js"), "y")
			mkfile(t, filepath.Join(dir, ".git", "HEAD"), "ref: refs/heads/main\n")
			mkfile(t, filepath.Join(dir, ".git", "refs", "heads", "main"), "1111\n")
			mkfile(t, filepath.Join(dir, ".git", "objects", "ab", "cd"), "obj")
			w, c := startWatcher(t, dir, Options{Backend: be})
			root := w.Root()
			p := func(rel string) string { return filepath.Join(root, filepath.FromSlash(rel)) }
			if st := w.Stats(); st.Files != 2 || st.Dirs != 1 {
				t.Fatalf("initial scan %+v (node_modules and .git must be skipped)", st)
			}

			// create
			mkfile(t, p("c.php"), "c")
			c.waitFor(t, "create", func(ch []Change, _ []string) bool { return has(ch, Created, p("c.php")) })
			c.reset()

			// modify
			mkfile(t, p("src/b.php"), "b changed")
			c.waitFor(t, "modify", func(ch []Change, _ []string) bool { return has(ch, Modified, p("src/b.php")) })
			c.reset()

			// rename
			if err := os.Rename(p("c.php"), p("src/d.php")); err != nil {
				t.Fatal(err)
			}
			c.waitFor(t, "rename", func(ch []Change, _ []string) bool { return hasRename(ch, p("c.php"), p("src/d.php")) })
			c.reset()

			// delete
			if err := os.Remove(p("a.php")); err != nil {
				t.Fatal(err)
			}
			c.waitFor(t, "delete", func(ch []Change, _ []string) bool { return has(ch, Deleted, p("a.php")) })
			c.reset()

			// new directory with files (the backend must pick up the new
			// directory and the files written right after creating it)
			mkfile(t, p("new/deep/e.php"), "e")
			c.waitFor(t, "new dir", func(ch []Change, _ []string) bool {
				return has(ch, Created, p("new")) && has(ch, Created, p("new/deep/e.php"))
			})
			c.reset()
			mkfile(t, p("new/deep/f.php"), "f")
			c.waitFor(t, "file in new dir", func(ch []Change, _ []string) bool { return has(ch, Created, p("new/deep/f.php")) })
			c.reset()

			// rename a directory: children are renamed with it
			if err := os.Rename(p("new"), p("moved")); err != nil {
				t.Fatal(err)
			}
			c.waitFor(t, "dir rename", func(ch []Change, _ []string) bool {
				return hasRename(ch, p("new"), p("moved")) && hasRename(ch, p("new/deep/e.php"), p("moved/deep/e.php"))
			})
			c.reset()

			// delete a directory
			if err := os.RemoveAll(p("moved")); err != nil {
				t.Fatal(err)
			}
			c.waitFor(t, "dir delete", func(ch []Change, _ []string) bool {
				return has(ch, Deleted, p("moved")) && has(ch, Deleted, p("moved/deep/f.php"))
			})
			c.reset()

			// git metadata
			mkfile(t, p(".git/objects/ef/gh"), "obj")
			mkfile(t, p(".git/refs/heads/main"), "2222\n")
			c.waitFor(t, "git ref", func(_ []Change, git []string) bool {
				for _, g := range git {
					if g == p(".git/refs/heads/main") {
						return true
					}
				}
				return false
			})
			c.reset()
			mkfile(t, p(".git/HEAD"), "ref: refs/heads/other\n")
			c.waitFor(t, "git HEAD", func(_ []Change, git []string) bool { return len(git) > 0 })

			// Excluded directories, Git objects and Tiegel's temporary
			// files never show up; a final marker proves the watcher has
			// processed everything before.
			c.reset()
			mkfile(t, p("node_modules/x/z.js"), "z")
			mkfile(t, p(".a.php.tiegel-123"), "tmp")
			mkfile(t, p("marker"), "m")
			c.waitFor(t, "marker", func(ch []Change, _ []string) bool { return has(ch, Created, p("marker")) })
			ch, _ := c.snapshot()
			for _, x := range ch {
				if strings.Contains(x.Path, "node_modules") || strings.Contains(x.Path, ".tiegel-") || strings.Contains(x.Path, string(filepath.Separator)+".git") {
					t.Fatalf("excluded path reported: %+v", x)
				}
			}
		})
	}
}

func TestRefreshDeliversSynchronously(t *testing.T) {
	dir := t.TempDir()
	mkfile(t, filepath.Join(dir, "a"), "1")
	w, c := startWatcher(t, dir, Options{Backend: Poll, PollInterval: time.Hour})
	mkfile(t, filepath.Join(w.Root(), "b"), "2")
	if err := w.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	ch, _ := c.snapshot()
	if !has(ch, Created, filepath.Join(w.Root(), "b")) {
		t.Fatalf("refresh did not report the new file: %+v", ch)
	}
	if st := w.Stats(); st.Rescans != 1 || st.Batches != 1 || st.Files != 2 {
		t.Fatalf("stats %+v", st)
	}
	_ = w.Close()
	if err := w.Refresh(context.Background()); err == nil {
		t.Fatal("refresh after close succeeded")
	}
	_ = w.Close() // idempotent
}

func TestExcludeOptions(t *testing.T) {
	dir := t.TempDir()
	mkfile(t, filepath.Join(dir, "vendor", "a.php"), "a")
	mkfile(t, filepath.Join(dir, "var", "cache", "c"), "c")
	mkfile(t, filepath.Join(dir, "var", "log", "l"), "l")
	mkfile(t, filepath.Join(dir, "node_modules", "n"), "n")
	w, _ := startWatcher(t, dir, Options{Backend: Poll, PollInterval: time.Hour, Exclude: []string{"vendor", "var/cache/"}})
	// node_modules is only excluded by default.
	if st := w.Stats(); st.Files != 2 {
		t.Fatalf("files %+v (want var/log/l and node_modules/n)", st)
	}
}

func TestParseBackend(t *testing.T) {
	for _, s := range []string{"", "auto", "fsevents", "fsnotify", "poll"} {
		if _, err := ParseBackend(s); err != nil {
			t.Fatal(s, err)
		}
	}
	if _, err := ParseBackend("inotify"); err == nil {
		t.Fatal("unknown backend accepted")
	}
	if _, err := Start(t.TempDir(), Options{Backend: "x"}); err == nil {
		t.Fatal("Start with unknown backend")
	}
	f := filepath.Join(t.TempDir(), "f")
	mkfile(t, f, "")
	if _, err := Start(f, Options{}); err == nil {
		t.Fatal("Start on a file")
	}
	if _, err := Start(filepath.Join(f, "missing"), Options{}); err == nil {
		t.Fatal("Start on a missing dir")
	}
}

func TestAutoBackend(t *testing.T) {
	w, _ := startWatcher(t, t.TempDir(), Options{Backend: autoBackend()})
	want := FSNotify
	if runtime.GOOS == "darwin" {
		want = Poll
		if fseventsAvailable {
			want = FSEvents
		}
	}
	if got := w.Stats().Backend; got != want {
		t.Fatalf("auto backend %s, want %s", got, want)
	}
}

// Many changes at once (more than maxPending notifications are folded
// into one rescan) are all reported.
func TestManyFiles(t *testing.T) {
	for _, be := range backends() {
		if be == FSNotify && runtime.GOOS == "darwin" {
			continue // kqueue: one descriptor per file, see D140
		}
		t.Run(string(be), func(t *testing.T) {
			dir := t.TempDir()
			w, c := startWatcher(t, dir, Options{Backend: be})
			const n = 500
			for i := range n {
				mkfile(t, filepath.Join(w.Root(), fmt.Sprintf("d%d", i%10), fmt.Sprintf("f%d.php", i)), "x")
			}
			c.waitFor(t, "all files", func(ch []Change, _ []string) bool {
				seen := map[string]bool{}
				for _, x := range ch {
					if x.Kind == Created && !x.Dir {
						seen[x.Path] = true
					}
				}
				return len(seen) == n
			})
		})
	}
}

// Performance: a project with 20,000 files. Logs startup time and memory
// of the snapshot; the bounds are generous so slow CI machines pass.
func TestLargeTreeStartup(t *testing.T) {
	if testing.Short() {
		t.Skip("short")
	}
	dir := t.TempDir()
	const files = 20000
	made := map[string]bool{}
	for i := range files {
		d := filepath.Join(dir, "src", fmt.Sprintf("m%02d", i%40), fmt.Sprintf("p%03d", i/40%25))
		if !made[d] {
			if err := os.MkdirAll(d, 0o755); err != nil {
				t.Fatal(err)
			}
			made[d] = true
		}
		if err := os.WriteFile(filepath.Join(d, fmt.Sprintf("File%05d.php", i)), []byte("<?php\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for _, be := range backends() {
		if be == FSNotify && runtime.GOOS == "darwin" {
			continue // kqueue would open 20,000 descriptors
		}
		t.Run(string(be), func(t *testing.T) {
			runtime.GC()
			var before, after runtime.MemStats
			runtime.ReadMemStats(&before)
			w, c := startWatcher(t, dir, Options{Backend: be})
			runtime.GC()
			runtime.ReadMemStats(&after)
			st := w.Stats()
			if st.Files != files {
				t.Fatalf("files %d, want %d", st.Files, files)
			}
			heap := int64(after.HeapAlloc) - int64(before.HeapAlloc)
			t.Logf("%s: %d files, %d dirs, startup %v, heap %.1f MiB", be, st.Files, st.Dirs, st.Startup, float64(heap)/(1<<20))
			if st.Startup > 30*time.Second {
				t.Fatalf("startup too slow: %v", st.Startup)
			}
			if heap > 64<<20 {
				t.Fatalf("snapshot too large: %d bytes", heap)
			}
			target := filepath.Join(w.Root(), "src", "m07", "p003", "File00127.php")
			mkfile(t, target, "<?php // changed\n")
			c.waitFor(t, "change in large tree", func(ch []Change, _ []string) bool { return has(ch, Modified, target) })
		})
	}
}

func TestPruneRootsAndExcluded(t *testing.T) {
	sep := string(filepath.Separator)
	got := pruneRoots(map[string]bool{"a": true, "a" + sep + "b": true, "c": true})
	if len(got) != 2 || !got["a"] || !got["c"] {
		t.Fatalf("prune %v", got)
	}
	if got := pruneRoots(map[string]bool{"": true, "a": true}); len(got) != 1 || !got[""] {
		t.Fatalf("prune all %v", got)
	}
	w := &Watcher{excNames: map[string]bool{"node_modules": true}, excPaths: []string{filepath.FromSlash("var/cache")}}
	for rel, want := range map[string]bool{
		"src/a.php":                 false,
		"node_modules":              true,
		"a/node_modules/b/c.js":     true,
		"var/cache":                 true,
		"var/cache/x":               true,
		"var/cachex":                false,
		"sub/.git":                  true,
		"src/.a.php.tiegel-1234":    true,
		"src/.hidden":               false,
		"src/node_modules_like.php": false,
	} {
		if got := w.excluded(filepath.FromSlash(rel)); got != want {
			t.Errorf("excluded(%s) = %v", rel, got)
		}
	}
	w.root = filepath.FromSlash("/Proj/Root")
	for in, want := range map[string]string{"/Proj/Root/a/b": "a/b", "/proj/root/a": "a", "/Proj/Root": "", "/Proj/Rootx/a": "-", "/other": "-"} {
		got, ok := w.rel(filepath.FromSlash(in))
		if !ok {
			got = "-"
		}
		if got != filepath.FromSlash(want) {
			t.Errorf("rel(%s) = %q, want %q", in, got, want)
		}
	}
	for g, want := range map[string]bool{"": true, "HEAD": true, "index": true, "index.lock": false, "refs/heads/x": true, "objects/ab": false, "logs/HEAD": false, "MERGE_HEAD": true} {
		if isGitMeta(g) != want {
			t.Errorf("isGitMeta(%q) != %v", g, want)
		}
	}
}

// Without file IDs (Windows) directory renames are paired through the
// files renamed along with them.
func TestDirRenameWithoutFileIDs(t *testing.T) {
	sep := string(filepath.Separator)
	j := func(s string) string { return strings.ReplaceAll(s, "/", sep) }
	w := &Watcher{root: filepath.FromSlash("/r")}
	file := entry{size: 3, mtime: 7}
	file2 := entry{size: 4, mtime: 7}
	dir := entry{dir: true, mtime: 1}
	oldE := map[string]entry{
		"new": dir, j("new/deep"): dir, j("new/deep/e.php"): file, j("new/f.php"): file2,
		"empty": dir, "a": dir, "b": dir,
	}
	newE := map[string]entry{
		"moved": dir, j("moved/deep"): dir, j("moved/deep/e.php"): file, j("moved/f.php"): file2,
		"empty2": dir, "c": dir,
	}
	ch := w.diff(oldE, newE)
	p := func(rel string) string { return w.abs(j(rel)) }
	for from, to := range map[string]string{"new": "moved", "new/deep": "moved/deep", "new/deep/e.php": "moved/deep/e.php", "new/f.php": "moved/f.php"} {
		if !hasRename(ch, p(from), p(to)) {
			t.Errorf("no rename %s → %s in %+v", from, to, ch)
		}
	}
	// Empty directories without content to compare stay created/deleted.
	for _, rel := range []string{"empty2", "c"} {
		if !has(ch, Created, p(rel)) {
			t.Errorf("%s not created: %+v", rel, ch)
		}
	}
	for _, rel := range []string{"empty", "a", "b"} {
		if !has(ch, Deleted, p(rel)) {
			t.Errorf("%s not deleted: %+v", rel, ch)
		}
	}
}
