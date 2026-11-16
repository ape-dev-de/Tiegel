package controller

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/ape-dev-de/tiegel/internal/api"
)

// The debug part of the controller is a client of the debug-xdebug
// module (debug.*): it follows the debug.* events (listener, sessions,
// status, breaks, breakpoints) and loads stack, variables and watches when
// a session stops. Breakpoints are project breakpoints (persistent, also
// without a session).

// DebugSession is a connected debug session.
type DebugSession struct {
	ID     string `json:"id"`
	File   string `json:"file"`
	IDEKey string `json:"ideKey"`
	Engine string `json:"engine"`
	// Status is starting, running, break, stopping or stopped.
	Status string `json:"status"`
}

// DebugFrame is a stack frame with the host file.
type DebugFrame struct {
	Level int    `json:"Level"`
	Where string `json:"Where"`
	File  string `json:"File"`
	URI   string `json:"FileURI"`
	Line  int    `json:"Line"`
}

// DebugVariable is a variable (property) with its loaded children.
type DebugVariable struct {
	Name        string           `json:"name"`
	FullName    string           `json:"fullname"`
	Type        string           `json:"type"`
	ClassName   string           `json:"className"`
	Value       string           `json:"value"`
	HasChildren bool             `json:"hasChildren"`
	NumChildren int              `json:"numChildren"`
	Page        int              `json:"page"`
	PageSize    int              `json:"pageSize"`
	Children    []*DebugVariable `json:"children"`
}

// DebugWatch is an evaluated watch expression.
type DebugWatch struct {
	Expr  string         `json:"expr"`
	Value *DebugVariable `json:"value"`
	Error string         `json:"error"`
}

// Breakpoint is a project breakpoint; File is the absolute path.
type Breakpoint struct {
	ID        string `json:"id"`
	Path      string `json:"path"`
	File      string `json:"-"`
	Line      int    `json:"line"`
	Condition string `json:"condition"`
	Enabled   bool   `json:"enabled"`
}

// ExecPoint is where a session stopped (absolute host file, 1-based line).
type ExecPoint struct {
	Session string
	File    string
	Line    int
}

// Debug is the debugger state of the window (UI goroutine). The *Version
// counters change whenever the corresponding data changes.
type Debug struct {
	c *Controller

	// Available is true while the debug-xdebug module runs.
	Available bool
	// ListenAddr is passed to debug.listen ("" = module default).
	ListenAddr string
	Listening  bool
	Addr       string

	Sessions []DebugSession
	// Current is the session shown in the panel.
	Current string
	// Exec is the execution point of the current session (nil while
	// running or without session).
	Exec *ExecPoint
	// BreakVersion changes on every stop (the window comes to the front).
	BreakVersion uint64

	Frames        []DebugFrame
	SelectedFrame int
	Variables     []*DebugVariable
	VarsVersion   uint64

	Watches      []DebugWatch
	WatchVersion uint64

	Breakpoints []Breakpoint
	BPVersion   uint64

	// Hints are Xdebug/interpreter hints shown once after the first
	// listener start without path mappings.
	Hints        []Hint
	HintsVersion uint64
	hintsChecked bool
}

func newDebug(c *Controller) *Debug { return &Debug{c: c} }

// reset clears session state (project change).
func (d *Debug) reset() {
	d.Sessions, d.Current, d.Exec = nil, "", nil
	d.clearStop()
	d.Breakpoints = nil
	d.BPVersion++
}

// refresh checks whether the module runs and loads breakpoints and
// watches.
func (d *Debug) refresh() {
	c := d.c
	if !c.Project.Open {
		return
	}
	c.Go(func(ctx context.Context) func() {
		var res struct {
			Breakpoints []Breakpoint `json:"breakpoints"`
		}
		err := c.call(ctx, "debug.breakpoints.list", nil, &res)
		var watches []DebugWatch
		if err == nil {
			_ = c.call(ctx, "debug.watches", map[string]any{}, &watches)
		}
		return func() {
			d.Available = err == nil
			if err != nil {
				d.Listening = false
				d.Sessions, d.Current, d.Exec = nil, "", nil
				d.clearStop()
				d.Breakpoints = nil
				d.BPVersion++
				return
			}
			d.setBreakpoints(res.Breakpoints)
			d.Watches = watches
			d.WatchVersion++
		}
	})
}

func (d *Debug) setBreakpoints(list []Breakpoint) {
	for i := range list {
		list[i].File = d.c.Abs(list[i].Path)
	}
	d.Breakpoints = list
	d.BPVersion++
}

// ---- events ---------------------------------------------------------------

func (d *Debug) handleEvent(topic string, payload json.RawMessage) {
	switch topic {
	case "debug.listening":
		var r struct {
			Addr string `json:"addr"`
		}
		_ = json.Unmarshal(payload, &r)
		d.Listening, d.Addr = true, r.Addr
	case "debug.stopped":
		d.Listening, d.Addr = false, ""
		d.Sessions, d.Current, d.Exec = nil, "", nil
		d.clearStop()
	case api.TopicDebugSessionStarted:
		var s DebugSession
		if json.Unmarshal(payload, &s) != nil {
			return
		}
		d.upsert(s)
		if d.Current == "" {
			d.Current = s.ID
		}
		// The engine waits for the first continuation; run to the first
		// breakpoint.
		d.command("debug.run", s.ID)
	case api.TopicDebugSessionEnded:
		var e struct {
			Session string `json:"session"`
			Error   string `json:"error"`
		}
		if json.Unmarshal(payload, &e) != nil {
			return
		}
		for i, s := range d.Sessions {
			if s.ID == e.Session {
				d.Sessions = append(d.Sessions[:i], d.Sessions[i+1:]...)
				break
			}
		}
		if d.Current == e.Session {
			d.Current = ""
			if len(d.Sessions) > 0 {
				d.Current = d.Sessions[0].ID
			}
			d.Exec = nil
			d.clearStop()
		}
		if e.Error != "" {
			d.c.Status.Notice = "Debug session ended: " + e.Error
		}
	case "debug.status":
		var st struct {
			Session string `json:"session"`
			Status  string `json:"status"`
		}
		if json.Unmarshal(payload, &st) != nil {
			return
		}
		for i := range d.Sessions {
			if d.Sessions[i].ID == st.Session {
				d.Sessions[i].Status = st.Status
			}
		}
		if st.Status != "break" && st.Session == d.Current {
			d.Exec = nil
			d.clearStop()
		}
		if st.Status == "stopping" {
			// The script finished; end the session.
			d.command("debug.stopSession", st.Session)
		}
	case api.TopicDebugBreak:
		var be api.DebugBreakEvent
		if json.Unmarshal(payload, &be) != nil {
			return
		}
		found := false
		for i := range d.Sessions {
			if d.Sessions[i].ID == be.Session {
				d.Sessions[i].Status = "break"
				found = true
			}
		}
		if !found {
			d.Sessions = append(d.Sessions, DebugSession{ID: be.Session, Status: "break"})
		}
		d.Current = be.Session
		d.Exec = &ExecPoint{Session: be.Session, File: be.File, Line: be.Line}
		d.BreakVersion++
		if be.File != "" && be.Line > 0 {
			loc := AtLine(be.File, be.Line)
			d.c.OpenFile(be.File, &loc)
		}
		d.loadStop(be.Session, 0)
	case api.TopicDebugBreakpoints:
		var list []Breakpoint
		if json.Unmarshal(payload, &list) == nil {
			d.setBreakpoints(list)
		}
	}
}

func (d *Debug) upsert(s DebugSession) {
	for i := range d.Sessions {
		if d.Sessions[i].ID == s.ID {
			d.Sessions[i] = s
			return
		}
	}
	d.Sessions = append(d.Sessions, s)
}

func (d *Debug) clearStop() {
	d.Frames, d.SelectedFrame, d.Variables = nil, 0, nil
	d.VarsVersion++
	for i := range d.Watches {
		d.Watches[i].Value, d.Watches[i].Error = nil, ""
	}
	d.WatchVersion++
}

// State returns the status of the current session ("" without one).
func (d *Debug) State() string {
	for _, s := range d.Sessions {
		if s.ID == d.Current {
			return s.Status
		}
	}
	return ""
}

// Paused reports whether the current session is stopped at a break.
func (d *Debug) Paused() bool { return d.State() == "break" }

// loadStop loads stack, variables of frame and watches of a stopped
// session.
func (d *Debug) loadStop(session string, frame int) {
	c := d.c
	c.Go(func(ctx context.Context) func() {
		var frames []DebugFrame
		ferr := c.call(ctx, "debug.stack", map[string]string{"session": session}, &frames)
		vars, verr := d.fetchVariables(ctx, session, frame, "", 0)
		var watches []DebugWatch
		werr := c.call(ctx, "debug.watches", map[string]string{"session": session}, &watches)
		return func() {
			if d.Current != session || !d.Paused() {
				return
			}
			if ferr == nil {
				d.Frames = frames
			}
			d.SelectedFrame = frame
			if verr == nil {
				d.Variables = vars.Variables
				d.VarsVersion++
			}
			if werr == nil {
				d.Watches = watches
				d.WatchVersion++
			}
		}
	})
}

type variablesResult struct {
	Variables []*DebugVariable `json:"variables"`
	Parent    *DebugVariable   `json:"parent"`
	HasMore   bool             `json:"hasMore"`
}

func (d *Debug) fetchVariables(ctx context.Context, session string, frame int, fullName string, page int) (variablesResult, error) {
	var res variablesResult
	p := map[string]any{"session": session, "depth": frame}
	if fullName != "" {
		p["fullname"] = fullName
		p["page"] = page
	}
	err := d.c.call(ctx, "debug.variables", p, &res)
	return res, err
}

// SelectFrame shows the variables of a stack frame and its location.
func (d *Debug) SelectFrame(level int) {
	if !d.Paused() {
		return
	}
	for _, f := range d.Frames {
		if f.Level == level && f.File != "" && f.Line > 0 {
			loc := AtLine(f.File, f.Line)
			d.c.OpenFile(f.File, &loc)
		}
	}
	d.SelectedFrame = level
	session := d.Current
	c := d.c
	c.Go(func(ctx context.Context) func() {
		vars, err := d.fetchVariables(ctx, session, level, "", 0)
		return func() {
			if err != nil || d.Current != session || d.SelectedFrame != level {
				return
			}
			d.Variables = vars.Variables
			d.VarsVersion++
		}
	})
}

// Expand loads the children (page) of a variable.
func (d *Debug) Expand(fullName string, page int) {
	if !d.Paused() {
		return
	}
	session, frame := d.Current, d.SelectedFrame
	c := d.c
	c.Go(func(ctx context.Context) func() {
		res, err := d.fetchVariables(ctx, session, frame, fullName, page)
		return func() {
			if err != nil {
				c.notice(err)
				return
			}
			v := FindVariable(d.Variables, fullName)
			if v == nil || d.Current != session {
				return
			}
			if page == 0 {
				v.Children = res.Variables
			} else {
				v.Children = append(v.Children, res.Variables...)
			}
			v.Page = page
			if res.Parent != nil {
				v.NumChildren, v.PageSize = res.Parent.NumChildren, res.Parent.PageSize
			}
			d.VarsVersion++
		}
	})
}

// FindVariable returns the variable with fullName below vs.
func FindVariable(vs []*DebugVariable, fullName string) *DebugVariable {
	for _, v := range vs {
		if v.FullName == fullName {
			return v
		}
		if f := FindVariable(v.Children, fullName); f != nil {
			return f
		}
	}
	return nil
}

// ---- listener and stepping -------------------------------------------------

// ToggleListen starts or stops the Xdebug listener.
func (d *Debug) ToggleListen() {
	if d.Listening {
		d.StopListening()
		return
	}
	d.Listen()
}

// Listen starts the listener (debug.listen). The first start without path
// mappings checks the Xdebug setup (runtime.diagnose) and keeps the hints.
func (d *Debug) Listen() {
	c := d.c
	addr := d.ListenAddr
	check := !d.hintsChecked
	d.hintsChecked = true
	c.Go(func(ctx context.Context) func() {
		p := map[string]any{}
		if addr != "" {
			p["addr"] = addr
		}
		var res struct {
			Listening bool      `json:"listening"`
			Addr      string    `json:"addr"`
			Mappings  []Mapping `json:"mappings"`
		}
		err := c.call(ctx, "debug.listen", p, &res)
		var diag Diagnosis
		var derr error = fmt.Errorf("skipped")
		if err == nil && check && len(res.Mappings) == 0 {
			derr = c.call(ctx, "runtime.diagnose", nil, &diag)
		}
		return func() {
			if err != nil {
				c.Status.Notice = "Debug listener: " + err.Error()
				return
			}
			d.Listening, d.Addr = res.Listening, res.Addr
			if derr == nil && len(diag.Hints) > 0 {
				d.Hints = diag.Hints
				d.HintsVersion++
			}
		}
	})
}

// StopListening stops the listener (all sessions end).
func (d *Debug) StopListening() {
	c := d.c
	c.Go(func(ctx context.Context) func() {
		err := c.call(ctx, "debug.stop", nil, nil)
		return func() {
			if err != nil {
				c.notice(err)
				return
			}
			d.Listening, d.Addr = false, ""
		}
	})
}

// DismissHints hides the setup hints.
func (d *Debug) DismissHints() {
	d.Hints = nil
	d.HintsVersion++
}

// Step runs a stepping command ("run", "stepOver", "stepInto", "stepOut",
// "stopSession") on the current session.
func (d *Debug) Step(cmd string) {
	if d.Current == "" {
		return
	}
	if cmd != "stopSession" && !d.Paused() {
		return
	}
	d.command("debug."+cmd, d.Current)
}

// StopSession stops a session.
func (d *Debug) StopSession(id string) { d.command("debug.stopSession", id) }

// SelectSession shows another session.
func (d *Debug) SelectSession(id string) {
	if d.Current == id {
		return
	}
	d.Current = id
	d.Exec = nil
	d.clearStop()
	if d.Paused() {
		d.loadStop(id, 0)
	}
}

func (d *Debug) command(method, session string) {
	c := d.c
	c.Go(func(ctx context.Context) func() {
		err := c.call(ctx, method, map[string]string{"session": session}, nil)
		if err != nil && api.ErrorCode(err) != api.CodeNotFound {
			return func() { c.notice(err) }
		}
		return nil
	})
}

// ---- breakpoints and watches ----------------------------------------------

// BreakpointAt returns the breakpoint of file (absolute) at the 1-based
// line.
func (d *Debug) BreakpointAt(file string, line int) (Breakpoint, bool) {
	for _, b := range d.Breakpoints {
		if b.File == file && b.Line == line {
			return b, true
		}
	}
	return Breakpoint{}, false
}

func (d *Debug) bpCall(method string, params any) {
	c := d.c
	c.Go(func(ctx context.Context) func() {
		var res struct {
			Breakpoints []Breakpoint `json:"breakpoints"`
		}
		err := c.call(ctx, method, params, &res)
		return func() {
			if err != nil {
				c.notice(err)
				return
			}
			d.setBreakpoints(res.Breakpoints)
		}
	})
}

// ToggleBreakpoint sets or removes the breakpoint at the 1-based line.
func (d *Debug) ToggleBreakpoint(file string, line int) {
	if !d.Available || line < 1 {
		return
	}
	if b, ok := d.BreakpointAt(file, line); ok {
		d.bpCall("debug.breakpoints.remove", map[string]string{"id": b.ID})
		return
	}
	d.bpCall("debug.breakpoints.set", map[string]any{"file": file, "line": line})
}

// SetCondition sets the condition of the breakpoint at line (creating it).
func (d *Debug) SetCondition(file string, line int, cond string) {
	p := map[string]any{"file": file, "line": line, "condition": strings.TrimSpace(cond)}
	if b, ok := d.BreakpointAt(file, line); ok {
		p["enabled"] = b.Enabled
	}
	d.bpCall("debug.breakpoints.set", p)
}

// SetConditionByID changes the condition of a listed breakpoint.
func (d *Debug) SetConditionByID(id, cond string) {
	for _, b := range d.Breakpoints {
		if b.ID == id {
			d.SetCondition(b.File, b.Line, cond)
		}
	}
}

// EnableBreakpoint enables or disables a breakpoint.
func (d *Debug) EnableBreakpoint(id string, on bool) {
	d.bpCall("debug.breakpoints.enable", map[string]any{"id": id, "enabled": on})
}

// RemoveBreakpoint removes a breakpoint.
func (d *Debug) RemoveBreakpoint(id string) {
	d.bpCall("debug.breakpoints.remove", map[string]string{"id": id})
}

// SetWatches replaces the watch expressions and evaluates them when a
// session is paused.
func (d *Debug) SetWatches(exprs []string) {
	c := d.c
	session := ""
	if d.Paused() {
		session = d.Current
	}
	d.Watches = d.Watches[:0]
	for _, e := range exprs {
		d.Watches = append(d.Watches, DebugWatch{Expr: e})
	}
	d.WatchVersion++
	c.Go(func(ctx context.Context) func() {
		err := c.call(ctx, "debug.watches.set", map[string]any{"expressions": exprs}, nil)
		var watches []DebugWatch
		werr := err
		if err == nil {
			p := map[string]string{}
			if session != "" {
				p["session"] = session
			}
			werr = c.call(ctx, "debug.watches", p, &watches)
		}
		return func() {
			if err != nil {
				c.notice(err)
				return
			}
			if werr == nil {
				d.Watches = watches
				d.WatchVersion++
			}
		}
	})
}

// WatchExpressions returns the watch expressions.
func (d *Debug) WatchExpressions() []string {
	out := make([]string, 0, len(d.Watches))
	for _, w := range d.Watches {
		out = append(out, w.Expr)
	}
	return out
}

// AtLine returns a location for a 1-based line of file; the editor moves
// the caret to the start of that line.
func AtLine(file string, line int) Location {
	return Location{File: file, Range: TextRange{Start: TextPos{Offset: -1, Line: line}, End: TextPos{Offset: -1, Line: line}}}
}
