package api

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"time"
	"unicode/utf8"

	"github.com/ape-dev-de/tiegel/internal/core/autosave"
	"github.com/ape-dev-de/tiegel/internal/core/settings"
	"github.com/ape-dev-de/tiegel/internal/core/watch"
	"github.com/ape-dev-de/tiegel/internal/editor/buffer"
	"github.com/ape-dev-de/tiegel/internal/gitcli"
)

// External changes (AP10.7, D141): every open document remembers the
// state of its file as last read or written (diskStamp). Saves compare
// the file with it under the save lock and refuse to overwrite a file
// another program changed (autosave.ErrExternalChange → -32004 with
// data.reason "externalChange"). The project watcher (internal/core/watch)
// reports changes; reconcile then reloads unmodified documents (one undo
// step, doc.reloaded) and flags modified ones (doc.externalConflict) or
// deleted files (doc.deletedOnDisk). Tiegel's own writes update the stamp
// in the same critical section, so they never look external.

// Topics of the file watcher and external changes.
const (
	// TopicFilesChanged: files or directories of the project were
	// created, modified, deleted or renamed (FilesChangedEvent).
	TopicFilesChanged = "files.changed"
	// TopicDocReloaded: an open document was replaced with the file on
	// disk (DocReloadedEvent).
	TopicDocReloaded = "doc.reloaded"
	// TopicDocExternalConflict: the file of a document with unsaved
	// changes was changed by another program (DocExternalEvent).
	TopicDocExternalConflict = "doc.externalConflict"
	// TopicDocDeletedOnDisk: the file of an open document was deleted or
	// renamed (DocExternalEvent); the document stays open.
	TopicDocDeletedOnDisk = "doc.deletedOnDisk"
	// TopicWatchStatus: the watcher started (initial scan done), stopped
	// or fell back to polling (WatchStatus).
	TopicWatchStatus = "watch.status"
)

// External change kinds in DocState.External and DocExternalEvent.Kind.
const (
	ExternalModified = "modified"
	ExternalDeleted  = "deleted"
	ExternalCreated  = "created"
)

// SettingWatchExclude (project or global settings): directory names or
// project-relative paths the watcher skips (default ["node_modules"]).
const SettingWatchExclude = "watch.exclude"

// FileChange is one entry of files.changed. Path/OldPath are display
// paths (project relative), File/OldFile absolute.
type FileChange struct {
	Path    string `json:"path"`
	File    string `json:"file"`
	Kind    string `json:"kind"` // created, modified, deleted, renamed
	OldPath string `json:"oldPath,omitempty"`
	OldFile string `json:"oldFile,omitempty"`
	Dir     bool   `json:"dir,omitempty"`
}

// FilesChangedEvent is the payload of files.changed.
type FilesChangedEvent struct {
	Changes []FileChange `json:"changes"`
}

// DocReloadedEvent is the payload of doc.reloaded.
type DocReloadedEvent struct {
	DocState
	// Source: "watcher" (external change), "refresh" (watch.refresh),
	// "resolve" (doc.resolveExternal / doc.save reload) or "api"
	// (a module rewrote the file, e.g. git checkout).
	Source string `json:"source"`
}

// DiffHunk is one differing region between the buffer and the disk
// content (1-based start lines, line counts; a count of 0 means an
// insertion before the start line).
type DiffHunk struct {
	BufferStart int `json:"bufferStart"`
	BufferLines int `json:"bufferLines"`
	DiskStart   int `json:"diskStart"`
	DiskLines   int `json:"diskLines"`
}

// ExternalDiff summarises how the disk content differs from the buffer.
type ExternalDiff struct {
	Hunks []DiffHunk `json:"hunks"`
	// Added/Removed count lines only on disk / only in the buffer.
	Added   int `json:"added"`
	Removed int `json:"removed"`
}

// DocExternalEvent is the payload of doc.externalConflict and
// doc.deletedOnDisk.
type DocExternalEvent struct {
	Path       string `json:"path"`
	File       string `json:"file"`
	Generation uint64 `json:"generation"`
	Version    uint64 `json:"version"`
	// Kind is modified, created or deleted.
	Kind string `json:"kind"`
	// RenamedTo is the new display path if the file was renamed.
	RenamedTo string `json:"renamedTo,omitempty"`
	// Modified: the buffer has unsaved changes.
	Modified bool `json:"modified"`
	// DiskSize/DiskTime describe the file on disk (not for deleted).
	DiskSize int64     `json:"diskSize,omitempty"`
	DiskTime time.Time `json:"diskTime,omitzero"`
	// Diff compares buffer and disk (omitted for deleted files and files
	// above maxDiffBytes).
	Diff *ExternalDiff `json:"diff,omitempty"`
	// Source: "watcher", "save" (a save was refused) or "refresh".
	Source string `json:"source"`
}

// maxDiffBytes bounds the diff in DocExternalEvent.
const maxDiffBytes = 4 << 20

// racyWindow: files modified within this time may have changed again
// without a visible size/mtime change (timestamp granularity), so their
// content is compared.
const racyWindow = 2 * time.Second

// diskStamp is the state of a document's file as last read or written.
type diskStamp struct {
	exists bool
	info   os.FileInfo
	hash   [sha256.Size]byte
}

func stampOf(fi os.FileInfo, data []byte) diskStamp {
	return diskStamp{exists: true, info: fi, hash: sha256.Sum256(data)}
}

// unchanged reports whether fi certainly describes the stamped content
// without reading it: same file, size and mtime, and old enough.
func (s diskStamp) unchanged(fi os.FileInfo) bool {
	return s.exists && s.info != nil && os.SameFile(s.info, fi) &&
		s.info.Size() == fi.Size() && s.info.ModTime().Equal(fi.ModTime()) &&
		time.Since(fi.ModTime()) >= racyWindow
}

// readDisk reads path and stamps it. The stat comes first: if the file
// changes in between, the stamp is older than the content, which only
// costs a content comparison later (never a missed change).
func readDisk(path string) (diskStamp, []byte, error) {
	fi, err := os.Stat(path)
	if err != nil {
		return diskStamp{}, nil, err
	}
	if fi.IsDir() {
		return diskStamp{}, nil, fmt.Errorf("%s is a directory", path)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return diskStamp{}, nil, err
	}
	return stampOf(fi, data), data, nil
}

// checkDisk compares the file with known. cur is the current state and
// data the current content (nil when the content was not read because
// the stat proves it unchanged, or the file does not exist).
func checkDisk(path string, known diskStamp) (changed bool, cur diskStamp, data []byte, err error) {
	fi, err := os.Stat(path)
	if errors.Is(err, fs.ErrNotExist) {
		return known.exists, diskStamp{}, nil, nil
	}
	if err != nil {
		return false, known, nil, err
	}
	if known.unchanged(fi) {
		return false, known, nil, nil
	}
	cur, data, err = readDisk(path)
	if errors.Is(err, fs.ErrNotExist) {
		return known.exists, diskStamp{}, nil, nil
	}
	if err != nil {
		return false, known, nil, err
	}
	return !known.exists || cur.hash != known.hash, cur, data, nil
}

// externalChangeError is returned by docSet.Save when the file changed
// on disk; it wraps autosave.ErrExternalChange.
type externalChangeError struct {
	path string
	kind string
}

func (e *externalChangeError) Error() string {
	return fmt.Sprintf("%s was %s on disk by another program; not overwritten", e.path, e.kind)
}

func (e *externalChangeError) Unwrap() error { return autosave.ErrExternalChange }

// externalKind classifies a change of known into cur.
func externalKind(known, cur diskStamp) string {
	switch {
	case !cur.exists:
		return ExternalDeleted
	case !known.exists:
		return ExternalCreated
	}
	return ExternalModified
}

// replaceMinimal replaces the buffer content with data as one undo step,
// touching only the range between the common prefix and suffix (so
// cursors and folds outside it survive). It reports whether the content
// changed.
func replaceMinimal(b *buffer.Buffer, data []byte) (bool, error) {
	cur := b.Bytes()
	if bytes.Equal(cur, data) {
		return false, nil
	}
	n := min(len(cur), len(data))
	p := 0
	for p < n && cur[p] == data[p] {
		p++
	}
	s := 0
	for s < n-p && cur[len(cur)-1-s] == data[len(data)-1-s] {
		s++
	}
	// Keep characters and CRLF pairs whole.
	for p > 0 && (p < len(cur) && !utf8.RuneStart(cur[p]) || cur[p-1] == '\r') {
		p--
	}
	for s > 0 && (!utf8.RuneStart(cur[len(cur)-s]) || cur[len(cur)-s] == '\n') {
		s--
	}
	b.BreakUndo()
	if _, err := b.Replace(p, len(cur)-p-s, data[p:len(data)-s]); err != nil {
		return false, err
	}
	b.BreakUndo()
	return true, nil
}

// lineDiff summarises the differences between buffer and disk text.
func lineDiff(buf, disk []byte) *ExternalDiff {
	if len(buf) > maxDiffBytes || len(disk) > maxDiffBytes {
		return nil
	}
	changes := gitcli.DiffLines(gitcli.SplitLines(buf), gitcli.SplitLines(disk))
	d := &ExternalDiff{Hunks: make([]DiffHunk, 0, len(changes))}
	for _, c := range changes {
		d.Hunks = append(d.Hunks, DiffHunk{
			BufferStart: c.OldStart + 1, BufferLines: c.OldEnd - c.OldStart,
			DiskStart: c.NewStart + 1, DiskLines: c.NewEnd - c.NewStart,
		})
		d.Removed += c.OldEnd - c.OldStart
		d.Added += c.NewEnd - c.NewStart
	}
	return d
}

// externalEventLocked builds the event for d (d.mu held).
func (a *App) externalEventLocked(d *Doc, kind string, cur diskStamp, data []byte, source string) DocExternalEvent {
	ev := DocExternalEvent{
		Path: a.displayPath(d.path), File: d.path, Generation: d.gen, Version: d.buf.Version(),
		Kind: kind, Modified: d.buf.Modified(), Source: source,
	}
	if cur.exists && cur.info != nil {
		ev.DiskSize, ev.DiskTime = cur.info.Size(), cur.info.ModTime()
		ev.Diff = lineDiff(d.buf.Bytes(), data)
	}
	return ev
}

// flagExternalLocked records a pending external change on d (d.mu held)
// and publishes doc.externalConflict (doc.deletedOnDisk for deletions)
// once per disk state.
func (a *App) flagExternalLocked(d *Doc, kind string, cur diskStamp, data []byte, source, renamedTo string) {
	if d.external == kind && d.extHash == cur.hash {
		return // already reported
	}
	d.external, d.extHash = kind, cur.hash
	ev := a.externalEventLocked(d, kind, cur, data, source)
	ev.RenamedTo = renamedTo
	topic := TopicDocExternalConflict
	if kind == ExternalDeleted {
		topic = TopicDocDeletedOnDisk
	}
	a.bus.Publish(topic, ev)
	a.bus.Publish(TopicDocChanged, a.stateLocked(d))
}

// setDiskLocked records the file state after Tiegel itself read or wrote
// it and clears a pending external change.
func (a *App) setDiskLocked(d *Doc, st diskStamp) {
	d.disk = st
	d.diskSeq++
	d.external, d.extHash = "", [sha256.Size]byte{}
}

// reloadLocked replaces the buffer with data from disk (one undo step),
// marks it saved and records st. d.mu is held.
func (a *App) reloadLocked(d *Doc, data []byte, st diskStamp, source string) {
	if _, err := replaceMinimal(d.buf, data); err != nil {
		a.log.Warn("reload", "file", d.path, "err", err)
		return
	}
	d.buf.MarkSaved()
	a.setDiskLocked(d, st)
	state := a.stateLocked(d)
	a.bus.Publish(TopicDocChanged, state)
	a.bus.Publish(TopicDocReloaded, DocReloadedEvent{DocState: state, Source: source})
}

// isCurrent reports whether d is still the open instance of its path.
func (a *App) isCurrent(d *Doc) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.docs[d.path] == d
}

// reconcile compares an open document with its file and reacts: nothing
// if the file is unchanged (e.g. Tiegel's own save), reload if the buffer
// is unmodified, otherwise flag the conflict. renamedTo is set when the
// watcher saw the file move. It runs under the save lock, so it never sees
// a half-finished save.
func (a *App) reconcile(d *Doc, source, renamedTo string) {
	_ = a.saver.Exclusive(func() error {
		if !a.isCurrent(d) {
			return nil
		}
		d.mu.Lock()
		known, seq := d.disk, d.diskSeq
		d.mu.Unlock()
		changed, cur, data, err := checkDisk(d.path, known)
		if err != nil {
			a.log.Warn("external change check", "file", d.path, "err", err)
			return nil
		}
		d.mu.Lock()
		defer d.mu.Unlock()
		if d.diskSeq != seq {
			return nil // a reload or save in between already handled it
		}
		if !changed {
			if d.external != "" && cur.exists {
				// The file came back as it was: nothing to resolve.
				a.setDiskLocked(d, cur)
				a.bus.Publish(TopicDocChanged, a.stateLocked(d))
			}
			return nil
		}
		kind := externalKind(known, cur)
		switch {
		case kind == ExternalDeleted:
			if renamedTo != "" {
				renamedTo = a.displayPath(renamedTo)
			}
			a.flagExternalLocked(d, kind, cur, nil, source, renamedTo)
		case bytes.Equal(d.buf.Bytes(), data):
			// Disk and buffer agree (e.g. another tool made the same
			// change): the buffer is saved.
			a.setDiskLocked(d, cur)
			d.buf.MarkSaved()
			a.bus.Publish(TopicDocChanged, a.stateLocked(d))
		case !d.buf.Modified():
			a.reloadLocked(d, data, cur, source)
		default:
			a.flagExternalLocked(d, kind, cur, data, source, "")
		}
		return nil
	})
}

// reconcileAll checks every open document against its file.
func (a *App) reconcileAll(source string) {
	for _, d := range a.openDocs() {
		a.reconcile(d, source, "")
	}
}

func (a *App) openDocs() []*Doc {
	a.mu.Lock()
	defer a.mu.Unlock()
	docs := make([]*Doc, 0, len(a.docs))
	for _, d := range a.docs {
		docs = append(docs, d)
	}
	return docs
}

// ReloadDocument replaces the content of an open, unmodified document
// with its file (one undo step), marks it saved and records the file
// state, so the change does not count as external. It reports whether the
// document was reloaded (false: not open, modified or unreadable). Callers
// that wrote the file hold the save lock (Exclusive) for write and reload.
func (a *App) ReloadDocument(path string) bool {
	d, err := a.doc(path)
	if err != nil {
		return false
	}
	st, data, err := readDisk(d.path)
	if err != nil {
		return false
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.buf.Modified() {
		return false
	}
	a.reloadLocked(d, data, st, "api")
	return true
}

// DocResolveExternalParams are the params of "doc.resolveExternal".
type DocResolveExternalParams struct {
	Path string `json:"path"`
	// Action: "keepMine" saves the buffer over the changed file,
	// "takeDisk" replaces the buffer with the file (one undo step).
	Action string `json:"action"`
}

// DocResolveResult is the result of "doc.resolveExternal".
type DocResolveResult struct {
	DocSaveResult
	Action string `json:"action"`
}

func (a *App) docResolveExternal(ctx context.Context, p DocResolveExternalParams) (DocResolveResult, error) {
	switch p.Action {
	case "keepMine":
		res, err := a.saveDoc(ctx, p.Path, true)
		return DocResolveResult{DocSaveResult: res, Action: p.Action}, err
	case "takeDisk":
		st, err := a.takeDisk(p.Path)
		return DocResolveResult{DocSaveResult: DocSaveResult{DocState: st}, Action: p.Action}, err
	}
	return DocResolveResult{}, Errorf(CodeInvalidParams, "action must be keepMine or takeDisk, got %q", p.Action)
}

// takeDisk replaces the document with its file (discarding unsaved
// changes as one undoable step).
func (a *App) takeDisk(path string) (DocState, error) {
	d, err := a.doc(path)
	if err != nil {
		return DocState{}, err
	}
	var state DocState
	err = a.saver.Exclusive(func() error {
		st, data, err := readDisk(d.path)
		if errors.Is(err, fs.ErrNotExist) {
			return Errorf(CodeNotFound, "%s no longer exists on disk: keep your version (keepMine) or close the document", path)
		}
		if err != nil {
			return Errorf(CodeFailed, "read %s: %w", path, err)
		}
		d.mu.Lock()
		defer d.mu.Unlock()
		a.reloadLocked(d, data, st, "resolve")
		state = a.stateLocked(d)
		return nil
	})
	return state, err
}

// --- watcher lifecycle -----------------------------------------------

// WatchStatus is the result of "watch.status" and the payload of
// watch.status events.
type WatchStatus struct {
	Running bool   `json:"running"`
	Ready   bool   `json:"ready"`
	Root    string `json:"root,omitempty"`
	Backend string `json:"backend,omitempty"`
	Dirs    int    `json:"dirs"`
	Files   int    `json:"files"`
	// StartupMs is the duration of the initial scan.
	StartupMs float64 `json:"startupMs"`
	Batches   int     `json:"batches"`
	Rescans   int     `json:"rescans"`
	// Degraded explains why polling took over, if it did.
	Degraded string `json:"degraded,omitempty"`
}

// WatchStartParams are the params of "watch.start".
type WatchStartParams struct {
	// Backend: auto (default), fsevents, fsnotify or poll.
	Backend string `json:"backend,omitempty"`
	// Exclude overrides the watch.exclude setting (directory names or
	// project-relative paths).
	Exclude []string `json:"exclude,omitempty"`
}

// WatchRefreshResult is the result of "watch.refresh".
type WatchRefreshResult struct {
	// External lists the open documents with a pending external change
	// after the refresh.
	External []DocState `json:"external"`
}

func (a *App) registerWatch() {
	mustHandle(a, "watch.start", "Start the project file watcher (files.changed, git.changed, external document changes)", a.watchStart)
	mustHandle(a, "watch.stop", "Stop the project file watcher", a.watchStop)
	mustHandle(a, "watch.status", "State of the project file watcher", a.watchStatusMethod)
	mustHandle(a, "watch.refresh", "Rescan the project and check open documents against disk (e.g. on window focus)", a.watchRefresh)
	mustHandle(a, "doc.resolveExternal", "Resolve an external change of a document (action keepMine or takeDisk)", a.docResolveExternal)
}

func (a *App) watchStart(_ context.Context, p WatchStartParams) (WatchStatus, error) {
	root, err := a.RequireProject()
	if err != nil {
		return WatchStatus{}, err
	}
	be, err := watch.ParseBackend(p.Backend)
	if err != nil {
		return WatchStatus{}, Errorf(CodeInvalidParams, "%v", err)
	}
	if err := a.startWatch(root, be, p.Exclude); err != nil {
		return WatchStatus{}, Errorf(CodeFailed, "%w", err)
	}
	return a.watchStatus(), nil
}

func (a *App) watchStop(context.Context, struct{}) (WatchStatus, error) {
	a.stopWatch()
	return a.watchStatus(), nil
}

func (a *App) watchStatusMethod(context.Context, struct{}) (WatchStatus, error) {
	return a.watchStatus(), nil
}

func (a *App) watchRefresh(ctx context.Context, _ struct{}) (WatchRefreshResult, error) {
	a.watchMu.Lock()
	w := a.watcher
	a.watchMu.Unlock()
	if w != nil {
		select {
		case <-w.Ready():
		case <-ctx.Done():
			return WatchRefreshResult{}, Errorf(CodeFailed, "watch.refresh: %w", ctx.Err())
		}
		if err := w.Refresh(ctx); err != nil {
			return WatchRefreshResult{}, Errorf(CodeFailed, "watch.refresh: %w", err)
		}
	}
	// Documents outside the watched tree (or without watcher) as well.
	a.reconcileAll("refresh")
	res := WatchRefreshResult{External: []DocState{}}
	for _, d := range a.openDocs() {
		d.mu.Lock()
		if d.external != "" {
			res.External = append(res.External, a.stateLocked(d))
		}
		d.mu.Unlock()
	}
	return res, nil
}

func (a *App) watchStatus() WatchStatus {
	a.watchMu.Lock()
	w := a.watcher
	a.watchMu.Unlock()
	if w == nil {
		return WatchStatus{}
	}
	st := w.Stats()
	return WatchStatus{
		Running: true, Ready: st.Ready, Root: st.Root, Backend: string(st.Backend),
		Dirs: st.Dirs, Files: st.Files, StartupMs: float64(st.Startup.Microseconds()) / 1000,
		Batches: st.Batches, Rescans: st.Rescans, Degraded: st.Degraded,
	}
}

// startWatch (re)starts the watcher for root.
func (a *App) startWatch(root string, be watch.Backend, exclude []string) error {
	a.stopWatch()
	if exclude == nil {
		exclude = settings.Get[[]string](a.settingsStore(), SettingWatchExclude, nil)
	}
	w, err := watch.Start(root, watch.Options{
		Backend:      be,
		Exclude:      exclude,
		PollInterval: a.opts.WatchPollInterval,
		OnBatch:      a.onWatchBatch,
		OnError: func(err error) {
			a.log.Warn("watch", "err", err)
			a.bus.Publish(TopicWatchStatus, a.watchStatus())
		},
	})
	if err != nil {
		return err
	}
	a.watchMu.Lock()
	a.watcher = w
	a.watchMu.Unlock()
	go func() {
		select {
		case <-w.Ready():
			a.bus.Publish(TopicWatchStatus, a.watchStatus())
		case <-a.closing:
		}
	}()
	return nil
}

// stopWatch stops the watcher (waiting for a batch in progress).
func (a *App) stopWatch() {
	a.watchMu.Lock()
	w := a.watcher
	a.watcher = nil
	a.watchMu.Unlock()
	if w != nil {
		_ = w.Close()
		a.bus.Publish(TopicWatchStatus, WatchStatus{})
	}
}

// onWatchBatch publishes the changes and reconciles affected documents.
// It runs on the watcher goroutine.
func (a *App) onWatchBatch(b watch.Batch) {
	if len(b.Changes) > 0 {
		ev := FilesChangedEvent{Changes: make([]FileChange, 0, len(b.Changes))}
		for _, c := range b.Changes {
			fc := FileChange{Path: a.displayPath(c.Path), File: c.Path, Kind: string(c.Kind), Dir: c.Dir}
			if c.OldPath != "" {
				fc.OldPath, fc.OldFile = a.displayPath(c.OldPath), c.OldPath
			}
			ev.Changes = append(ev.Changes, fc)
		}
		a.bus.Publish(TopicFilesChanged, ev)
	}
	if len(b.Git) > 0 {
		paths := make([]string, len(b.Git))
		for i, g := range b.Git {
			paths[i] = a.displayPath(g)
		}
		a.bus.Publish(TopicGitChanged, GitChangedEvent{Op: "external", Paths: paths})
	}
	a.mu.Lock()
	type hit struct {
		d         *Doc
		renamedTo string
	}
	var hits []hit
	for _, c := range b.Changes {
		if c.Dir {
			continue // children are reported on their own
		}
		if d := a.docs[c.Path]; d != nil {
			hits = append(hits, hit{d: d})
		}
		if c.Kind == watch.Renamed {
			if d := a.docs[c.OldPath]; d != nil {
				hits = append(hits, hit{d: d, renamedTo: c.Path})
			}
		}
	}
	a.mu.Unlock()
	for _, h := range hits {
		a.reconcile(h.d, "watcher", h.renamedTo)
	}
}
