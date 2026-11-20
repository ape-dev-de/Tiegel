package terminal

import (
	"regexp"
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
	ExitCode int `json:"exitCode"`
	// Bells counts BEL characters (a client flashes when it grows).
	Bells int `json:"bells,omitempty"`
	// LastExit is the exit code of the last command reported by shell
	// integration (OSC 133;D), -1 when unknown.
	LastExit int    `json:"lastExit"`
	Seq      uint64 `json:"seq"`
}

// ScreenLine is one line of a snapshot.
type ScreenLine struct {
	Index   int    `json:"index"`
	Text    string `json:"text"`
	Spans   []Span `json:"spans,omitempty"`
	Wrapped bool   `json:"wrapped,omitempty"`
	// Mark holds shell integration marks (vt.Mark bits: 1 prompt,
	// 2 input, 4 output, 8 command done).
	Mark int `json:"mark,omitempty"`
	// Links are file locations and URLs in the line (columns, end
	// exclusive).
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
	// UnderlineStyle is set for underlines other than single (2 double,
	// 3 curly, 4 dotted, 5 dashed); UL is the underline color.
	UnderlineStyle int    `json:"underlineStyle,omitempty"`
	UL             string `json:"ul,omitempty"`
	Overline       bool   `json:"overline,omitempty"`
	Blink          bool   `json:"blink,omitempty"`
	Inverse        bool   `json:"inverse,omitempty"`
	Hidden         bool   `json:"hidden,omitempty"`
	Strike         bool   `json:"strike,omitempty"`
	// URL is the target of a hyperlink (OSC 8).
	URL string `json:"url,omitempty"`
}

// Link is a file location or a URL in a line.
type Link struct {
	Col    int    `json:"col"`
	EndCol int    `json:"endCol"`
	Path   string `json:"path,omitempty"`
	File   string `json:"file,omitempty"`
	Line   int    `json:"line,omitempty"`
	Column int    `json:"column,omitempty"`
	// URL is set for web links (printed or OSC 8); File is empty then.
	URL string `json:"url,omitempty"`
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

func spanOf(t *vt.Terminal, s vt.Span) Span {
	a := s.Style.Attr
	sp := Span{Col: s.Col, Width: s.Width, Text: s.Text, FG: ColorString(s.Style.FG), BG: ColorString(s.Style.BG),
		Bold: a&vt.Bold != 0, Faint: a&vt.Faint != 0, Italic: a&vt.Italic != 0, Underline: a&vt.Underline != 0,
		Overline: a&vt.Overline != 0, Blink: a&vt.Blink != 0, Inverse: a&vt.Inverse != 0, Hidden: a&vt.Hidden != 0,
		Strike: a&vt.Strike != 0, UL: ColorString(s.Style.UL), URL: t.LinkURL(s.Style.Link)}
	if u := a.UnderlineStyle(); u > vt.ULSingle {
		sp.UnderlineStyle = int(u)
	}
	return sp
}

// MaxScreenLines bounds the lines of one snapshot.
const MaxScreenLines = 2000

// ScreenOptions select the lines of a snapshot.
type ScreenOptions struct {
	// Top is the first line (absolute index); negative: the screen
	// (History). Count is the number of lines (0: Rows).
	Top, Count int
	// NoSpans leaves out the styled spans (text only).
	NoSpans bool
	// URLs adds web links (printed http(s) URLs and OSC 8 hyperlinks).
	URLs bool
	// Linker finds file locations in the lines; nil: no file links. The
	// session's working directory (OSC 7) and start directory are tried
	// before the linker's own directories.
	Linker *run.Linker
}

// snapshot builds a Screen of t (the caller holds the lock of t). The
// returned column maps (byte offset → column per line) serve the link
// search outside the lock.
func snapshot(t *vt.Terminal, o ScreenOptions) (Screen, [][]int) {
	cols, rows := t.Size()
	sc := Screen{Cols: cols, Rows: rows, History: t.History(), Total: t.Total(), Dropped: t.Dropped(),
		Cursor: t.Cursor(), Modes: t.Modes(), Title: t.Title(), Bells: t.Bells(), LastExit: t.LastExit(), Seq: t.Seq()}
	top, count := o.Top, o.Count
	if top < 0 {
		top = sc.History
	}
	if count <= 0 {
		count = rows
	}
	top = max(0, min(top, sc.Total))
	count = max(0, min(count, sc.Total-top, MaxScreenLines))
	sc.Top = top
	sc.Lines = make([]ScreenLine, count)
	colMaps := make([][]int, count)
	for i := range sc.Lines {
		idx := top + i
		l := t.Line(idx)
		text, cm := lineText(t, l)
		sc.Lines[i] = ScreenLine{Index: idx, Text: text, Wrapped: l.Wrapped, Mark: int(l.Mark)}
		colMaps[i] = cm
		spans := t.Spans(idx)
		if !o.NoSpans {
			sc.Lines[i].Spans = make([]Span, 0, len(spans))
			for _, sp := range spans {
				sc.Lines[i].Spans = append(sc.Lines[i].Spans, spanOf(t, sp))
			}
		}
		if o.URLs {
			sc.Lines[i].Links = hyperlinks(t, spans)
		}
	}
	return sc, colMaps
}

// hyperlinks returns the OSC 8 links of a line (adjacent spans with the
// same target merged).
func hyperlinks(t *vt.Terminal, spans []vt.Span) []Link {
	var out []Link
	for _, s := range spans {
		u := t.LinkURL(s.Style.Link)
		if u == "" {
			continue
		}
		if n := len(out); n > 0 && out[n-1].URL == u && out[n-1].EndCol == s.Col {
			out[n-1].EndCol = s.Col + s.Width
			continue
		}
		out = append(out, Link{Col: s.Col, EndCol: s.Col + s.Width, URL: u})
	}
	return out
}

// urlRe matches printed web links.
var urlRe = regexp.MustCompile(`\bhttps?://[^\s<>"'` + "`" + `]+`)

// findURLs returns the printed URLs of a line as byte ranges; trailing
// punctuation and unbalanced closing brackets are not part of a URL.
func findURLs(text string) [][2]int {
	var out [][2]int
	for _, m := range urlRe.FindAllStringIndex(text, -1) {
		s, e := m[0], m[1]
		for e > s {
			c := text[e-1]
			if strings.IndexByte(".,;:!?'\"", c) >= 0 {
				e--
				continue
			}
			if open := strings.IndexByte(")]}", c); open >= 0 {
				o := "([{"[open]
				if strings.Count(text[s:e], string(o)) < strings.Count(text[s:e], string(c)) {
					e--
					continue
				}
			}
			break
		}
		if e-s > len("https://") {
			out = append(out, [2]int{s, e})
		}
	}
	return out
}

// addLinks finds printed URLs and file locations (with lk) in the lines
// of sc; it runs outside the session lock (file checks may be slow).
func addLinks(sc *Screen, colMaps [][]int, urls bool, lk *run.Linker) {
	for i := range sc.Lines {
		l := &sc.Lines[i]
		cm := colMaps[i]
		taken := func(c0, c1 int) bool {
			for _, x := range l.Links {
				if c0 < x.EndCol && c1 > x.Col {
					return true
				}
			}
			return false
		}
		if urls {
			for _, m := range findURLs(l.Text) {
				if m[1] >= len(cm) {
					continue
				}
				c0, c1 := cm[m[0]], cm[m[1]]
				if !taken(c0, c1) {
					l.Links = append(l.Links, Link{Col: c0, EndCol: c1, URL: l.Text[m[0]:m[1]]})
				}
			}
		}
		if lk == nil {
			continue
		}
		for _, f := range lk.Links(l.Text) {
			if f.Start >= len(cm) || f.End >= len(cm) {
				continue
			}
			c0, c1 := cm[f.Start], cm[f.End]
			if taken(c0, c1) {
				continue
			}
			l.Links = append(l.Links, Link{Col: c0, EndCol: c1, Path: f.Path, File: f.File, Line: f.Line, Column: f.Column})
		}
	}
}

// Screen returns a snapshot of the session.
func (s *Session) Screen(o ScreenOptions) Screen {
	s.mu.Lock()
	sc, colMaps := snapshot(s.term, o)
	sc.ID, sc.Running, sc.ExitCode = s.info.ID, s.info.Running, s.info.ExitCode
	cwd, dir := s.term.Dir(), s.info.Dir
	s.mu.Unlock()

	var lk *run.Linker
	if o.Linker != nil {
		x := *o.Linker
		x.Dirs = append(nonEmpty(cwd, dir), x.Dirs...)
		lk = &x
	}
	addLinks(&sc, colMaps, o.URLs, lk)
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
	cols := make([]int, 0, len(l.Cells)+1)
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
