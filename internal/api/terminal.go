package api

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"time"

	"github.com/ape-dev-de/tiegel/internal/core/bus"
	"github.com/ape-dev-de/tiegel/internal/core/hooks"
	"github.com/ape-dev-de/tiegel/internal/core/settings"
	"github.com/ape-dev-de/tiegel/internal/core/shellenv"
	"github.com/ape-dev-de/tiegel/internal/run"
	"github.com/ape-dev-de/tiegel/internal/terminal"
	"github.com/ape-dev-de/tiegel/internal/terminal/vt"
)

// Kernel service "terminal" (AP15.5): interactive programs in pseudo
// terminals with VT emulation. Shell sessions start in the project with
// the shell from terminal.shell and the login-shell PATH; other kinds
// (e.g. a shell inside a compose service) are resolved by plugins
// through HookTerminalLaunch.

// Topics of the terminal service.
const (
	// TopicTerminalOpened: a session started (terminal.Info).
	TopicTerminalOpened = "terminal.opened"
	// TopicTerminalOutput: program output, batched (terminal.Output);
	// clients fetch the screen with terminal.screen.
	TopicTerminalOutput = "terminal.output"
	// TopicTerminalChanged: name, title or working directory changed
	// (terminal.Info).
	TopicTerminalChanged = "terminal.changed"
	// TopicTerminalExited: the program exited (terminal.Info).
	TopicTerminalExited = "terminal.exited"
	// TopicTerminalClosed: a session was closed (TerminalIDParams).
	TopicTerminalClosed = "terminal.closed"
)

// TerminalLaunch is the value of the terminal.launch filter: a session
// of a kind other than "shell" asks the plugin owning the kind for the
// program to start. The provider sets Program (and Args, Env, Dir,
// Name) or explains in Err why it cannot.
type TerminalLaunch struct {
	// Kind and Target come from terminal.open (e.g. "docker.service",
	// "app"); Shell is the requested shell inside the target ("" =
	// provider default).
	Kind   string
	Target string
	Shell  string
	// The answer.
	Program string
	Args    []string
	// Env is added to the session environment ("K=V").
	Env []string
	// Dir is the start directory ("" = project root).
	Dir  string
	Name string
	Err  string
	// Stop ends the processes on the remote side on terminal.close
	// (killing the local client, e.g. `docker exec`, leaves them): the
	// program reports the PID of the remote shell when it is started
	// through terminal.ReportPIDArgs, and Stop gets it (D370).
	Stop terminal.StopRemoteFunc
}

// TerminalKind is a session kind a plugin offers (terminal.kinds), so
// that clients open plugin sessions without knowing plugin names.
type TerminalKind struct {
	Kind string `json:"kind"`
	// Label names the kind for menus.
	Label string `json:"label"`
	// Target says what the target of terminal.open names
	// (TerminalTargetService, TerminalTargetContainer, or a plugin's
	// own word).
	Target string `json:"target"`
}

// Targets of terminal kinds.
const (
	// TerminalTargetService: a compose service of the open project.
	TerminalTargetService = "service"
	// TerminalTargetContainer: a container by name or ID.
	TerminalTargetContainer = "container"
)

// HookTerminalKinds collects the session kinds of the enabled plugins.
var HookTerminalKinds = hooks.NewFilter[[]TerminalKind]("terminal.kinds")

// HookTerminalLaunch resolves non-shell sessions (provided by plugins,
// e.g. docker for "docker.service" and "docker.container").
var HookTerminalLaunch = hooks.NewFilter[TerminalLaunch]("terminal.launch")

// TerminalOpenParams are the params of "terminal.open".
type TerminalOpenParams struct {
	Name string `json:"name,omitempty"`
	// Kind is "shell" (default) or a plugin kind with Target.
	Kind   string `json:"kind,omitempty"`
	Target string `json:"target,omitempty"`
	// Shell and Args override terminal.shell/terminal.shellArgs (for
	// plugin kinds Shell is the shell inside the target).
	Shell string   `json:"shell,omitempty"`
	Args  []string `json:"args,omitempty"`
	// Dir is the start directory (default: the project root, else the
	// home directory).
	Dir string `json:"dir,omitempty"`
	// Env adds variables ("K=V").
	Env  []string `json:"env,omitempty"`
	Cols int      `json:"cols,omitempty"`
	Rows int      `json:"rows,omitempty"`
}

// TerminalIDParams address a session.
type TerminalIDParams struct {
	ID string `json:"id"`
}

// TerminalInputParams are the params of "terminal.input": exactly one
// of Data (raw text, sent as is), Key (encoded for the program's modes)
// and Paste (bracketed when the program asked for it).
type TerminalInputParams struct {
	ID    string  `json:"id"`
	Data  string  `json:"data,omitempty"`
	Key   *vt.Key `json:"key,omitempty"`
	Paste string  `json:"paste,omitempty"`
}

// TerminalResizeParams are the params of "terminal.resize".
type TerminalResizeParams struct {
	ID   string `json:"id"`
	Cols int    `json:"cols"`
	Rows int    `json:"rows"`
}

// TerminalRenameParams are the params of "terminal.rename".
type TerminalRenameParams struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// TerminalScreenParams are the params of "terminal.screen".
type TerminalScreenParams struct {
	ID string `json:"id"`
	// Top is the first line (absolute index into scrollback + screen);
	// omitted or negative: the first screen row.
	Top *int `json:"top,omitempty"`
	// Count is the number of lines (0: the screen height, at most 2000).
	Count int `json:"count,omitempty"`
	// NoSpans returns text only; Links adds file locations.
	NoSpans bool `json:"noSpans,omitempty"`
	Links   bool `json:"links,omitempty"`
}

// TerminalTextParams are the params of "terminal.text". Without a range
// the whole scrollback and screen is returned.
type TerminalTextParams struct {
	ID string `json:"id"`
	// From and To are {line, col} positions (absolute line index, end
	// column exclusive).
	From *TerminalPos `json:"from,omitempty"`
	To   *TerminalPos `json:"to,omitempty"`
	// Screen returns only the visible screen.
	Screen bool `json:"screen,omitempty"`
}

// TerminalPos is a position in a session.
type TerminalPos struct {
	Line int `json:"line"`
	Col  int `json:"col"`
}

// TerminalText is the result of "terminal.text".
type TerminalText struct {
	Text string `json:"text"`
}

// TerminalWaitParams are the params of "terminal.wait".
type TerminalWaitParams struct {
	ID string `json:"id"`
	// TimeoutMs bounds the wait (0: until the program exits or the
	// request is cancelled).
	TimeoutMs int `json:"timeoutMs,omitempty"`
}

func (a *App) registerTerminal() {
	m := &a.terms
	if a.opts.TerminalStart != nil {
		m.Start = a.opts.TerminalStart
	}
	m.OnOpen = func(i terminal.Info) { a.bus.Publish(TopicTerminalOpened, i) }
	m.OnOutput = func(o terminal.Output) { a.bus.Publish(TopicTerminalOutput, o) }
	m.OnChange = func(i terminal.Info) { a.bus.Publish(TopicTerminalChanged, i) }
	m.OnExit = func(i terminal.Info) { a.bus.Publish(TopicTerminalExited, i) }
	m.OnClose = func(id string) { a.bus.Publish(TopicTerminalClosed, TerminalIDParams{ID: id}) }
	for _, topic := range []string{TopicProjectClosed, TopicProjectOpened} {
		a.bus.Subscribe(topic, func(bus.Event) { a.closeForeignTerminals() })
	}
	mustHandle(a, "terminal.open", "Open a terminal session: the shell in the project, or a plugin kind such as a shell in a compose service", a.terminalOpen)
	mustHandle(a, "terminal.list", "Terminal sessions with state", func(context.Context, struct{}) ([]terminal.Info, error) { return m.List(), nil })
	mustHandle(a, "terminal.kinds", "Session kinds of the enabled plugins (kind, label, what the target names)", a.terminalKinds)
	mustHandle(a, "terminal.input", "Send text, a key (encoded for the program's modes) or pasted text to a session", a.terminalInput)
	mustHandle(a, "terminal.resize", "Change the size of a session (columns, rows)", a.terminalResize)
	mustHandle(a, "terminal.rename", "Rename a terminal tab", a.terminalRename)
	mustHandle(a, "terminal.screen", "Snapshot of screen or scrollback lines: text, styled spans, cursor, modes, file links", a.terminalScreen)
	mustHandle(a, "terminal.text", "Text of a session (all, the screen, or a range) for copying", a.terminalText)
	mustHandle(a, "terminal.wait", "Wait until the program of a session exited", a.terminalWait)
	mustHandle(a, "terminal.close", "Close a session (hangup, then the process tree is killed)", a.terminalClose)
}

func (a *App) terminalSession(id string) (*terminal.Session, error) {
	s, err := a.terms.Get(id)
	if err != nil {
		return nil, Errorf(CodeNotFound, "%w", err)
	}
	return s, nil
}

func (a *App) terminalOpen(ctx context.Context, p TerminalOpenParams) (terminal.Info, error) {
	root := a.ProjectRoot()
	ps := a.ProjectSettings()
	o := terminal.Options{Name: p.Name, Kind: p.Kind, Target: p.Target, Project: root,
		Scrollback: settings.Resolve(terminal.KeyScrollback, 10000, a.global)}
	o.Cols, o.Rows = p.Cols, p.Rows
	if o.Cols < 0 || o.Rows < 0 || o.Cols > 1000 || o.Rows > 500 {
		return terminal.Info{}, Errorf(CodeInvalidParams, "invalid size %dx%d", o.Cols, o.Rows)
	}
	dir := p.Dir
	if dir != "" && !filepath.IsAbs(dir) && root != "" {
		dir = filepath.Join(root, dir)
	}
	var extra []string
	switch p.Kind {
	case "", terminal.KindShell:
		o.Program = p.Shell
		o.Args = p.Args
		if o.Program == "" {
			o.Program = settings.Resolve(terminal.KeyShell, "", ps, a.global)
			if p.Args == nil {
				o.Args = settings.Resolve[[]string](terminal.KeyShellArgs, nil, ps, a.global)
			}
		}
		if o.Program == "" {
			o.Program = terminal.DefaultShell(a.opts.GOOS, os.Getenv, nil)
		}
		if len(o.Args) == 0 && p.Args == nil {
			o.Args = terminal.DefaultArgs(o.Program)
		}
	default:
		if p.Target == "" {
			return terminal.Info{}, Errorf(CodeInvalidParams, "kind %q needs a target", p.Kind)
		}
		q, err := HookTerminalLaunch.Apply(ctx, a.hooks, TerminalLaunch{Kind: p.Kind, Target: p.Target, Shell: p.Shell})
		if err != nil {
			return terminal.Info{}, Errorf(CodeFailed, "terminal.launch: %w", err)
		}
		if q.Err != "" {
			return terminal.Info{}, Errorf(CodeUnavailable, "%s", q.Err)
		}
		if q.Program == "" {
			return terminal.Info{}, Errorf(CodeUnavailable, "no enabled plugin opens terminals of kind %q", p.Kind)
		}
		o.Program, o.Args, extra, o.StopRemote = q.Program, q.Args, q.Env, q.Stop
		if dir == "" {
			dir = q.Dir
		}
		if o.Name == "" {
			o.Name = q.Name
		}
	}
	if dir == "" {
		dir = root
	}
	if dir == "" {
		dir, _ = os.UserHomeDir()
	}
	if st, err := os.Stat(dir); dir != "" && (err != nil || !st.IsDir()) {
		return terminal.Info{}, Errorf(CodeInvalidParams, "directory %s does not exist", dir)
	}
	o.Dir = dir
	path := ""
	if a.opts.TerminalPath != nil {
		path = a.opts.TerminalPath(ctx)
	} else if runtime.GOOS != "windows" {
		path = shellenv.Default().Path(ctx)
	}
	o.Env = terminal.Env(os.Environ(), path, append(extra, p.Env...))
	info, err := a.terms.Open(o)
	if err != nil {
		return terminal.Info{}, Errorf(CodeUnavailable, "%w", err)
	}
	return info, nil
}

func (a *App) terminalInput(ctx context.Context, p TerminalInputParams) (struct{}, error) {
	s, err := a.terminalSession(p.ID)
	if err != nil {
		return struct{}{}, err
	}
	n := 0
	for _, set := range []bool{p.Data != "", p.Key != nil, p.Paste != ""} {
		if set {
			n++
		}
	}
	if n != 1 {
		return struct{}{}, Errorf(CodeInvalidParams, "give exactly one of data, key and paste")
	}
	switch {
	case p.Key != nil:
		err = s.Key(ctx, *p.Key)
	case p.Paste != "":
		err = s.Paste(ctx, p.Paste)
	default:
		err = s.Input(ctx, []byte(p.Data))
	}
	switch {
	case err == nil:
		return struct{}{}, nil
	case errors.Is(err, terminal.ErrExited):
		return struct{}{}, Errorf(CodeConflict, "%w", err)
	case p.Key != nil && !errors.Is(err, context.Canceled):
		return struct{}{}, Errorf(CodeInvalidParams, "%w", err)
	}
	return struct{}{}, Errorf(CodeFailed, "%w", err)
}

func (a *App) terminalResize(_ context.Context, p TerminalResizeParams) (terminal.Info, error) {
	s, err := a.terminalSession(p.ID)
	if err != nil {
		return terminal.Info{}, err
	}
	if err := s.Resize(p.Cols, p.Rows); err != nil {
		return terminal.Info{}, Errorf(CodeInvalidParams, "%w", err)
	}
	return s.Info(), nil
}

func (a *App) terminalRename(_ context.Context, p TerminalRenameParams) (terminal.Info, error) {
	s, err := a.terminalSession(p.ID)
	if err != nil {
		return terminal.Info{}, err
	}
	if p.Name == "" {
		return terminal.Info{}, Errorf(CodeInvalidParams, "name is required")
	}
	return s.Rename(p.Name), nil
}

func (a *App) terminalScreen(_ context.Context, p TerminalScreenParams) (terminal.Screen, error) {
	s, err := a.terminalSession(p.ID)
	if err != nil {
		return terminal.Screen{}, err
	}
	o := terminal.ScreenOptions{Top: -1, Count: p.Count, NoSpans: p.NoSpans}
	if p.Top != nil && *p.Top >= 0 {
		o.Top = *p.Top
	}
	if p.Links {
		o.Linker = &run.Linker{Dirs: []string{a.ProjectRoot()}, Exists: run.FileExists}
	}
	return s.Screen(o), nil
}

func (a *App) terminalText(_ context.Context, p TerminalTextParams) (TerminalText, error) {
	s, err := a.terminalSession(p.ID)
	if err != nil {
		return TerminalText{}, err
	}
	switch {
	case p.Screen:
		return TerminalText{Text: s.ScreenText()}, nil
	case p.From != nil && p.To != nil:
		return TerminalText{Text: s.Text(p.From.Line, p.From.Col, p.To.Line, p.To.Col)}, nil
	case p.From != nil || p.To != nil:
		return TerminalText{}, Errorf(CodeInvalidParams, "give both from and to")
	}
	return TerminalText{Text: s.AllText()}, nil
}

func (a *App) terminalWait(ctx context.Context, p TerminalWaitParams) (terminal.Info, error) {
	s, err := a.terminalSession(p.ID)
	if err != nil {
		return terminal.Info{}, err
	}
	if p.TimeoutMs > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, time.Duration(p.TimeoutMs)*time.Millisecond)
		defer cancel()
	}
	info, err := s.Wait(ctx)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			return info, Errorf(CodeConflict, "still running after %d ms", p.TimeoutMs)
		}
		return info, Errorf(CodeFailed, "%w", err)
	}
	return info, nil
}

func (a *App) terminalClose(_ context.Context, p TerminalIDParams) (struct{}, error) {
	if err := a.terms.Close(p.ID); err != nil {
		if errors.Is(err, terminal.ErrNotFound) {
			return struct{}{}, Errorf(CodeNotFound, "%w", err)
		}
		return struct{}{}, Errorf(CodeFailed, "%w", err)
	}
	return struct{}{}, nil
}

// closeTerminals ends all sessions (App.Close).
func (a *App) closeTerminals() {
	a.terms.CloseAll()
}

// terminalKinds lists the plugin session kinds (first entry of a kind
// wins, entries without kind are dropped).
func (a *App) terminalKinds(ctx context.Context, _ struct{}) ([]TerminalKind, error) {
	list, err := HookTerminalKinds.Apply(ctx, a.hooks, nil)
	if err != nil {
		return nil, Errorf(CodeFailed, "terminal.kinds: %w", err)
	}
	out := []TerminalKind{}
	seen := map[string]bool{}
	for _, k := range list {
		if k.Kind == "" || k.Kind == terminal.KindShell || seen[k.Kind] {
			continue
		}
		seen[k.Kind] = true
		out = append(out, k)
	}
	return out, nil
}

// closeForeignTerminals closes the sessions that do not belong to the
// open project (D371): on project.closed and project.opened, so that no
// session (and no shell in a container of the old stack) keeps running
// unseen. It runs on the bus goroutine; sessions close in parallel.
func (a *App) closeForeignTerminals() {
	root := a.ProjectRoot()
	a.terms.CloseMatching(func(i terminal.Info) bool { return i.Project != root })
}
