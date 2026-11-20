package terminal

import (
	"fmt"
	"strings"
	"testing"
)

// TestEmulateAllLinesWithScreen: selecting more than MaxScreenLines
// lines keeps the screen (the last lines) and Text holds all of them
// (review wave 9 B9).
func TestEmulateAllLinesWithScreen(t *testing.T) {
	var b strings.Builder
	for i := range 3000 {
		fmt.Fprintf(&b, "line %d\r\n", i)
	}
	b.WriteString("prompt$")
	r := Emulate([]byte(b.String()), EmulateOptions{Cols: 40, Rows: 5, Scrollback: 5000,
		Screen: ScreenOptions{Top: 0, Count: 1 << 30, NoSpans: true}})
	if len(r.Screen.Lines) != MaxScreenLines || r.Omitted != r.Screen.Total-MaxScreenLines || r.Omitted == 0 {
		t.Fatalf("%d lines, %d omitted of %d", len(r.Screen.Lines), r.Omitted, r.Screen.Total)
	}
	if last := r.Screen.Lines[len(r.Screen.Lines)-1]; last.Text != "prompt$" {
		t.Fatalf("screen missing, last row %q", last.Text)
	}
	if !strings.HasPrefix(r.Text, "line 0\nline 1\n") || !strings.HasSuffix(r.Text, "line 2999\nprompt$") {
		t.Fatalf("text %q…%q", r.Text[:20], r.Text[len(r.Text)-20:])
	}
}

// TestEmulateScrollbackCapped: the scrollback of an emulator run is
// bounded by lines × columns (review wave 9 B1).
func TestEmulateScrollbackCapped(t *testing.T) {
	if n := emulateScrollback(1000000, 1000); n*1000 > maxEmulateCells {
		t.Fatalf("scrollback %d at 1000 columns", n)
	}
	if n := emulateScrollback(-1, 80); n != 10000 {
		t.Fatalf("default scrollback %d", n)
	}
	// A run with a huge requested scrollback keeps at most the cap.
	data := strings.Repeat("x\r\n", 20000)
	r := Emulate([]byte(data), EmulateOptions{Cols: 1000, Rows: 2, Scrollback: 1000000, Screen: ScreenOptions{Top: -1}})
	if r.Screen.History > maxEmulateCells/1000 {
		t.Fatalf("history %d", r.Screen.History)
	}
}
