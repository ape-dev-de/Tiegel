package headless

import (
	"fmt"
	"io"
	"strconv"
	"strings"
	"text/tabwriter"

	"github.com/ape-dev-de/tiegel/internal/api"
	"github.com/ape-dev-de/tiegel/internal/modules/livetemplates"
)

// Live templates (plugin live-templates, AP17.3): list the templates
// (optionally those that apply at a position) and expand one into a file.

func init() {
	for name, takes := range map[string]bool{"offset": true, "at": true, "abbr": true, "end": true, "set": true, "clipboard": true, "surround": false} {
		flagSpec[name] = takes
	}
	register(&command{name: "templates",
		usage:   "templates [list [FILE (--at LINE[:COL] | --offset N)] [--surround] [--all] | expand FILE (--at LINE[:COL] | --offset N) [--abbr ABBR] [--end LINE[:COL] | --end N] [--set NAME=VALUE]... [--clipboard TEXT] [--dry-run]]",
		summary: "live templates: list (those applying at a position) or expand one (Tab: the word before the position; --end: Surround With)",
		maxArgs: 2, cwdProject: true, run: runTemplates})
}

// parsePos reads a position "LINE[:COL]" (1-based) or a byte offset.
func parsePos(at, offset string) (api.Pos, error) {
	if at != "" {
		l, c, _ := strings.Cut(at, ":")
		line, err := strconv.Atoi(l)
		if err != nil || line < 1 {
			return api.Pos{}, usagef("position %q: want LINE[:COL]", at)
		}
		col := 1
		if c != "" {
			if col, err = strconv.Atoi(c); err != nil || col < 1 {
				return api.Pos{}, usagef("position %q: want LINE[:COL]", at)
			}
		}
		return api.Pos{Line: line, Col: col}, nil
	}
	n, err := strconv.Atoi(offset)
	if err != nil || n < 0 {
		return api.Pos{}, usagef("offset %q: want a byte offset", offset)
	}
	return api.Pos{Offset: n}, nil
}

func runTemplates(c *cli) error {
	sub, args := "list", c.p.args
	if len(args) > 0 {
		sub, args = args[0], args[1:]
	}
	switch sub {
	case "list":
		p := livetemplates.ListParams{Surround: c.p.has("surround"), All: c.p.has("all")}
		if len(args) > 0 {
			p.Path = args[0]
			if c.p.has("at") || c.p.has("offset") {
				pos, err := parsePos(c.p.value("at"), c.p.value("offset"))
				if err != nil {
					return err
				}
				p.Pos = &pos
			}
		}
		var res livetemplates.ListResult
		if err := c.call("templates.list", p, &res); err != nil {
			return err
		}
		return c.emit(res, func(w io.Writer) {
			if res.Context != "" {
				fmt.Fprintf(w, "# context %s\n", res.Context)
			}
			tw := tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)
			for _, t := range res.Templates {
				flags := t.Source
				if t.Surround {
					flags += ",surround"
				}
				if t.Disabled {
					flags += ",off"
				}
				fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", t.Abbreviation, t.Description, strings.Join(t.Contexts, ","), flags)
			}
			_ = tw.Flush()
			for _, p := range res.Problems {
				fmt.Fprintf(c.env.Stderr, "%s line %d: %s\n", livetemplates.SettingUser, p.Line, p.Message)
			}
		})
	case "expand":
		if len(args) != 1 || (!c.p.has("at") && !c.p.has("offset")) {
			return usagef("usage: tiegel --headless templates expand FILE (--at LINE[:COL] | --offset N) [--abbr ABBR] [--end POS] [--set NAME=VALUE]... [--clipboard TEXT] [--dry-run]")
		}
		pos, err := parsePos(c.p.value("at"), c.p.value("offset"))
		if err != nil {
			return err
		}
		p := livetemplates.ExpandParams{Path: args[0], Pos: pos, Abbreviation: c.p.value("abbr"), Clipboard: c.p.value("clipboard")}
		if e := c.p.value("end"); e != "" {
			var end api.Pos
			if strings.Contains(e, ":") || c.p.has("at") {
				end, err = parsePos(e, "")
			} else {
				end, err = parsePos("", e)
			}
			if err != nil {
				return err
			}
			p.End = &end
		}
		for _, kv := range c.p.flags["set"] {
			k, v, ok := strings.Cut(kv, "=")
			if !ok {
				return usagef("--set %q: want NAME=VALUE", kv)
			}
			if p.Values == nil {
				p.Values = map[string]string{}
			}
			p.Values[k] = v
		}
		if c.p.has("dry-run") {
			p.Apply = new(bool)
		}
		var res livetemplates.ExpandResult
		if err := c.call("templates.expand", p, &res); err != nil {
			return err
		}
		return c.emit(res, func(w io.Writer) {
			verb := "expanded"
			if !res.Applied {
				verb = "would expand"
			}
			fmt.Fprintf(w, "%s %q (%s) at %d-%d:\n%s\n", verb, res.Abbreviation, res.Context, res.Start, res.End, res.Text)
			for _, f := range res.Fields {
				fmt.Fprintf(w, "tab stop %s:", f.Name)
				for _, r := range f.Ranges {
					fmt.Fprintf(w, " %d-%d", r.Start, r.End)
				}
				fmt.Fprintln(w)
			}
			fmt.Fprintf(w, "caret %d\n", res.Caret)
		})
	}
	return usagef("unknown templates command %q (list, expand)", sub)
}
