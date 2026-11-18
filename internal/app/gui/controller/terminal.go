package controller

import (
	"context"
	"encoding/json"
	"slices"
	"strings"

	"github.com/ape-dev-de/tiegel/internal/api"
	"github.com/ape-dev-de/tiegel/internal/terminal"
	"github.com/ape-dev-de/tiegel/internal/terminal/vt"
)

// Terminal tool window state (AP15.5): one tab per session of the open
// project, the latest screen snapshot of each and the terminal settings.
// The backend is the kernel's terminal.* service; input goes through one
// sender goroutine per tab so that keys arrive in order.

// TermTab is one terminal session.
type TermTab struct {
	Info terminal.Info
	// Screen is the latest snapshot of the lines the view shows.
	Screen *terminal.Screen
	// Version changes with Info and Screen.
	Version uint64

	// The window the view asks for: Follow (the screen) or Top; Rows.
	follow     bool
	top, rows  int
	fetching   bool
	dirty      bool
	input      chan api.TerminalInputParams
	closeAsked bool
}

// TermSettings are the terminal settings the view uses.
type TermSettings struct {
	OptionAsMeta   bool
	EscapeToEditor bool
	OverrideKeys   bool
	CloseOnExit    bool
	FontSize       float64
}

// Terminals is the terminal state of the window.
type Terminals struct {
	c *Controller
	// Tabs in creation order; Selected is the shown one (session ID).
	Tabs     []*TermTab
	Selected string
	Settings TermSettings
	// Version changes when tabs, selection or settings change.
	Version uint64
	// Opened is set when a session opened from this window (the view
	// shows and focuses the tool window); the view clears it.
	Opened  bool
	project string
}

func newTerminals(c *Controller) *Terminals {
	return &Terminals{c: c, Settings: TermSettings{EscapeToEditor: true, OverrideKeys: true, CloseOnExit: true}}
}

// Tab returns the tab of session id.
func (t *Terminals) Tab(id string) *TermTab {
	for _, x := range t.Tabs {
		if x.Info.ID == id {
			return x
		}
	}
	return nil
}

// Current returns the selected tab (nil without tabs).
func (t *Terminals) Current() *TermTab {
	if x := t.Tab(t.Selected); x != nil {
		return x
	}
	if len(t.Tabs) > 0 {
		return t.Tabs[len(t.Tabs)-1]
	}
	return nil
}

// Select shows the tab of session id.
func (t *Terminals) Select(id string) {
	if t.Tab(id) != nil && t.Selected != id {
		t.Selected = id
		t.Version++
	}
}

// LoadSettings reads the terminal settings.
func (t *Terminals) LoadSettings() {
	t.c.Go(func(ctx context.Context) func() {
		be := t.c.be
		s := TermSettings{
			OptionAsMeta:   getEffective(ctx, be, terminal.KeyOptionAsMeta, false),
			EscapeToEditor: getEffective(ctx, be, terminal.KeyEscape, terminal.EscapeEditor) != terminal.EscapeTerminal,
			OverrideKeys:   getEffective(ctx, be, terminal.KeyOverrideKeys, true),
			CloseOnExit:    getEffective(ctx, be, terminal.KeyCloseOnExit, true),
			FontSize:       getEffective(ctx, be, terminal.KeyFontSize, 0.0),
		}
		return func() {
			t.Settings = s
			t.Version++
		}
	})
}

// Open starts a session (shell in the project, or p.Kind/p.Target) and
// selects its tab.
func (t *Terminals) Open(p api.TerminalOpenParams) {
	t.c.Go(func(ctx context.Context) func() {
		var info terminal.Info
		err := t.c.call(ctx, "terminal.open", p, &info)
		return func() {
			if err != nil {
				t.c.Status.Notice = "Terminal: " + err.Error()
				return
			}
			t.add(info)
			t.Selected = info.ID
			t.Opened = true
			t.Version++
		}
	})
}

// add creates the tab of a session if it is new.
func (t *Terminals) add(info terminal.Info) *TermTab {
	if x := t.Tab(info.ID); x != nil {
		return x
	}
	x := &TermTab{Info: info, follow: true, input: make(chan api.TerminalInputParams, 1024)}
	t.Tabs = append(t.Tabs, x)
	if t.Selected == "" {
		t.Selected = info.ID
	}
	go t.send(x.input)
	t.Version++
	t.refresh(x)
	return x
}

// send delivers the input of one tab in order.
func (t *Terminals) send(ch chan api.TerminalInputParams) {
	for {
		select {
		case p, ok := <-ch:
			if !ok {
				return
			}
			if err := t.c.call(t.c.ctx, "terminal.input", p, nil); err != nil && api.ErrorCode(err) != api.CodeConflict && t.c.ctx.Err() == nil {
				t.c.post(func() { t.c.notice(err) })
			}
		case <-t.c.ctx.Done():
			return
		}
	}
}

// drop removes a tab without asking the backend.
func (t *Terminals) drop(id string) {
	i := slices.IndexFunc(t.Tabs, func(x *TermTab) bool { return x.Info.ID == id })
	if i < 0 {
		return
	}
	close(t.Tabs[i].input)
	t.Tabs = slices.Delete(t.Tabs, i, i+1)
	if t.Selected == id {
		t.Selected = ""
		if len(t.Tabs) > 0 {
			t.Selected = t.Tabs[min(i, len(t.Tabs)-1)].Info.ID
		}
	}
	t.Version++
}

func (t *Terminals) queue(id string, p api.TerminalInputParams) {
	x := t.Tab(id)
	if x == nil || !x.Info.Running {
		return
	}
	p.ID = id
	select {
	case x.input <- p:
	default:
		t.c.Status.Notice = "Terminal: input dropped (the program does not read)"
	}
}

// SendText sends typed text.
func (t *Terminals) SendText(id, s string) {
	t.queue(id, api.TerminalInputParams{Data: s})
}

// SendKey sends a key press (encoded by the backend for the program's
// modes).
func (t *Terminals) SendKey(id string, k vt.Key) {
	t.queue(id, api.TerminalInputParams{Key: &k})
}

// Paste sends pasted text.
func (t *Terminals) Paste(id, s string) {
	if s != "" {
		t.queue(id, api.TerminalInputParams{Paste: s})
	}
}

// Resize sets the grid size of a session.
func (t *Terminals) Resize(id string, cols, rows int) {
	x := t.Tab(id)
	if x == nil || cols <= 0 || rows <= 0 {
		return
	}
	x.rows = rows
	t.c.Go(func(ctx context.Context) func() {
		var info terminal.Info
		err := t.c.call(ctx, "terminal.resize", api.TerminalResizeParams{ID: id, Cols: cols, Rows: rows}, &info)
		return func() {
			if err != nil {
				return
			}
			if x := t.Tab(id); x != nil {
				x.Info.Cols, x.Info.Rows = info.Cols, info.Rows
				t.refresh(x)
			}
		}
	})
}

// View sets the lines the view shows: the screen when follow is set,
// otherwise rows lines from top. A changed window is fetched.
func (t *Terminals) View(id string, follow bool, top, rows int) {
	x := t.Tab(id)
	if x == nil || rows <= 0 {
		return
	}
	if x.follow == follow && x.rows == rows && (follow || x.top == top) {
		return
	}
	x.follow, x.top, x.rows = follow, top, rows
	t.refresh(x)
}

// refresh fetches the screen of a tab (coalesced: one request at a time).
func (t *Terminals) refresh(x *TermTab) {
	if x.fetching {
		x.dirty = true
		return
	}
	x.fetching, x.dirty = true, false
	p := api.TerminalScreenParams{ID: x.Info.ID, Count: x.rows, Links: true}
	if !x.follow {
		top := x.top
		p.Top = &top
	}
	t.c.Go(func(ctx context.Context) func() {
		var sc terminal.Screen
		err := t.c.call(ctx, "terminal.screen", p, &sc)
		return func() {
			x.fetching = false
			if err == nil && t.Tab(x.Info.ID) == x {
				x.Screen = &sc
				x.Version++
				t.Version++
			}
			if x.dirty && t.Tab(x.Info.ID) == x {
				t.refresh(x)
			}
		}
	})
}

// Copy reads the text between two positions and passes it to done.
func (t *Terminals) Copy(id string, from, to api.TerminalPos, done func(string)) {
	t.c.Go(func(ctx context.Context) func() {
		var txt api.TerminalText
		err := t.c.call(ctx, "terminal.text", api.TerminalTextParams{ID: id, From: &from, To: &to}, &txt)
		return func() {
			if err != nil {
				t.c.notice(err)
				return
			}
			done(txt.Text)
		}
	})
}

// Rename renames a tab.
func (t *Terminals) Rename(id, name string) {
	name = strings.TrimSpace(name)
	if x := t.Tab(id); x == nil || name == "" {
		return
	}
	t.c.Go(func(ctx context.Context) func() {
		var info terminal.Info
		err := t.c.call(ctx, "terminal.rename", api.TerminalRenameParams{ID: id, Name: name}, &info)
		return func() {
			if err != nil {
				t.c.notice(err)
				return
			}
			if x := t.Tab(id); x != nil {
				x.Info = info
				x.Version++
				t.Version++
			}
		}
	})
}

// Close closes a session and its tab.
func (t *Terminals) Close(id string) {
	x := t.Tab(id)
	if x == nil || x.closeAsked {
		return
	}
	x.closeAsked = true
	t.c.Go(func(ctx context.Context) func() {
		err := t.c.call(ctx, "terminal.close", api.TerminalIDParams{ID: id}, nil)
		return func() {
			if err != nil && api.ErrorCode(err) != api.CodeNotFound {
				t.c.notice(err)
			}
			t.drop(id)
		}
	})
}

// RunningCount returns the number of sessions whose program runs.
func (t *Terminals) RunningCount() int {
	n := 0
	for _, x := range t.Tabs {
		if x.Info.Running {
			n++
		}
	}
	return n
}

// ProjectChanged shows the sessions of the open project (called by the
// view when the project root changes). Sessions of other projects keep
// running in the backend.
func (t *Terminals) ProjectChanged() {
	root := t.c.Project.Root
	if root == t.project {
		return
	}
	t.project = root
	for _, x := range slices.Clone(t.Tabs) {
		if x.Info.Project != root {
			t.drop(x.Info.ID)
		}
	}
	t.LoadSettings()
	if root == "" {
		return
	}
	t.c.Go(func(ctx context.Context) func() {
		var list []terminal.Info
		err := t.c.call(ctx, "terminal.list", nil, &list)
		return func() {
			if err != nil || t.c.Project.Root != root {
				return
			}
			for _, i := range list {
				if i.Project == root {
					t.add(i)
				}
			}
		}
	})
}

// handleEvent follows terminal.* events (UI goroutine).
func (t *Terminals) handleEvent(topic string, payload json.RawMessage) {
	switch topic {
	case api.TopicTerminalOpened:
		var info terminal.Info
		if json.Unmarshal(payload, &info) == nil && info.Project == t.c.Project.Root && t.c.Project.Root != "" {
			t.add(info)
		}
	case api.TopicTerminalOutput:
		var o terminal.Output
		if json.Unmarshal(payload, &o) == nil {
			if x := t.Tab(o.ID); x != nil {
				t.refresh(x)
			}
		}
	case api.TopicTerminalChanged, api.TopicTerminalExited:
		var info terminal.Info
		if json.Unmarshal(payload, &info) != nil {
			return
		}
		x := t.Tab(info.ID)
		if x == nil {
			return
		}
		x.Info = info
		x.Version++
		t.Version++
		if topic == api.TopicTerminalExited {
			if t.Settings.CloseOnExit && info.ExitCode == 0 {
				t.Close(info.ID)
				return
			}
			t.refresh(x)
		}
	case api.TopicTerminalClosed:
		var p api.TerminalIDParams
		if json.Unmarshal(payload, &p) == nil {
			t.drop(p.ID)
		}
	case "settings.changed":
		var ch settingsChange
		if json.Unmarshal(payload, &ch) == nil && strings.HasPrefix(ch.Key, "terminal.") {
			t.LoadSettings()
		}
	}
}
