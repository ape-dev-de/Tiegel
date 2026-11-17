package gui

import (
	"fmt"
	"strings"

	"gioui.org/layout"

	"github.com/ape-dev-de/tiegel/internal/app/gui/controller"
	"github.com/ape-dev-de/tiegel/internal/ui/atoms"
	"github.com/ape-dev-de/tiegel/internal/ui/molecules"
	"github.com/ape-dev-de/tiegel/internal/ui/tokens"
)

// Language server UI (M11, AP11.3/11.4): the status bar entry shows the
// supervisor state and opens a menu (choose server, on/off, restart, log,
// and when the server is missing: reason, searched locations, Choose
// Path…, Install…). The same menu opens from the "… needs a language
// server" notices. The "Language Server" tool window shows the state, the
// searched locations, a running installation and the server log (stderr,
// supervisor events, optional JSON-RPC trace).

// ToolLSP is the language server tool window.
const ToolLSP = "lsp"

type lspDock struct {
	v         *View
	refreshed bool
	project   string
	logSeen   uint64

	restart, trace, install, cancel, path atoms.Button
	list                                  molecules.ScrollList
	follow                                bool
}

func installLSP(v *View) *lspDock {
	d := &lspDock{v: v, follow: true,
		restart: atoms.Button{Text: "Restart", Kind: atoms.Secondary, Glyph: atoms.GlyphRefresh},
		trace:   atoms.Button{Text: "Trace", Kind: atoms.Ghost},
		install: atoms.Button{Text: "Install…", Kind: atoms.Primary},
		cancel:  atoms.Button{Text: "Cancel install", Kind: atoms.Secondary, Glyph: atoms.GlyphStop},
		path:    atoms.Button{Text: "Choose Path…", Kind: atoms.Ghost, Glyph: atoms.GlyphFolder},
	}
	v.Registry.AddToolWindow(ToolWindow{
		ID: ToolLSP, Title: "Language Server", Glyph: atoms.GlyphListen, Side: Bottom, Order: 60, Share: 0.35,
		Badge: func() (string, atoms.BadgeKind) {
			switch v.ctl.LSP.Status.State {
			case controller.LSPNotFound, controller.LSPStopped:
				return "!", atoms.BadgeWarning
			case controller.LSPCrashed:
				return "…", atoms.BadgeWarning
			}
			if v.ctl.LSP.Install.Running() {
				return "…", atoms.BadgeSuccess
			}
			return "", 0
		},
		Layout: d.layout,
		OnOpen: func() { v.ctl.RefreshLSP(); v.ctl.LoadLSPLog() },
	})
	v.Registry.AddFrameHandler(d.frame)
	return d
}

// frame loads the server list once per project.
func (d *lspDock) frame(v *View, _ layout.Context) {
	root := v.ctl.Project.Root
	if root != d.project {
		d.project, d.refreshed = root, false
	}
	if !d.refreshed && v.ctl.Project.Open {
		d.refreshed = true
		v.ctl.RefreshLSP()
	}
}

func (d *lspDock) ctl() *controller.Controller { return d.v.ctl }

// selectedPlugin returns the plugin to offer "Install…" for.
func (d *lspDock) selectedPlugin() (plugin, name string) {
	st := d.ctl().LSP.Status
	if st.Plugin != "" {
		return st.Plugin, orDefault(st.Name, st.Server)
	}
	for _, s := range d.ctl().LSP.Servers {
		if s.Selected || st.Selected == s.ID {
			return s.Plugin, s.Name
		}
	}
	return "", ""
}

// menuItems builds the status bar menu.
func (d *lspDock) menuItems() []MenuItem {
	c := d.ctl()
	st := c.LSP.Status
	name := orDefault(st.Name, st.Server)
	head := "Language server: " + orDefault(st.State, "unknown")
	if name != "" {
		head = name + ": " + orDefault(st.State, "unknown")
	}
	items := []MenuItem{{Title: head, Disabled: true}}
	if st.Reason != "" && st.State != controller.LSPRunning {
		items = append(items, MenuItem{Title: firstLine(st.Reason), Disabled: true})
	}
	// Choose server: only servers of enabled plugins.
	sel := orDefault(st.Selected, "auto")
	choose := []MenuItem{{Title: "Automatic", Checked: sel == "auto", Run: func() { c.SelectLSP("auto") }}}
	for _, s := range c.LSP.Servers {
		title := s.Name
		if !s.Found {
			title += " (not installed)"
		}
		if s.Proprietary {
			title += " – proprietary"
		}
		id := s.ID
		choose = append(choose, MenuItem{Title: title, Checked: sel == id, Run: func() { c.SelectLSP(id) }})
	}
	choose = append(choose, MenuItem{Title: "Off (use the index)", Checked: sel == "none", Run: func() { c.SelectLSP("none") }})
	items = append(items, MenuItem{Separator: true}, MenuItem{Title: "Server", Children: choose})
	if sel == "none" {
		items = append(items, MenuItem{Title: "Turn On", Run: func() { c.SelectLSP("auto") }})
	} else {
		items = append(items,
			MenuItem{Title: "Restart", Icon: "refresh", Run: c.RestartLSP},
			MenuItem{Title: "Turn Off", Run: c.DisableLSP})
	}
	items = append(items, MenuItem{Title: "Show Log", Run: func() { d.v.ShowTool(ToolLSP) }})
	if st.State == controller.LSPNotFound {
		items = append(items, MenuItem{Separator: true},
			MenuItem{Title: fmt.Sprintf("Searched Locations (%d)…", len(st.Searched)), Run: func() { d.v.ShowTool(ToolLSP) }})
		if plugin, pname := d.selectedPlugin(); plugin != "" {
			items = append(items,
				MenuItem{Title: "Choose Path of " + pname + "…", Run: d.choosePath},
				MenuItem{Title: "Install " + pname + "…", Run: func() { d.openInstall(plugin) }})
		}
	}
	return items
}

// openMenu shows the language server menu.
func (d *lspDock) openMenu() {
	d.ctl().RefreshLSP()
	d.v.ShowMenu("Language Server", d.menuItems())
}

// choosePath asks for the executable of the selected server.
func (d *lspDock) choosePath() {
	st := d.ctl().LSP.Status
	server := st.Server
	if server == "" {
		for _, s := range d.ctl().LSP.Servers {
			if s.Selected {
				server = s.ID
			}
		}
	}
	if server == "" {
		return
	}
	d.v.OpenChooser(ChooserSpec{
		Title: "Executable of " + orDefault(st.Name, server), Hint: "Absolute path, e.g. /opt/homebrew/bin/" + server, NoList: true,
		OnChoose: func(_ int, q string) {
			if strings.TrimSpace(q) != "" {
				d.ctl().SetLSPPath(server, q)
			}
		},
	})
}

// installDetail describes a plan for the confirmation popup.
func installDetail(p controller.InstallPlan) string {
	var b strings.Builder
	if p.Note != "" {
		b.WriteString(p.Note + "\n\n")
	}
	if p.Display != "" {
		fmt.Fprintf(&b, "Command: %s\nTarget: %s", p.Display, p.Scope)
		if p.Dir != "" {
			fmt.Fprintf(&b, " (in %s)", p.Dir)
		}
		b.WriteString("\n")
		if p.ChangesComposerJSON {
			b.WriteString("composer.json and composer.lock of the project will change.\n")
		} else {
			b.WriteString("The project's composer.json is not changed.\n")
		}
	}
	for _, e := range p.Effects {
		b.WriteString("• " + e + "\n")
	}
	if p.Hint != "" {
		b.WriteString("\n" + p.Hint)
	}
	return strings.TrimSpace(b.String())
}

// openInstall previews the installation and asks for confirmation.
func (d *lspDock) openInstall(plugin string) { d.planInstall(plugin, "") }

func (d *lspDock) planInstall(plugin, scope string) {
	d.ctl().PlanInstall(plugin, scope, func(p controller.InstallPlan) {
		pop := &Popup{Title: "Install " + p.Tool, Detail: installDetail(p), Width: 520}
		if p.Available {
			pop.Buttons = append(pop.Buttons, &PopupButton{Text: "Install", Kind: atoms.Primary, OnClick: func() {
				d.ctl().RunInstall(p)
				d.v.ShowTool(ToolLSP)
			}})
			for _, s := range p.Scopes {
				if s != p.Scope {
					other := s
					pop.Buttons = append(pop.Buttons, &PopupButton{Text: "Into " + other + " instead…", Kind: atoms.Ghost, OnClick: func() {
						d.v.Post(func() { d.planInstall(plugin, other) })
					}})
				}
			}
		}
		pop.Buttons = append(pop.Buttons, &PopupButton{Text: "Cancel", Kind: atoms.Secondary})
		d.v.OpenPopup(pop)
	})
}

// noticeClick opens the menu for "… needs a language server" notices.
func (d *lspDock) noticeClick() bool {
	if !controller.IsNeedsLSPNotice(d.ctl().Status.Notice) {
		return false
	}
	d.ctl().Status.Notice = ""
	d.openMenu()
	return true
}

// lines returns the text lines of the tool window body.
func (d *lspDock) lines() []atoms.Label {
	c := d.ctl()
	st := c.LSP.Status
	var out []atoms.Label
	if st.State == controller.LSPNotFound && len(st.Searched) > 0 {
		out = append(out, atoms.Strong("Searched locations"))
		for _, s := range st.Searched {
			out = append(out, atoms.Code("  "+s))
		}
	}
	if r := c.LSP.Install; r != nil {
		out = append(out, atoms.Strong(fmt.Sprintf("Installation of %s: %s – %s", r.Tool, r.State, r.Display)))
		for _, l := range r.Output {
			out = append(out, atoms.Code("  "+l))
		}
		if r.Error != "" {
			out = append(out, atoms.Muted("  "+r.Error))
		}
	}
	if len(c.LSP.Log) > 0 {
		out = append(out, atoms.Strong("Server log"))
	}
	for _, e := range c.LSP.Log {
		l := atoms.Code(e.Kind + "  " + e.Text).SingleLine()
		if e.Kind == "event" {
			l = atoms.Muted(e.Text).SingleLine()
		}
		out = append(out, l)
	}
	return out
}

func (d *lspDock) layout(gtx layout.Context, th *tokens.Theme) layout.Dimensions {
	c := d.ctl()
	st := c.LSP.Status
	if d.restart.Clicked(gtx) {
		c.RestartLSP()
	}
	if d.trace.Clicked(gtx) {
		c.SetLSPTrace(!st.Trace)
	}
	if d.install.Clicked(gtx) {
		if plugin, _ := d.selectedPlugin(); plugin != "" {
			d.openInstall(plugin)
		}
	}
	if d.cancel.Clicked(gtx) {
		c.CancelInstall()
	}
	if d.path.Clicked(gtx) {
		d.choosePath()
	}
	d.trace.Kind = atoms.Ghost
	if st.Trace {
		d.trace.Kind = atoms.Secondary
	}
	lines := d.lines()
	if d.logSeen != c.LSP.LogVersion+c.LSP.Version && d.follow {
		d.logSeen = c.LSP.LogVersion + c.LSP.Version
		d.list.ScrollTo(len(lines))
	}
	title := c.Status.LSP
	if title == "" {
		title = "Language server"
	}
	detail := c.Status.LSPHint
	kind := molecules.BannerInfo
	switch st.State {
	case controller.LSPNotFound, controller.LSPStopped, controller.LSPCrashed:
		kind = molecules.BannerWarning
	}
	btns := []*atoms.Button{&d.restart, &d.trace}
	if st.State == controller.LSPNotFound {
		btns = append(btns, &d.path, &d.install)
	}
	if c.LSP.Install.Running() {
		btns = append(btns, &d.cancel)
	}
	return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			children := []layout.FlexChild{layout.Flexed(1, molecules.Banner{Kind: kind, Title: title, Detail: detail}.Fn(th))}
			for _, b := range btns {
				children = append(children, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return layout.Inset{Left: th.Space.XS}.Layout(gtx, func(gtx layout.Context) layout.Dimensions { return b.Layout(gtx, th) })
				}))
			}
			return layout.Inset{Left: th.Space.S, Right: th.Space.S, Top: th.Space.XS, Bottom: th.Space.XS}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return layout.Flex{Alignment: layout.Middle}.Layout(gtx, children...)
			})
		}),
		layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
			if len(lines) == 0 {
				return layout.Inset{Left: th.Space.S, Top: th.Space.XS}.Layout(gtx, atoms.Muted("No log yet. Trace adds the JSON-RPC messages.").Fn(th))
			}
			return d.list.Layout(gtx, th, len(lines), func(gtx layout.Context, i int) layout.Dimensions {
				return layout.Inset{Left: th.Space.S, Right: th.Space.S}.Layout(gtx, lines[i].Fn(th))
			})
		}),
	)
}
