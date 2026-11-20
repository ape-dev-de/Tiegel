package headless

import (
	"encoding/base64"
	"fmt"
	"io"
	"math"
	"os"
	"strconv"
	"strings"

	"github.com/ape-dev-de/tiegel/internal/api"
	"github.com/ape-dev-de/tiegel/internal/terminal"
)

// Terminal emulator core without a program (AP18.6, D41):
// "terminal-emulate" feeds a recorded byte stream (file, stdin or --send
// text) into a fresh emulator and prints the screen; with --json the
// whole snapshot (spans with attributes, cursor, modes, links, replies).

func init() {
	flagSpec["chunk"] = true
	flagSpec["resize"] = true
	flagSpec["history"] = true
	register(&command{name: "terminal-emulate",
		usage:   "terminal-emulate [FILE|-] [--send TEXT]... [--cols N] [--rows N] [--chunk N] [--resize COLSxROWS] [--history N] [--scrollback]",
		summary: "feed program output (a file, - for stdin, or --send text with \\e, \\xHH escapes) into a fresh terminal emulator and print the screen (--json: spans, cursor, modes, links, replies); --chunk splits the input, --resize reflows afterwards, --scrollback prints the scrollback too",
		maxArgs: 1, run: runTerminalEmulate})
}

// parseSize parses "COLSxROWS".
func parseSize(s string) (cols, rows int, err error) {
	c, r, ok := strings.Cut(strings.ToLower(s), "x")
	cols, err1 := strconv.Atoi(c)
	rows, err2 := strconv.Atoi(r)
	if !ok || err1 != nil || err2 != nil || cols <= 0 || rows <= 0 {
		return 0, 0, usagef("--resize needs COLSxROWS like 80x24, got %q", s)
	}
	return cols, rows, nil
}

func runTerminalEmulate(c *cli) error {
	var data []byte
	if len(c.p.args) == 1 {
		var err error
		if c.p.args[0] == "-" {
			data, err = io.ReadAll(io.LimitReader(c.env.Stdin, 64<<20))
		} else {
			data, err = os.ReadFile(c.p.args[0])
		}
		if err != nil {
			return fmt.Errorf("terminal-emulate: %w", err)
		}
	}
	for _, s := range c.p.flags["send"] {
		u, err := unescapeSend(s)
		if err != nil {
			return err
		}
		data = append(data, u...)
	}
	if len(c.p.args) == 0 && len(c.p.flags["send"]) == 0 {
		return usagef("give a FILE, - for stdin, or --send TEXT")
	}
	p := api.TerminalEmulateParams{DataBase64: base64.StdEncoding.EncodeToString(data), Links: true}
	var err error
	if p.Cols, err = intFlag(c, "cols"); err != nil {
		return err
	}
	if p.Rows, err = intFlag(c, "rows"); err != nil {
		return err
	}
	if p.Cols == 0 {
		p.Cols = 80
	}
	if p.Rows == 0 {
		p.Rows = 24
	}
	if p.Chunk, err = intFlag(c, "chunk"); err != nil {
		return err
	}
	if p.Scrollback, err = intFlag(c, "history"); err != nil {
		return err
	}
	if r := c.p.value("resize"); r != "" {
		if p.ResizeCols, p.ResizeRows, err = parseSize(r); err != nil {
			return err
		}
	}
	if c.p.has("scrollback") {
		// All lines from the oldest one through the screen: Text has
		// them all, the JSON lines the last ones (see "omitted").
		zero := 0
		p.Top, p.Count = &zero, math.MaxInt32
	}
	var res terminal.EmulateResult
	if err := c.call("terminal.emulate", p, &res); err != nil {
		return err
	}
	return c.emit(res, func(w io.Writer) {
		fmt.Fprintln(w, res.Text)
		if res.Replies != "" {
			fmt.Fprintf(c.env.Stderr, "[replies %q]\n", res.Replies)
		}
	})
}
