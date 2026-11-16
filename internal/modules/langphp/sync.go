package langphp

import (
	"context"
	"sort"
	"time"

	"github.com/ape-dev-de/tiegel/internal/api"
	"github.com/ape-dev-de/tiegel/internal/core/bus"
	"github.com/ape-dev-de/tiegel/internal/lsp/protocol"
)

// syncTimeout bounds a single sync notification.
const syncTimeout = 5 * time.Second

func syncCtx() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), syncTimeout)
}

// onDoc handles doc.* events in publish order.
func (m *Module) onDoc(e bus.Event) {
	s := m.current()
	if s == nil {
		return
	}
	switch ev := e.Payload.(type) {
	case api.DocState:
		if e.Topic != api.TopicDocOpened || ev.Language != languageID {
			return
		}
		d, err := m.app.Document(ev.Path)
		if err != nil {
			return
		}
		m.syncMu.Lock()
		m.openLocked(s, d.Snapshot())
		m.syncMu.Unlock()
	case api.DocEditEvent:
		if ev.Language != languageID {
			return
		}
		m.edited(s, ev)
	case api.DocCloseResult:
		abs, err := m.app.ResolvePath(ev.Path)
		if err != nil {
			return
		}
		uri := protocol.URIFromPath(abs)
		m.syncMu.Lock()
		if _, _, ok := s.client.Document(uri); ok {
			ctx, cancel := syncCtx()
			_ = s.client.DidClose(ctx, uri)
			cancel()
		}
		m.syncMu.Unlock()
		m.diagMu.Lock()
		delete(m.diags, abs)
		m.diagMu.Unlock()
	}
}

// openLocked sends didOpen for a snapshot (syncMu held). A document the
// server already has is left alone: the doc.edited events that follow in the
// same (ordered) subscription bring it up to date incrementally, and a gap
// is repaired by edited.
func (m *Module) openLocked(s *server, d api.DocSnapshot) {
	uri := protocol.URIFromPath(d.File)
	ctx, cancel := syncCtx()
	defer cancel()
	if _, _, ok := s.client.Document(uri); ok {
		return
	}
	if err := s.client.DidOpen(ctx, uri, languageID, int(d.Version), string(d.Text)); err != nil {
		m.app.Logger().Warn("lang-php: didOpen", "file", d.File, "err", err)
	}
}

// edited forwards one buffer change incrementally.
func (m *Module) edited(s *server, ev api.DocEditEvent) {
	uri := protocol.URIFromPath(ev.File)
	m.syncMu.Lock()
	defer m.syncMu.Unlock()
	_, ver, ok := s.client.Document(uri)
	if !ok || ev.Version <= uint64(ver) {
		return // not synced (it will be opened) or already contained
	}
	ctx, cancel := syncCtx()
	defer cancel()
	if ev.Version == uint64(ver)+1 {
		r := protocol.Range{
			Start: protocol.Position{Line: ev.Range.Start.Line, Character: ev.Range.Start.Character},
			End:   protocol.Position{Line: ev.Range.End.Line, Character: ev.Range.End.Character},
		}
		if err := s.client.DidChange(ctx, uri, int(ev.Version), []protocol.TextDocumentContentChangeEvent{{Range: &r, Text: ev.Text}}); err == nil {
			return
		}
	}
	// A gap (or a failed change): resync with the full text.
	if d, err := m.app.Document(ev.File); err == nil {
		m.resyncLocked(ctx, s, d.Snapshot())
	}
}

// resyncLocked replaces the server's copy with the snapshot.
func (m *Module) resyncLocked(ctx context.Context, s *server, d api.DocSnapshot) {
	uri := protocol.URIFromPath(d.File)
	_, ver, ok := s.client.Document(uri)
	switch {
	case !ok:
		_ = s.client.DidOpen(ctx, uri, languageID, int(d.Version), string(d.Text))
	case uint64(ver) != d.Version:
		_ = s.client.DidChange(ctx, uri, int(d.Version), []protocol.TextDocumentContentChangeEvent{{Text: string(d.Text)}})
	}
}

// onFilesChanged forwards renames and moves to the server
// (workspace/didRenameFiles, if it registered for them). The open
// documents are re-synchronised by the doc.closed/doc.opened events of the
// rename.
func (m *Module) onFilesChanged(e bus.Event) {
	ev, ok := e.Payload.(api.FilesChangedEvent)
	s := m.current()
	if !ok || s == nil {
		return
	}
	var files []protocol.FileRename
	for _, c := range ev.Changes {
		if c.Kind == api.FileRenamed && c.Source == api.FileSourceTiegel {
			files = append(files, protocol.FileRename{OldURI: protocol.URIFromPath(c.OldFile), NewURI: protocol.URIFromPath(c.File)})
		}
	}
	ctx, cancel := syncCtx()
	defer cancel()
	if _, err := s.client.DidRenameFiles(ctx, files); err != nil {
		m.app.Logger().Warn("lang-php: didRenameFiles", "err", err)
	}
}

func (m *Module) onSaved(e bus.Event) {
	ev, ok := e.Payload.(api.FileSavedEvent)
	s := m.current()
	if !ok || s == nil {
		return
	}
	uri := protocol.URIFromPath(ev.File)
	m.syncMu.Lock()
	defer m.syncMu.Unlock()
	if _, _, open := s.client.Document(uri); !open {
		return
	}
	if d, err := m.app.Document(ev.File); err == nil {
		ctx, cancel := syncCtx()
		m.resyncLocked(ctx, s, d.Snapshot())
		_ = s.client.DidSave(ctx, uri)
		cancel()
	}
}

// Diagnostic is one finding of the language server. Start/End are byte
// offsets, Line/Col 1-based (Col in characters).
type Diagnostic struct {
	Severity string `json:"severity"`
	Message  string `json:"message"`
	Source   string `json:"source,omitempty"`
	Code     string `json:"code,omitempty"`
	Start    int    `json:"start"`
	End      int    `json:"end"`
	Line     int    `json:"line"`
	Col      int    `json:"col"`
	EndLine  int    `json:"endLine"`
	EndCol   int    `json:"endCol"`
}

// Diagnostics is the payload of lsp.diagnostics and an entry of the
// "lsp.diagnostics" method result.
type Diagnostics struct {
	Path string `json:"path"`
	File string `json:"file"`
	// Version is the document version the server diagnosed.
	Version     uint64       `json:"version"`
	Diagnostics []Diagnostic `json:"diagnostics"`
}

// SeverityName returns the API name of an LSP severity.
func SeverityName(s protocol.DiagnosticSeverity) string {
	switch s {
	case protocol.SeverityError:
		return "error"
	case protocol.SeverityWarning:
		return "warning"
	case protocol.SeverityInformation:
		return "information"
	case protocol.SeverityHint:
		return "hint"
	}
	return "error" // LSP: missing severity is up to the client
}

// textPos converts an LSP position in text to API coordinates.
func textPos(text []byte, p protocol.Position) api.TextPos {
	off, err := protocol.OffsetOf(string(text), p)
	if err != nil {
		off = len(text)
	}
	return api.PosAt(text, off)
}

func textRange(text []byte, r protocol.Range) api.TextRange {
	return api.TextRange{Start: textPos(text, r.Start), End: textPos(text, r.End)}
}

func (m *Module) onDiagnostics(s *server, p protocol.PublishDiagnosticsParams) {
	if s.closed.Load() {
		return
	}
	abs, err := p.URI.Path()
	if err != nil {
		return
	}
	text, ver, ok := s.client.Document(p.URI)
	var data []byte
	if ok {
		data = []byte(text)
	} else if data, _, err = m.app.ReadText(abs); err != nil {
		data = nil
	}
	if p.Version != nil {
		ver = *p.Version
	}
	ev := Diagnostics{Path: m.app.DisplayPath(abs), File: abs, Version: uint64(max(ver, 0)), Diagnostics: []Diagnostic{}}
	for _, d := range p.Diagnostics {
		r := textRange(data, d.Range)
		dd := Diagnostic{
			Severity: SeverityName(d.Severity), Message: d.Message, Source: d.Source,
			Start: r.Start.Offset, End: r.End.Offset, Line: r.Start.Line, Col: r.Start.Col, EndLine: r.End.Line, EndCol: r.End.Col,
		}
		if d.Code != nil {
			dd.Code = d.Code.Value
		}
		ev.Diagnostics = append(ev.Diagnostics, dd)
	}
	m.diagMu.Lock()
	m.diags[abs] = ev
	m.app.Bus().Publish(api.TopicLSPDiagnostics, ev)
	m.diagMu.Unlock()
}

func (m *Module) clearDiags() {
	m.diagMu.Lock()
	m.diags = map[string]Diagnostics{}
	m.diagMu.Unlock()
}

// DiagnosticsParams are the params of "lsp.diagnostics".
type DiagnosticsParams struct {
	// Path selects one document; empty returns all.
	Path string `json:"path,omitempty"`
}

func (m *Module) lspDiagnostics(_ context.Context, p DiagnosticsParams) ([]Diagnostics, error) {
	out := []Diagnostics{}
	var abs string
	if p.Path != "" {
		var err error
		if abs, err = m.app.ResolvePath(p.Path); err != nil {
			return nil, err
		}
	}
	m.diagMu.Lock()
	for f, d := range m.diags {
		if abs == "" || f == abs {
			out = append(out, d)
		}
	}
	m.diagMu.Unlock()
	sort.Slice(out, func(i, j int) bool { return out[i].File < out[j].File })
	return out, nil
}

// ensureSync returns the snapshot of an open PHP document after making sure
// the server has exactly this version.
func (m *Module) ensureSync(s *server, path string) (api.DocSnapshot, protocol.DocumentURI, error) {
	d, err := m.app.Document(path)
	if err != nil {
		return api.DocSnapshot{}, "", err
	}
	snap := d.Snapshot()
	if snap.Language != languageID {
		return snap, "", api.Errorf(api.CodeInvalidParams, "%s is not a PHP document (language %s)", path, snap.Language)
	}
	m.syncMu.Lock()
	ctx, cancel := syncCtx()
	m.resyncLocked(ctx, s, snap)
	cancel()
	m.syncMu.Unlock()
	return snap, protocol.URIFromPath(snap.File), nil
}
