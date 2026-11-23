package phpindex

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"github.com/ape-dev-de/tiegel/internal/api"
	"github.com/ape-dev-de/tiegel/internal/core/tasks"
	"github.com/ape-dev-de/tiegel/internal/index"
)

// maxUpdatedFiles caps the file list of index.updated.
const maxUpdatedFiles = 1000

// state is the index of one open project and its background worker. All
// index work happens on the worker goroutine (run), except flushDoc, which
// a gutter request uses to index a pending document edit immediately.
type state struct {
	app      *api.App
	mopts    Options
	root     string
	ix       *index.Index
	cacheDir string

	ctx       context.Context
	cancel    context.CancelFunc
	startOnce sync.Once
	stopOnce  sync.Once
	done      chan struct{}
	readyCh   chan struct{}
	wake      chan struct{}

	mu         sync.Mutex
	started    bool
	phase      string
	progress   index.Progress
	err        string
	fromCache  bool
	lastSync   time.Time
	duration   time.Duration
	syncReq    bool
	syncFull   bool
	waiters    []chan struct{}
	files      map[string]bool
	docs       map[string]bool // edited, waiting for the debounce delay
	docsDue    map[string]bool
	docTimer   *time.Timer
	saveTimer  *time.Timer
	saveDue    bool
	cacheDirty bool
}

func newState(app *api.App, mopts Options, opts index.Options, cacheDir string) *state {
	ctx, cancel := context.WithCancel(context.Background())
	return &state{
		app: app, mopts: mopts, root: opts.Root, ix: index.New(opts), cacheDir: cacheDir,
		ctx: ctx, cancel: cancel, done: make(chan struct{}), readyCh: make(chan struct{}), wake: make(chan struct{}, 1),
		phase: "idle", files: map[string]bool{}, docs: map[string]bool{}, docsDue: map[string]bool{},
	}
}

// start launches the worker (once).
func (s *state) start() {
	s.startOnce.Do(func() {
		s.mu.Lock()
		s.started = true
		s.mu.Unlock()
		go s.run()
	})
}

// stop ends the worker and writes the cache.
func (s *state) stop() {
	s.stopOnce.Do(func() {
		s.cancel()
		s.mu.Lock()
		started := s.started
		if s.docTimer != nil {
			s.docTimer.Stop()
		}
		if s.saveTimer != nil {
			s.saveTimer.Stop()
		}
		s.mu.Unlock()
		if started {
			<-s.done
		}
		s.ix.Close()
	})
}

func (s *state) signal() {
	select {
	case s.wake <- struct{}{}:
	default:
	}
}

func (s *state) isReady() bool {
	select {
	case <-s.readyCh:
		return true
	default:
		return false
	}
}

func (s *state) waitReady(ctx context.Context) error {
	select {
	case <-s.readyCh:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	case <-s.done:
		return api.Errorf(api.CodeModuleDisabled, "php-index stopped")
	}
}

func (s *state) status() Status {
	s.mu.Lock()
	defer s.mu.Unlock()
	st := Status{
		State: s.phase, Root: s.root, Progress: s.progress, Stats: s.ix.Stats(), Generation: s.ix.Generation(),
		FromCache: s.fromCache, Error: s.err, Libraries: !s.ix.Options().SkipLibraries,
	}
	if !s.lastSync.IsZero() {
		st.LastSync = s.lastSync.Format(time.RFC3339)
		st.Duration = s.duration.Milliseconds()
	}
	return st
}

// requestSync asks for a full scan (full: drop index and cache first); the
// returned channel is closed when it has run.
func (s *state) requestSync(full bool) <-chan struct{} {
	ch := make(chan struct{})
	s.mu.Lock()
	s.syncReq = true
	s.syncFull = s.syncFull || full
	s.waiters = append(s.waiters, ch)
	s.mu.Unlock()
	s.signal()
	return ch
}

// queueFiles schedules re-indexing paths from disk.
func (s *state) queueFiles(paths ...string) {
	s.mu.Lock()
	for _, p := range paths {
		s.files[p] = true
	}
	s.mu.Unlock()
	s.signal()
}

// queueDoc schedules re-indexing an open document after the debounce
// delay.
func (s *state) queueDoc(path string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.docs[path] = true
	if s.docTimer == nil {
		s.docTimer = time.AfterFunc(s.mopts.DocDelay, s.docsElapsed)
		return
	}
	s.docTimer.Reset(s.mopts.DocDelay)
}

func (s *state) docsElapsed() {
	s.mu.Lock()
	for p := range s.docs {
		s.docsDue[p] = true
	}
	clear(s.docs)
	s.mu.Unlock()
	s.signal()
}

// flushDoc indexes the current text of path right away if it is an open
// document (its doc.edited events may still be on their way); unchanged
// text costs only a hash.
func (s *state) flushDoc(_ context.Context, path string) {
	d, err := s.app.Document(path)
	if err != nil {
		return
	}
	s.mu.Lock()
	delete(s.docs, path)
	delete(s.docsDue, path)
	s.mu.Unlock()
	if changed, _ := s.ix.UpdateText(path, d.Snapshot().Text); changed {
		s.changed([]string{path}, "doc")
	}
}

// updateDoc indexes the current text of an open document (or the file if
// it was closed meanwhile).
func (s *state) updateDoc(path string) bool {
	if d, err := s.app.Document(path); err == nil {
		changed, _ := s.ix.UpdateText(path, d.Snapshot().Text)
		return changed
	}
	changed, _ := s.ix.UpdateFile(path)
	return changed
}

func (s *state) setPhase(phase string) {
	s.mu.Lock()
	s.phase = phase
	s.mu.Unlock()
}

func (s *state) run() {
	defer close(s.done)
	defer s.saveCache()
	if !index.TreeSitter {
		s.setPhase("unavailable")
		return
	}
	s.setPhase("loading")
	if s.cacheDir != "" {
		if err := s.ix.Load(s.cacheDir); err == nil {
			s.mu.Lock()
			s.fromCache = true
			s.mu.Unlock()
			s.publishUpdated(nil, "cache")
		}
	}
	s.sync(false)
	close(s.readyCh)
	for {
		select {
		case <-s.ctx.Done():
			return
		case <-s.wake:
		}
		s.process()
	}
}

func (s *state) process() {
	s.mu.Lock()
	syncReq, full, waiters := s.syncReq, s.syncFull, s.waiters
	s.syncReq, s.syncFull, s.waiters = false, false, nil
	files := s.files
	s.files = map[string]bool{}
	docs := s.docsDue
	s.docsDue = map[string]bool{}
	saveDue := s.saveDue
	s.saveDue = false
	s.mu.Unlock()

	if syncReq {
		s.sync(full)
		for _, w := range waiters {
			close(w)
		}
	}
	var changed []string
	for _, p := range sortedKeys(files) {
		if s.ctx.Err() != nil {
			return
		}
		if docs[p] {
			continue
		}
		if _, err := s.app.Document(p); err == nil {
			docs[p] = true // open: the buffer is newer than the disk
			continue
		}
		if c, _ := s.ix.UpdateFile(p); c {
			changed = append(changed, p)
		}
	}
	if len(changed) > 0 {
		s.changed(changed, "file")
	}
	changed = changed[:0]
	for _, p := range sortedKeys(docs) {
		if s.updateDoc(p) {
			changed = append(changed, p)
		}
	}
	if len(changed) > 0 {
		s.changed(changed, "doc")
	}
	if saveDue {
		s.saveCache()
	}
}

func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// sync runs a full scan with progress events.
func (s *state) sync(full bool) {
	if full {
		s.ix.Reset()
		if s.cacheDir != "" {
			_ = os.Remove(filepath.Join(s.cacheDir, index.CacheFile))
		}
	}
	s.setPhase("indexing")
	bus := s.app.Bus()
	// The scan is a background task (D507); cancelling it stops this
	// run only, the index keeps what it has and follows later changes.
	task := s.app.BeginTask(s.ctx, tasks.Options{Title: "Indexing PHP files", Module: ID, Cancellable: true})
	res, err := s.ix.Sync(task.Context(), func(p index.Progress) {
		s.mu.Lock()
		s.progress = p
		s.mu.Unlock()
		bus.Publish(TopicProgress, p)
		if p.Phase == "scanning" {
			task.Progress(0, 0, "Scanning files")
		} else {
			task.Progress(p.Done, p.Total, "")
		}
	})
	task.End(err)
	s.mu.Lock()
	s.lastSync, s.duration = time.Now(), res.Duration
	s.err = ""
	s.phase = "ready"
	if err != nil && !errors.Is(err, context.Canceled) {
		s.err, s.phase = err.Error(), "error"
	}
	s.mu.Unlock()
	if len(res.Changed)+len(res.Removed) > 0 || full {
		s.publishUpdated(append(res.Changed, res.Removed...), "sync")
		s.mu.Lock()
		s.cacheDirty = true
		s.mu.Unlock()
		s.saveCache()
	}
}

// changed publishes index.updated and schedules the cache write.
func (s *state) changed(paths []string, reason string) {
	s.publishUpdated(paths, reason)
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cacheDirty = true
	if s.saveTimer == nil {
		s.saveTimer = time.AfterFunc(s.mopts.SaveDelay, func() {
			s.mu.Lock()
			s.saveDue = true
			s.mu.Unlock()
			s.signal()
		})
		return
	}
	s.saveTimer.Reset(s.mopts.SaveDelay)
}

func (s *state) publishUpdated(paths []string, reason string) {
	ev := UpdatedEvent{Files: []string{}, Generation: s.ix.Generation(), Reason: reason}
	for _, p := range paths {
		if len(ev.Files) == maxUpdatedFiles {
			ev.Truncated = true
			break
		}
		ev.Files = append(ev.Files, s.app.DisplayPath(p))
	}
	s.app.Bus().Publish(TopicUpdated, ev)
}

func (s *state) saveCache() {
	s.mu.Lock()
	dirty := s.cacheDirty
	s.cacheDirty = false
	s.mu.Unlock()
	if !dirty || s.cacheDir == "" {
		return
	}
	if err := s.ix.Save(s.cacheDir); err != nil {
		s.app.Logger().Warn("php-index: cache", "err", err)
	}
}
