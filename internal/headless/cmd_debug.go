package headless

import (
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"
	"sync"

	"github.com/ape-dev-de/tiegel/internal/api"
	"github.com/ape-dev-de/tiegel/internal/modules/debugxdebug"
)

func init() {
	flagSpec["condition"] = true
	register(
		&command{name: "debug-listen", usage: "debug-listen [--addr HOST:PORT] [--map HOST=REMOTE]...", summary: "listen for Xdebug and print events until Ctrl+C", maxArgs: 0, run: runDebugListen},
		&command{name: "breakpoints", usage: "breakpoints [set FILE:LINE [--condition C] | remove FILE:LINE | enable FILE:LINE | disable FILE:LINE]", summary: "list or change the project breakpoints (persistent)", maxArgs: 2, cwdProject: true, run: runBreakpoints},
	)
}

func runDebugListen(c *cli) error {
	params := debugxdebug.DebugListenParams{Addr: c.p.value("addr")}
	for _, m := range c.p.flags["map"] {
		host, remote, ok := strings.Cut(m, "=")
		if !ok || host == "" || remote == "" {
			return usagef("--map needs HOST=REMOTE, got %q", m)
		}
		params.Mappings = append(params.Mappings, api.MappingInfo{Host: host, Remote: remote})
	}
	var mu sync.Mutex
	out := c.env.Stdout
	print := func(ev api.Event) {
		mu.Lock()
		defer mu.Unlock()
		if c.json {
			b, _ := json.Marshal(ev)
			fmt.Fprintf(out, "%s\n", b)
			return
		}
		fmt.Fprintf(out, "%s %s\n", ev.Topic, ev.Payload)
	}
	sess := c.app.NewSession(print)
	defer sess.Close()
	sess.Subscribe("debug.*")
	raw, _ := json.Marshal(params)
	if _, err := sess.Call(c.ctx, "debug.listen", raw); err != nil {
		return err
	}
	<-c.ctx.Done()
	_, err := sess.Call(c.ctx, "debug.stop", nil)
	if err != nil && c.ctx.Err() == nil {
		return err
	}
	return nil
}

// parseLocation splits "FILE:LINE".
func parseLocation(s string) (string, int, error) {
	i := strings.LastIndexByte(s, ':')
	if i <= 0 {
		return "", 0, usagef("need FILE:LINE, got %q", s)
	}
	line, err := strconv.Atoi(s[i+1:])
	if err != nil || line < 1 {
		return "", 0, usagef("need FILE:LINE with a line number >= 1, got %q", s)
	}
	return s[:i], line, nil
}

func runBreakpoints(c *cli) error {
	var res debugxdebug.BreakpointsResult
	if len(c.p.args) == 0 {
		if err := c.call("debug.breakpoints.list", nil, &res); err != nil {
			return err
		}
		return c.emit(res.Breakpoints, func(w io.Writer) { printBreakpoints(w, res.Breakpoints) })
	}
	usage := usagef("usage: tiegel --headless %s", commands["breakpoints"].usage)
	if len(c.p.args) != 2 {
		return usage
	}
	file, line, err := parseLocation(c.p.args[1])
	if err != nil {
		return err
	}
	params := debugxdebug.BreakpointParams{File: file, Line: line}
	method := ""
	switch c.p.args[0] {
	case "set":
		method, params.Condition = "debug.breakpoints.set", c.p.value("condition")
	case "remove":
		method = "debug.breakpoints.remove"
	case "enable", "disable":
		on := c.p.args[0] == "enable"
		method, params.Enabled = "debug.breakpoints.enable", &on
	default:
		return usage
	}
	if err := c.call(method, params, &res); err != nil {
		return err
	}
	return c.emit(res, func(w io.Writer) {
		if res.Changed != nil {
			fmt.Fprintf(w, "%s %s\n", c.p.args[0], res.Changed.ID)
		}
	})
}

func printBreakpoints(w io.Writer, list []debugxdebug.Breakpoint) {
	if len(list) == 0 {
		fmt.Fprintln(w, "no breakpoints")
		return
	}
	for _, bp := range list {
		line := fmt.Sprintf("%s:%d", bp.Path, bp.Line)
		if !bp.Enabled {
			line += " [disabled]"
		}
		if bp.Condition != "" {
			line += " if " + bp.Condition
		}
		fmt.Fprintln(w, line)
	}
}
