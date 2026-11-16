package index

import (
	"context"
	"errors"
	"fmt"
	"hash/fnv"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"
)

// Options configure an Index.
type Options struct {
	// Root is the project directory (absolute).
	Root string
	// SkipLibraries leaves vendor/ out. By default it is indexed (also when
	// .gitignore lists it) and its files are marked Library.
	SkipLibraries bool
	// IncludeIgnored indexes files matched by .gitignore.
	IncludeIgnored bool
	// Exclude lists additional directories or files to skip: path.Match
	// patterns against the project relative, slash separated path or the
	// base name (e.g. "var/cache", "tests/fixtures", "*.tpl.php").
	Exclude []string
	// Extensions are the file extensions to index without dot (default
	// "php").
	Extensions []string
	// Workers is the number of parallel parsers (default GOMAXPROCS).
	Workers int
	// MaxFileSize skips larger files (default 4 MiB; generated code).
	MaxFileSize int64
}

func (o *Options) normalize() {
	if len(o.Extensions) == 0 {
		o.Extensions = []string{"php"}
	}
	if o.Workers <= 0 {
		o.Workers = runtime.GOMAXPROCS(0)
	}
	if o.MaxFileSize <= 0 {
		o.MaxFileSize = 4 << 20
	}
}

// Progress reports the state of a Sync.
type Progress struct {
	// Phase is "scanning", "indexing" or "ready".
	Phase string `json:"phase"`
	// Done and Total count the files to parse in this run.
	Done  int `json:"done"`
	Total int `json:"total"`
}

// Stats summarise the index content.
type Stats struct {
	Files     int `json:"files"`
	Libraries int `json:"libraryFiles"`
	Classes   int `json:"classes"`
	Functions int `json:"functions"`
	Constants int `json:"constants"`
	Members   int `json:"members"`
}

// SyncResult describes the changes of one Sync.
type SyncResult struct {
	// Changed lists added or re-indexed files, Removed dropped ones
	// (absolute paths).
	Changed  []string
	Removed  []string
	Scanned  int
	Duration time.Duration
}

type classEntry struct {
	f *File
	c *Class
}

type funcEntry struct {
	f  *File
	fn *Function
}

type constEntry struct {
	f *File
	c *Constant
}

// Index is a project-wide PHP symbol index. All methods are safe for
// concurrent use; queries take a read lock and never wait for parsing.
type Index struct {
	opts Options

	mu         sync.RWMutex
	files      map[string]*File
	classes    map[string][]classEntry // folded FQN
	subtypes   map[string][]classEntry // folded parent FQN -> direct subtypes (extends/implements)
	traitUsers map[string][]classEntry // folded trait FQN -> users
	funcs      map[string][]funcEntry
	consts     map[string][]constEntry
	gen        uint64 // increases with every change

	symMu    sync.Mutex
	syms     []symEntry
	symTypes int // syms[:symTypes] are the class-like entries
	symGen   uint64

	syncMu sync.Mutex // one Sync at a time

	poolMu  sync.Mutex
	parsers []*parser
	closed  bool
}

// New creates an empty index for opts.Root.
func New(opts Options) *Index {
	opts.normalize()
	return &Index{
		opts:       opts,
		files:      map[string]*File{},
		classes:    map[string][]classEntry{},
		subtypes:   map[string][]classEntry{},
		traitUsers: map[string][]classEntry{},
		funcs:      map[string][]funcEntry{},
		consts:     map[string][]constEntry{},
	}
}

// Options returns the (normalized) options.
func (ix *Index) Options() Options { return ix.opts }

// Close releases the parsers. The index stays queryable.
func (ix *Index) Close() {
	ix.poolMu.Lock()
	defer ix.poolMu.Unlock()
	for _, p := range ix.parsers {
		p.close()
	}
	ix.parsers, ix.closed = nil, true
}

func (ix *Index) getParser() (*parser, error) {
	ix.poolMu.Lock()
	if ix.closed {
		ix.poolMu.Unlock()
		return nil, errors.New("index: closed")
	}
	if n := len(ix.parsers); n > 0 {
		p := ix.parsers[n-1]
		ix.parsers = ix.parsers[:n-1]
		ix.poolMu.Unlock()
		return p, nil
	}
	ix.poolMu.Unlock()
	return newParser()
}

func (ix *Index) putParser(p *parser) {
	ix.poolMu.Lock()
	defer ix.poolMu.Unlock()
	if ix.closed || len(ix.parsers) >= ix.opts.Workers {
		p.close()
		return
	}
	ix.parsers = append(ix.parsers, p)
}

// Generation increases with every change of the index content.
func (ix *Index) Generation() uint64 {
	ix.mu.RLock()
	defer ix.mu.RUnlock()
	return ix.gen
}

// Stats counts the indexed symbols.
func (ix *Index) Stats() Stats {
	ix.mu.RLock()
	defer ix.mu.RUnlock()
	var s Stats
	for _, f := range ix.files {
		s.Files++
		if f.Library {
			s.Libraries++
		}
		s.Classes += len(f.Classes)
		s.Functions += len(f.Funcs)
		s.Constants += len(f.Consts)
		for i := range f.Classes {
			s.Members += len(f.Classes[i].Members)
		}
	}
	return s
}

// File returns the indexed data of path (absolute).
func (ix *Index) File(path string) (*File, bool) {
	ix.mu.RLock()
	defer ix.mu.RUnlock()
	f, ok := ix.files[path]
	return f, ok
}

// Files returns all indexed files sorted by path.
func (ix *Index) Files() []*File {
	ix.mu.RLock()
	out := make([]*File, 0, len(ix.files))
	for _, f := range ix.files {
		out = append(out, f)
	}
	ix.mu.RUnlock()
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out
}

// Accepts reports whether path has an indexed extension.
func (ix *Index) Accepts(path string) bool {
	ext := strings.TrimPrefix(filepath.Ext(path), ".")
	for _, e := range ix.opts.Extensions {
		if strings.EqualFold(e, ext) {
			return true
		}
	}
	return false
}

// IsLibrary reports whether path lies in a library directory (vendor/).
func (ix *Index) IsLibrary(path string) bool {
	rel, err := filepath.Rel(ix.opts.Root, path)
	if err != nil {
		return false
	}
	for _, seg := range strings.Split(filepath.ToSlash(rel), "/") {
		if libraryDirs[seg] {
			return true
		}
	}
	return false
}

// put replaces the data of f.Path (caller holds the write lock).
func (ix *Index) putLocked(f *File) {
	if old, ok := ix.files[f.Path]; ok {
		ix.removeLocked(old)
	}
	ix.files[f.Path] = f
	for i := range f.Classes {
		c := &f.Classes[i]
		e := classEntry{f: f, c: c}
		key := fold(c.FQN)
		ix.classes[key] = append(ix.classes[key], e)
		for _, p := range c.Extends {
			ix.subtypes[fold(p)] = append(ix.subtypes[fold(p)], e)
		}
		for _, p := range c.Implements {
			ix.subtypes[fold(p)] = append(ix.subtypes[fold(p)], e)
		}
		for _, t := range c.Traits {
			ix.traitUsers[fold(t)] = append(ix.traitUsers[fold(t)], e)
		}
	}
	for i := range f.Funcs {
		key := fold(f.Funcs[i].FQN)
		ix.funcs[key] = append(ix.funcs[key], funcEntry{f: f, fn: &f.Funcs[i]})
	}
	for i := range f.Consts {
		key := fold(f.Consts[i].FQN)
		ix.consts[key] = append(ix.consts[key], constEntry{f: f, c: &f.Consts[i]})
	}
	ix.gen++
}

func dropFile[E any](m map[string][]E, key string, f *File, fileOf func(E) *File) {
	list := m[key]
	out := list[:0]
	for _, e := range list {
		if fileOf(e) != f {
			out = append(out, e)
		}
	}
	if len(out) == 0 {
		delete(m, key)
		return
	}
	clear(list[len(out):])
	m[key] = out
}

func (ix *Index) removeLocked(f *File) {
	cf := func(e classEntry) *File { return e.f }
	for i := range f.Classes {
		c := &f.Classes[i]
		dropFile(ix.classes, fold(c.FQN), f, cf)
		for _, p := range c.Extends {
			dropFile(ix.subtypes, fold(p), f, cf)
		}
		for _, p := range c.Implements {
			dropFile(ix.subtypes, fold(p), f, cf)
		}
		for _, t := range c.Traits {
			dropFile(ix.traitUsers, fold(t), f, cf)
		}
	}
	for i := range f.Funcs {
		dropFile(ix.funcs, fold(f.Funcs[i].FQN), f, func(e funcEntry) *File { return e.f })
	}
	for i := range f.Consts {
		dropFile(ix.consts, fold(f.Consts[i].FQN), f, func(e constEntry) *File { return e.f })
	}
	delete(ix.files, f.Path)
	ix.gen++
}

// Remove drops path from the index; it reports whether it was indexed.
func (ix *Index) Remove(path string) bool {
	ix.mu.Lock()
	defer ix.mu.Unlock()
	f, ok := ix.files[path]
	if ok {
		ix.removeLocked(f)
	}
	return ok
}

func hashOf(src []byte) uint64 {
	h := fnv.New64a()
	_, _ = h.Write(src)
	return h.Sum64()
}

// UpdateText re-indexes path from src (e.g. the text of an open, edited
// document). The stored disk state (mtime, size) of path is kept so that a
// later Sync does not overwrite the newer text with the file on disk; it
// reports whether the index changed.
func (ix *Index) UpdateText(path string, src []byte) (bool, error) {
	h := hashOf(src)
	ix.mu.RLock()
	old, ok := ix.files[path]
	ix.mu.RUnlock()
	if ok && old.Hash == h {
		return false, nil
	}
	p, err := ix.getParser()
	if err != nil {
		return false, err
	}
	f := p.parse(path, src)
	ix.putParser(p)
	f.Hash, f.Library = h, ix.IsLibrary(path)
	if ok {
		f.ModTime, f.Size = old.ModTime, old.Size
	} else if st, err := os.Stat(path); err == nil {
		f.ModTime, f.Size = st.ModTime().UnixNano(), st.Size()
	}
	ix.mu.Lock()
	ix.putLocked(f)
	ix.mu.Unlock()
	return true, nil
}

// UpdateFile re-indexes path from disk (or removes it if it is gone or no
// longer indexable); it reports whether the index changed.
func (ix *Index) UpdateFile(path string) (bool, error) {
	st, err := os.Stat(path)
	if err != nil || st.IsDir() || !ix.Accepts(path) || ix.excluded(path) {
		return ix.Remove(path), nil
	}
	src, err := os.ReadFile(path)
	if err != nil {
		return ix.Remove(path), nil
	}
	h := hashOf(src)
	ix.mu.Lock()
	if old, ok := ix.files[path]; ok && old.Hash == h {
		old.ModTime, old.Size = st.ModTime().UnixNano(), st.Size()
		ix.mu.Unlock()
		return false, nil
	}
	ix.mu.Unlock()
	p, err := ix.getParser()
	if err != nil {
		return false, err
	}
	f := p.parse(path, src)
	ix.putParser(p)
	f.Hash, f.Library = h, ix.IsLibrary(path)
	f.ModTime, f.Size = st.ModTime().UnixNano(), st.Size()
	ix.mu.Lock()
	ix.putLocked(f)
	ix.mu.Unlock()
	return true, nil
}

// excluded reports whether a single path would be skipped by a scan
// (exclude patterns, .gitignore is not consulted here).
func (ix *Index) excluded(path string) bool {
	rel, err := filepath.Rel(ix.opts.Root, path)
	if err != nil || strings.HasPrefix(rel, "..") {
		return true
	}
	rel = filepath.ToSlash(rel)
	if ix.opts.SkipLibraries && ix.IsLibrary(path) {
		return true
	}
	parts := strings.Split(rel, "/")
	for i := range parts {
		if ix.matchExclude(strings.Join(parts[:i+1], "/"), parts[i]) || alwaysSkipped[parts[i]] {
			return true
		}
	}
	return false
}

type parsed struct {
	f       *File
	path    string
	touched bool // same content, only the disk state changed
	mtime   int64
	size    int64
	err     error
}

// Sync brings the index up to date with the project directory: new and
// changed files (mtime/size, then content hash) are parsed in parallel,
// deleted ones dropped. progress, if not nil, is called from the calling
// goroutine. A cancelled context stops the run; files parsed so far stay
// indexed.
func (ix *Index) Sync(ctx context.Context, progress func(Progress)) (SyncResult, error) {
	if !TreeSitter {
		return SyncResult{}, fmt.Errorf("index: %w", errNoTreeSitter())
	}
	ix.syncMu.Lock()
	defer ix.syncMu.Unlock()
	start := time.Now()
	report := func(p Progress) {
		if progress != nil {
			progress(p)
		}
	}
	report(Progress{Phase: "scanning"})
	found, err := ix.scan(ctx)
	if err != nil {
		return SyncResult{}, err
	}
	var res SyncResult
	res.Scanned = len(found)
	var todo []scanned
	ix.mu.RLock()
	seen := make(map[string]bool, len(found))
	for _, s := range found {
		seen[s.path] = true
		if f, ok := ix.files[s.path]; !ok || f.ModTime != s.mtime || f.Size != s.size {
			todo = append(todo, s)
		}
	}
	var gone []string
	for p := range ix.files {
		if !seen[p] {
			gone = append(gone, p)
		}
	}
	ix.mu.RUnlock()
	if len(gone) > 0 {
		ix.mu.Lock()
		for _, p := range gone {
			if f, ok := ix.files[p]; ok {
				ix.removeLocked(f)
			}
		}
		ix.mu.Unlock()
		sort.Strings(gone)
		res.Removed = gone
	}

	total := len(todo)
	report(Progress{Phase: "indexing", Total: total})
	jobs := make(chan scanned)
	results := make(chan parsed, 64)
	var wg sync.WaitGroup
	workers := min(ix.opts.Workers, max(total, 1))
	var werr error
	var werrOnce sync.Once
	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			p, err := ix.getParser()
			if err != nil {
				werrOnce.Do(func() { werr = err })
				for range jobs { //nolint:revive // drain
				}
				return
			}
			defer ix.putParser(p)
			for s := range jobs {
				results <- ix.parseOne(p, s)
			}
		}()
	}
	go func() {
		defer close(jobs)
		for _, s := range todo {
			select {
			case jobs <- s:
			case <-ctx.Done():
				return
			}
		}
	}()
	go func() { wg.Wait(); close(results) }()

	done := 0
	lastReport := time.Now()
	batch := make([]parsed, 0, 64)
	flush := func() {
		if len(batch) == 0 {
			return
		}
		ix.mu.Lock()
		for _, r := range batch {
			switch {
			case r.err != nil || r.f == nil && !r.touched:
				if f, ok := ix.files[r.path]; ok {
					ix.removeLocked(f)
				}
			case r.touched:
				if f, ok := ix.files[r.path]; ok {
					f.ModTime, f.Size = r.mtime, r.size
				}
			default:
				ix.putLocked(r.f)
				res.Changed = append(res.Changed, r.path)
			}
		}
		ix.mu.Unlock()
		batch = batch[:0]
	}
	for r := range results {
		batch = append(batch, r)
		done++
		if len(batch) == cap(batch) {
			flush()
		}
		if time.Since(lastReport) > 100*time.Millisecond {
			flush()
			report(Progress{Phase: "indexing", Done: done, Total: total})
			lastReport = time.Now()
		}
	}
	flush()
	sort.Strings(res.Changed)
	res.Duration = time.Since(start)
	if werr != nil {
		return res, werr
	}
	if err := ctx.Err(); err != nil {
		return res, err
	}
	report(Progress{Phase: "ready", Done: done, Total: total})
	return res, nil
}

func (ix *Index) parseOne(p *parser, s scanned) parsed {
	src, err := os.ReadFile(s.path)
	if err != nil {
		return parsed{path: s.path, err: err}
	}
	h := hashOf(src)
	ix.mu.RLock()
	old, ok := ix.files[s.path]
	same := ok && old.Hash == h
	ix.mu.RUnlock()
	if same {
		return parsed{path: s.path, touched: true, mtime: s.mtime, size: s.size}
	}
	f := p.parse(s.path, src)
	f.Library, f.Hash, f.ModTime, f.Size = s.library, h, s.mtime, s.size
	return parsed{path: s.path, f: f}
}

// Reset drops the whole index content.
func (ix *Index) Reset() {
	ix.mu.Lock()
	defer ix.mu.Unlock()
	for _, f := range ix.files {
		ix.removeLocked(f)
	}
}

// replaceAll installs files (from the cache) as the index content.
func (ix *Index) replaceAll(files []*File) {
	ix.mu.Lock()
	defer ix.mu.Unlock()
	for _, f := range files {
		ix.putLocked(f)
	}
}
