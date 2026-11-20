package headless

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/ape-dev-de/tiegel/internal/api"
)

func init() {
	register(&command{name: "layout",
		usage:   "layout [get [NAME]] | layout list | layout reset | layout save NAME [LAYOUT-JSON] | layout apply NAME | layout delete NAME",
		summary: "tool window layout of the project (sides, slots, sizes, tabs, corners) and named layouts: show, reset, save, apply, delete",
		maxArgs: 3, cwdProject: true, run: runLayout})
}

func runLayout(c *cli) error {
	usage := usagef("usage: tiegel --headless %s", commands["layout"].usage)
	verb, args := "get", c.p.args
	if len(args) > 0 {
		verb, args = args[0], args[1:]
	}
	var st api.LayoutState
	var err error
	switch {
	case verb == "get" && len(args) <= 1:
		p := api.LayoutParams{}
		if len(args) == 1 {
			p.Name = args[0]
		}
		err = c.call("layout.get", p, &st)
	case verb == "list" && len(args) == 0:
		err = c.call("layout.get", api.LayoutParams{}, &st)
		if err == nil {
			return c.emit(st.Names, func(w io.Writer) { printLayoutNames(w, st.Names) })
		}
	case verb == "reset" && len(args) == 0:
		err = c.call("layout.reset", struct{}{}, &st)
		if err == nil {
			return c.emit(st, func(w io.Writer) { fmt.Fprintln(w, "tool window layout reset to the defaults") })
		}
	case verb == "save" && (len(args) == 1 || len(args) == 2):
		p := api.LayoutSaveParams{Name: args[0]}
		if len(args) == 2 {
			p.Layout = json.RawMessage(args[1])
		}
		err = c.call("layout.save", p, &st)
		if err == nil {
			return c.emit(st, func(w io.Writer) { fmt.Fprintf(w, "saved layout %q\n", st.Name) })
		}
	case verb == "apply" && len(args) == 1:
		err = c.call("layout.apply", api.LayoutParams{Name: args[0]}, &st)
		if err == nil {
			return c.emit(st, func(w io.Writer) { fmt.Fprintf(w, "applied layout %q to the project\n", st.Name) })
		}
	case verb == "delete" && len(args) == 1:
		err = c.call("layout.delete", api.LayoutParams{Name: args[0]}, &st)
		if err == nil {
			return c.emit(st, func(w io.Writer) { fmt.Fprintf(w, "deleted layout %q\n", st.Name) })
		}
	default:
		return usage
	}
	if err != nil {
		return err
	}
	return c.emit(st, func(w io.Writer) {
		switch {
		case st.Name != "":
			fmt.Fprintf(w, "layout %q:\n", st.Name)
		case !st.Found:
			fmt.Fprintln(w, "project layout: defaults (nothing stored)")
		default:
			fmt.Fprintln(w, "project layout:")
		}
		if st.Found {
			var buf bytes.Buffer
			if json.Indent(&buf, st.Layout, "  ", "  ") == nil {
				fmt.Fprintf(w, "  %s\n", buf.String())
			}
		}
		printLayoutNames(w, st.Names)
	})
}

func printLayoutNames(w io.Writer, names []string) {
	if len(names) == 0 {
		fmt.Fprintln(w, "named layouts: none")
		return
	}
	fmt.Fprintf(w, "named layouts: %s\n", strings.Join(names, ", "))
}
