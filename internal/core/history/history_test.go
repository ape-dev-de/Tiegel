package history

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

type fakeClock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *fakeClock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *fakeClock) advance(d time.Duration) {
	c.mu.Lock()
	c.t = c.t.Add(d)
	c.mu.Unlock()
}

func newStore(t *testing.T) (*Store, *fakeClock) {
	t.Helper()
	clk := &fakeClock{t: time.Date(2026, 11, 16, 12, 0, 0, 0, time.UTC)}
	s, err := Open(filepath.Join(t.TempDir(), "history"), WithClock(clk.now))
	if err != nil {
		t.Fatal(err)
	}
	return s, clk
}

func countObjects(t *testing.T, s *Store) int {
	t.Helper()
	n := 0
	_ = filepath.WalkDir(filepath.Join(s.Dir(), "objects"), func(_ string, d os.DirEntry, _ error) error {
		if d != nil && !d.IsDir() {
			n++
		}
		return nil
	})
	return n
}

func TestSaveListContentDedup(t *testing.T) {
	s, clk := newStore(t)
	path := filepath.Join(t.TempDir(), "a.php")

	r1, created, err := s.Save(path, []byte("<?php echo 1;"), "manual")
	if err != nil || !created || r1.ID != 1 || r1.Label != "manual" || r1.Size != 13 {
		t.Fatalf("r1=%+v created=%v err=%v", r1, created, err)
	}
	clk.advance(time.Second)
	again, created, err := s.Save(path, []byte("<?php echo 1;"), "x")
	if err != nil || created || again.ID != 1 {
		t.Fatalf("identical content must not create a revision: %+v %v %v", again, created, err)
	}
	r2, created, _ := s.Save(path, []byte("<?php echo 2;"), "")
	if !created || r2.ID != 2 || !r2.Time.After(r1.Time) {
		t.Fatalf("r2=%+v", r2)
	}
	clk.advance(time.Second)
	r3, _, _ := s.Save(path, []byte("<?php echo 1;"), "") // back to v1 content
	if r3.ID != 3 || r3.Hash != r1.Hash {
		t.Fatalf("r3=%+v", r3)
	}
	other := filepath.Join(filepath.Dir(path), "b.php")
	if _, _, err := s.Save(other, []byte("<?php echo 1;"), ""); err != nil {
		t.Fatal(err)
	}
	if n := countObjects(t, s); n != 2 {
		t.Fatalf("objects = %d, want 2 (deduplicated)", n)
	}

	revs, err := s.List(path)
	if err != nil || len(revs) != 3 || revs[0].ID != 3 || revs[2].ID != 1 {
		t.Fatalf("list = %+v %v", revs, err)
	}
	data, err := s.Content(revs[1])
	if err != nil || string(data) != "<?php echo 2;" {
		t.Fatalf("content = %q %v", data, err)
	}
	files, err := s.Files()
	if err != nil || len(files) != 2 || !strings.HasSuffix(files[0], "a.php") {
		t.Fatalf("files = %v %v", files, err)
	}
	if revs, _ := s.List(filepath.Join(filepath.Dir(path), "none.php")); len(revs) != 0 {
		t.Fatal("unknown file has history")
	}
}

func TestPathsAreNormalised(t *testing.T) {
	s, _ := newStore(t)
	dir := t.TempDir()
	_, _, _ = s.Save(filepath.Join(dir, "x", "..", "f.php"), []byte("a"), "")
	revs, _ := s.List(filepath.Join(dir, "f.php"))
	if len(revs) != 1 || revs[0].Path != filepath.Join(dir, "f.php") {
		t.Fatalf("revs = %+v", revs)
	}
}

func TestRestore(t *testing.T) {
	s, clk := newStore(t)
	path := filepath.Join(t.TempDir(), "a.php")
	for _, c := range []string{"v1", "v2", "v3"} {
		if err := os.WriteFile(path, []byte(c), 0o644); err != nil {
			t.Fatal(err)
		}
		if _, _, err := s.Save(path, []byte(c), ""); err != nil {
			t.Fatal(err)
		}
		clk.advance(time.Minute)
	}
	latest, err := s.Restore(path, 1)
	if err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(path)
	if string(data) != "v1" {
		t.Fatalf("file = %q", data)
	}
	if latest.ID != 4 || latest.Label != "restore #1" {
		t.Fatalf("latest = %+v", latest)
	}
	if _, err := s.Restore(path, 99); !errors.Is(err, ErrNotFound) {
		t.Fatalf("restore missing: %v", err)
	}
	if _, err := s.Get(path, 99); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
	if r, err := s.Get(path, 2); err != nil || r.ID != 2 {
		t.Fatal(err)
	}
}

func TestContentErrors(t *testing.T) {
	s, _ := newStore(t)
	if _, err := s.Content(Revision{Hash: "short"}); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
	missing := Revision{Hash: strings.Repeat("ab", 32)}
	if _, err := s.Content(missing); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
	// not gzip
	_ = os.MkdirAll(filepath.Dir(s.objectPath(missing.Hash)), 0o700)
	_ = os.WriteFile(s.objectPath(missing.Hash), []byte("garbage"), 0o600)
	if _, err := s.Content(missing); err == nil {
		t.Fatal("expected gzip error")
	}
	// valid gzip, wrong hash
	path := filepath.Join(t.TempDir(), "f")
	rev, _, _ := s.Save(path, []byte("hello"), "")
	data, _ := os.ReadFile(s.objectPath(rev.Hash))
	_ = os.WriteFile(s.objectPath(missing.Hash), data, 0o600)
	if _, err := s.Content(missing); err == nil || !strings.Contains(err.Error(), "corrupt") {
		t.Fatalf("expected corrupt error, got %v", err)
	}
	// truncated gzip
	_ = os.WriteFile(s.objectPath(missing.Hash), data[:len(data)-6], 0o600)
	if _, err := s.Content(missing); err == nil {
		t.Fatal("expected read error")
	}
	// object is a directory
	_ = os.Remove(s.objectPath(missing.Hash))
	_ = os.MkdirAll(s.objectPath(missing.Hash), 0o700)
	if _, err := s.Content(missing); err == nil {
		t.Fatal("expected error for directory object")
	}
	if _, err := s.Restore(path, 1); err != nil {
		t.Fatal(err)
	}
	// Restore of a revision whose object vanished.
	_ = os.Remove(s.objectPath(rev.Hash))
	if _, err := s.Restore(path, 1); !errors.Is(err, ErrNotFound) {
		t.Fatalf("restore without object: %v", err)
	}
}

func TestDamagedIndexLinesAreSkipped(t *testing.T) {
	s, _ := newStore(t)
	path := filepath.Join(t.TempDir(), "f")
	_, _, _ = s.Save(path, []byte("1"), "")
	idx := s.indexPath(path)
	f, _ := os.OpenFile(idx, os.O_APPEND|os.O_WRONLY, 0o600)
	_, _ = f.WriteString("{broken\n")
	_ = f.Close()
	_, _, _ = s.Save(path, []byte("2"), "")
	revs, err := s.List(path)
	if err != nil || len(revs) != 2 || revs[0].ID != 2 {
		t.Fatalf("revs = %+v %v", revs, err)
	}
}

func TestPrune(t *testing.T) {
	s, clk := newStore(t)
	dir := t.TempDir()
	a, b := filepath.Join(dir, "a"), filepath.Join(dir, "b")
	for i := 0; i < 5; i++ {
		_, _, _ = s.Save(a, []byte{byte('0' + i)}, "")
		clk.advance(24 * time.Hour)
	}
	_, _, _ = s.Save(b, []byte("old"), "")
	clk.advance(10 * 24 * time.Hour)

	st, err := s.Prune(Policy{MaxPerFile: 3})
	if err != nil || st.Revisions != 2 || st.Objects != 2 {
		t.Fatalf("prune count: %+v %v", st, err)
	}
	revs, _ := s.List(a)
	if len(revs) != 3 || revs[2].ID != 3 {
		t.Fatalf("revs = %+v", revs)
	}
	// IDs keep increasing after pruning
	r, _, _ := s.Save(a, []byte("new"), "")
	if r.ID != 6 {
		t.Fatalf("id after prune = %d", r.ID)
	}

	st, err = s.Prune(Policy{MaxAge: 7 * 24 * time.Hour})
	if err != nil || st.Revisions != 4 {
		t.Fatalf("prune age: %+v %v", st, err)
	}
	if revs, _ := s.List(b); len(revs) != 0 {
		t.Fatal("old file history not pruned")
	}
	files, _ := s.Files()
	if len(files) != 1 || files[0] != a {
		t.Fatalf("files = %v", files)
	}
	if n := countObjects(t, s); n != 1 {
		t.Fatalf("objects = %d", n)
	}
	if st, _ := s.Prune(Policy{}); st.Revisions != 0 || st.Objects != 0 {
		t.Fatalf("no-op prune: %+v", st)
	}
}

func TestErrors(t *testing.T) {
	file := filepath.Join(t.TempDir(), "file")
	_ = os.WriteFile(file, nil, 0o600)
	if _, err := Open(file); err == nil {
		t.Fatal("open on file must fail")
	}
	s, _ := newStore(t)
	path := filepath.Join(t.TempDir(), "f")
	// index path is a directory -> read error
	_ = os.MkdirAll(s.indexPath(path), 0o700)
	if _, _, err := s.Save(path, []byte("x"), ""); err == nil {
		t.Fatal("expected index read error")
	}
	if _, err := s.List(path); err == nil {
		t.Fatal("expected list error")
	}
	if _, err := s.Get(path, 1); err == nil {
		t.Fatal("expected get error")
	}
	if _, err := s.Files(); err == nil {
		t.Fatal("expected files error")
	}
	if _, err := s.Prune(Policy{}); err == nil {
		t.Fatal("expected prune error")
	}
	_ = os.RemoveAll(filepath.Join(s.Dir(), "index"))
	if _, err := s.Files(); err == nil {
		t.Fatal("expected error without index dir")
	}
	if _, err := s.Prune(Policy{}); err == nil {
		t.Fatal("expected prune error without index dir")
	}
}

func TestSaveWriteErrors(t *testing.T) {
	s, _ := newStore(t)
	path := filepath.Join(t.TempDir(), "f")
	// objects dir replaced by a file -> object write fails
	objs := filepath.Join(s.Dir(), "objects")
	_ = os.RemoveAll(objs)
	_ = os.WriteFile(objs, nil, 0o600)
	if _, _, err := s.Save(path, []byte("x"), ""); err == nil {
		t.Fatal("expected object write error")
	}
	_ = os.Remove(objs)
	_ = os.MkdirAll(objs, 0o700)
	// index dir missing -> append fails
	_ = os.RemoveAll(filepath.Join(s.Dir(), "index"))
	if _, _, err := s.Save(path, []byte("x"), ""); err == nil {
		t.Fatal("expected index append error")
	}
	// prune with objects dir replaced by a file: walk fails
	_ = os.MkdirAll(filepath.Join(s.Dir(), "index"), 0o700)
	_ = os.RemoveAll(objs)
	if _, err := s.Prune(Policy{}); err == nil {
		t.Fatal("expected walk error")
	}
	// restore target not writable (parent is a file)
	_ = os.MkdirAll(objs, 0o700)
	blocker := filepath.Join(t.TempDir(), "blocker")
	_ = os.WriteFile(blocker, nil, 0o600)
	target := filepath.Join(blocker, "f.php")
	if _, _, err := s.Save(target, []byte("x"), ""); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Restore(target, 1); err == nil {
		t.Fatal("expected restore write error")
	}
}

func TestConcurrentSaves(t *testing.T) {
	s, _ := newStore(t)
	path := filepath.Join(t.TempDir(), "f")
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			for j := 0; j < 10; j++ {
				if _, _, err := s.Save(path, []byte{byte(i), byte(j)}, ""); err != nil {
					t.Error(err)
				}
			}
		}(i)
	}
	wg.Wait()
	revs, _ := s.List(path)
	seen := map[uint64]bool{}
	for _, r := range revs {
		if seen[r.ID] {
			t.Fatalf("duplicate id %d", r.ID)
		}
		seen[r.ID] = true
	}
	if len(revs) != 80 {
		t.Fatalf("revs = %d", len(revs))
	}
}
