package api

import (
	"bytes"
	"context"
	"errors"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode/utf8"

	"github.com/ape-dev-de/tiegel/internal/core/gitignore"
	"github.com/ape-dev-de/tiegel/internal/editor/search"
)

// TopicSearchResults carries the batches of a streamed project search
// (ProjectSearchEvent). The last event of a search has Done set and the
// summary.
const TopicSearchResults = "search.results"

// Search scopes of ProjectSearchParams.
const (
	ScopeProject   = "project"
	ScopeDirectory = "directory"
	ScopeOpenFiles = "openFiles"
)

// Defaults of the project search.
const (
	defaultSearchLimit   = 5000
	defaultReplaceLimit  = 100000
	defaultMaxFileSize   = 4 << 20
	binarySniffBytes     = 8000
	maxPreviewBytes      = 400
	previewContextBefore = 120
	streamBatchMatches   = 200
	streamFlushInterval  = 100 * time.Millisecond
)

// ProjectSearchParams are the params of "search.inProject" (Find in Files).
type ProjectSearchParams struct {
	Pattern       string `json:"pattern"`
	Regex         bool   `json:"regex,omitempty"`
	CaseSensitive bool   `json:"caseSensitive,omitempty"`
	WholeWord     bool   `json:"wholeWord,omitempty"`
	// Mask is a comma separated list of file name globs ("*.php,*.inc");
	// a leading "!" excludes ("!*.min.js"). Globs containing "/" match the
	// project-relative path. Empty matches all files.
	Mask string `json:"mask,omitempty"`
	// Scope is "project" (default), "directory" (Dir, relative to the
	// project root) or "openFiles" (open documents only, works without a
	// project). A non-empty Dir implies "directory".
	Scope string `json:"scope,omitempty"`
	Dir   string `json:"dir,omitempty"`
	// Paths restricts the search to these files (scope and mask are
	// ignored), e.g. to replace in a reviewed selection.
	Paths []string `json:"paths,omitempty"`
	// Libraries also searches vendor/ and node_modules/.
	Libraries bool `json:"libraries,omitempty"`
	// Ignored also searches files excluded by .gitignore.
	Ignored bool `json:"ignored,omitempty"`
	// Limit caps the number of matches (default 5000).
	Limit int `json:"limit,omitempty"`
	// MaxFileSize skips larger files (bytes, default 4 MiB). Open
	// documents are always searched.
	MaxFileSize int64 `json:"maxFileSize,omitempty"`
	// Stream returns at once with the search ID; the matches follow as
	// search.results events (subscribe to "search.*" first).
	Stream bool `json:"stream,omitempty"`
	// ID names the search for search.cancel and the events (default: a
	// generated ID).
	ID string `json:"id,omitempty"`
}

// FileMatches are the matches of one file. Open documents are searched in
// their buffer (Open, Version).
type FileMatches struct {
	Path    string        `json:"path"`
	File    string        `json:"file"`
	Open    bool          `json:"open,omitempty"`
	Version uint64        `json:"version,omitempty"`
	Matches []SearchMatch `json:"matches"`
}

// ProjectSearchResult is the result of "search.inProject" and the summary
// of the last search.results event.
type ProjectSearchResult struct {
	ID string `json:"id"`
	// Running is true in the immediate answer of a streamed search.
	Running bool          `json:"running,omitempty"`
	Files   []FileMatches `json:"files"`
	// Matches and FilesMatched count the returned matches and files.
	Matches      int `json:"matches"`
	FilesMatched int `json:"filesMatched"`
	// FilesSearched counts the scanned files; SkippedBinary and
	// SkippedLarge the files left out.
	FilesSearched int   `json:"filesSearched"`
	SkippedBinary int   `json:"skippedBinary,omitempty"`
	SkippedLarge  int   `json:"skippedLarge,omitempty"`
	Limited       bool  `json:"limited,omitempty"`
	Cancelled     bool  `json:"cancelled,omitempty"`
	DurationMs    int64 `json:"durationMs"`
}

// ProjectSearchEvent is the payload of search.results.
type ProjectSearchEvent struct {
	ID    string        `json:"id"`
	Files []FileMatches `json:"files,omitempty"`
	// Done marks the last event of the search; Summary is set then
	// (without files).
	Done    bool                 `json:"done,omitempty"`
	Summary *ProjectSearchResult `json:"summary,omitempty"`
	Error   string               `json:"error,omitempty"`
}

// SearchCancelParams are the params of "search.cancel".
type SearchCancelParams struct {
	ID string `json:"id"`
}

// SearchCancelResult is the result of "search.cancel".
type SearchCancelResult struct {
	ID string `json:"id"`
	// Cancelled is false if no search with this ID was running.
	Cancelled bool `json:"cancelled"`
}

// ProjectReplaceParams are the params of "search.replaceInProject". The
// search part selects the files; every match of a selected file is
// replaced (computed again against the current text when applying).
type ProjectReplaceParams struct {
	ProjectSearchParams
	Replacement string `json:"replacement"`
	// Preview only reports what would change.
	Preview bool `json:"preview,omitempty"`
}

// ReplaceMatch is a match with its replacement text (preview).
type ReplaceMatch struct {
	SearchMatch
	Replacement string `json:"replacement"`
}

// FileReplace reports one file of "search.replaceInProject".
type FileReplace struct {
	Path string `json:"path"`
	File string `json:"file"`
	// Open: the file is an open document; the replacement is one undoable
	// edit of its buffer (not saved).
	Open     bool `json:"open,omitempty"`
	Replaced int  `json:"replaced"`
	// Version is the document version after the edit (open documents).
	Version uint64 `json:"version,omitempty"`
	// Revision is the Local History revision of the written file.
	Revision uint64         `json:"revision,omitempty"`
	Matches  []ReplaceMatch `json:"matches,omitempty"`
	Error    string         `json:"error,omitempty"`
}

// ProjectReplaceResult is the result of "search.replaceInProject".
type ProjectReplaceResult struct {
	Preview       bool          `json:"preview,omitempty"`
	Files         []FileReplace `json:"files"`
	Replaced      int           `json:"replaced"`
	FilesChanged  int           `json:"filesChanged"`
	Failed        int           `json:"failed,omitempty"`
	FilesSearched int           `json:"filesSearched"`
	SkippedBinary int           `json:"skippedBinary,omitempty"`
	SkippedLarge  int           `json:"skippedLarge,omitempty"`
	Limited       bool          `json:"limited,omitempty"`
}

// searchRegistry tracks running project searches by ID.
type searchRegistry struct {
	mu      sync.Mutex
	seq     uint64
	running map[string]context.CancelFunc
}

func (r *searchRegistry) start(ctx context.Context, id string) (string, context.Context, func(), error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.running == nil {
		r.running = map[string]context.CancelFunc{}
	}
	if id == "" {
		r.seq++
		id = "s" + strconv.FormatUint(r.seq, 10)
	}
	if _, dup := r.running[id]; dup {
		return "", nil, nil, Errorf(CodeConflict, "a search with id %q is running", id)
	}
	cctx, cancel := context.WithCancel(ctx)
	r.running[id] = cancel
	done := func() {
		r.mu.Lock()
		delete(r.running, id)
		r.mu.Unlock()
		cancel()
	}
	return id, cctx, done, nil
}

func (r *searchRegistry) cancel(id string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	c, ok := r.running[id]
	if ok {
		c()
	}
	return ok
}

func (a *App) registerProjectSearch() {
	mustHandle(a, "search.inProject", "Find in files: text/regex, case, whole word, mask, scope, .gitignore/vendor (streamed with stream:true)", a.searchInProject)
	mustHandle(a, "search.cancel", "Cancel a running project search by ID", a.searchCancel)
	mustHandle(a, "search.replaceInProject", "Replace in files (preview or apply; open documents undoable, files with Local History)", a.replaceInProject)
}

func (a *App) searchCancel(_ context.Context, p SearchCancelParams) (SearchCancelResult, error) {
	if p.ID == "" {
		return SearchCancelResult{}, Errorf(CodeInvalidParams, "id is required")
	}
	return SearchCancelResult{ID: p.ID, Cancelled: a.searches.cancel(p.ID)}, nil
}

// maskSet is a compiled file mask.
type maskSet struct{ include, exclude []string }

func parseMask(s string) (maskSet, error) {
	var m maskSet
	for _, part := range strings.Split(s, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		neg := strings.HasPrefix(part, "!")
		part = strings.TrimPrefix(part, "!")
		if _, err := path.Match(part, ""); err != nil {
			return m, Errorf(CodeInvalidParams, "mask %q: %v", part, err)
		}
		if neg {
			m.exclude = append(m.exclude, part)
		} else {
			m.include = append(m.include, part)
		}
	}
	return m, nil
}

func globMatch(pat, rel string) bool {
	target := path.Base(rel)
	if strings.Contains(pat, "/") {
		target = rel
	}
	ok, _ := path.Match(pat, target)
	return ok
}

func (m maskSet) match(rel string) bool {
	for _, p := range m.exclude {
		if globMatch(p, rel) {
			return false
		}
	}
	if len(m.include) == 0 {
		return true
	}
	for _, p := range m.include {
		if globMatch(p, rel) {
			return true
		}
	}
	return false
}

// searchCandidate is one file to scan.
type searchCandidate struct {
	abs, rel string
	open     bool
	version  uint64
	text     []byte // open documents: the buffer snapshot
}

// scanStats are the counters of a scan.
type scanStats struct {
	searched, binary, large atomic.Int64
	matches                 atomic.Int64
	limited                 atomic.Bool
}

// scanner runs a project search over candidates.
type scanner struct {
	a     *App
	p     ProjectSearchParams
	q     *search.Query
	mask  maskSet
	limit int
	stats scanStats
}

func (a *App) newScanner(p ProjectSearchParams, defLimit int) (*scanner, error) {
	q, err := SearchParams{Pattern: p.Pattern, Regex: p.Regex, CaseSensitive: p.CaseSensitive, WholeWord: p.WholeWord}.compile()
	if err != nil {
		return nil, err
	}
	mask, err := parseMask(p.Mask)
	if err != nil {
		return nil, err
	}
	if p.Limit <= 0 {
		p.Limit = defLimit
	}
	if p.MaxFileSize <= 0 {
		p.MaxFileSize = defaultMaxFileSize
	}
	if p.Dir != "" && p.Scope == "" {
		p.Scope = ScopeDirectory
	}
	switch p.Scope {
	case "", ScopeProject, ScopeDirectory, ScopeOpenFiles:
	default:
		return nil, Errorf(CodeInvalidParams, "unknown scope %q (project, directory, openFiles)", p.Scope)
	}
	return &scanner{a: a, p: p, q: q, mask: mask, limit: p.Limit}, nil
}

// produce sends the candidates to out (closing it at the end).
func (s *scanner) produce(ctx context.Context, out chan<- searchCandidate) error {
	defer close(out)
	open := map[string]DocSnapshot{}
	for _, d := range s.a.Documents() {
		open[d.File] = d
	}
	send := func(c searchCandidate) bool {
		if d, ok := open[c.abs]; ok {
			c.open, c.version, c.text = true, d.Version, d.Text
		}
		select {
		case out <- c:
			return true
		case <-ctx.Done():
			return false
		}
	}
	if len(s.p.Paths) > 0 {
		for _, p := range s.p.Paths {
			abs, err := s.a.resolvePath(p)
			if err != nil {
				return err
			}
			if !send(searchCandidate{abs: abs, rel: s.a.displayPath(abs)}) {
				return ctx.Err()
			}
		}
		return nil
	}
	if s.p.Scope == ScopeOpenFiles {
		files := make([]string, 0, len(open))
		for f := range open {
			files = append(files, f)
		}
		sort.Strings(files)
		for _, f := range files {
			rel := s.a.displayPath(f)
			if !s.mask.match(filepath.ToSlash(rel)) {
				continue
			}
			if !send(searchCandidate{abs: f, rel: rel}) {
				return ctx.Err()
			}
		}
		return nil
	}
	pr, err := s.a.requireProject()
	if err != nil {
		return err
	}
	root := pr.root
	relDir := ""
	if s.p.Scope == ScopeDirectory {
		relDir = filepath.ToSlash(filepath.Clean(s.p.Dir))
		if filepath.IsAbs(s.p.Dir) {
			r, err := filepath.Rel(root, s.p.Dir)
			if err != nil {
				return Errorf(CodeInvalidParams, "dir: %v", err)
			}
			relDir = filepath.ToSlash(r)
		}
		if relDir == "." || relDir == "/" {
			relDir = ""
		}
		if relDir == ".." || strings.HasPrefix(relDir, "../") {
			return Errorf(CodeInvalidParams, "dir must be inside the project")
		}
	}
	var ign gitignore.Set
	ign.Load(root, "")
	if relDir != "" {
		parts := strings.Split(relDir, "/")
		for i := range parts[:len(parts)-1] {
			ign.Load(root, strings.Join(parts[:i+1], "/"))
		}
	}
	start := filepath.Join(root, filepath.FromSlash(relDir))
	if st, err := os.Stat(start); err != nil {
		return notFoundOr(err, "search %s", relDir)
	} else if !st.IsDir() {
		return Errorf(CodeInvalidParams, "%s is not a directory", relDir)
	}
	visited := map[string]bool{}
	werr := filepath.WalkDir(start, func(p string, d fs.DirEntry, err error) error {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if err != nil {
			return nil // unreadable entries are skipped
		}
		rel, rerr := filepath.Rel(root, p)
		if rerr != nil {
			return nil
		}
		rel = filepath.ToSlash(rel)
		if d.IsDir() {
			if p == start {
				if rel != "." {
					ign.Load(root, rel)
				}
				return nil
			}
			name := d.Name()
			if alwaysSkipped[name] || (libraryDirs[name] && !s.p.Libraries) || (!s.p.Ignored && ign.Match(rel, true)) {
				return filepath.SkipDir
			}
			ign.Load(root, rel)
			return nil
		}
		if !d.Type().IsRegular() && d.Type()&fs.ModeSymlink == 0 {
			return nil
		}
		if (!s.p.Ignored && ign.Match(rel, false)) || !s.mask.match(rel) {
			return nil
		}
		visited[p] = true
		if !send(searchCandidate{abs: p, rel: rel}) {
			return ctx.Err()
		}
		return nil
	})
	if werr != nil {
		return werr
	}
	// Open documents that do not exist on disk yet (doc.open create:true).
	for f := range open {
		if visited[f] || exists(f) {
			continue
		}
		rel := s.a.displayPath(f)
		if filepath.IsAbs(rel) || (relDir != "" && !strings.HasPrefix(rel, relDir+"/")) || !s.mask.match(rel) {
			continue
		}
		if !send(searchCandidate{abs: f, rel: rel}) {
			return ctx.Err()
		}
	}
	return nil
}

// load returns the text of a candidate or nil if it is skipped.
func (s *scanner) load(c searchCandidate) []byte {
	if c.open {
		return c.text
	}
	st, err := os.Stat(c.abs)
	if err != nil || st.IsDir() {
		return nil
	}
	if st.Size() > s.p.MaxFileSize {
		s.stats.large.Add(1)
		return nil
	}
	data, err := os.ReadFile(c.abs)
	if err != nil {
		return nil
	}
	if bytes.IndexByte(data[:min(len(data), binarySniffBytes)], 0) >= 0 {
		s.stats.binary.Add(1)
		return nil
	}
	return data
}

// run scans all candidates in parallel and calls found for every file with
// matches (concurrently). found receives at most the matches that fit into
// the limit.
func (s *scanner) run(ctx context.Context, found func(c searchCandidate, text []byte, ms []search.Match)) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	jobs := make(chan searchCandidate, 64)
	var prodErr error
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		prodErr = s.produce(ctx, jobs)
	}()
	workers := runtime.GOMAXPROCS(0)
	var wwg sync.WaitGroup
	for range workers {
		wwg.Add(1)
		go func() {
			defer wwg.Done()
			for c := range jobs {
				if ctx.Err() != nil {
					continue // drain
				}
				text := s.load(c)
				if text == nil {
					continue
				}
				s.stats.searched.Add(1)
				remaining := s.limit - int(s.stats.matches.Load())
				if remaining <= 0 {
					s.stats.limited.Store(true)
					cancel()
					continue
				}
				ms := s.q.FindAllBytes(text, remaining+1)
				if len(ms) == 0 {
					continue
				}
				total := int(s.stats.matches.Add(int64(len(ms))))
				if over := total - s.limit; over > 0 {
					keep := len(ms) - over
					s.stats.matches.Add(-int64(over))
					s.stats.limited.Store(true)
					cancel()
					if keep <= 0 {
						continue
					}
					ms = ms[:keep]
				}
				found(c, text, ms)
			}
		}()
	}
	wwg.Wait()
	wg.Wait()
	if prodErr != nil && !errors.Is(prodErr, context.Canceled) {
		return prodErr
	}
	return nil
}

// searchMatches converts matches of text into API matches with line,
// column and a line preview.
func searchMatches(text []byte, ms []search.Match) []SearchMatch {
	out := make([]SearchMatch, 0, len(ms))
	line, lineStart, pos := 1, 0, 0
	for _, m := range ms {
		if m.Start > pos {
			seg := text[pos:m.Start]
			if n := bytes.Count(seg, []byte{'\n'}); n > 0 {
				line += n
				lineStart = pos + bytes.LastIndexByte(seg, '\n') + 1
			}
			pos = m.Start
		}
		lineEnd := bytes.IndexByte(text[lineStart:], '\n')
		if lineEnd < 0 {
			lineEnd = len(text)
		} else {
			lineEnd += lineStart
		}
		lt := text[lineStart:lineEnd]
		lt = bytes.TrimSuffix(lt, []byte{'\r'})
		sm := SearchMatch{
			Start: m.Start, End: m.End, Line: line,
			Col:  utf8.RuneCount(text[lineStart:m.Start]) + 1,
			Text: string(text[m.Start:m.End]),
		}
		if len(lt) > maxPreviewBytes {
			from := max(0, m.Start-lineStart-previewContextBefore)
			for from > 0 && !utf8.RuneStart(lt[from]) {
				from--
			}
			to := min(len(lt), from+maxPreviewBytes)
			for to < len(lt) && !utf8.RuneStart(lt[to]) {
				to++
			}
			sm.LineText, sm.PreviewStart = string(lt[from:to]), from
		} else {
			sm.LineText = string(lt)
		}
		out = append(out, sm)
	}
	return out
}

func (a *App) searchInProject(ctx context.Context, p ProjectSearchParams) (ProjectSearchResult, error) {
	s, err := a.newScanner(p, defaultSearchLimit)
	if err != nil {
		return ProjectSearchResult{}, err
	}
	if s.p.Scope != ScopeOpenFiles && len(s.p.Paths) == 0 {
		if _, err := a.requireProject(); err != nil {
			return ProjectSearchResult{}, err
		}
	}
	base := ctx
	if p.Stream {
		base = context.WithoutCancel(ctx)
	}
	id, sctx, done, err := a.searches.start(base, p.ID)
	if err != nil {
		return ProjectSearchResult{}, err
	}
	if !p.Stream {
		defer done()
		var mu sync.Mutex
		var files []FileMatches
		start := time.Now()
		err := s.run(sctx, func(c searchCandidate, text []byte, ms []search.Match) {
			fm := FileMatches{Path: c.rel, File: c.abs, Open: c.open, Version: c.version, Matches: searchMatches(text, ms)}
			mu.Lock()
			files = append(files, fm)
			mu.Unlock()
		})
		if err != nil {
			return ProjectSearchResult{}, err
		}
		sort.Slice(files, func(i, j int) bool { return files[i].Path < files[j].Path })
		res := s.summary(id, start, sctx, ctx)
		res.Files = files
		if res.Files == nil {
			res.Files = []FileMatches{}
		}
		res.FilesMatched = len(files)
		return res, nil
	}
	go a.streamSearch(ctx, s, id, sctx, done)
	return ProjectSearchResult{ID: id, Running: true, Files: []FileMatches{}}, nil
}

// summary builds the result counters. The search counts as cancelled when
// its context ended without reaching the limit.
func (s *scanner) summary(id string, start time.Time, sctx, parent context.Context) ProjectSearchResult {
	res := ProjectSearchResult{
		ID: id, Matches: int(s.stats.matches.Load()), FilesSearched: int(s.stats.searched.Load()),
		SkippedBinary: int(s.stats.binary.Load()), SkippedLarge: int(s.stats.large.Load()),
		Limited: s.stats.limited.Load(), DurationMs: time.Since(start).Milliseconds(),
	}
	res.Cancelled = !res.Limited && (sctx.Err() != nil || parent.Err() != nil)
	return res
}

// streamSearch runs s and publishes batches as search.results events.
func (a *App) streamSearch(_ context.Context, s *scanner, id string, sctx context.Context, done func()) {
	start := time.Now()
	var mu sync.Mutex
	var batch []FileMatches
	pending, files := 0, 0
	flush := func() {
		if len(batch) == 0 {
			return
		}
		a.bus.Publish(TopicSearchResults, ProjectSearchEvent{ID: id, Files: batch})
		batch, pending = nil, 0
	}
	stop := make(chan struct{})
	ticker := time.NewTicker(streamFlushInterval)
	go func() {
		for {
			select {
			case <-ticker.C:
				mu.Lock()
				flush()
				mu.Unlock()
			case <-stop:
				return
			}
		}
	}()
	err := s.run(sctx, func(c searchCandidate, text []byte, ms []search.Match) {
		fm := FileMatches{Path: c.rel, File: c.abs, Open: c.open, Version: c.version, Matches: searchMatches(text, ms)}
		mu.Lock()
		batch = append(batch, fm)
		pending += len(fm.Matches)
		files++
		if pending >= streamBatchMatches {
			flush()
		}
		mu.Unlock()
	})
	ticker.Stop()
	close(stop)
	mu.Lock()
	flush()
	sum := s.summary(id, start, sctx, context.Background())
	sum.FilesMatched = files
	mu.Unlock()
	ev := ProjectSearchEvent{ID: id, Done: true, Summary: &sum}
	if err != nil {
		ev.Error = err.Error()
	}
	done()
	a.bus.Publish(TopicSearchResults, ev)
}

func (a *App) replaceInProject(ctx context.Context, p ProjectReplaceParams) (ProjectReplaceResult, error) {
	p.Stream = false
	s, err := a.newScanner(p.ProjectSearchParams, defaultReplaceLimit)
	if err != nil {
		return ProjectReplaceResult{}, err
	}
	if s.p.Scope != ScopeOpenFiles && len(s.p.Paths) == 0 {
		if _, err := a.requireProject(); err != nil {
			return ProjectReplaceResult{}, err
		}
	}
	_, sctx, done, err := a.searches.start(ctx, p.ID)
	if err != nil {
		return ProjectReplaceResult{}, err
	}
	defer done()
	var mu sync.Mutex
	var files []FileReplace
	err = s.run(sctx, func(c searchCandidate, text []byte, ms []search.Match) {
		fr := FileReplace{Path: c.rel, File: c.abs, Open: c.open, Replaced: len(ms)}
		if p.Preview {
			sms := searchMatches(text, ms)
			fr.Matches = make([]ReplaceMatch, len(ms))
			for i, m := range ms {
				fr.Matches[i] = ReplaceMatch{SearchMatch: sms[i], Replacement: string(s.q.ExpandBytes(text, m, p.Replacement))}
			}
		}
		mu.Lock()
		files = append(files, fr)
		mu.Unlock()
	})
	if err != nil {
		return ProjectReplaceResult{}, err
	}
	if sctx.Err() != nil && !s.stats.limited.Load() {
		return ProjectReplaceResult{}, Errorf(CodeFailed, "replace cancelled before any file was changed")
	}
	sort.Slice(files, func(i, j int) bool { return files[i].Path < files[j].Path })
	res := ProjectReplaceResult{
		Preview: p.Preview, Files: files, FilesSearched: int(s.stats.searched.Load()),
		SkippedBinary: int(s.stats.binary.Load()), SkippedLarge: int(s.stats.large.Load()), Limited: s.stats.limited.Load(),
	}
	if res.Files == nil {
		res.Files = []FileReplace{}
	}
	if p.Preview {
		for _, f := range files {
			res.Replaced += f.Replaced
		}
		res.FilesChanged = len(files)
		return res, nil
	}
	q := s.q
	for i := range res.Files {
		f := &res.Files[i]
		op := FileOp{Path: f.File, Changes: func(text []byte) ([]TextChange, error) {
			ms := q.FindAllBytes(text, 0)
			out := make([]TextChange, len(ms))
			for j, m := range ms {
				out[j] = TextChange{Start: m.Start, End: m.End, Text: string(q.ExpandBytes(text, m, p.Replacement))}
			}
			return out, nil
		}}
		rs, err := a.ApplyFileOps(ctx, "replace in path", []FileOp{op})
		if err != nil {
			f.Error, f.Replaced = err.Error(), 0
			res.Failed++
			continue
		}
		r := rs[0]
		f.Open, f.Replaced, f.Version, f.Revision = r.Open, r.Edits, r.Version, r.Revision
		res.Replaced += r.Edits
		if r.Edits > 0 {
			res.FilesChanged++
		}
	}
	return res, nil
}
