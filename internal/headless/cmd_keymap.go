package headless

import (
	"fmt"
	"io"
	"strings"
	"text/tabwriter"

	"github.com/ape-dev-de/tiegel/internal/api"
)

func init() {
	for name, takes := range map[string]bool{"context": true, "query": true, "chord": true, "user": false, "from": true, "to": true, "write": false} {
		flagSpec[name] = takes
	}
	register(
		&command{name: "keymap", usage: "keymap [list [--context C] [--query Q] [--chord CHORD] [--user] | set ACTION [CHORD...] | add ACTION CHORD... | reset [ACTION] | preset mac|pc | conflicts]",
			summary: "list and change keyboard shortcuts (user overrides, presets, conflicts)", maxArgs: -1, run: runKeymap},
		&command{name: "join-lines", usage: "join-lines FILE --from LINE[:COL] [--to LINE[:COL]] [--write]",
			summary: "join the selected lines (or the line with the next); prints the result, --write saves", minArgs: 1, maxArgs: 1, cwdProject: true, run: runJoinLines},
		&command{name: "toggle-case", usage: "toggle-case FILE --from LINE[:COL] [--to LINE[:COL]] [--write]",
			summary: "toggle the case of a range (or the word at --from); prints the result, --write saves", minArgs: 1, maxArgs: 1, cwdProject: true, run: runToggleCase},
	)
}

func runKeymap(c *cli) error {
	args := c.p.args
	sub := "list"
	if len(args) > 0 {
		sub, args = args[0], args[1:]
	}
	switch sub {
	case "list":
		var st api.KeymapState
		p := api.KeymapListParams{Context: c.p.value("context"), Query: c.p.value("query"), Chord: c.p.value("chord"), User: c.p.has("user")}
		if err := c.call("keymap.list", p, &st); err != nil {
			return err
		}
		return c.emit(st, func(w io.Writer) {
			tw := tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)
			fmt.Fprintf(tw, "# preset %s\n", st.Preset)
			for _, b := range st.Bindings {
				src := ""
				if b.Source == "user" {
					src = "user"
				}
				fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n", b.Action, b.Context, strings.Join(b.Chords, ", "), src, b.Title)
			}
			_ = tw.Flush()
		})
	case "set", "add":
		if len(args) == 0 || (sub == "add" && len(args) < 2) {
			return usagef("keymap %s needs ACTION and chords", sub)
		}
		p := api.KeymapSetParams{Action: args[0], Chords: append([]string{}, args[1:]...), Add: sub == "add"}
		var res api.KeymapSetResult
		if err := c.call("keymap.set", p, &res); err != nil {
			return err
		}
		return c.emit(res, func(w io.Writer) {
			if b := res.Binding; b != nil {
				chords := strings.Join(b.Chords, ", ")
				if chords == "" {
					chords = "(none)"
				}
				fmt.Fprintf(w, "%s: %s\n", b.Action, chords)
			}
			printConflicts(w, res.Conflicts)
		})
	case "preset":
		if len(args) != 1 {
			return usagef("keymap preset needs mac or pc")
		}
		var res api.KeymapSetResult
		if err := c.call("keymap.set", api.KeymapSetParams{Preset: args[0]}, &res); err != nil {
			return err
		}
		return c.emit(res, func(w io.Writer) { fmt.Fprintf(w, "preset %s\n", res.Preset) })
	case "reset":
		if len(args) > 1 {
			return usagef("keymap reset takes at most one ACTION")
		}
		p := api.KeymapResetParams{}
		if len(args) == 1 {
			p.Action = args[0]
		}
		var res api.KeymapResetResult
		if err := c.call("keymap.reset", p, &res); err != nil {
			return err
		}
		return c.emit(res, func(w io.Writer) {
			fmt.Fprintf(w, "reset %d override(s), preset %s\n", len(res.Reset), res.Preset)
		})
	case "conflicts":
		var cs []api.KeymapConflict
		if err := c.call("keymap.conflicts", nil, &cs); err != nil {
			return err
		}
		return c.emit(cs, func(w io.Writer) {
			if len(cs) == 0 {
				fmt.Fprintln(w, "no conflicts")
			}
			printConflicts(w, cs)
		})
	}
	return usagef("unknown keymap command %q (list, set, add, reset, preset, conflicts)", sub)
}

func printConflicts(w io.Writer, cs []api.KeymapConflict) {
	for _, k := range cs {
		fmt.Fprintf(w, "conflict %s: %s (%s) and %s (%s)\n", k.Chord, k.Action, k.Context, k.Other, k.OtherCtx)
	}
}

func runJoinLines(c *cli) error  { return c.selectionEdit("edit.joinLines") }
func runToggleCase(c *cli) error { return c.selectionEdit("edit.toggleCase") }

// selectionEdit opens FILE, runs an edit.* method on --from/--to and
// prints the new text (or saves it with --write).
func (c *cli) selectionEdit(method string) error {
	if c.p.value("from") == "" {
		return usagef("--from LINE[:COL] is required")
	}
	from, err := parseLineCol(c.p.value("from"))
	if err != nil {
		return err
	}
	p := api.EditRangeParams{Path: c.p.args[0], Start: from}
	if s := c.p.value("to"); s != "" {
		to, err := parseLineCol(s)
		if err != nil {
			return err
		}
		p.End = &to
	}
	if err := c.call("doc.open", api.DocOpenParams{Path: p.Path}, nil); err != nil {
		return err
	}
	var res api.EditRangeResult
	if err := c.call(method, p, &res); err != nil {
		return err
	}
	if c.p.has("write") && res.Changed {
		if err := c.call("doc.save", api.DocSaveParams{Path: p.Path}, nil); err != nil {
			return err
		}
	}
	var text api.DocText
	if err := c.call("doc.text", api.DocTextParams{Path: p.Path}, &text); err != nil {
		return err
	}
	out := struct {
		api.EditRangeResult
		Text    string `json:"text"`
		Written bool   `json:"written"`
	}{res, text.Text, c.p.has("write") && res.Changed}
	return c.emit(out, func(w io.Writer) {
		if c.p.has("write") {
			if res.Changed {
				fmt.Fprintf(w, "saved %s\n", p.Path)
			} else {
				fmt.Fprintln(w, "nothing to change")
			}
			return
		}
		fmt.Fprint(w, text.Text)
	})
}
