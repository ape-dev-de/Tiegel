package terminal

import (
	"strconv"
	"strings"

	"github.com/ape-dev-de/tiegel/internal/run"
	"github.com/ape-dev-de/tiegel/internal/terminal/vt"
)

// Screen is a snapshot of lines of a session (see Session.Screen).
type Screen struct {
	ID   string `json:"id"`
	Cols int    `json:"cols"`
	Rows int    `json:"rows"`
	// History is the number of scrollback lines above the screen; line
	// History is the first screen row. Total = History + Rows.
	History int `json:"history"`
	Total   int `json:"total"`
	// Dropped counts lines discarded from the scrollback (the absolute
	// index of a line is stable while Dropped does not change).
	Dropped int `json:"dropped"`
	// Top is the index of Lines[0].
	Top   int          `json:"top"`
	Lines []ScreenLine `json:"lines"`
	// Cursor is relative to the screen (row 0 = line History).
	Cursor  vt.Cursor `json:"cursor"`
	Modes   vt.Modes  `json:"modes"`
	Title   string    `json:"title,omitempty"`
	Running bool      `json:"running"`
	// ExitCode is valid when Running is false.
	ExitCode int    `json:"exitCode"`
	Seq      uint64 `json:"seq"`
}

// ScreenLine is one line of a snapshot.
type ScreenLine struct {
	Index   int    `json:"index"`
	Text    string `json:"text"`
	Spans   []Span `json:"spans,omitempty"`
	Wrapped bool   `json:"wrapped,omitempty"`
	// Links are file locations in the line (columns, end exclusive).
	Links []Link `json:"links,omitempty"`
}

// Span is a styled run of a line. Colors are "" (default), "pN" (palette
// entry N, 0–255) or "#rrggbb".
type Span struct {
	Col       int    `json:"col"`
	Width     int    `json:"width"`
	Text      string `json:"text"`
	FG        string `json:"fg,omitempty"`
	BG        string `json:"bg,omitempty"`
	Bold      bool   `json:"bold,omitempty"`
	Faint     bool   `json:"faint,omitempty"`
	Italic    bool   `json:"italic,omitempty"`
	Underline bool   `json:"underline,omitempty"`
	Blink     bool   `json:"blink,omitempty"`
	Inverse   bool   `json:"inverse,omitempty"`
	Hidden    bool   `json:"hidden,omitempty"`
	Strike    bool   `json:"strike,omitempty"`
}

// Link is a file location in a line.
type Link struct {
	Col    int    `json:"col"`
	EndCol int    `json:"endCol"`
	Path   string `json:"path"`
	File   string `json:"file"`
	Line   int    `json:"line"`
	Column int    `json:"column,omitempty"`
}

// ColorString encodes c for Span.
func ColorString(c vt.Color) string {
	if i, ok := c.Index(); ok {
		return "p" + strconv.Itoa(int(i))
	}
	if r, g, b, ok := c.RGB(); ok {
		const hex = "0123456789abcdef"
		return string([]byte{'#', hex[r>>4], hex[r&15], hex[g>>4], hex[g&15], hex[b>>4], hex[b&15]})
	}
	return ""
}

// ParseColor decodes a Span color: palette index (or -1) and RGB.
func ParseColor(s string) (index int, r, g, b uint8, ok bool) {
	switch {
	case s == "":
		return -1, 0, 0, 0, false
	case s[0] == 'p':
		n, err := strconv.Atoi(s[1:])
		if err != nil || n < 0 || n > 255 {
			return -1, 0, 0, 0, false
		}
		return n, 0, 0, 0, true
	case s[0] == '#' && len(s) == 7:
		v, err := strconv.ParseUint(s[1:], 16, 32)
		if err != nil {
			return -1, 0, 0, 0, false
		}
		return -1, uint8(v >> 16), uint8(v >> 8), uint8(v), true
	}
	return -1, 0, 0, 0, false
}

func spanOf(s vt.Span) Span {
	a := s.Style.Attr
	return Span{Col: s.Col, Width: s.Width, Text: s.Text, FG: ColorString(s.Style.FG), BG: ColorString(s.Style.BG),
		Bold: a&vt.Bold != 0, Faint: a&vt.Faint != 0, Italic: a&vt.Italic != 0, Underline: a&vt.Underline != 0,
		Blink: a&vt.Blink != 0, Inverse: a&vt.Inverse != 0, Hidden: a&vt.Hidden != 0, Strike: a&vt.Strike != 0}
}

// ScreenOptions select the lines of a snapshot.
type ScreenOptions struct {
	// Top is the first line (absolute index); negative: the screen
	// (History). Count is the number of lines (0: Rows).
	Top, Count int
	// NoSpans leaves out the styled spans (text only).
	NoSpans bool
	// Linker finds file locations in the lines; nil: no links. The
	// session's working directory (OSC 7) and start directory are tried
	// before the linker's own directories.
	Linker *run.Linker
}

// Screen returns a snapshot of the session.
func (s *Session) Screen(o ScreenOptions) Screen {
	s.mu.Lock()
	t := s.term
	cols, rows := t.Size()
	sc := Screen{ID: s.info.ID, Cols: cols, Rows: rows, History: t.History(), Total: t.Total(), Dropped: t.Dropped(),
		Cursor: t.Cursor(), Modes: t.Modes(), Title: t.Title(), Running: s.info.Running, ExitCode: s.info.ExitCode, Seq: t.Seq()}
	top, count := o.Top, o.Count
	if top < 0 {
		top = sc.History
	}
	if count <= 0 {
		count = rows
	}
	top = max(0, min(top, sc.Total))
	count = min(count, sc.Total-top, 2000)
	sc.Top = top
	sc.Lines = make([]ScreenLine, count)
	colMaps := make([][]int, count)
	for i := range sc.Lines {
		idx := top + i
		l := t.Line(idx)
		text, cm := lineText(t, l)
		sc.Lines[i] = ScreenLine{Index: idx, Text: text, Wrapped: l.Wrapped}
		colMaps[i] = cm
		if !o.NoSpans {
			for _, sp := range t.Spans(idx) {
				sc.Lines[i].Spans = append(sc.Lines[i].Spans, spanOf(sp))
			}
		}
	}
	cwd, dir := t.Dir(), s.info.Dir
	s.mu.Unlock()

	// File checks happen outside the session lock (slow file systems).
	if o.Linker != nil {
		lk := *o.Linker
		lk.Dirs = append(nonEmpty(cwd, dir), lk.Dirs...)
		for i := range sc.Lines {
			for _, l := range lk.Links(sc.Lines[i].Text) {
				cm := colMaps[i]
				if l.Start >= len(cm) || l.End >= len(cm) {
					continue
				}
				sc.Lines[i].Links = append(sc.Lines[i].Links, Link{Col: cm[l.Start], EndCol: cm[l.End], Path: l.Path,
					File: l.File, Line: l.Line, Column: l.Column})
			}
		}
	}
	return sc
}

func nonEmpty(ss ...string) []string {
	var out []string
	for _, s := range ss {
		if s != "" {
			out = append(out, s)
		}
	}
	return out
}

// lineText returns the text of a line without trailing blanks and the
// column of every byte offset (len(text)+1 entries).
func lineText(t *vt.Terminal, l vt.Line) (string, []int) {
	var b strings.Builder
	var cols []int
	for c, cell := range l.Cells {
		if cell.Width == 0 {
			continue
		}
		g := t.Grapheme(cell)
		for range len(g) {
			cols = append(cols, c)
		}
		b.WriteString(g)
	}
	text := b.String()
	trimmed := strings.TrimRight(text, " ")
	cols = cols[:len(trimmed)]
	end := 0
	if n := len(cols); n > 0 {
		end = cols[n-1] + 1
		// The end column of a wide last character.
		last := l.Cells[cols[n-1]]
		if last.Width == 2 {
			end++
		}
	}
	cols = append(cols, end)
	return trimmed, cols
}
