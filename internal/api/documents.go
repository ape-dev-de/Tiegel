package api

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/ape-dev-de/tiegel/internal/core/autosave"
	"github.com/ape-dev-de/tiegel/internal/core/editorconfig"
	"github.com/ape-dev-de/tiegel/internal/core/history"
	"github.com/ape-dev-de/tiegel/internal/core/langdetect"
	"github.com/ape-dev-de/tiegel/internal/editor/buffer"
	"github.com/ape-dev-de/tiegel/internal/gitcli"
)

// Doc is an open document: the editor buffer shared by every client of the
// App (API handlers, the in-process GUI, modules).
//
// Threading rule: a buffer.Buffer is not safe for concurrent use. Every read
// or write of Buffer() must happen between Lock and Unlock. The GUI edits the
// buffer directly on its UI goroutine while holding the lock; API handlers
// take the same lock. All downstream consumers (doc.edited/doc.changed
// events, LSP sync, feature diagnostics, git gutter) are driven by
// buffer.OnEdit, so edits made through any path are seen by everyone.
// Listeners run while the lock is held and must not block.
type Doc struct {
	mu  sync.Mutex
	app *App
	gen uint64 // generation: unique per opened instance, see DocState
	buf *buffer.Buffer

	// path (absolute) and lang change when the file is renamed: written
	// with mu, App.mu and nameMu held; read with any of them held.
	nameMu sync.RWMutex
	path   string
	lang   langdetect.Language

	// External changes (external.go), guarded by mu: the file state as
	// last read or written by Tiegel, a counter of its updates, and the
	// pending external change ("" or External*) with the hash it was
	// reported for.
	disk     diskStamp
	diskSeq  uint64
	external string
	extHash  [32]byte
}

// Lock acquires the document lock.
func (d *Doc) Lock() { d.mu.Lock() }

// Unlock releases the document lock.
func (d *Doc) Unlock() { d.mu.Unlock() }

// Buffer returns the shared buffer. Only use it while holding the lock.
func (d *Doc) Buffer() *buffer.Buffer { return d.buf }

// Path returns the absolute file path. It changes when the file is
// renamed or moved (files.rename, files.move; see doc.renamed).
func (d *Doc) Path() string {
	d.nameMu.RLock()
	defer d.nameMu.RUnlock()
	return d.path
}

// Language returns the detected language (it may change on rename).
func (d *Doc) Language() langdetect.Language {
	d.nameMu.RLock()
	defer d.nameMu.RUnlock()
	return d.lang
}

// setName changes path and language; the caller holds d.mu.
func (d *Doc) setName(path string, lang langdetect.Language) {
	d.nameMu.Lock()
	d.path, d.lang = path, lang
	d.nameMu.Unlock()
}

// Generation returns the document's generation: a number unique per opened
// instance. Closing and reopening a path yields a new generation.
func (d *Doc) Generation() uint64 { return d.gen }

// newDoc creates a document and wires buffer.OnEdit to the bus.
func (a *App) newDoc(abs string, data []byte, disk diskStamp) *Doc {
	a.mu.Lock()
	a.docSeq++
	gen := a.docSeq
	a.mu.Unlock()
	d := &Doc{app: a, gen: gen, path: abs, lang: a.detectLang(abs, head(data)), buf: buffer.New(data), disk: disk}
	d.buf.OnEdit(func(e buffer.Edit) { a.onEdit(d, e) })
	return d
}

// onEdit runs inside the buffer change with d locked: it publishes
// doc.edited (the change itself, LSP-ready) and doc.changed (new state).
// Publish only enqueues, and holding the lock keeps the per-document order.
func (a *App) onEdit(d *Doc, e buffer.Edit) {
	ev := DocEditEvent{
		Path: a.displayPath(d.path), File: d.path, Language: string(d.lang), Version: d.buf.Version(),
		Start: e.StartByte, OldEnd: e.OldEndByte, NewEnd: e.NewEndByte,
		StartPoint:  TextPoint{Line: e.StartPoint.Row, Column: e.StartPoint.Column},
		OldEndPoint: TextPoint{Line: e.OldEndPoint.Row, Column: e.OldEndPoint.Column},
		NewEndPoint: TextPoint{Line: e.NewEndPoint.Row, Column: e.NewEndPoint.Column},
		Text:        string(e.Text),
		OldText:     string(e.OldText),
	}
	// UTF-16 positions in the document before the change (LSP coordinates).
	// The line prefix before the start is unchanged by the edit.
	startCol := buffer.UTF16Col(d.buf.Line(e.StartPoint.Row), e.StartPoint.Column)
	ev.Range = UTF16Range{Start: UTF16Pos{Line: e.StartPoint.Row, Character: startCol}}
	if i := strings.LastIndexByte(ev.OldText, '\n'); i >= 0 {
		ev.Range.End = UTF16Pos{Line: e.OldEndPoint.Row, Character: utf16Len(ev.OldText[i+1:])}
	} else {
		ev.Range.End = UTF16Pos{Line: e.StartPoint.Row, Character: startCol + utf16Len(ev.OldText)}
	}
	a.bus.Publish(TopicDocEdited, ev)
	a.bus.Publish(TopicDocChanged, a.stateLocked(d))
	// Every edit counts as activity for the autosave idle timer.
	a.saver.Activity()
}

func utf16Len(s string) int {
	n := 0
	for _, r := range s {
		if r >= 0x10000 {
			n += 2
		} else {
			n++
		}
	}
	return n
}

// Pos addresses a position in a document. If Line is set (1-based), Col is
// the 1-based column counted in characters (Unicode code points; 0 or 1 is
// the line start, values past the end clamp to the line end). Otherwise Offset
// is the 0-based byte offset.
type Pos struct {
	Offset int `json:"offset,omitempty"`
	Line   int `json:"line,omitempty"`
	Col    int `json:"col,omitempty"`
}

func (p Pos) offset(b *buffer.Buffer) (int, error) {
	if p.Line > 0 {
		if p.Line > b.LineCount() {
			return 0, Errorf(CodeInvalidParams, "line %d out of range (document has %d lines)", p.Line, b.LineCount())
		}
		col := p.Col - 1
		if col < 0 {
			col = 0
		}
		return b.LineStart(p.Line-1) + buffer.ByteColFromRunes(b.Line(p.Line-1), col), nil
	}
	if p.Offset < 0 || p.Offset > b.Len() {
		return 0, Errorf(CodeInvalidParams, "offset %d out of range (document has %d bytes)", p.Offset, b.Len())
	}
	return p.Offset, nil
}

// DocState describes an open document after an operation.
type DocState struct {
	Path string `json:"path"`
	// Generation identifies the opened instance of the document; it
	// changes when the document is closed and opened again. Pass it to
	// doc.close so a late close cannot close a newer instance.
	Generation uint64 `json:"generation"`
	Version    uint64 `json:"version"`
	Modified   bool   `json:"modified"`
	Length     int    `json:"length"`
	Lines      int    `json:"lines"`
	Language   string `json:"language"`
	EOL        string `json:"eol"`
	CanUndo    bool   `json:"canUndo"`
	CanRedo    bool   `json:"canRedo"`
	// External is set while the file has an unresolved change by another
	// program: "modified", "created" or "deleted" (deleted on disk; the
	// document stays open). See doc.resolveExternal.
	External string `json:"external,omitempty"`
}

func (a *App) stateLocked(d *Doc) DocState {
	eol := "lf"
	if d.buf.EOL() == buffer.CRLF {
		eol = "crlf"
	}
	return DocState{
		Path:       a.displayPath(d.path),
		Generation: d.gen,
		Version:    d.buf.Version(),
		Modified:   d.buf.Modified(),
		Length:     d.buf.Len(),
		Lines:      d.buf.LineCount(),
		Language:   string(d.lang),
		EOL:        eol,
		CanUndo:    d.buf.CanUndo(),
		CanRedo:    d.buf.CanRedo(),
		External:   d.external,
	}
}

func (a *App) registerDocs() {
	mustHandle(a, "doc.open", "Open a file as an editable document", a.docOpen)
	mustHandle(a, "doc.close", "Close a document (unsaved changes only with discard:true; optional generation)", a.docClose)
	mustHandle(a, "doc.list", "List open documents", a.docList)
	mustHandle(a, "doc.text", "Text of a document or of a range", a.docText)
	mustHandle(a, "doc.insert", "Insert text at a position", a.docInsert)
	mustHandle(a, "doc.delete", "Delete a range", a.docDelete)
	mustHandle(a, "doc.replace", "Replace a range (or the whole document) with text", a.docReplace)
	mustHandle(a, "doc.undo", "Undo the last change", a.docUndo)
	mustHandle(a, "doc.redo", "Redo the last undone change", a.docRedo)
	mustHandle(a, "doc.save", "Save a document (file.beforeSave hooks, Local History, file.saved); refuses to overwrite external changes unless force", a.docSave)
	mustHandle(a, "doc.saveAll", "Save all modified documents", a.docSaveAll)
	mustHandle(a, "history.list", "Local History revisions of a file, newest first", a.historyList)
	mustHandle(a, "history.content", "Content of a Local History revision", a.historyContent)
	mustHandle(a, "history.restore", "Restore a Local History revision to disk (and open document)", a.historyRestore)
	mustHandle(a, "history.recovery", "Recovery points of unsaved changes that could not be saved (e.g. on close)", a.historyRecovery)
	mustHandle(a, "history.recoveryDismiss", "Remove recovery hints (paths, or all); the Local History revisions stay", a.historyRecoveryDismiss)
}

// doc returns the open document for path.
func (a *App) doc(path string) (*Doc, error) {
	abs, err := a.resolvePath(path)
	if err != nil {
		return nil, err
	}
	a.mu.Lock()
	d := a.docs[abs]
	a.mu.Unlock()
	if d == nil {
		return nil, Errorf(CodeNotFound, "document not open: %s (use doc.open)", path)
	}
	return d, nil
}

// DocOpenParams are the params of "doc.open".
type DocOpenParams struct {
	Path string `json:"path"`
	// Create opens an empty document if the file does not exist.
	Create bool `json:"create,omitempty"`
	// Text includes the document text in the result.
	Text bool `json:"text,omitempty"`
}

// DocOpenResult is the result of "doc.open".
type DocOpenResult struct {
	DocState
	// AlreadyOpen is true if the document was open before.
	AlreadyOpen bool   `json:"alreadyOpen,omitempty"`
	Text        string `json:"text,omitempty"`
	// EditorConfig are the .editorconfig properties of the file (absent
	// when none apply or files.editorconfig is off). Clients apply indent
	// style/size and tab width; final newline and trailing whitespace are
	// applied by the kernel on save.
	EditorConfig *editorconfig.Props `json:"editorconfig,omitempty"`
	// CharsetWarning reports content not matching the .editorconfig
	// charset (read check only).
	CharsetWarning string `json:"charsetWarning,omitempty"`
}

func (a *App) docOpen(_ context.Context, p DocOpenParams) (DocOpenResult, error) {
	abs, err := a.resolvePath(p.Path)
	if err != nil {
		return DocOpenResult{}, err
	}
	a.mu.Lock()
	d, already := a.docs[abs]
	a.mu.Unlock()
	if !already {
		disk, data, err := readDisk(abs)
		switch {
		case errors.Is(err, fs.ErrNotExist) && p.Create:
			data = nil
		case err != nil:
			return DocOpenResult{}, notFoundOr(err, "doc.open")
		}
		d = a.newDoc(abs, data, disk)
		a.mu.Lock()
		if existing, ok := a.docs[abs]; ok {
			d, already = existing, true
		} else {
			a.docs[abs] = d
		}
		a.mu.Unlock()
	}
	ec, hasEC := a.editorConfig(abs)
	d.mu.Lock()
	res := DocOpenResult{DocState: a.stateLocked(d), AlreadyOpen: already}
	if p.Text {
		res.Text = d.buf.String()
	}
	if hasEC {
		res.EditorConfig = &ec
		if ec.Charset != "" {
			res.CharsetWarning = editorconfig.CheckCharset(ec.Charset, d.buf.Bytes())
		}
	}
	if !already {
		a.bus.Publish(TopicDocOpened, res.DocState)
		a.bus.Publish(TopicFileOpened, FileOpenedEvent{Path: res.Path, File: d.path, Language: string(d.lang), Version: res.Version, Size: res.Length})
	}
	d.mu.Unlock()
	return res, nil
}

// DocCloseParams are the params of "doc.close".
type DocCloseParams struct {
	Path string `json:"path"`
	// Generation, if non-zero, must match the open document's generation
	// (see DocState); otherwise the document was closed and reopened in
	// between and the request fails with CodeConflict.
	Generation uint64 `json:"generation,omitempty"`
	// Discard allows closing a document with unsaved changes (they are
	// lost). Without it such a close fails with CodeConflict.
	Discard bool `json:"discard,omitempty"`
}

// DocCloseResult is the result of "doc.close".
type DocCloseResult struct {
	Path       string `json:"path"`
	Generation uint64 `json:"generation,omitempty"`
	// Discarded is true if unsaved changes were thrown away.
	Discarded bool `json:"discarded"`
	// RenamedTo is set when the document was not closed but renamed or
	// moved (files.rename, files.move): it lives on under this path (see
	// doc.renamed).
	RenamedTo string `json:"renamedTo,omitempty"`
}

func (a *App) docClose(_ context.Context, p DocCloseParams) (DocCloseResult, error) {
	d, err := a.doc(p.Path)
	if err != nil {
		return DocCloseResult{}, err
	}
	var res DocCloseResult
	// No save may be in flight while closing: it could still write content
	// that is being discarded here.
	err = a.saver.Exclusive(func() error {
		d.mu.Lock()
		defer d.mu.Unlock()
		if p.Generation != 0 && p.Generation != d.gen {
			return Errorf(CodeConflict, "%s was reopened (generation %d, request expects %d); not closed", p.Path, d.gen, p.Generation).
				WithData(map[string]uint64{"generation": d.gen})
		}
		if d.buf.Modified() && !p.Discard {
			return Errorf(CodeConflict, "%s has unsaved changes: save it first (doc.save) or close with discard:true", p.Path).
				WithData(a.stateLocked(d))
		}
		a.mu.Lock()
		current := a.docs[d.path] == d
		if current {
			delete(a.docs, d.path)
		}
		a.mu.Unlock()
		if !current {
			return Errorf(CodeNotFound, "document not open: %s", p.Path)
		}
		res = DocCloseResult{Path: a.displayPath(d.path), Generation: d.gen, Discarded: d.buf.Modified()}
		return nil
	})
	if err != nil {
		return DocCloseResult{}, err
	}
	a.bus.Publish(TopicDocClosed, res)
	return res, nil
}

func (a *App) docList(_ context.Context, _ struct{}) ([]DocState, error) {
	a.mu.Lock()
	docs := make([]*Doc, 0, len(a.docs))
	for _, d := range a.docs {
		docs = append(docs, d)
	}
	a.mu.Unlock()
	out := make([]DocState, 0, len(docs))
	for _, d := range docs {
		d.mu.Lock()
		out = append(out, a.stateLocked(d))
		d.mu.Unlock()
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out, nil
}

// DocTextParams are the params of "doc.text". Without Start/End the whole
// document is returned.
type DocTextParams struct {
	Path  string `json:"path"`
	Start *Pos   `json:"start,omitempty"`
	End   *Pos   `json:"end,omitempty"`
}

// DocText is the result of "doc.text".
type DocText struct {
	Path    string `json:"path"`
	Version uint64 `json:"version"`
	Text    string `json:"text"`
}

func (a *App) docText(_ context.Context, p DocTextParams) (DocText, error) {
	d, err := a.doc(p.Path)
	if err != nil {
		return DocText{}, err
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	start, end, err := rangeOf(d.buf, p.Start, p.End)
	if err != nil {
		return DocText{}, err
	}
	return DocText{Path: a.displayPath(d.path), Version: d.buf.Version(), Text: string(d.buf.Slice(start, end))}, nil
}

// rangeOf resolves an optional range; nil bounds mean document start/end.
func rangeOf(b *buffer.Buffer, s, e *Pos) (int, int, error) {
	start, end := 0, b.Len()
	var err error
	if s != nil {
		if start, err = s.offset(b); err != nil {
			return 0, 0, err
		}
	}
	if e != nil {
		if end, err = e.offset(b); err != nil {
			return 0, 0, err
		}
	}
	if end < start {
		return 0, 0, Errorf(CodeInvalidParams, "range end %d before start %d", end, start)
	}
	return start, end, nil
}

// DocInsertParams are the params of "doc.insert".
type DocInsertParams struct {
	Path string `json:"path"`
	At   Pos    `json:"at"`
	Text string `json:"text"`
	// Version, if set (also 0), must match the document version
	// (optimistic locking for concurrent clients, see VersionOf).
	Version *uint64 `json:"version,omitempty"`
}

// VersionOf returns a pointer to v for the Version field of the edit
// params.
func VersionOf(v uint64) *uint64 { return &v }

func nonZero(v uint64) *uint64 {
	if v == 0 {
		return nil
	}
	return &v
}

// DocDeleteParams are the params of "doc.delete".
type DocDeleteParams struct {
	Path    string  `json:"path"`
	Start   Pos     `json:"start"`
	End     Pos     `json:"end"`
	Version *uint64 `json:"version,omitempty"`
}

// DocReplaceParams are the params of "doc.replace". Without Start and End
// the whole document is replaced.
type DocReplaceParams struct {
	Path    string  `json:"path"`
	Start   *Pos    `json:"start,omitempty"`
	End     *Pos    `json:"end,omitempty"`
	Text    string  `json:"text"`
	Version *uint64 `json:"version,omitempty"`
}

// edit runs fn on the locked document after the version check. Events are
// published by the buffer's OnEdit listener.
func (a *App) edit(path string, version *uint64, fn func(b *buffer.Buffer) error) (DocState, error) {
	d, err := a.doc(path)
	if err != nil {
		return DocState{}, err
	}
	d.mu.Lock()
	before := d.buf.Version()
	if version != nil && *version != before {
		d.mu.Unlock()
		return DocState{}, Errorf(CodeConflict, "version mismatch: document is at %d, request expects %d", before, *version).
			WithData(map[string]uint64{"version": before})
	}
	err = fn(d.buf)
	st := a.stateLocked(d)
	d.mu.Unlock()
	return st, err
}

func (a *App) docInsert(_ context.Context, p DocInsertParams) (DocState, error) {
	return a.edit(p.Path, p.Version, func(b *buffer.Buffer) error {
		off, err := p.At.offset(b)
		if err != nil {
			return err
		}
		b.BreakUndo()
		if _, err := b.Insert(off, []byte(p.Text)); err != nil {
			return Errorf(CodeInvalidParams, "insert: %w", err)
		}
		return nil
	})
}

func (a *App) docDelete(_ context.Context, p DocDeleteParams) (DocState, error) {
	return a.edit(p.Path, p.Version, func(b *buffer.Buffer) error {
		start, end, err := rangeOf(b, &p.Start, &p.End)
		if err != nil {
			return err
		}
		b.BreakUndo()
		if _, err := b.Delete(start, end-start); err != nil {
			return Errorf(CodeInvalidParams, "delete: %w", err)
		}
		return nil
	})
}

func (a *App) docReplace(_ context.Context, p DocReplaceParams) (DocState, error) {
	return a.edit(p.Path, p.Version, func(b *buffer.Buffer) error {
		start, end, err := rangeOf(b, p.Start, p.End)
		if err != nil {
			return err
		}
		if string(b.Slice(start, end)) == p.Text {
			return nil
		}
		b.BreakUndo()
		if _, err := b.Replace(start, end-start, []byte(p.Text)); err != nil {
			return Errorf(CodeInvalidParams, "replace: %w", err)
		}
		return nil
	})
}

// UndoResult is the result of "doc.undo" and "doc.redo".
type UndoResult struct {
	DocState
	// Done is false if there was nothing to undo/redo.
	Done bool `json:"done"`
}

func (a *App) docUndo(_ context.Context, p PathParams) (UndoResult, error) {
	var done bool
	st, err := a.edit(p.Path, nil, func(b *buffer.Buffer) error {
		_, _, done = b.Undo()
		return nil
	})
	return UndoResult{DocState: st, Done: done}, err
}

func (a *App) docRedo(_ context.Context, p PathParams) (UndoResult, error) {
	var done bool
	st, err := a.edit(p.Path, nil, func(b *buffer.Buffer) error {
		_, _, done = b.Redo()
		return nil
	})
	return UndoResult{DocState: st, Done: done}, err
}

// DocSaveParams are the params of "doc.save".
type DocSaveParams struct {
	Path string `json:"path"`
	// Force overwrites the file even if another program changed it since
	// it was read or written (otherwise -32004, data.reason
	// "externalChange").
	Force bool `json:"force,omitempty"`
	// Reload discards the buffer and takes the file from disk instead of
	// saving (same as doc.resolveExternal takeDisk).
	Reload bool `json:"reload,omitempty"`
}

// DocSaveResult is the result of "doc.save".
type DocSaveResult struct {
	DocState
	Size int `json:"size"`
	// Revision is the Local History revision ID (0 without project).
	Revision uint64 `json:"revision,omitempty"`
}

// ExternalChangeData is the error data of a save refused because of an
// external change (-32004).
type ExternalChangeData struct {
	Reason     string `json:"reason"` // "externalChange"
	Path       string `json:"path"`
	Kind       string `json:"kind"` // modified, created, deleted
	Generation uint64 `json:"generation"`
}

func (a *App) docSave(ctx context.Context, p DocSaveParams) (DocSaveResult, error) {
	switch {
	case p.Force && p.Reload:
		return DocSaveResult{}, Errorf(CodeInvalidParams, "force and reload exclude each other")
	case p.Reload:
		st, err := a.takeDisk(p.Path)
		return DocSaveResult{DocState: st}, err
	}
	return a.saveDoc(ctx, p.Path, p.Force)
}

func (a *App) saveDoc(ctx context.Context, path string, force bool) (DocSaveResult, error) {
	d, err := a.doc(path)
	if err != nil {
		return DocSaveResult{}, err
	}
	// The snapshot is taken under the save lock: a copy taken earlier
	// could be older than what a concurrent autosave has written.
	ev, err := a.saver.SaveWith(ctx, autosave.TriggerManual, func() (autosave.Doc, error) {
		d.mu.Lock()
		defer d.mu.Unlock()
		return autosave.Doc{Path: d.path, Content: d.buf.Bytes(), Version: d.buf.Version(), Instance: d.gen, Force: force}, nil
	})
	if errors.Is(err, autosave.ErrDocGone) {
		return DocSaveResult{}, Errorf(CodeNotFound, "save %s: document was closed", path)
	}
	var ext *externalChangeError
	if errors.As(err, &ext) {
		return DocSaveResult{}, Errorf(CodeConflict, "save %s: %w (save with force, or doc.resolveExternal)", path, err).
			WithData(ExternalChangeData{Reason: "externalChange", Path: a.displayPath(d.Path()), Kind: ext.kind, Generation: d.gen})
	}
	if err != nil {
		return DocSaveResult{}, Errorf(CodeFailed, "save %s: %w", path, err)
	}
	d.mu.Lock()
	res := DocSaveResult{DocState: a.stateLocked(d), Size: ev.Size, Revision: ev.Revision.ID}
	a.bus.Publish(TopicDocSaved, res)
	d.mu.Unlock()
	return res, nil
}

// SaveAllResult is the result of "doc.saveAll".
type SaveAllResult struct {
	Saved []string `json:"saved"`
}

func (a *App) docSaveAll(ctx context.Context, _ struct{}) (SaveAllResult, error) {
	res := SaveAllResult{Saved: []string{}}
	for _, d := range (docSet{a}).Dirty() {
		r, err := a.saveDoc(ctx, d.Path, false)
		if err != nil {
			return res, err
		}
		res.Saved = append(res.Saved, r.Path)
	}
	return res, nil
}

// docSet adapts the open documents to autosave.DirtyDocs.
type docSet struct{ a *App }

func (s docSet) Dirty() []autosave.Doc {
	s.a.mu.Lock()
	docs := make([]*Doc, 0, len(s.a.docs))
	for _, d := range s.a.docs {
		docs = append(docs, d)
	}
	s.a.mu.Unlock()
	var out []autosave.Doc
	for _, d := range docs {
		d.mu.Lock()
		if d.buf.Modified() {
			out = append(out, autosave.Doc{Path: d.path, Content: d.buf.Bytes(), Version: d.buf.Version(), Instance: d.gen})
		}
		d.mu.Unlock()
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out
}

// Save writes the snapshot. Snapshots carrying an Instance are only
// written while that very document instance is open (closing runs under
// the save lock, so it cannot happen during the write).
func (s docSet) Save(_ context.Context, doc autosave.Doc) error {
	s.a.mu.Lock()
	d := s.a.docs[doc.Path]
	s.a.mu.Unlock()
	if doc.Instance != 0 && (d == nil || d.gen != doc.Instance) {
		return autosave.ErrDocGone
	}
	if d != nil && !doc.Force {
		// Never overwrite a change made by another program (D141). This
		// runs under the save lock, like every other Tiegel writer of
		// document files, so the stamp cannot be stale here.
		d.mu.Lock()
		known := d.disk
		d.mu.Unlock()
		changed, cur, data, err := checkDisk(doc.Path, known)
		if err != nil {
			return fmt.Errorf("check %s: %w", doc.Path, err)
		}
		if changed && !(cur.exists && bytes.Equal(data, doc.Content)) {
			kind := externalKind(known, cur)
			d.mu.Lock()
			s.a.flagExternalLocked(d, kind, cur, data, "save", "")
			d.mu.Unlock()
			return &externalChangeError{path: s.a.displayPath(doc.Path), kind: kind}
		}
	}
	fi, err := writeFileAtomic(doc.Path, doc.Content)
	if err != nil {
		return err
	}
	if d == nil {
		return nil
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	s.a.setDiskLocked(d, stampOf(fi, doc.Content))
	if d.buf.Version() != doc.Version {
		return nil // changed while saving: stays modified
	}
	if !bytes.Equal(d.buf.Bytes(), doc.Content) {
		// A file.beforeSave filter changed the content (e.g. a formatter
		// or .editorconfig): apply only the differences, so carets, folds
		// and LSP changes outside them stay put (D245).
		if err := applyFiltered(d.buf, doc.Content); err != nil {
			return fmt.Errorf("updating buffer after filter: %w", err)
		}
	}
	d.buf.MarkSaved()
	return nil
}

// applyFiltered changes the buffer content to data as one undo step made
// of minimal edits: one per differing line region (line diff), narrowed to
// the differing bytes within it. Edits are applied back to front so
// earlier offsets stay valid.
func applyFiltered(b *buffer.Buffer, data []byte) error {
	cur := b.Bytes()
	if bytes.Equal(cur, data) {
		return nil
	}
	oldLines, newLines := gitcli.SplitLines(cur), gitcli.SplitLines(data)
	oldOff, newOff := lineOffsets(oldLines), lineOffsets(newLines)
	changes := gitcli.DiffLines(oldLines, newLines)
	b.BreakUndo()
	b.BeginGroup(buffer.GroupOther, nil)
	defer func() {
		b.EndGroup(nil)
		b.BreakUndo()
	}()
	for i := len(changes) - 1; i >= 0; i-- {
		c := changes[i]
		start := oldOff[c.OldStart]
		o := cur[start:oldOff[c.OldEnd]]
		n := data[newOff[c.NewStart]:newOff[c.NewEnd]]
		p, s := commonAffixes(o, n)
		if _, err := b.Replace(start+p, len(o)-p-s, n[p:len(n)-s]); err != nil {
			return err
		}
	}
	return nil
}

// lineOffsets returns the start offset of every line plus the total
// length.
func lineOffsets(lines []string) []int {
	off := make([]int, len(lines)+1)
	for i, l := range lines {
		off[i+1] = off[i] + len(l)
	}
	return off
}

// commonAffixes returns the lengths of the common prefix and suffix of a
// and b (not overlapping), keeping UTF-8 characters and CRLF pairs whole.
func commonAffixes(a, b []byte) (p, s int) {
	n := min(len(a), len(b))
	for p < n && a[p] == b[p] {
		p++
	}
	for s < n-p && a[len(a)-1-s] == b[len(b)-1-s] {
		s++
	}
	for p > 0 && (p < len(a) && !utf8.RuneStart(a[p]) || a[p-1] == '\r') {
		p--
	}
	for s > 0 && (!utf8.RuneStart(a[len(a)-s]) || a[len(a)-s] == '\n' && len(a)-s > 0 && a[len(a)-s-1] == '\r') {
		s--
	}
	return p, s
}

// reload replaces the content of an open document with its file (also
// when modified) and marks it saved (used after history.restore, under
// the save lock).
func (a *App) reload(abs string) {
	a.mu.Lock()
	d := a.docs[abs]
	a.mu.Unlock()
	if d == nil {
		return
	}
	st, data, err := readDisk(abs)
	if err != nil {
		return
	}
	d.mu.Lock()
	a.reloadLocked(d, data, st, "api")
	d.mu.Unlock()
}

// writeFileAtomic writes data to a temporary file next to path and renames
// it over path, keeping the permissions of an existing file. It returns
// the file info of the written file, taken before the rename so that it
// describes exactly this content (a later stat could already see another
// program's write).
func writeFileAtomic(path string, data []byte) (os.FileInfo, error) {
	mode := os.FileMode(0o644)
	if st, err := os.Stat(path); err == nil {
		mode = st.Mode().Perm()
	}
	f, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".tiegel-*")
	if err != nil {
		return nil, fmt.Errorf("write %s: %w", path, err)
	}
	tmp := f.Name()
	_, werr := f.Write(data)
	if werr == nil {
		werr = f.Sync()
	}
	cerr := f.Close()
	if werr == nil {
		werr = cerr
	}
	if werr == nil {
		werr = os.Chmod(tmp, mode)
	}
	var fi os.FileInfo
	if werr == nil {
		fi, werr = os.Lstat(tmp)
	}
	if werr == nil {
		werr = os.Rename(tmp, path)
	}
	if werr != nil {
		_ = os.Remove(tmp)
		return nil, fmt.Errorf("write %s: %w", path, werr)
	}
	return fi, nil
}

// historySink records saves in the Local History of the open project; without
// a project nothing is recorded.
type historySink struct{ a *App }

func (h historySink) Save(path string, content []byte, label string) (history.Revision, bool, error) {
	if h.a.ProjectRoot() == "" {
		return history.Revision{}, false, nil
	}
	st, err := h.a.projectHistory()
	if err != nil {
		return history.Revision{}, false, err
	}
	return st.Save(path, content, label)
}

func (a *App) historyList(_ context.Context, p PathParams) ([]history.Revision, error) {
	abs, err := a.resolvePath(p.Path)
	if err != nil {
		return nil, err
	}
	st, err := a.projectHistory()
	if err != nil {
		return nil, err
	}
	revs, err := st.List(abs)
	if errors.Is(err, history.ErrNotFound) {
		return []history.Revision{}, nil
	}
	if err != nil {
		return nil, Errorf(CodeFailed, "history: %w", err)
	}
	return revs, nil
}

// RevisionParams address one Local History revision.
type RevisionParams struct {
	Path string `json:"path"`
	ID   uint64 `json:"id"`
}

// RevisionContent is the result of "history.content".
type RevisionContent struct {
	Revision history.Revision `json:"revision"`
	Text     string           `json:"text"`
}

func (a *App) revision(p RevisionParams) (*history.Store, string, history.Revision, error) {
	abs, err := a.resolvePath(p.Path)
	if err != nil {
		return nil, "", history.Revision{}, err
	}
	st, err := a.projectHistory()
	if err != nil {
		return nil, "", history.Revision{}, err
	}
	rev, err := st.Get(abs, p.ID)
	if err != nil {
		code := CodeFailed
		if errors.Is(err, history.ErrNotFound) {
			code = CodeNotFound
		}
		return nil, "", history.Revision{}, Errorf(code, "revision %d of %s: %w", p.ID, p.Path, err)
	}
	return st, abs, rev, nil
}

func (a *App) historyContent(_ context.Context, p RevisionParams) (RevisionContent, error) {
	st, _, rev, err := a.revision(p)
	if err != nil {
		return RevisionContent{}, err
	}
	data, err := st.Content(rev)
	if err != nil {
		return RevisionContent{}, Errorf(CodeFailed, "history: %w", err)
	}
	return RevisionContent{Revision: rev, Text: string(data)}, nil
}

func (a *App) historyRestore(_ context.Context, p RevisionParams) (history.Revision, error) {
	st, abs, _, err := a.revision(p)
	if err != nil {
		return history.Revision{}, err
	}
	var rev history.Revision
	// Under the save lock: a save in flight must not overwrite the
	// restored file with an older snapshot after the buffer was reloaded.
	err = a.saver.Exclusive(func() error {
		var err error
		if rev, err = st.Restore(abs, p.ID); err != nil {
			return Errorf(CodeFailed, "restore: %w", err)
		}
		a.reload(abs)
		return nil
	})
	return rev, err
}

// Exclusive runs fn while no document is being saved (it holds the save
// lock of the autosave manager). Modules that write files of possibly open
// documents and reload them (git discard/checkout/resolve) use it, so a
// save in flight cannot overwrite their result with an older snapshot. fn
// must not save documents (doc.save, autosave) – that would deadlock.
func (a *App) Exclusive(fn func() error) error { return a.saver.Exclusive(fn) }
