package main

import (
	"fmt"
	"slices"
	"strings"

	"gioui.org/layout"
	"gioui.org/unit"

	"github.com/ape-dev-de/tiegel/internal/ui/atoms"
	"github.com/ape-dev-de/tiegel/internal/ui/organisms/debug"
	"github.com/ape-dev-de/tiegel/internal/ui/organisms/runtime"
	"github.com/ape-dev-de/tiegel/internal/ui/tokens"
)

// The organism pages act as a tiny fake host: they apply the events of the
// organisms to their sample data, so the catalog is interactive.

// debugHost holds sample debugger data and applies debug events to it.
type debugHost struct {
	listening bool
	state     debug.State
	muted     bool
	frames    []debug.Frame
	vars      []*debug.Variable
	watches   []debug.Watch
	bps       []debug.Breakpoint
	sessions  []debug.Session
	nextBP    int
	log       string
}

func newDebugHost() *debugHost {
	h := &debugHost{listening: true, state: debug.StateBreak, nextBP: 4}
	h.frames = []debug.Frame{
		{Level: 0, Where: "App\\Mail\\Mailer->send", File: "/Users/ada/shop/src/Mail/Mailer.php", Line: 42},
		{Level: 1, Where: "App\\Queue\\Worker->handle", File: "/Users/ada/shop/src/Queue/Worker.php", Line: 88},
		{Level: 2, Where: "App\\Queue\\Worker->run", File: "/Users/ada/shop/src/Queue/Worker.php", Line: 31},
		{Level: 3, Where: "{main}", File: "/Users/ada/shop/bin/worker.php", Line: 12},
	}
	h.vars = sampleVariables()
	h.watches = []debug.Watch{
		{Expression: "$msg->to", Value: "ada@example.org", Type: "string"},
		{Expression: "count($queue)", Value: "17", Type: "int"},
		{Expression: "$undefined", Error: "Undefined variable $undefined"},
	}
	h.bps = []debug.Breakpoint{
		{ID: "1", File: "/Users/ada/shop/src/Mail/Mailer.php", Line: 42, Enabled: true},
		{ID: "2", File: "/Users/ada/shop/src/Mail/Mailer.php", Line: 57, Condition: "$retries > 3", Enabled: true},
		{ID: "3", File: "/Users/ada/shop/src/Queue/Worker.php", Line: 88},
	}
	h.sessions = []debug.Session{
		{ID: "s1", Title: "worker.php", Detail: "CLI", State: debug.StateBreak},
		{ID: "s2", Title: "index.php", Detail: "GET /checkout", State: debug.StateRunning},
	}
	return h
}

// sampleVariables returns a variables tree with lazily loaded children.
func sampleVariables() []*debug.Variable {
	return []*debug.Variable{
		{Name: "$this", FullName: "$this", Type: "object", ClassName: "App\\Mail\\Mailer", HasChildren: true, NumChildren: 2},
		{Name: "$msg", FullName: "$msg", Type: "object", ClassName: "App\\Mail\\Message", HasChildren: true, NumChildren: 4, Children: []*debug.Variable{
			{Name: "to", FullName: "$msg->to", Type: "string", Value: "ada@example.org"},
			{Name: "subject", FullName: "$msg->subject", Type: "string", Value: "Your order"},
			{Name: "retries", FullName: "$msg->retries", Type: "int", Value: "2"},
			{Name: "headers", FullName: "$msg->headers", Type: "array", HasChildren: true, NumChildren: 0},
		}},
		{Name: "$queue", FullName: "$queue", Type: "array", HasChildren: true, NumChildren: 17, PageSize: 5},
		{Name: "$ok", FullName: "$ok", Type: "bool", Value: "1"},
		{Name: "$error", FullName: "$error", Type: "null"},
	}
}

// children fabricates a page of children for the demo.
func (h *debugHost) children(v *debug.Variable, page int) {
	switch v.Type {
	case "array":
		size := v.PageSize
		if size == 0 {
			size = v.NumChildren
		}
		for i := page * size; i < min(v.NumChildren, (page+1)*size); i++ {
			v.Children = append(v.Children, &debug.Variable{Name: fmt.Sprint(i), FullName: fmt.Sprintf("%s[%d]", v.FullName, i), Type: "int", Value: fmt.Sprint(1000 + i)})
		}
		v.Page = page
	default:
		v.Children = []*debug.Variable{
			{Name: "transport", FullName: v.FullName + "->transport", Type: "object", ClassName: "App\\Mail\\SmtpTransport"},
			{Name: "log", FullName: v.FullName + "->log", Type: "object", ClassName: "Psr\\Log\\NullLogger"},
		}
	}
}

func (h *debugHost) apply(evs []debug.Event) {
	for _, ev := range evs {
		h.log = fmt.Sprintf("%T %+v", ev, ev)
		h.log = strings.TrimPrefix(h.log, "debug.")
		switch ev := ev.(type) {
		case debug.ActionEvent:
			switch ev.Action {
			case debug.ActionToggleListen:
				h.listening = !h.listening
			case debug.ActionMuteBreakpoints:
				h.muted = !h.muted
			case debug.ActionStop:
				h.state = debug.StateNone
			case debug.ActionResume:
				h.state = debug.StateRunning
			default:
				h.state = debug.StateBreak
			}
		case debug.ExpandEvent:
			if v := debug.Find(h.vars, ev.FullName); v != nil {
				h.children(v, ev.Page)
			}
		case debug.WatchAddEvent:
			h.watches = append(h.watches, debug.Watch{Expression: ev.Expression, Value: "…"})
		case debug.WatchEditEvent:
			h.watches[ev.Index].Expression = ev.Expression
		case debug.WatchRemoveEvent:
			h.watches = slices.Delete(h.watches, ev.Index, ev.Index+1)
		case debug.BreakpointToggleEvent:
			for i := range h.bps {
				if h.bps[i].ID == ev.ID {
					h.bps[i].Enabled = ev.Enabled
				}
			}
		case debug.BreakpointConditionEvent:
			for i := range h.bps {
				if h.bps[i].ID == ev.ID {
					h.bps[i].Condition = ev.Condition
				}
			}
		case debug.BreakpointRemoveEvent:
			h.bps = slices.DeleteFunc(h.bps, func(b debug.Breakpoint) bool { return b.ID == ev.ID })
		case debug.SessionStopEvent:
			for i := range h.sessions {
				if h.sessions[i].ID == ev.ID {
					h.sessions[i].State = debug.StateStopped
				}
			}
		}
	}
}

// fill copies the host data into the panel's organisms.
func (h *debugHost) fill(p *debug.DebugPanel) {
	p.Toolbar.Listening, p.Toolbar.Port, p.Toolbar.State, p.Toolbar.Muted = h.listening, 9003, h.state, h.muted
	p.Stack.Frames = h.frames
	p.Variables.Roots = h.vars
	p.Watches.Items = h.watches
	p.Breakpoints.Items = h.bps
	p.Sessions.Items = h.sessions
	if p.Sessions.Selected == "" {
		p.Sessions.Selected = "s1"
	}
}

func (h *debugHost) status() string {
	if h.log == "" {
		return "Interact with the organism – events appear here."
	}
	return "Last event: " + h.log
}

// organismPage builds a page for one debug organism backed by a debugHost.
func debugOrganismPage(name, summary string, glyph *atoms.Glyph, height int, w func(h *debugHost, p *debug.DebugPanel, th *tokens.Theme) layout.Widget) *page {
	h := newDebugHost()
	p := debug.NewDebugPanel()
	return &page{
		Level: "Organisms", Name: name, Glyph: glyph, Summary: summary,
		Variants: func(th *tokens.Theme) []variant {
			h.fill(p)
			inner := w(h, p, th)
			return []variant{
				{"Sample data", sizedPx(height, inner)},
				{"Events", atoms.Muted(h.status()).Fn(th)},
			}
		},
	}
}

// sizedPx is sized with an int height in dp.
func sizedPx(h int, w layout.Widget) layout.Widget {
	if h <= 0 {
		return w
	}
	return sized(unit.Dp(h), w)
}

func debugToolbarPage() *page {
	return debugOrganismPage("DebugToolbar",
		"Listener toggle, resume, step over/into/out, stop, mute breakpoints. Buttons follow the session state; shortcuts are data (debug.Shortcuts).",
		atoms.GlyphDebug, 0, func(h *debugHost, p *debug.DebugPanel, th *tokens.Theme) layout.Widget {
			return func(gtx layout.Context) layout.Dimensions {
				h.apply(p.Toolbar.Update(gtx))
				h.fill(p)
				return p.Toolbar.Layout(gtx, th)
			}
		})
}

func callStackPage() *page {
	return debugOrganismPage("CallStackList", "Call stack frames; click to select a frame (FrameSelectEvent).",
		atoms.GlyphList, 140, func(h *debugHost, p *debug.DebugPanel, th *tokens.Theme) layout.Widget {
			return func(gtx layout.Context) layout.Dimensions {
				h.apply(p.Stack.Update(gtx))
				return p.Stack.Layout(gtx, th)
			}
		})
}

func variablesPage() *page {
	return debugOrganismPage("VariablesTree", "Lazy tree: expanding asks the host for children (ExpandEvent), arrays page with “Load more…”, the selected row offers copy.",
		atoms.GlyphList, 300, func(h *debugHost, p *debug.DebugPanel, th *tokens.Theme) layout.Widget {
			return func(gtx layout.Context) layout.Dimensions {
				h.apply(p.Variables.Update(gtx))
				h.fill(p)
				return p.Variables.Layout(gtx, th)
			}
		})
}

func watchesPage() *page {
	return debugOrganismPage("WatchesList", "Add expressions with Enter or +, double click to edit (Enter/Escape), × removes.",
		atoms.GlyphSearch, 200, func(h *debugHost, p *debug.DebugPanel, th *tokens.Theme) layout.Widget {
			return func(gtx layout.Context) layout.Dimensions {
				h.apply(p.Watches.Update(gtx))
				h.fill(p)
				return p.Watches.Layout(gtx, th)
			}
		})
}

func breakpointsListPage() *page {
	return debugOrganismPage("BreakpointsList", "BreakpointRows with enable checkbox; click jumps, the selected row edits the condition (pencil) or removes (×).",
		atoms.GlyphDebug, 160, func(h *debugHost, p *debug.DebugPanel, th *tokens.Theme) layout.Widget {
			return func(gtx layout.Context) layout.Dimensions {
				h.apply(p.Breakpoints.Update(gtx))
				h.fill(p)
				return p.Breakpoints.Layout(gtx, th)
			}
		})
}

func sessionListPage() *page {
	return debugOrganismPage("SessionList", "Concurrent debug sessions with state; select one or stop it.",
		atoms.GlyphDebug, 90, func(h *debugHost, p *debug.DebugPanel, th *tokens.Theme) layout.Widget {
			return func(gtx layout.Context) layout.Dimensions {
				h.apply(p.Sessions.Update(gtx))
				h.fill(p)
				return p.Sessions.Layout(gtx, th)
			}
		})
}

func debugPanelPage() *page {
	h := newDebugHost()
	p := debug.NewDebugPanel()
	return &page{
		Level: "Templates", Name: "DebugPanel", Glyph: atoms.GlyphDebug,
		Summary: "Debugger tool window: toolbar, sessions, frames, breakpoints, variables and watches in resizable splits. All events go through DebugPanel.Update.",
		Variants: func(th *tokens.Theme) []variant {
			return []variant{
				{"Paused session (drag the dividers)", sized(480, func(gtx layout.Context) layout.Dimensions {
					h.fill(p)
					h.apply(p.Update(gtx))
					h.fill(p)
					return p.Layout(gtx, th)
				})},
				{"Events", atoms.Muted(h.status()).Fn(th)},
			}
		},
	}
}

// ---- Runtime -----------------------------------------------------------

func interpreterPickerPage() *page {
	p := &runtime.InterpreterPicker{Items: []runtime.Interpreter{
		{ID: "local:/opt/homebrew/bin/php", Label: "php (/opt/homebrew/bin/php)", Kind: runtime.KindLocal, Version: "8.3.4", Detail: "/opt/homebrew/Cellar/php/8.3.4/bin/php"},
		{ID: "local:/usr/local/bin/php8.1", Label: "php8.1 (/usr/local/bin/php8.1)", Kind: runtime.KindLocal, Version: "8.1.27", Detail: "/usr/local/bin/php8.1"},
		{ID: "compose:shop:php", Label: "compose:php", Kind: runtime.KindCompose, Detail: "php:8.3-fpm", Score: 115,
			Reasons: []string{"official php image (php:8.3-fpm)", "service name \"php\"", "mounts the project directory"}},
		{ID: "compose:shop:worker", Label: "compose:worker", Kind: runtime.KindCompose, Detail: "shop/worker:latest", Score: 40,
			Reasons: []string{"image name contains \"php\""}},
	}, Selected: "compose:shop:php"}
	last := ""
	return &page{
		Level: "Organisms", Name: "InterpreterPicker", Glyph: atoms.GlyphComputer,
		Summary: "Local binaries and Docker Compose candidates (score + reasons, best marked) in a dropdown; selection is reported as SelectInterpreterEvent.",
		Variants: func(th *tokens.Theme) []variant {
			label := "Picker"
			if last != "" {
				label += " – selected " + last
			}
			return []variant{
				{label, fixedWidth(460, func(gtx layout.Context) layout.Dimensions {
					for _, ev := range p.Update(gtx) {
						last = ev.(runtime.SelectInterpreterEvent).ID
					}
					d := p.Layout(gtx, th)
					d.Size.Y = max(d.Size.Y, gtx.Dp(150)) // room for the menu
					return d
				})},
			}
		},
	}
}

func pathMappingPage() *page {
	items := []runtime.Mapping{
		{Host: "/Users/ada/shop", Remote: "/var/www/html"},
		{Host: "/Users/ada/shop/vendor", Remote: "/var/www/html/vendor"},
		{Host: "/Users/ada/.composer", Remote: "composer"},
	}
	e := &runtime.PathMappingEditor{}
	log := "Edit a cell and press Enter, add or remove rows."
	return &page{
		Level: "Organisms", Name: "PathMappingEditor", Glyph: atoms.GlyphSwap,
		Summary: "Host ↔ container paths in a table. Enter or leaving a row commits (MappingChangeEvent); invalid container paths are marked.",
		Variants: func(th *tokens.Theme) []variant {
			return []variant{
				{"Mappings", sized(200, func(gtx layout.Context) layout.Dimensions {
					e.Items = items
					for _, ev := range e.Update(gtx) {
						log = fmt.Sprintf("%T %+v", ev, ev)
						switch ev := ev.(type) {
						case runtime.MappingAddEvent:
							items = append(items, runtime.Mapping{})
						case runtime.MappingChangeEvent:
							items[ev.Index] = runtime.Mapping{Host: ev.Host, Remote: ev.Remote}
						case runtime.MappingRemoveEvent:
							items = slices.Delete(items, ev.Index, ev.Index+1)
						}
					}
					e.Items = items
					return e.Layout(gtx, th)
				})},
				{"Events", atoms.Muted(strings.TrimPrefix(log, "runtime.")).Fn(th)},
			}
		},
	}
}

func hintsPage() *page {
	l := &runtime.XdebugHintsList{Items: []runtime.Hint{
		{Severity: runtime.SeverityWarning, Code: "no-host-gateway", Message: "host.docker.internal does not resolve on Linux; add extra_hosts: [\"host.docker.internal:host-gateway\"] to the service."},
		{Severity: runtime.SeverityWarning, Code: "xdebug-mode-without-debug", Message: "XDEBUG_MODE is \"coverage\"; add \"debug\" to stop at breakpoints."},
		{Severity: runtime.SeverityInfo, Code: "no-path-mapping", Message: "No path mapping for the project directory; Tiegel derived one from the bind mounts."},
		{Severity: runtime.SeverityError, Code: "unknown-service", Message: "Service \"php\" is not defined or not enabled."},
	}}
	empty := &runtime.XdebugHintsList{}
	clicked := ""
	return &page{
		Level: "Organisms", Name: "XdebugHintsList", Glyph: atoms.GlyphWarning,
		Summary: "Setup hints with severity icon and stable code; click reports HintSelectEvent.",
		Variants: func(th *tokens.Theme) []variant {
			label := "Hints"
			if clicked != "" {
				label += " – clicked " + clicked
			}
			return []variant{
				{label, sized(260, func(gtx layout.Context) layout.Dimensions {
					for _, ev := range l.Update(gtx) {
						clicked = ev.(runtime.HintSelectEvent).Code
					}
					return l.Layout(gtx, th)
				})},
				{"No hints", func(gtx layout.Context) layout.Dimensions { return empty.Layout(gtx, th) }},
			}
		},
	}
}

func statusbarVersionPage() *page {
	auto := &runtime.StatusbarVersion{Version: "8.3", Interpreter: "compose:php", Source: "composer.json: require php ^8.3; interpreter reports 8.3.4"}
	override := &runtime.StatusbarVersion{Version: "8.1", Interpreter: "local", Source: "Set manually in the project settings", Override: true}
	unknown := &runtime.StatusbarVersion{}
	clicks := 0
	return &page{
		Level: "Organisms", Name: "StatusbarVersion", Glyph: atoms.GlyphInfo,
		Summary: "Status bar entry “PHP 8.3 · compose:php”; the tooltip names the source, a click asks for the override (OverrideEvent).",
		Variants: func(th *tokens.Theme) []variant {
			bar := func(s *runtime.StatusbarVersion) layout.Widget {
				return func(gtx layout.Context) layout.Dimensions {
					clicks += len(s.Update(gtx))
					d := s.Layout(gtx, th)
					d.Size.Y += gtx.Dp(40) // room for the tooltip
					return d
				}
			}
			return []variant{
				{"Detected", bar(auto)},
				{"Manual override", bar(override)},
				{"Unknown", bar(unknown)},
				{"Events", atoms.Muted(fmt.Sprintf("Override requested %d times", clicks)).Fn(th)},
			}
		},
	}
}
