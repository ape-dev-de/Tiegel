package terminal

import (
	"github.com/ape-dev-de/tiegel/internal/terminal/vt"
)

// EmulateOptions describe a run of the emulator without a program.
type EmulateOptions struct {
	Cols, Rows int
	// Scrollback lines (0: none, negative: vt.DefaultScrollback).
	Scrollback int
	// Chunk feeds the data in pieces of this many bytes (0: at once), to
	// check sequences split across reads.
	Chunk int
	// ResizeCols and ResizeRows resize the terminal after the data
	// (0: keep).
	ResizeCols, ResizeRows int
	// Screen selects the lines of the result.
	Screen ScreenOptions
}

// EmulateResult is the state after Emulate.
type EmulateResult struct {
	Screen Screen `json:"screen"`
	// Text is the selected lines as text.
	Text string `json:"text"`
	// Replies are the answers the terminal sent to the program (device
	// reports), Clipboard the texts programs wrote to the clipboard
	// (OSC 52), OSC the operating system commands it passed on.
	Replies   string   `json:"replies,omitempty"`
	Clipboard []string `json:"clipboard,omitempty"`
	OSC       []string `json:"osc,omitempty"`
	Cwd       string   `json:"cwd,omitempty"`
	// Omitted is the number of selected lines left out of Screen.Lines
	// (at most MaxScreenLines, the last ones); Text holds all of them.
	Omitted int `json:"omitted,omitempty"`
}

// maxEmulateCells bounds scrollback lines × columns of an emulator run,
// so a large scrollback at a large width cannot exhaust the memory.
const maxEmulateCells = 4 << 20

// emulateScrollback caps the scrollback n for widths up to cols.
func emulateScrollback(n, cols int) int {
	if n < 0 {
		n = vt.DefaultScrollback
	}
	return min(n, maxEmulateCells/max(1, cols))
}

// Emulate feeds program output into a fresh terminal and returns the
// resulting screen: the terminal core without a pseudo terminal, for
// tests and the headless CLI.
func Emulate(data []byte, o EmulateOptions) EmulateResult {
	if o.Cols <= 0 {
		o.Cols = DefaultCols
	}
	if o.Rows <= 0 {
		o.Rows = DefaultRows
	}
	o.Cols, o.Rows = min(o.Cols, 1000), min(o.Rows, 500)
	t := vt.New(o.Cols, o.Rows, emulateScrollback(o.Scrollback, max(o.Cols, min(o.ResizeCols, 1000))))
	var res EmulateResult
	var replies []byte
	t.Reply = func(b []byte) { replies = append(replies, b...) }
	t.Clipboard = func(_, text string) { res.Clipboard = append(res.Clipboard, text) }
	t.OSC = func(num, text string) { res.OSC = append(res.OSC, num+";"+text) }
	for len(data) > 0 {
		n := len(data)
		if o.Chunk > 0 {
			n = min(n, o.Chunk)
		}
		_, _ = t.Write(data[:n])
		data = data[n:]
	}
	if o.ResizeCols > 0 || o.ResizeRows > 0 {
		cols, rows := t.Size()
		if o.ResizeCols > 0 {
			cols = min(o.ResizeCols, 1000)
		}
		if o.ResizeRows > 0 {
			rows = min(o.ResizeRows, 500)
		}
		t.Resize(cols, rows)
	}
	// The selected range; Screen.Lines holds at most its last
	// MaxScreenLines lines, Text all of it.
	cols, rows := t.Size()
	top, count := o.Screen.Top, o.Screen.Count
	if top < 0 {
		top = t.History()
	}
	if count <= 0 {
		count = rows
	}
	top = max(0, min(top, t.Total()))
	count = max(0, min(count, t.Total()-top))
	so := o.Screen
	so.Top, so.Count = top, count
	if count > MaxScreenLines {
		res.Omitted = count - MaxScreenLines
		so.Top, so.Count = top+res.Omitted, MaxScreenLines
	}
	sc, colMaps := snapshot(t, so)
	sc.Running = true
	addLinks(&sc, colMaps, o.Screen.URLs, o.Screen.Linker)
	res.Screen = sc
	res.Replies = string(replies)
	res.Cwd = t.Dir()
	if count > 0 {
		res.Text = trimTrailingLines(t.Text(top, 0, top+count-1, cols))
	}
	return res
}
