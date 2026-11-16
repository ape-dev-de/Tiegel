// Package editorview is Tiegel's code editor widget for Gio. It renders
// and edits a buffer.Buffer through the UI-independent editor model
// (internal/editor/{buffer,cursor,fold,search}) and adds everything that
// needs a screen: virtual rendering of the visible lines only, scrolling,
// mouse and keyboard input including IME, the clipboard, the gutter with
// line numbers, fold arrows and marker slots, syntax colors, diagnostics,
// search and bracket highlights.
//
// The widget follows the component convention of package ui:
//
//	ed := editorview.New(buffer.NewString(src))
//	for { ev, ok := ed.Update(gtx); if !ok { break }; … } // optional
//	ed.Layout(gtx, th)
//
// Keyboard shortcuts come from a keymap.Keymap (data only); the widget
// translates Gio key events into keymap chords and runs the bound
// keymap.Action. Actions the widget cannot perform itself (find, replace,
// go to line, save) are reported as ActionEvent.
package editorview

import (
	"image"
	"runtime"
	"slices"
	"time"

	"gioui.org/io/event"
	"gioui.org/layout"

	"github.com/ape-dev-de/tiegel/internal/editor/buffer"
	"github.com/ape-dev-de/tiegel/internal/editor/cursor"
	"github.com/ape-dev-de/tiegel/internal/editor/fold"
	"github.com/ape-dev-de/tiegel/internal/editor/highlight"
	"github.com/ape-dev-de/tiegel/internal/editor/keymap"
	"github.com/ape-dev-de/tiegel/internal/editor/search"
	"github.com/ape-dev-de/tiegel/internal/ui/atoms"
)

// LineComments maps language IDs (as used by core/langdetect) to their
// line comment prefix. Hosts set Editor.LineComment from it.
var LineComments = map[string]string{
	"php": "//", "javascript": "//", "typescript": "//", "go": "//", "css": "",
	"json": "", "xml": "", "html": "", "yaml": "#", "env": "#", "shell": "#",
	"dockerfile": "#", "ini": ";", "sql": "--", "twig": "", "makefile": "#",
	"python": "#", "toml": "#", "markdown": "",
}

// Editor is the code editor widget. Create it with New; the zero value is
// not usable. Configuration fields may be changed between frames.
type Editor struct {
	// Keymap maps key chords to actions. New sets the default keymap for
	// the running OS (keymap.ForOS).
	Keymap *keymap.Keymap
	// Highlighter colors the visible text; nil draws plain text.
	Highlighter highlight.Highlighter
	// TabWidth is the display width of a tab and the indent size (default
	// 4).
	TabWidth int
	// InsertSpaces makes Tab insert TabWidth spaces instead of a tab.
	InsertSpaces bool
	// LineComment is the line comment prefix used by the toggle comment
	// action, e.g. "//" for PHP or "#" for YAML (see LineComments).
	// Empty disables the action.
	LineComment string
	// ReadOnly disables all modifications; selection and copy still work.
	ReadOnly bool
	// AutoClose inserts closing brackets and quotes (default true).
	AutoClose bool

	buf   *buffer.Buffer
	cur   *cursor.Model
	folds *fold.Model
	unsub func()

	markers MarkerProvider
	diags   []Diagnostic
	query   *search.Query

	events []Event
	stale  int // events at the queue head that survived one Layout

	focused    bool
	blinkStart time.Time
	scroll     image.Point
	scrollCare bool
	maxCols    int

	geo   geometry
	met   metrics
	cache lineCache

	ptr   pointerState
	ime   imeState
	brk   bracketCache
	srch  searchCache
	spans []highlight.Span

	vis      []visLine
	lineText []byte
	shp      shapeScratch
	filters  []event.Filter

	filtersFor     *keymap.Keymap
	filtersVersion uint64

	expand   []cursor.State // selection expand history
	lineClip string         // clipboard text copied as whole lines
	scratch  []byte
}

// New returns an editor for b with one caret at the start. Call Close
// when the editor is discarded to detach it from the buffer.
func New(b *buffer.Buffer) *Editor {
	e := &Editor{
		Keymap:       keymap.ForOS(runtime.GOOS),
		TabWidth:     4,
		InsertSpaces: true,
		LineComment:  "//",
		AutoClose:    true,
		buf:          b,
		cur:          cursor.New(b),
		folds:        fold.New(b.LineCount()),
	}
	e.cur.Lines = e.folds
	e.cache.init()
	e.ime.reset()
	e.unsub = b.OnEdit(e.onEdit)
	return e
}

// Close detaches the editor from its buffer.
func (e *Editor) Close() {
	if e.unsub != nil {
		e.unsub()
		e.unsub = nil
	}
	e.cur.Close()
}

// Buffer returns the edited buffer.
func (e *Editor) Buffer() *buffer.Buffer { return e.buf }

// Cursor returns the cursor model. Changing selections through it is
// fine; call RevealCaret afterwards to scroll them into view.
func (e *Editor) Cursor() *cursor.Model { return e.cur }

// Folds returns the folding model.
func (e *Editor) Folds() *fold.Model { return e.folds }

// SetFoldRanges replaces the foldable ranges (e.g. from Tree-sitter or
// highlight.BraceFolds), keeping the state of unchanged ranges.
func (e *Editor) SetFoldRanges(rs []fold.Range) {
	e.folds.SetLineCount(e.buf.LineCount())
	e.folds.SetRanges(rs)
}

// SetGutterMarkers installs the provider for gutter markers (breakpoints,
// VCS changes, icons). It is queried for the visible lines every frame;
// nil removes all markers.
func (e *Editor) SetGutterMarkers(p MarkerProvider) { e.markers = p }

// SetDiagnostics replaces the diagnostics drawn as underlines. Their
// ranges follow subsequent edits until the next call.
func (e *Editor) SetDiagnostics(d []Diagnostic) {
	e.diags = slices.Clone(d)
	slices.SortFunc(e.diags, func(a, b Diagnostic) int { return a.Start - b.Start })
}

// Diagnostics returns the current diagnostics.
func (e *Editor) Diagnostics() []Diagnostic { return slices.Clone(e.diags) }

// DiagnosticsAt returns the diagnostics whose range contains off (for
// hover popups).
func (e *Editor) DiagnosticsAt(off int) []Diagnostic {
	var out []Diagnostic
	for _, d := range e.diags {
		if d.Start <= off && off <= d.End {
			out = append(out, d)
		}
	}
	return out
}

// SetSearch sets the query whose matches are highlighted and used by the
// find next/previous actions; nil clears it.
func (e *Editor) SetSearch(q *search.Query) {
	e.query = q
	e.srch = searchCache{}
}

// Search returns the current query.
func (e *Editor) Search() *search.Query { return e.query }

// RevealCaret scrolls the primary caret into view on the next Layout and
// expands folds hiding any caret.
func (e *Editor) RevealCaret() {
	e.revealCarets()
	e.scrollCare = true
}

// SetCaret places a single caret at off and scrolls it into view.
func (e *Editor) SetCaret(off int) {
	e.cur.SetCaret(off)
	e.RevealCaret()
}

// ScrollOffset returns the scroll position in pixels.
func (e *Editor) ScrollOffset() image.Point { return e.scroll }

// SetScrollOffset sets the scroll position in pixels; it is clamped on
// the next Layout.
func (e *Editor) SetScrollOffset(p image.Point) {
	e.scroll = p
	e.scrollCare = false
}

// Focused reports whether the editor had the keyboard focus during the
// last frame.
func (e *Editor) Focused() bool { return e.focused }

// Update returns the next event since the last call. Call it until it
// returns false, before Layout. Events that are not collected before the
// following Layout are dropped.
func (e *Editor) Update(gtx layout.Context) (Event, bool) {
	e.processInput(gtx)
	if len(e.events) == 0 {
		return nil, false
	}
	ev := e.events[0]
	e.events = e.events[1:]
	if e.stale > 0 {
		e.stale--
	}
	return ev, true
}

func (e *Editor) emit(ev Event) {
	const maxPending = 1024
	if len(e.events) >= maxPending {
		e.events = e.events[1:]
		if e.stale > 0 {
			e.stale--
		}
	}
	e.events = append(e.events, ev)
}

// onEdit runs for every buffer change, including undo/redo and edits by
// others (replace all, formatting).
func (e *Editor) onEdit(ed buffer.Edit) {
	e.folds.ApplyEdit(ed)
	if e.folds.LineCount() != e.buf.LineCount() {
		e.folds.SetLineCount(e.buf.LineCount())
	}
	e.cache.applyEdit(ed)
	for i := range e.diags {
		e.diags[i].Start = mapOffset(e.diags[i].Start, ed)
		e.diags[i].End = mapOffset(e.diags[i].End, ed)
	}
	e.emit(ChangeEvent{Edit: ed})
}

// mapOffset maps off across an edit; offsets inside the replaced range
// move to its start.
func mapOffset(off int, e buffer.Edit) int {
	switch {
	case off <= e.StartByte:
		return off
	case off >= e.OldEndByte:
		return off + e.NewEndByte - e.OldEndByte
	default:
		return e.StartByte
	}
}

// revealCarets expands folds that hide a caret.
func (e *Editor) revealCarets() {
	s := e.buf.Snapshot()
	for _, sel := range e.cur.Selections() {
		if l := s.OffsetToPos(sel.Head).Line; e.folds.IsHidden(l) {
			e.folds.Reveal(l)
		}
	}
}

// Severity classifies a diagnostic.
type Severity uint8

// Diagnostic severities, most severe first.
const (
	SeverityError Severity = iota
	SeverityWarning
	SeverityInfo
	SeverityHint
)

// Diagnostic is a problem reported for a byte range of the document,
// drawn as a wavy underline.
type Diagnostic struct {
	Start, End int
	Severity   Severity
	Message    string
	Source     string
}

// Event is an event reported by Update.
type Event interface{ isEditorEvent() }

// ChangeEvent reports a change of the buffer (typing, undo, external
// edits). Hosts use it for dirty markers, re-parsing and LSP sync.
type ChangeEvent struct{ Edit buffer.Edit }

// FocusEvent reports that the editor gained or lost the keyboard focus.
// Losing focus triggers autosave in the host.
type FocusEvent struct{ Focused bool }

// GutterArea identifies a gutter column.
type GutterArea uint8

// Gutter columns from left to right.
const (
	GutterIcons GutterArea = iota
	GutterNumbers
	GutterChanges
	GutterFold
)

// GutterClickEvent reports a click into the gutter (e.g. to toggle a
// breakpoint). Fold arrow clicks are handled by the editor and reported
// with Area GutterFold.
type GutterClickEvent struct {
	Line   int // 0-based document line
	Area   GutterArea
	Mods   keymap.Mods
	Clicks int
}

// ActionEvent reports an action the editor does not execute itself (see
// keymap.External), e.g. keymap.Find.
type ActionEvent struct{ Action keymap.Action }

func (ChangeEvent) isEditorEvent()      {}
func (FocusEvent) isEditorEvent()       {}
func (GutterClickEvent) isEditorEvent() {}
func (ActionEvent) isEditorEvent()      {}

// MarkerKind selects how a gutter marker is drawn.
type MarkerKind uint8

// Marker kinds.
const (
	// MarkerBreakpoint is a filled circle in the icon column.
	MarkerBreakpoint MarkerKind = iota
	// MarkerBreakpointDisabled is a hollow circle.
	MarkerBreakpointDisabled
	// MarkerBreakpointConditional is a filled circle with a ring.
	MarkerBreakpointConditional
	// MarkerExecutionPoint marks the line where the debugger stopped; the
	// line background is tinted.
	MarkerExecutionPoint
	// MarkerGitAdded, MarkerGitModified and MarkerGitDeleted draw VCS
	// change bars in the change column.
	MarkerGitAdded
	MarkerGitModified
	MarkerGitDeleted
	// MarkerIcon draws Glyph in the icon column, colored by Tone.
	MarkerIcon
)

// Tone selects a semantic color for icon markers.
type Tone uint8

// Tones.
const (
	ToneMuted Tone = iota
	ToneAccent
	ToneInfo
	ToneSuccess
	ToneWarning
	ToneDanger
)

// Marker is one gutter marker of a line.
type Marker struct {
	Kind  MarkerKind
	Glyph *atoms.Glyph // MarkerIcon only
	Tone  Tone         // MarkerIcon only
}

// MarkerProvider supplies the gutter markers of a document line. It is
// called for the visible lines every frame and must be fast.
type MarkerProvider interface {
	Markers(line int) []Marker
}

// MarkerFunc adapts a function to MarkerProvider.
type MarkerFunc func(line int) []Marker

// Markers calls f.
func (f MarkerFunc) Markers(line int) []Marker { return f(line) }

// LineMarkers is a static MarkerProvider: document line → markers.
type LineMarkers map[int][]Marker

// Markers returns m[line].
func (m LineMarkers) Markers(line int) []Marker { return m[line] }
