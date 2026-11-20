package terminal

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// Recorded byte streams of typical programs (AP18.6). They are
// synthetic: written by fixtureStreams below to look like what vim,
// less, htop, a powerline prompt and a full-screen CLI agent send, not
// captured from other projects. `go test ./internal/terminal -run
// TestStreamFixtures -update-fixtures` rewrites the .vt files and the
// golden screens.

var updateFixtures = flag.Bool("update-fixtures", false, "rewrite testdata/*.vt and *.golden")

const esc = "\x1b"

func csi(s string) string { return esc + "[" + s }

func cup(row, col int) string { return csi(strconv.Itoa(row) + ";" + strconv.Itoa(col) + "H") }

// fixtureStreams returns the synthetic streams by name with their size.
func fixtureStreams() map[string]struct {
	cols, rows int
	data       string
} {
	type fx = struct {
		cols, rows int
		data       string
	}
	out := map[string]fx{}

	// vim: alternate screen, scroll region, status line, insert mode,
	// cursor shapes, bracketed paste, application cursor keys.
	var v strings.Builder
	v.WriteString("$ vim a.php\r\n")
	v.WriteString(csi("?1049h") + csi("?1h") + esc + "=" + csi("?2004h") + csi("?1004h") + csi("22;0;0t"))
	v.WriteString(esc + "]0;a.php - VIM" + "\x07")
	v.WriteString(csi("H") + csi("2J"))
	src := []string{"<?php", "", "function add(int $a, int $b): int", "{", "    return $a + $b;", "}"}
	for i, l := range src {
		v.WriteString(cup(i+1, 1) + csi("38;5;130m") + l + csi("m"))
	}
	for r := len(src) + 1; r <= 8; r++ {
		v.WriteString(cup(r, 1) + csi("1;34m") + "~" + csi("m"))
	}
	v.WriteString(cup(9, 1) + csi("7m") + "a.php" + strings.Repeat(" ", 25) + "3,5  All" + csi("27m"))
	v.WriteString(cup(10, 1) + csi("1m") + "-- INSERT --" + csi("m") + csi("6 q"))
	v.WriteString(cup(3, 1) + csi("4h") + "// " + csi("4l"))
	// Scroll the text area (rows 1–8) up by one inside the region.
	v.WriteString(csi("1;8r") + cup(8, 1) + "\n" + csi("r"))
	v.WriteString(cup(8, 1) + "// end" + cup(4, 4))
	out["vim"] = fx{40, 10, v.String()}

	// less: alternate screen, reverse-video prompt, reverse index to
	// scroll back, search highlight.
	var l strings.Builder
	l.WriteString("$ less log.txt\r\n" + csi("?1049h") + csi("?1h") + esc + "=" + csi("H") + csi("2J"))
	for i := 1; i <= 5; i++ {
		l.WriteString(fmt.Sprintf("line %d of the log\r\n", i))
	}
	l.WriteString(csi("7m") + "log.txt" + csi("27m") + csi("K"))
	l.WriteString("\r" + csi("K") + csi("H") + esc + "M" + "line 0 of the log" + cup(6, 1) + csi("K") + ":")
	l.WriteString(cup(3, 1) + "line " + csi("7m") + "2" + csi("27m") + " of the log" + cup(6, 2))
	out["less"] = fx{30, 6, l.String()}

	// htop: synchronized full redraws with 256 colors and bars.
	var h strings.Builder
	h.WriteString(csi("?1049h") + csi("?25l") + csi("?1000h") + csi("?1006h"))
	for f := 0; f < 3; f++ {
		h.WriteString(csi("?2026h") + csi("H"))
		for cpu := 0; cpu < 4; cpu++ {
			n := (f*7 + cpu*5) % 20
			h.WriteString(cup(cpu+1, 1) + fmt.Sprintf("%2d", cpu) + csi("1;38;5;245m") + "[" + csi("38;5;40m") +
				strings.Repeat("|", n) + csi("38;5;245m") + strings.Repeat(" ", 20-n) + "]" + csi("m") + csi("K"))
		}
		h.WriteString(cup(6, 1) + csi("30;46m") + fmt.Sprintf("  PID USER      CPU%% frame %d", f) + csi("K") + csi("m"))
		for p := 0; p < 3; p++ {
			h.WriteString(cup(7+p, 1) + fmt.Sprintf("%5d app       %4.1f php-fpm", 100+p, float64(f+p)*1.5) + csi("K"))
		}
		h.WriteString(cup(10, 1) + csi("30;46m") + "F1" + csi("m") + "Help " + csi("30;46m") + "F10" + csi("m") + "Quit" + csi("K"))
		h.WriteString(csi("?2026l"))
	}
	out["htop"] = fx{40, 10, h.String()}

	// A powerline prompt with shell integration (OSC 133), cwd (OSC 7),
	// title, true-color segments, private-use separators and a right
	// prompt placed with CHA.
	var pw strings.Builder
	prompt := func(dir string, code int) {
		pw.WriteString(esc + "]133;A" + esc + "\\" + esc + "]7;file://host" + dir + esc + "\\" + esc + "]0;zsh " + dir + "\x07")
		pw.WriteString(csi("38;2;255;255;255;48;2;0;95;135m") + " " + dir + " " + csi("38;2;0;95;135;48;2;68;68;68m") + "\ue0b0")
		pw.WriteString(csi("38;2;220;220;220m") + " \ue0a0 main " + csi("0;38;2;68;68;68m") + "\ue0b0" + csi("m"))
		pw.WriteString(csi("s") + csi("50G") + csi("38;5;242m") + "\ue0b2" + csi("48;5;242;38;5;255m") + " 12:00 " + csi("m") + csi("u") + " ")
		pw.WriteString(esc + "]133;B" + esc + "\\")
		_ = code
	}
	prompt("/home/dev/app", 0)
	pw.WriteString("ls --color" + "\r\n" + esc + "]133;C" + esc + "\\")
	pw.WriteString(csi("1;34m") + "src" + csi("0m") + "  " + csi("1;32m") + "run.sh" + csi("0m") + "  composer.json\r\n")
	pw.WriteString(esc + "]133;D;0" + esc + "\\")
	prompt("/home/dev/app", 0)
	out["powerline"] = fx{60, 6, pw.String()}

	// Wide characters, emoji, combining marks, box drawing (Unicode and
	// DEC graphics), a wide character at the end of a line.
	var e strings.Builder
	e.WriteString("╭──────╮\r\n│ 日本 │\r\n╰──────╯\r\n")
	e.WriteString("e\u0301 n\u0303 ok 👍 🇩🇪 ❤\ufe0f\r\n")
	e.WriteString(esc + "(0" + "lqqk" + esc + "(B" + " tree\r\n")
	e.WriteString("123456789世界\r\n")
	e.WriteString("▀▄█░▒▓ ⠿ ✓")
	out["emoji"] = fx{10, 8, e.String()}

	// A full-screen CLI agent on the main screen: capability queries,
	// focus and SGR mouse reporting, bracketed paste, a rounded input
	// box redrawn in place with cursor-up and erase-line, synchronized
	// updates and an OSC 8 link.
	var a strings.Builder
	a.WriteString("$ agent\r\n")
	a.WriteString(csi("?2026$p") + csi("?u") + csi("c") + esc + "]11;?" + esc + "\\" + csi(">0q"))
	a.WriteString(csi("?2004h") + csi("?1004h") + csi("?25l"))
	box := func(text string) {
		a.WriteString(csi("38;5;244m") + "╭" + strings.Repeat("─", 28) + "╮" + csi("m") + "\r\n")
		a.WriteString(csi("38;5;244m") + "│" + csi("m") + " > " + text + strings.Repeat(" ", 25-len(text)) + csi("38;5;244m") + "│" + csi("m") + "\r\n")
		a.WriteString(csi("38;5;244m") + "╰" + strings.Repeat("─", 28) + "╯" + csi("m") + "\r\n")
		a.WriteString(csi("2m") + "? for shortcuts" + csi("m"))
	}
	a.WriteString(csi("1m") + "✻ Welcome" + csi("m") + " see " + esc + "]8;id=1;https://example.com/docs" + esc + "\\" + csi("4m") + "docs" + csi("24m") + esc + "]8;;" + esc + "\\" + "\r\n\r\n")
	box("")
	for _, typed := range []string{"f", "fi", "fix the bug"} {
		a.WriteString(csi("?2026h") + "\r" + csi("3A"))
		for i := 0; i < 4; i++ {
			a.WriteString(csi("2K") + csi("1B"))
		}
		a.WriteString(csi("4A") + "\r")
		box(typed)
		a.WriteString(csi("?2026l"))
	}
	out["agent"] = fx{32, 8, a.String()}
	return out
}

// golden renders the parts of a result the fixtures check.
func golden(r EmulateResult) string {
	var b strings.Builder
	sc := r.Screen
	fmt.Fprintf(&b, "size %dx%d history %d\n", sc.Cols, sc.Rows, sc.History)
	b.WriteString("--- screen\n")
	for _, l := range sc.Lines {
		b.WriteString(l.Text + "\n")
	}
	b.WriteString("--- styles\n")
	for _, l := range sc.Lines {
		for _, s := range l.Spans {
			if s == (Span{Col: s.Col, Width: s.Width, Text: s.Text}) {
				continue // default style
			}
			js, _ := json.Marshal(s)
			fmt.Fprintf(&b, "%d: %s\n", l.Index, js)
		}
		if l.Mark != 0 {
			fmt.Fprintf(&b, "%d: mark %d\n", l.Index, l.Mark)
		}
		for _, k := range l.Links {
			fmt.Fprintf(&b, "%d: link %d-%d %s\n", l.Index, k.Col, k.EndCol, k.URL)
		}
	}
	cur, _ := json.Marshal(sc.Cursor)
	modes, _ := json.Marshal(sc.Modes)
	fmt.Fprintf(&b, "--- state\ncursor %s\nmodes %s\ntitle %q\ncwd %q\nlastExit %d\nreplies %q\n", cur, modes, sc.Title, r.Cwd, sc.LastExit, r.Replies)
	return b.String()
}

func TestStreamFixtures(t *testing.T) {
	for name, f := range fixtureStreams() {
		t.Run(name, func(t *testing.T) {
			vtPath := filepath.Join("testdata", name+".vt")
			goldPath := filepath.Join("testdata", name+".golden")
			if *updateFixtures {
				if err := os.MkdirAll("testdata", 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(vtPath, []byte(f.data), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			data, err := os.ReadFile(vtPath)
			if err != nil {
				t.Fatal(err)
			}
			if string(data) != f.data {
				t.Fatalf("%s is out of date: run with -update-fixtures", vtPath)
			}
			o := EmulateOptions{Cols: f.cols, Rows: f.rows, Scrollback: -1, Screen: ScreenOptions{Top: -1, URLs: true}}
			got := golden(Emulate(data, o))
			// Byte-wise feeding gives the same screen (sequences and
			// UTF-8 split across reads).
			o.Chunk = 1
			if split := golden(Emulate(data, o)); split != got {
				t.Errorf("byte-wise feeding differs:\n%s\nwant\n%s", split, got)
			}
			if *updateFixtures {
				if err := os.WriteFile(goldPath, []byte(got), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			want, err := os.ReadFile(goldPath)
			if err != nil {
				t.Fatal(err)
			}
			if got != string(want) {
				t.Errorf("screen differs from %s:\n%s", goldPath, got)
			}
		})
	}
}
