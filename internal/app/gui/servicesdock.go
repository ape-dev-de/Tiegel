package gui

import (
	"fmt"
	"strings"

	"gioui.org/layout"

	"github.com/ape-dev-de/tiegel/internal/app/gui/controller"
	"github.com/ape-dev-de/tiegel/internal/editor/keymap"
	"github.com/ape-dev-de/tiegel/internal/ui/atoms"
	"github.com/ape-dev-de/tiegel/internal/ui/molecules"
	"github.com/ape-dev-de/tiegel/internal/ui/tokens"
)

// Services tool window (AP14.3, Cmd+8 / Alt+8): Docker context › compose
// project › service › containers with state badges and port links, a
// toolbar for up/start/stop/restart/down, logs and "Open Shell", the log
// panel (follow, filter) and a context switcher. The window is visible
// only while the controller watches the engine (Services.SetVisible).

// ToolServices is the Services tool window.
const ToolServices = "services"

// ActServicesView toggles the Services tool window.
const ActServicesView keymap.Action = "view.services"

// Toolbar item IDs.
const (
	svcRefresh    = "refresh"
	svcUp         = "up"
	svcStart      = "start"
	svcStop       = "stop"
	svcRestart    = "restart"
	svcDown       = "down"
	svcLogs       = "logs"
	svcShell      = "shell"
	svcTerminal   = "terminal"
	svcStandalone = "standalone"
)

// svcNodeKind is the kind of a tree row.
type svcNodeKind int

const (
	svcContext svcNodeKind = iota
	svcProject
	svcService
	svcContainer
	svcOthers
	svcStandaloneNode
)

// svcNode is a row of the services tree.
type svcNode struct {
	key       string
	kind      svcNodeKind
	depth     int
	name      string
	value     string
	detail    string
	state     string
	health    string
	service   string
	container string
	ports     []controller.DockerPort
	err       string
	children  bool
}

type servicesDock struct {
	v *View

	rows    map[string]*molecules.TreeRow
	ports   map[string]*atoms.Button
	sel     string
	nodes   []svcNode
	list    molecules.ScrollList
	logList molecules.ScrollList
	split   molecules.Split
	toolbar molecules.Toolbar
	ctxBtn  atoms.Button
	dismiss atoms.Button
	clear   atoms.Button
	follow  *atoms.Checkbox
	filter  *molecules.SearchField
	logSeen uint64
}

func installServices(v *View) *servicesDock {
	d := &servicesDock{v: v,
		rows:    map[string]*molecules.TreeRow{},
		ports:   map[string]*atoms.Button{},
		split:   molecules.Split{Ratio: 0.45},
		ctxBtn:  atoms.Button{Kind: atoms.Ghost, Glyph: atoms.GlyphContainer},
		dismiss: atoms.Button{Kind: atoms.Ghost, Text: "Dismiss"},
		clear:   atoms.Button{Kind: atoms.Ghost, Text: "Clear", Glyph: atoms.GlyphDelete},
		follow:  atoms.NewCheckbox("Follow", atoms.Checked),
		filter:  molecules.NewSearchField("Filter log"),
	}
	v.Registry.AddToolWindow(ToolWindow{
		ID: ToolServices, Title: "Services", Glyph: atoms.GlyphContainer, Side: Bottom, Order: 35, Share: 0.4,
		Mac: "Cmd+8", PC: "Alt+8", ActionID: ActServicesView,
		Badge: func() (string, atoms.BadgeKind) {
			s := v.ctl.Services
			if !s.Visible() || !s.Loaded {
				return "", 0
			}
			running := 0
			for _, p := range s.Snapshot.Projects {
				for _, svc := range p.Services {
					if svc.State == "running" {
						running++
					}
				}
			}
			if running == 0 {
				return "", 0
			}
			return fmt.Sprint(running), atoms.BadgeSuccess
		},
		Layout: d.layout,
	})
	v.Registry.AddFrameHandler(d.frame)
	return d
}

func (d *servicesDock) svc() *controller.Services { return d.v.ctl.Services }

// frame starts and stops the engine watch with the window's visibility.
func (d *servicesDock) frame(v *View, _ layout.Context) {
	d.svc().SetVisible(v.ToolShown(ToolServices))
}

// buildNodes flattens the snapshot into tree rows.
func (d *servicesDock) buildNodes() []svcNode {
	s := d.svc()
	snap := s.Snapshot
	var out []svcNode
	ctxName := snap.Context
	if ctxName == "" {
		ctxName = s.Effective
	}
	if ctxName == "" {
		ctxName = "Docker"
	}
	detail := snap.Endpoint
	if snap.Remote {
		detail += " (remote)"
	}
	out = append(out, svcNode{key: "ctx", kind: svcContext, name: ctxName, detail: detail, children: true, err: snap.Error})
	if !d.expanded("ctx", true) {
		return out
	}
	for _, p := range snap.Projects {
		pk := "p/" + p.Name
		out = append(out, svcNode{key: pk, kind: svcProject, depth: 1, name: p.Name, detail: strings.Join(p.Files, ", "), err: p.Error, children: len(p.Services) > 0})
		if !d.expanded(pk, true) {
			continue
		}
		for _, svc := range p.Services {
			sk := pk + "/" + svc.Name
			value := svc.Image
			if !svc.Defined {
				value = "not in the compose files"
			}
			out = append(out, svcNode{key: sk, kind: svcService, depth: 2, name: svc.Name, value: value, state: svc.State, health: svc.Health,
				service: svc.Name, ports: svc.Ports, children: len(svc.Containers) > 0})
			if !d.expanded(sk, false) {
				continue
			}
			for _, c := range svc.Containers {
				out = append(out, svcNode{key: sk + "/" + c.Name, kind: svcContainer, depth: 3, name: c.Name, value: c.Status, state: c.State, health: c.Health,
					service: svc.Name, container: c.Name, ports: c.Ports})
			}
		}
	}
	if len(snap.Projects) == 0 && s.Loaded {
		out = append(out, svcNode{key: "none", kind: svcOthers, depth: 1, name: "No compose file in the project"})
	}
	if s.Standalone {
		out = append(out, svcNode{key: "others", kind: svcOthers, depth: 1, name: "Other containers", children: len(snap.Standalone) > 0})
		if d.expanded("others", true) {
			for _, c := range snap.Standalone {
				value := c.Image
				if c.ComposeProject != "" {
					value = c.ComposeProject + "/" + c.ComposeService
				}
				out = append(out, svcNode{key: "o/" + c.ID, kind: svcStandaloneNode, depth: 2, name: c.Name, value: value, state: c.State, container: c.Name, ports: c.Ports})
			}
		}
	}
	return out
}

// expanded returns the stored expansion state of key (def if new).
func (d *servicesDock) expanded(key string, def bool) bool {
	r, ok := d.rows[key]
	if !ok {
		r = &molecules.TreeRow{Expanded: def}
		d.rows[key] = r
	}
	return r.Expanded
}

func (d *servicesDock) selected() (svcNode, bool) {
	for _, n := range d.nodes {
		if n.key == d.sel {
			return n, true
		}
	}
	return svcNode{}, false
}

// target returns what the toolbar acts on: the selected service or
// container, else the whole project.
func (d *servicesDock) target() (service, container string) {
	n, ok := d.selected()
	if !ok {
		return "", ""
	}
	switch n.kind {
	case svcService:
		return n.service, ""
	case svcContainer:
		return n.service, ""
	case svcStandaloneNode:
		return "", n.container
	}
	return "", ""
}

func (d *servicesDock) toolbarItems() []molecules.ToolbarItem {
	s := d.svc()
	service, container := d.target()
	what := "Project"
	switch {
	case container != "":
		what = "Container " + container
	case service != "":
		what = "Service " + service
	}
	hasProject := len(s.Snapshot.Projects) > 0 && s.Snapshot.Projects[0].Error == ""
	canAct := s.Busy == "" && (hasProject || container != "")
	return []molecules.ToolbarItem{
		{ID: svcRefresh, Glyph: atoms.GlyphRefresh, Label: "Refresh"},
		{Separator: true},
		{ID: svcUp, Glyph: atoms.GlyphRun, Label: "Up: create and start (" + what + ")", Disabled: !canAct || container != ""},
		{ID: svcStart, Glyph: atoms.GlyphArrowRight, Label: "Start (" + what + ")", Disabled: !canAct},
		{ID: svcStop, Glyph: atoms.GlyphStop, Label: "Stop (" + what + ")", Disabled: !canAct},
		{ID: svcRestart, Glyph: atoms.GlyphRefresh, Label: "Restart (" + what + ")", Disabled: !canAct},
		{ID: svcDown, Glyph: atoms.GlyphDelete, Label: "Down: stop and remove the project's containers and networks…", Disabled: !canAct || container != "" || !hasProject},
		{Separator: true},
		{ID: svcLogs, Glyph: atoms.GlyphList, Label: "Show Logs (" + what + ")", Disabled: !hasProject && container == ""},
		{ID: svcTerminal, Glyph: atoms.GlyphComputer, Label: "Open Shell (" + what + ") in the Terminal", Disabled: service == "" && container == ""},
		{ID: svcShell, Glyph: atoms.GlyphUpload, Label: "Open Shell in External Terminal", Disabled: service == "" && container == ""},
		{Separator: true},
		{ID: svcStandalone, Glyph: atoms.GlyphContainer, Label: "Show Other Containers", Active: s.Standalone},
	}
}

func (d *servicesDock) run(id string) {
	s := d.svc()
	service, container := d.target()
	switch id {
	case svcRefresh:
		s.Refresh()
		s.LoadContexts()
	case svcUp, svcStart, svcStop, svcRestart:
		s.Action(id, service, container, false)
	case svcDown:
		name := ""
		if len(s.Snapshot.Projects) > 0 {
			name = s.Snapshot.Projects[0].Name
		}
		d.v.Confirm("Down compose project "+name+"?",
			"docker compose down stops and removes the containers and networks of "+name+". Named volumes are kept.",
			"Down", func() { s.Action(svcDown, "", "", true) })
	case svcLogs:
		s.ShowLogs(service, container)
	case svcTerminal:
		d.v.termd.openServiceShell(service, container)
	case svcShell:
		s.OpenShell(service, container)
	case svcStandalone:
		s.SetStandalone(!s.Standalone)
	}
}

// contextMenu lists the Docker contexts to switch to.
func (d *servicesDock) contextMenu() {
	s := d.svc()
	items := []MenuItem{{Title: "Docker context", Disabled: true}}
	for _, c := range s.Contexts {
		name := c.Name
		title := name
		if c.Provider != "" && c.Provider != "unknown" {
			title += " (" + c.Provider + ")"
		}
		if c.Remote {
			title += " – remote"
		}
		items = append(items, MenuItem{Title: title, Checked: name == s.Effective, Run: func() { s.SelectContext(name) }})
	}
	d.v.ShowMenu("Docker Context", items)
}

func stateBadge(state, health string) atoms.Badge {
	switch {
	case state == "running" && health == "unhealthy":
		return atoms.Badge{Text: "unhealthy", Kind: atoms.BadgeDanger}
	case state == "running" && health == "starting":
		return atoms.Badge{Text: "starting", Kind: atoms.BadgeWarning}
	case state == "running":
		return atoms.Badge{Text: "running", Kind: atoms.BadgeSuccess}
	case state == "missing":
		return atoms.Badge{Text: "not created", Kind: atoms.BadgeNeutral}
	case state == "restarting" || state == "paused":
		return atoms.Badge{Text: state, Kind: atoms.BadgeWarning}
	case state == "":
		return atoms.Badge{}
	}
	return atoms.Badge{Text: state, Kind: atoms.BadgeNeutral}
}

func (d *servicesDock) portButton(key string, p controller.DockerPort) *atoms.Button {
	b, ok := d.ports[key]
	if !ok {
		b = &atoms.Button{Kind: atoms.Ghost}
		d.ports[key] = b
	}
	b.Text = fmt.Sprintf(":%d", p.Published)
	return b
}

// trailing draws the state badge and the port links of a row.
func (d *servicesDock) trailing(gtx layout.Context, th *tokens.Theme, n svcNode) layout.Dimensions {
	var children []layout.FlexChild
	for i, p := range n.ports {
		if p.URL == "" {
			continue
		}
		b := d.portButton(fmt.Sprintf("%s#%d", n.key, i), p)
		url := p.URL
		if b.Clicked(gtx) {
			d.svc().OpenURL(url) // only on click (privacy)
		}
		children = append(children, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Inset{Right: th.Space.XXS}.Layout(gtx, func(gtx layout.Context) layout.Dimensions { return b.Layout(gtx, th) })
		}))
	}
	if b := stateBadge(n.state, n.health); b.Text != "" {
		children = append(children, layout.Rigid(b.Fn(th)))
	}
	if n.err != "" {
		children = append(children, layout.Rigid(atoms.Badge{Text: "error", Kind: atoms.BadgeDanger}.Fn(th)))
	}
	return layout.Flex{Alignment: layout.Middle}.Layout(gtx, children...)
}

func (d *servicesDock) layoutTree(gtx layout.Context, th *tokens.Theme) layout.Dimensions {
	s := d.svc()
	d.nodes = d.buildNodes()
	if !s.Loaded && s.Err == "" {
		return layout.Inset{Left: th.Space.S, Top: th.Space.XS}.Layout(gtx, atoms.Muted("Loading containers…").Fn(th))
	}
	nodes := d.nodes
	return d.list.Layout(gtx, th, len(nodes), func(gtx layout.Context, i int) layout.Dimensions {
		n := nodes[i]
		r := d.rows[n.key]
		if r == nil {
			r = &molecules.TreeRow{}
			d.rows[n.key] = r
		}
		r.Name, r.Value, r.Depth, r.Expandable = n.name, n.value, n.depth, n.children
		r.Detail = n.detail
		if n.err != "" {
			r.Detail = firstLine(n.err)
		}
		r.Selected = n.key == d.sel
		switch n.kind {
		case svcContext:
			r.Glyph = atoms.GlyphCloud
		case svcProject:
			r.Glyph = atoms.GlyphFolder
		case svcService, svcContainer, svcStandaloneNode:
			r.Glyph = atoms.GlyphContainer
		default:
			r.Glyph = nil
		}
		node := n
		r.Trailing = func(gtx layout.Context) layout.Dimensions { return d.trailing(gtx, th, node) }
		clicked, _, double := r.Update(gtx)
		if clicked {
			d.sel = n.key
		}
		if double && (n.kind == svcService || n.kind == svcContainer || n.kind == svcStandaloneNode) {
			service, container := n.service, ""
			if n.kind == svcStandaloneNode {
				service, container = "", n.container
			}
			s.ShowLogs(service, container)
		}
		return r.Layout(gtx, th)
	})
}

func (d *servicesDock) logTitle() string {
	l := d.svc().Logs
	what := "all services"
	switch {
	case l.Container != "":
		what = l.Container
	case l.Service != "":
		what = l.Service
	}
	state := ""
	switch {
	case l.Loading:
		state = " – loading…"
	case l.ID != "":
		state = " – following"
	}
	return "Logs: " + what + state
}

func (d *servicesDock) layoutLogs(gtx layout.Context, th *tokens.Theme) layout.Dimensions {
	s := d.svc()
	if d.follow.Update(gtx) {
		s.SetFollow(d.follow.Checked())
	}
	if changed, _ := d.filter.Update(gtx); changed {
		s.SetLogFilter(d.filter.Text())
	}
	if d.clear.Clicked(gtx) {
		s.ClearLogs()
	}
	lines := s.VisibleLogs()
	if d.logSeen != s.LogVersion {
		d.logSeen = s.LogVersion
		if s.Logs.Follow {
			d.logList.ScrollTo(len(lines))
		}
	}
	return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Inset{Left: th.Space.S, Right: th.Space.S, Top: th.Space.XXS, Bottom: th.Space.XXS}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
					layout.Flexed(1, atoms.Strong(d.logTitle()).SingleLine().Fn(th)),
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						gtx.Constraints.Max.X = min(gtx.Constraints.Max.X, gtx.Dp(180))
						gtx.Constraints.Min.X = gtx.Constraints.Max.X
						return layout.Inset{Left: th.Space.S}.Layout(gtx, func(gtx layout.Context) layout.Dimensions { return d.filter.Layout(gtx, th) })
					}),
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						return layout.Inset{Left: th.Space.S}.Layout(gtx, func(gtx layout.Context) layout.Dimensions { return d.follow.Layout(gtx, th) })
					}),
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						return layout.Inset{Left: th.Space.XS}.Layout(gtx, func(gtx layout.Context) layout.Dimensions { return d.clear.Layout(gtx, th) })
					}),
				)
			})
		}),
		layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
			switch {
			case s.Logs.Err != "" && len(lines) == 0:
				return layout.Inset{Left: th.Space.S, Top: th.Space.XS}.Layout(gtx, atoms.Muted(s.Logs.Err).Fn(th))
			case !s.Logs.Open:
				return layout.Inset{Left: th.Space.S, Top: th.Space.XS}.Layout(gtx, atoms.Muted("Select a service and click Show Logs (or double-click it).").Fn(th))
			case len(lines) == 0 && !s.Logs.Loading:
				return layout.Inset{Left: th.Space.S, Top: th.Space.XS}.Layout(gtx, atoms.Muted("No log lines.").Fn(th))
			}
			return d.logList.Layout(gtx, th, len(lines), func(gtx layout.Context, i int) layout.Dimensions {
				l := lines[i]
				text := l.Text
				if l.Container != "" {
					text = l.Container + " | " + text
				}
				lbl := atoms.Code(text).SingleLine()
				if l.Stream == "stderr" {
					lbl.Role = atoms.RoleMuted
				}
				return layout.Inset{Left: th.Space.S, Right: th.Space.S}.Layout(gtx, lbl.Fn(th))
			})
		}),
	)
}

func (d *servicesDock) header(gtx layout.Context, th *tokens.Theme) layout.Dimensions {
	s := d.svc()
	for _, id := range d.toolbar.Update(gtx) {
		d.run(id)
	}
	if d.ctxBtn.Clicked(gtx) {
		s.LoadContexts()
		d.contextMenu()
	}
	name := s.Effective
	if name == "" {
		name = s.Snapshot.Context
	}
	if name == "" {
		name = "default"
	}
	d.ctxBtn.Text = "Context: " + name
	d.toolbar.Items = d.toolbarItems()
	status := ""
	switch {
	case s.Busy != "":
		status = s.Busy + "…"
	case s.Snapshot.Refresh == "poll":
		status = "refreshing every few seconds (docker events unavailable)"
	}
	return layout.Inset{Left: th.Space.S, Right: th.Space.S, Top: th.Space.XXS, Bottom: th.Space.XXS}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
			layout.Rigid(func(gtx layout.Context) layout.Dimensions { return d.ctxBtn.Layout(gtx, th) }),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return layout.Inset{Left: th.Space.S}.Layout(gtx, func(gtx layout.Context) layout.Dimensions { return d.toolbar.Layout(gtx, th) })
			}),
			layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
				return layout.Inset{Left: th.Space.S}.Layout(gtx, atoms.Small(status).SingleLine().Fn(th))
			}),
		)
	})
}

func (d *servicesDock) layout(gtx layout.Context, th *tokens.Theme) layout.Dimensions {
	s := d.svc()
	if d.dismiss.Clicked(gtx) {
		s.DismissError()
	}
	if d.follow.Checked() != s.Logs.Follow {
		d.follow.State = atoms.Unchecked
		if s.Logs.Follow {
			d.follow.State = atoms.Checked
		}
	}
	return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
		layout.Rigid(func(gtx layout.Context) layout.Dimensions { return d.header(gtx, th) }),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			if s.Err == "" && len(s.Output) == 0 {
				return layout.Dimensions{}
			}
			kind, title, detail := molecules.BannerInfo, "Done", strings.Join(lastLines(s.Output, 3), "\n")
			if s.Err != "" {
				kind, title = molecules.BannerWarning, firstLine(s.Err)
			}
			return layout.Inset{Left: th.Space.S, Right: th.Space.S, Bottom: th.Space.XXS}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
					layout.Flexed(1, molecules.Banner{Kind: kind, Title: title, Detail: detail}.Fn(th)),
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						return layout.Inset{Left: th.Space.XS}.Layout(gtx, func(gtx layout.Context) layout.Dimensions { return d.dismiss.Layout(gtx, th) })
					}),
				)
			})
		}),
		layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
			return d.split.Layout(gtx, th,
				func(gtx layout.Context) layout.Dimensions { return d.layoutTree(gtx, th) },
				func(gtx layout.Context) layout.Dimensions { return d.layoutLogs(gtx, th) })
		}),
	)
}

// lastLines returns the last n lines.
func lastLines(lines []string, n int) []string {
	if len(lines) > n {
		return lines[len(lines)-n:]
	}
	return lines
}
