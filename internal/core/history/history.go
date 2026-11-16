// Package history implements Local History: every save of a file is kept
// as a revision that can be listed and restored, independent of Git.
//
// Storage layout below the store directory (usually
// settings.ProjectDir(...)/history):
//
//	objects/<first 2 hex>/<sha256>.gz   gzip-compressed content, content-addressed
//	index/<sha256 of file path>.jsonl   one JSON line per revision, oldest first
//
// Content is deduplicated: identical content is stored once no matter how
// many revisions or files refer to it, and saving content identical to the
// latest revision of a file creates no new revision. Prune removes old
// revisions by age and count and garbage-collects unreferenced objects.
package history

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/ape-dev-de/tiegel/internal/core/internal/atomicfile"
)

// ErrNotFound is returned when a file or revision has no history entry.
var ErrNotFound = errors.New("history: revision not found")

// Revision describes one saved version of a file.
type Revision struct {
	// ID is unique per file and increases with every revision.
	ID    uint64    `json:"id"`
	Path  string    `json:"path"`
	Time  time.Time `json:"time"`
	Hash  string    `json:"hash"` // hex SHA-256 of the content
	Size  int64     `json:"size"`
	Label string    `json:"label,omitempty"` // e.g. "autosave: focusLost", "restore #3"
}

// Option configures a Store.
type Option func(*Store)

// WithClock sets the time source (for tests).
func WithClock(now func() time.Time) Option { return func(s *Store) { s.now = now } }

// Store is a Local History store. It is safe for concurrent use within one
// process.
type Store struct {
	dir string
	now func() time.Time

	mu sync.Mutex
}

// Open opens (creating if needed) a history store in dir.
func Open(dir string, opts ...Option) (*Store, error) {
	s := &Store{dir: dir, now: time.Now}
	for _, o := range opts {
		o(s)
	}
	for _, sub := range []string{"objects", "index"} {
		if err := os.MkdirAll(filepath.Join(dir, sub), 0o700); err != nil {
			return nil, fmt.Errorf("history: open: %w", err)
		}
	}
	return s, nil
}

// Dir returns the store directory.
func (s *Store) Dir() string { return s.dir }

func normPath(path string) (string, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("history: path: %w", err)
	}
	return filepath.Clean(abs), nil
}

func (s *Store) indexPath(path string) string {
	sum := sha256.Sum256([]byte(path))
	return filepath.Join(s.dir, "index", hex.EncodeToString(sum[:])+".jsonl")
}

func (s *Store) objectPath(hash string) string {
	return filepath.Join(s.dir, "objects", hash[:2], hash+".gz")
}

// readIndex returns the revisions of an index file, oldest first. Damaged
// lines are skipped.
func readIndex(file string) ([]Revision, error) {
	f, err := os.Open(file)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("history: read index: %w", err)
	}
	defer f.Close()
	var revs []Revision
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64*1024), 1<<20)
	for sc.Scan() {
		var r Revision
		if json.Unmarshal(sc.Bytes(), &r) == nil && len(r.Hash) == 64 {
			revs = append(revs, r)
		}
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("history: read index: %w", err)
	}
	return revs, nil
}

func encodeIndex(revs []Revision) []byte {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	for _, r := range revs {
		_ = enc.Encode(r) // Revision always encodes
	}
	return buf.Bytes()
}

func (s *Store) writeObject(hash string, content []byte) error {
	p := s.objectPath(hash)
	if _, err := os.Stat(p); err == nil {
		return nil // deduplicated
	}
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	_, _ = zw.Write(content) // writes to a bytes.Buffer cannot fail
	_ = zw.Close()
	if err := atomicfile.Write(p, buf.Bytes(), 0o600); err != nil {
		return fmt.Errorf("history: write object: %w", err)
	}
	return nil
}

// Save records content as a new revision of path (made absolute). If it
// equals the latest revision, no revision is added and created is false.
func (s *Store) Save(path string, content []byte, label string) (rev Revision, created bool, err error) {
	path, err = normPath(path)
	if err != nil {
		return Revision{}, false, err
	}
	sum := sha256.Sum256(content)
	hash := hex.EncodeToString(sum[:])

	s.mu.Lock()
	defer s.mu.Unlock()
	idx := s.indexPath(path)
	revs, err := readIndex(idx)
	if err != nil {
		return Revision{}, false, err
	}
	var next uint64 = 1
	if n := len(revs); n > 0 {
		last := revs[n-1]
		if last.Hash == hash {
			return last, false, nil
		}
		next = last.ID + 1
	}
	if err := s.writeObject(hash, content); err != nil {
		return Revision{}, false, err
	}
	rev = Revision{ID: next, Path: path, Time: s.now().UTC(), Hash: hash, Size: int64(len(content)), Label: label}
	f, err := os.OpenFile(idx, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return Revision{}, false, fmt.Errorf("history: append index: %w", err)
	}
	_, werr := f.Write(encodeIndex([]Revision{rev}))
	serr := f.Sync()
	cerr := f.Close()
	if err := errors.Join(werr, serr, cerr); err != nil {
		return Revision{}, false, fmt.Errorf("history: append index: %w", err)
	}
	return rev, true, nil
}

// List returns the revisions of path, newest first.
func (s *Store) List(path string) ([]Revision, error) {
	path, err := normPath(path)
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	revs, err := readIndex(s.indexPath(path))
	if err != nil {
		return nil, err
	}
	for i, j := 0, len(revs)-1; i < j; i, j = i+1, j-1 {
		revs[i], revs[j] = revs[j], revs[i]
	}
	return revs, nil
}

// Files returns all paths with history, sorted.
func (s *Store) Files() ([]string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	entries, err := os.ReadDir(filepath.Join(s.dir, "index"))
	if err != nil {
		return nil, fmt.Errorf("history: list files: %w", err)
	}
	var files []string
	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), ".jsonl") {
			continue
		}
		revs, err := readIndex(filepath.Join(s.dir, "index", e.Name()))
		if err != nil {
			return nil, err
		}
		if len(revs) > 0 {
			files = append(files, revs[0].Path)
		}
	}
	sort.Strings(files)
	return files, nil
}

// Get returns the revision id of path.
func (s *Store) Get(path string, id uint64) (Revision, error) {
	revs, err := s.List(path)
	if err != nil {
		return Revision{}, err
	}
	for _, r := range revs {
		if r.ID == id {
			return r, nil
		}
	}
	return Revision{}, fmt.Errorf("%w: %s #%d", ErrNotFound, path, id)
}

// Content returns the stored content of rev.
func (s *Store) Content(rev Revision) ([]byte, error) {
	if len(rev.Hash) != 64 {
		return nil, fmt.Errorf("%w: invalid hash %q", ErrNotFound, rev.Hash)
	}
	f, err := os.Open(s.objectPath(rev.Hash))
	if errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("%w: object %s", ErrNotFound, rev.Hash)
	}
	if err != nil {
		return nil, fmt.Errorf("history: read object: %w", err)
	}
	defer f.Close()
	zr, err := gzip.NewReader(f)
	if err != nil {
		return nil, fmt.Errorf("history: read object %s: %w", rev.Hash, err)
	}
	data, err := io.ReadAll(zr)
	if err != nil {
		return nil, fmt.Errorf("history: read object %s: %w", rev.Hash, err)
	}
	if sum := sha256.Sum256(data); hex.EncodeToString(sum[:]) != rev.Hash {
		return nil, fmt.Errorf("history: object %s is corrupt", rev.Hash)
	}
	return data, nil
}

// Restore writes revision id back to path on disk (atomically) and records
// the restored content as a new revision labelled "restore #<id>". It
// returns the new (or, if the content equals the latest revision, the
// existing) latest revision.
func (s *Store) Restore(path string, id uint64) (Revision, error) {
	rev, err := s.Get(path, id)
	if err != nil {
		return Revision{}, err
	}
	data, err := s.Content(rev)
	if err != nil {
		return Revision{}, err
	}
	if err := atomicfile.Write(rev.Path, data, 0o644); err != nil {
		return Revision{}, fmt.Errorf("history: restore: %w", err)
	}
	latest, _, err := s.Save(rev.Path, data, fmt.Sprintf("restore #%d", id))
	return latest, err
}

// Policy controls Prune. Zero fields mean "no limit".
type Policy struct {
	// MaxAge removes revisions older than this.
	MaxAge time.Duration
	// MaxPerFile keeps at most this many newest revisions per file.
	MaxPerFile int
}

// PruneStats reports what Prune removed.
type PruneStats struct {
	Revisions int
	Objects   int
}

// Prune removes revisions according to p and deletes content objects that
// are no longer referenced.
func (s *Store) Prune(p Policy) (PruneStats, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var st PruneStats
	idxDir := filepath.Join(s.dir, "index")
	entries, err := os.ReadDir(idxDir)
	if err != nil {
		return st, fmt.Errorf("history: prune: %w", err)
	}
	now := s.now()
	live := map[string]bool{}
	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), ".jsonl") {
			continue
		}
		file := filepath.Join(idxDir, e.Name())
		revs, err := readIndex(file)
		if err != nil {
			return st, err
		}
		keep := revs
		if p.MaxPerFile > 0 && len(keep) > p.MaxPerFile {
			keep = keep[len(keep)-p.MaxPerFile:]
		}
		if p.MaxAge > 0 {
			cut := 0
			for cut < len(keep) && now.Sub(keep[cut].Time) > p.MaxAge {
				cut++
			}
			keep = keep[cut:]
		}
		st.Revisions += len(revs) - len(keep)
		switch {
		case len(keep) == 0:
			if err := os.Remove(file); err != nil {
				return st, fmt.Errorf("history: prune: %w", err)
			}
		case len(keep) < len(revs):
			if err := atomicfile.Write(file, encodeIndex(keep), 0o600); err != nil {
				return st, fmt.Errorf("history: prune: %w", err)
			}
		}
		for _, r := range keep {
			live[r.Hash] = true
		}
	}

	objDir := filepath.Join(s.dir, "objects")
	err = filepath.WalkDir(objDir, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(d.Name(), ".gz") {
			return nil
		}
		if !live[strings.TrimSuffix(d.Name(), ".gz")] {
			if err := os.Remove(p); err != nil {
				return err
			}
			st.Objects++
		}
		return nil
	})
	if err != nil {
		return st, fmt.Errorf("history: prune objects: %w", err)
	}
	return st, nil
}

// Move transfers the history of oldPath to newPath, e.g. after a rename:
// the revisions of oldPath itself and, if it was a directory, of every file
// below it now belong to the corresponding path under newPath. If the new
// path already has revisions, both lists are merged by time and the IDs of
// the merged file are renumbered. It returns the number of files moved.
func (s *Store) Move(oldPath, newPath string) (int, error) {
	oldPath, err := normPath(oldPath)
	if err != nil {
		return 0, err
	}
	newPath, err = normPath(newPath)
	if err != nil {
		return 0, err
	}
	if oldPath == newPath {
		return 0, nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	idxDir := filepath.Join(s.dir, "index")
	entries, err := os.ReadDir(idxDir)
	if err != nil {
		return 0, fmt.Errorf("history: move: %w", err)
	}
	prefix := oldPath + string(filepath.Separator)
	moved := 0
	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), ".jsonl") {
			continue
		}
		file := filepath.Join(idxDir, e.Name())
		revs, err := readIndex(file)
		if err != nil {
			return moved, err
		}
		if len(revs) == 0 {
			continue
		}
		from := revs[0].Path
		var to string
		switch {
		case from == oldPath:
			to = newPath
		case strings.HasPrefix(from, prefix):
			to = newPath + string(filepath.Separator) + from[len(prefix):]
		default:
			continue
		}
		target := s.indexPath(to)
		existing, err := readIndex(target)
		if err != nil {
			return moved, err
		}
		merged := append(existing, revs...)
		sort.SliceStable(merged, func(i, j int) bool { return merged[i].Time.Before(merged[j].Time) })
		for i := range merged {
			merged[i].Path = to
			if len(existing) > 0 {
				merged[i].ID = uint64(i + 1)
			}
		}
		if err := atomicfile.Write(target, encodeIndex(merged), 0o600); err != nil {
			return moved, fmt.Errorf("history: move: %w", err)
		}
		if err := os.Remove(file); err != nil {
			return moved, fmt.Errorf("history: move: %w", err)
		}
		moved++
	}
	return moved, nil
}
