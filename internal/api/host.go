package api

import (
	"bytes"
	"os"
	"sort"
	"unicode/utf8"

	"github.com/ape-dev-de/tiegel/internal/core/settings"
	"github.com/ape-dev-de/tiegel/internal/editor/buffer"
)

// This file holds the in-process accessors that modules and the GUI use.

// SettingInterpreter stores the selected interpreter ID (project settings,
// or global settings without a project).
const SettingInterpreter = "php-runtime.interpreter"

// SettingInterpreterLegacy is the key SettingInterpreter had before the
// settings moved into the php-runtime namespace (D190); it is still read
// as fallback.
const SettingInterpreterLegacy = "php.interpreter"

// SettingComposeFiles lists the compose files of the project when they are
// not in the project root (absolute or relative to it; the first file's
// folder is the compose project directory). Declared by the docker module,
// read by docker and php-runtime (D523); empty means the project root's
// compose files.
const SettingComposeFiles = "docker.composeFiles"

// SelectedInterpreter returns the interpreter ID stored in s (new key,
// then the legacy key), or "".
func SelectedInterpreter(s *settings.Store) string {
	if s == nil {
		return ""
	}
	if v, ok, err := settings.Lookup[string](s, SettingInterpreter); ok && err == nil {
		return v
	}
	return settings.Get(s, SettingInterpreterLegacy, "")
}

// SettingPHPVersionOverride stores the PHP version override (written by
// php-features with php.setVersionOverride, read by php-runtime's
// php.version).
const SettingPHPVersionOverride = "php.versionOverride"

// Options returns the options the App was created with (ConfigDir filled).
func (a *App) Options() Options { return a.opts }

// ResolvePath makes p absolute: relative paths are taken relative to the
// project root, or to the working directory without a project.
func (a *App) ResolvePath(p string) (string, error) { return a.resolvePath(p) }

// DisplayPath returns abs relative to the project root (slash separated) if
// it lies inside, otherwise abs itself.
func (a *App) DisplayPath(abs string) string { return a.displayPath(abs) }

// RequireProject returns the root of the open project or a CodeNoProject
// error.
func (a *App) RequireProject() (string, error) {
	pr, err := a.requireProject()
	if err != nil {
		return "", err
	}
	return pr.root, nil
}

// SettingsStore returns the project settings, or the global ones without a
// project.
func (a *App) SettingsStore() *settings.Store { return a.settingsStore() }

func (a *App) settingsStore() *settings.Store {
	if ps := a.ProjectSettings(); ps != nil {
		return ps
	}
	return a.global
}

// Document returns the open document for path (CodeNotFound if it is not
// open). The GUI edits its Buffer directly while holding the document lock;
// see Doc for the threading rule.
func (a *App) Document(path string) (*Doc, error) { return a.doc(path) }

// DocSnapshot is a copy of an open document's state and text.
type DocSnapshot struct {
	Path     string
	File     string
	Language string
	Version  uint64
	Modified bool
	Text     []byte
}

// Snapshot copies the document's text and state.
func (d *Doc) Snapshot() DocSnapshot {
	d.mu.Lock()
	defer d.mu.Unlock()
	return DocSnapshot{
		Path: d.app.displayPath(d.path), File: d.path, Language: string(d.lang),
		Version: d.buf.Version(), Modified: d.buf.Modified(), Text: d.buf.Bytes(),
	}
}

// Documents returns snapshots of all open documents sorted by path.
func (a *App) Documents() []DocSnapshot {
	a.mu.Lock()
	docs := make([]*Doc, 0, len(a.docs))
	for _, d := range a.docs {
		docs = append(docs, d)
	}
	a.mu.Unlock()
	out := make([]DocSnapshot, 0, len(docs))
	for _, d := range docs {
		out = append(out, d.Snapshot())
	}
	sort.Slice(out, func(i, j int) bool { return out[i].File < out[j].File })
	return out
}

// ReadText returns the content of path: the open document's buffer if it is
// open (open=true), otherwise the file on disk.
func (a *App) ReadText(path string) (text []byte, open bool, err error) {
	abs, err := a.resolvePath(path)
	if err != nil {
		return nil, false, err
	}
	a.mu.Lock()
	d := a.docs[abs]
	a.mu.Unlock()
	if d != nil {
		return d.Snapshot().Text, true, nil
	}
	data, err := os.ReadFile(abs)
	if err != nil {
		return nil, false, notFoundOr(err, "read %s", path)
	}
	return data, false, nil
}

// EditDocument replaces bytes [start, end) of an open document with text as
// one undo step. version, if non-zero, must match. Events follow from the
// buffer's OnEdit listener.
func (a *App) EditDocument(path string, version uint64, start, end int, text string) (DocState, error) {
	return a.edit(path, nonZero(version), func(b *buffer.Buffer) error {
		if start < 0 || end < start || end > b.Len() {
			return Errorf(CodeInvalidParams, "range [%d,%d) out of bounds (document has %d bytes)", start, end, b.Len())
		}
		if string(b.Slice(start, end)) == text {
			return nil
		}
		b.BreakUndo()
		if _, err := b.Replace(start, end-start, []byte(text)); err != nil {
			return Errorf(CodeInvalidParams, "replace: %w", err)
		}
		b.BreakUndo()
		return nil
	})
}

// TextPos is a position in API conventions with all forms filled: Offset is
// the 0-based byte offset, Line and Col are 1-based (Col in characters).
type TextPos struct {
	Offset int `json:"offset"`
	Line   int `json:"line"`
	Col    int `json:"col"`
}

// TextRange is a range of TextPos.
type TextRange struct {
	Start TextPos `json:"start"`
	End   TextPos `json:"end"`
}

// ByteOffset resolves p against text (see Pos).
func (p Pos) ByteOffset(text []byte) (int, error) {
	if p.Line > 0 {
		start, ok := lineStart(text, p.Line-1)
		if !ok {
			return 0, Errorf(CodeInvalidParams, "line %d out of range (document has %d lines)", p.Line, bytes.Count(text, []byte{'\n'})+1)
		}
		end := bytes.IndexByte(text[start:], '\n')
		line := text[start:]
		if end >= 0 {
			line = text[start : start+end]
		}
		col := p.Col - 1
		if col < 0 {
			col = 0
		}
		return start + buffer.ByteColFromRunes(line, col), nil
	}
	if p.Offset < 0 || p.Offset > len(text) {
		return 0, Errorf(CodeInvalidParams, "offset %d out of range (document has %d bytes)", p.Offset, len(text))
	}
	return p.Offset, nil
}

// lineStart returns the byte offset of 0-based line i.
func lineStart(text []byte, i int) (int, bool) {
	off := 0
	for ; i > 0; i-- {
		j := bytes.IndexByte(text[off:], '\n')
		if j < 0 {
			return 0, false
		}
		off += j + 1
	}
	return off, true
}

// PosAt converts a byte offset into a TextPos (clamped to the text).
func PosAt(text []byte, off int) TextPos {
	if off < 0 {
		off = 0
	}
	if off > len(text) {
		off = len(text)
	}
	ls := bytes.LastIndexByte(text[:off], '\n') + 1
	return TextPos{Offset: off, Line: bytes.Count(text[:ls], []byte{'\n'}) + 1, Col: utf8.RuneCount(text[ls:off]) + 1}
}
