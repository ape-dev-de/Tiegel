package headless

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/ape-dev-de/tiegel/internal/api"
	dockermod "github.com/ape-dev-de/tiegel/internal/modules/docker"
	"github.com/ape-dev-de/tiegel/internal/terminal"
)

// Embedded terminal (AP15.5): "terminal" opens a session, sends input,
// waits until the output settles (or the program exits), prints the
// screen and closes the session. In serve mode the terminal.* methods
// keep sessions open across requests.

func init() {
	flagSpec["arg"] = true
	flagSpec["send"] = true
	flagSpec["idle"] = true
	flagSpec["cols"] = true
	flagSpec["rows"] = true
	flagSpec["service"] = true
	flagSpec["target"] = true
	flagSpec["scrollback"] = false
	flagSpec["kinds"] = false
	flagSpec["find"] = true
	register(&command{name: "terminal",
		usage:   "terminal --kinds | terminal [--shell SH] [--arg A]... [--dir DIR] [--service S | --kind K --target T] [--send TEXT]... [--idle 300ms] [--timeout 10s] [--cols N] [--rows N] [--scrollback] [--find TEXT] [--env K=V]...",
		summary: "open a terminal session (project shell or a shell in a compose service), send input (\\n, \\r, \\t, \\e, \\xHH escapes), print the screen (--find: the matches of TEXT in scrollback and screen), close it; --kinds lists the session kinds of the plugins",
		maxArgs: 0, cwdProject: true, run: runTerminal})
}

// terminalOutput is the JSON output of "terminal".
type terminalOutput struct {
	Terminal terminal.Info        `json:"terminal"`
	Screen   terminal.Screen      `json:"screen"`
	Text     string               `json:"text"`
	Find     *terminal.FindResult `json:"find,omitempty"`
}

// unescapeSend interprets the escapes of --send: \n \r \t \e \\ \xHH and
// \uHHHH.
func unescapeSend(s string) (string, error) {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c != '\\' || i+1 == len(s) {
			b.WriteByte(c)
			continue
		}
		i++
		switch s[i] {
		case 'n':
			b.WriteByte('\n')
		case 'r':
			b.WriteByte('\r')
		case 't':
			b.WriteByte('\t')
		case 'e':
			b.WriteByte(0x1b)
		case '\\':
			b.WriteByte('\\')
		case 'x', 'u':
			n := 2
			if s[i] == 'u' {
				n = 4
			}
			if i+n >= len(s) {
				return "", usagef("--send: incomplete \\%c escape", s[i])
			}
			v, err := strconv.ParseUint(s[i+1:i+1+n], 16, 32)
			if err != nil {
				return "", usagef("--send: bad \\%c escape %q", s[i], s[i+1:i+1+n])
			}
			if s[i] == 'x' {
				b.WriteByte(byte(v))
			} else {
				b.WriteRune(rune(v))
			}
			i += n
		default:
			b.WriteByte('\\')
			b.WriteByte(s[i])
		}
	}
	return b.String(), nil
}

func durationFlag(c *cli, name string, def time.Duration) (time.Duration, error) {
	v := c.p.value(name)
	if v == "" {
		return def, nil
	}
	d, err := time.ParseDuration(v)
	if err != nil || d <= 0 {
		return 0, usagef("--%s needs a duration like %s, got %q", name, def, v)
	}
	return d, nil
}

func intFlag(c *cli, name string) (int, error) {
	v := c.p.value(name)
	if v == "" {
		return 0, nil
	}
	n, err := strconv.Atoi(v)
	if err != nil || n <= 0 {
		return 0, usagef("--%s needs a positive number, got %q", name, v)
	}
	return n, nil
}

func runTerminal(c *cli) error {
	if c.p.has("kinds") {
		var kinds []api.TerminalKind
		if err := c.call("terminal.kinds", nil, &kinds); err != nil {
			return err
		}
		return c.emit(kinds, func(w io.Writer) {
			for _, k := range kinds {
				fmt.Fprintf(w, "%s\t%s\t%s\n", k.Kind, k.Target, k.Label)
			}
		})
	}
	p := api.TerminalOpenParams{Shell: c.p.value("shell"), Args: c.p.flags["arg"], Dir: c.p.value("dir"), Env: c.p.flags["env"],
		Kind: c.p.value("kind"), Target: c.p.value("target")}
	if s := c.p.value("service"); s != "" {
		if p.Kind != "" {
			return usagef("give either --service or --kind")
		}
		p.Kind, p.Target = dockermod.TerminalKindService, s
	}
	var err error
	if p.Cols, err = intFlag(c, "cols"); err != nil {
		return err
	}
	if p.Rows, err = intFlag(c, "rows"); err != nil {
		return err
	}
	if p.Cols == 0 {
		p.Cols = 100
	}
	if p.Rows == 0 {
		p.Rows = 30
	}
	idle, err := durationFlag(c, "idle", 300*time.Millisecond)
	if err != nil {
		return err
	}
	timeout, err := durationFlag(c, "timeout", 10*time.Second)
	if err != nil {
		return err
	}
	var sends []string
	for _, s := range c.p.flags["send"] {
		u, err := unescapeSend(s)
		if err != nil {
			return err
		}
		sends = append(sends, u)
	}

	// Output events mark activity; the screen is read once it settles.
	var (
		mu   sync.Mutex
		last = time.Now()
		id   string
	)
	activity := make(chan struct{}, 1)
	sess := c.app.NewSession(func(ev api.Event) {
		var o terminal.Output
		if ev.Topic != api.TopicTerminalOutput || json.Unmarshal(ev.Payload, &o) != nil {
			return
		}
		mu.Lock()
		if id == "" || o.ID == id {
			last = time.Now()
		}
		mu.Unlock()
		select {
		case activity <- struct{}{}:
		default:
		}
	})
	defer sess.Close()
	sess.Subscribe(api.TopicTerminalOutput)

	var info terminal.Info
	if err := c.call("terminal.open", p, &info); err != nil {
		return err
	}
	mu.Lock()
	id = info.ID
	mu.Unlock()
	bg := &cli{ctx: context.WithoutCancel(c.ctx), app: c.app, sess: c.sess, env: c.env, p: c.p, json: c.json}
	defer func() { _ = bg.call("terminal.close", api.TerminalIDParams{ID: info.ID}, nil) }()

	deadline := time.Now().Add(timeout)
	exited := func() bool {
		var cur terminal.Info
		var list []terminal.Info
		if bg.call("terminal.list", nil, &list) == nil {
			for _, i := range list {
				if i.ID == info.ID {
					cur = i
				}
			}
		}
		return !cur.Running
	}
	settle := func() {
		for time.Now().Before(deadline) && c.ctx.Err() == nil {
			mu.Lock()
			quiet := time.Since(last)
			mu.Unlock()
			if quiet >= idle || exited() {
				return
			}
			select {
			case <-activity:
			case <-time.After(idle - quiet):
			case <-c.ctx.Done():
			}
		}
	}
	settle()
	for _, s := range sends {
		if err := c.call("terminal.input", api.TerminalInputParams{ID: info.ID, Data: s}, nil); err != nil {
			return err
		}
		mu.Lock()
		last = time.Now()
		mu.Unlock()
		settle()
	}
	var sc terminal.Screen
	sp := api.TerminalScreenParams{ID: info.ID, NoSpans: true}
	if c.p.has("scrollback") {
		zero := 0
		sp.Top, sp.Count = &zero, 2000
	}
	if err := bg.call("terminal.screen", sp, &sc); err != nil {
		return err
	}
	var txt api.TerminalText
	if err := bg.call("terminal.text", api.TerminalTextParams{ID: info.ID, Screen: !c.p.has("scrollback")}, &txt); err != nil {
		return err
	}
	var found *terminal.FindResult
	if q := c.p.value("find"); q != "" {
		found = &terminal.FindResult{}
		if err := bg.call("terminal.find", api.TerminalFindParams{ID: info.ID, FindOptions: terminal.FindOptions{Query: q}}, found); err != nil {
			return err
		}
	}
	var list []terminal.Info
	if err := bg.call("terminal.list", nil, &list); err == nil {
		for _, i := range list {
			if i.ID == info.ID {
				info = i
			}
		}
	}
	return c.emit(terminalOutput{Terminal: info, Screen: sc, Text: txt.Text, Find: found}, func(w io.Writer) {
		if found != nil {
			for _, m := range found.Matches {
				fmt.Fprintf(w, "%d:%d-%d:%d\n", m.Line, m.Col, m.EndLine, m.EndCol)
			}
			return
		}
		fmt.Fprintln(w, txt.Text)
		if !info.Running {
			fmt.Fprintf(c.env.Stderr, "[exited with code %d]\n", info.ExitCode)
		}
	})
}
