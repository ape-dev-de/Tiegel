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
	t := vt.New(min(o.Cols, 1000), min(o.Rows, 500), o.Scrollback)
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
	sc, colMaps := snapshot(t, o.Screen)
	sc.Running = true
	addLinks(&sc, colMaps, o.Screen.URLs, o.Screen.Linker)
	res.Screen = sc
	res.Replies = string(replies)
	res.Cwd = t.Dir()
	if n := len(sc.Lines); n > 0 {
		res.Text = trimTrailingLines(t.Text(sc.Top, 0, sc.Top+n-1, sc.Cols))
	}
	return res
}
