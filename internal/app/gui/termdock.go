package gui

import (
	"fmt"
	"strings"

	"gioui.org/io/key"
	"gioui.org/layout"
	"gioui.org/unit"
	"gioui.org/widget"

	"github.com/ape-dev-de/tiegel/internal/api"
	"github.com/ape-dev-de/tiegel/internal/app/gui/controller"
	"github.com/ape-dev-de/tiegel/internal/editor/keymap"
	"github.com/ape-dev-de/tiegel/internal/terminal"
	"github.com/ape-dev-de/tiegel/internal/ui/atoms"
	"github.com/ape-dev-de/tiegel/internal/ui/molecules"
	"github.com/ape-dev-de/tiegel/internal/ui/organisms/termview"
	"github.com/ape-dev-de/tiegel/internal/ui/tokens"
)

// Terminal tool window (AP15.5): one tab per terminal session with the
// termview organism; Alt+F12 toggles it (and focuses the terminal).
// Sessions are the kernel's terminal.* service; a shell inside a compose
// service comes from the Services window (kind docker.service).

// Terminal tool window and action IDs.
const (
	ToolTerminal = "terminal"

	ActTerminalView   keymap.Action = "view.terminal"
	ActTerminalNew    keymap.Action = "terminal.new"
	ActTerminalClose  keymap.Action = "terminal.closeTab"
	ActTerminalRename keymap.Action = "terminal.renameTab"
)

type termDock struct {
	v       *View
	project string

	views    map[string]*termview.View
	tabs     map[string]*molecules.Tab
	addBtn   atoms.IconButton
	renameBt atoms.IconButton
	// focusNext focuses the current terminal on the next layout.
	focusNext bool
	// shownID is the tab whose view was laid out last.
	shownID string
	rename  atoms.TextField
}

func installTerminal(v *View) *termDock {
	d := &termDock{v: v, views: map[string]*termview.View{}, tabs: map[string]*molecules.Tab{},
		addBtn:   atoms.IconButton{Glyph: atoms.GlyphAdd, Description: "New terminal tab"},
		renameBt: atoms.IconButton{Glyph: atoms.GlyphEdit, Description: "Rename terminal tab"},
	}
	v.Registry.AddToolWindow(ToolWindow{
		ID: ToolTerminal, Title: "Terminal", Glyph: atoms.GlyphComputer, Side: Bottom, Order: 16, Share: 0.35,
		Mac: "Alt+F12", PC: "Alt+F12", ActionID: ActTerminalView,
		Badge: func() (string, atoms.BadgeKind) {
			if n := v.ctl.Terminals.RunningCount(); n > 1 {
				return fmt.Sprint(n), atoms.BadgeInfo
			}
			return "", 0
		},
		Layout: d.layout,
		// Alt+F12 from elsewhere focuses an open terminal; from the
		// terminal it hides the window.
		BeforeToggle: func() bool {
			if v.ToolShown(ToolTerminal) && !d.focused() {
				d.focusNext = true
				v.activeTool = ToolTerminal
				return true
			}
			return false
		},
		OnOpen: func() {
			if len(v.ctl.Terminals.Tabs) == 0 {
				d.newTab()
			}
			d.focusNext = true
		},
	})
	v.Registry.AddFrameHandler(d.frame)
	for _, a := range []Action{
		{ID: ActTerminalNew, Title: "New Terminal Tab", Menu: "View", Section: "terminal", Run: func(*View) { d.newTab() }},
		{ID: ActTerminalClose, Title: "Close Terminal Tab", Menu: "View", Section: "terminal", Run: func(*View) {
			if t := v.ctl.Terminals.Current(); t != nil {
				v.ctl.Terminals.Close(t.Info.ID)
			}
		}},
		{ID: ActTerminalRename, Title: "Rename Terminal Tab…", Menu: "View", Section: "terminal", Run: func(*View) { d.openRename() }},
	} {
		v.Registry.AddAction(a)
	}
	return d
}

func (d *termDock) terms() *controller.Terminals { return d.v.ctl.Terminals }

// newTab opens a shell in the project.
func (d *termDock) newTab() {
	d.terms().Open(api.TerminalOpenParams{})
}

// OpenServiceShell opens a shell inside a compose service or a
// container (Services window) in the embedded terminal.
func (d *termDock) openServiceShell(service, container string) {
	p := api.TerminalOpenParams{Kind: "docker.service", Target: service}
	if container != "" {
		p = api.TerminalOpenParams{Kind: "docker.container", Target: container}
	}
	d.terms().Open(p)
}

// frame follows the project and shows the window when a session opened
// from this window.
func (d *termDock) frame(v *View, _ layout.Context) {
	if root := v.ctl.Project.Root; root != d.project {
		d.project = root
		v.ctl.Terminals.ProjectChanged()
	}
	if t := v.ctl.Terminals; t.Opened {
		t.Opened = false
		v.ShowTool(ToolTerminal)
		v.activeTool = ToolTerminal
		d.focusNext = true
	}
}

// focused reports whether a terminal view has the keyboard focus.
func (d *termDock) focused() bool {
	if d == nil || !d.v.ToolShown(ToolTerminal) {
		return false
	}
	tv := d.views[d.shownID]
	return tv != nil && tv.Focused()
}

// keepShortcut reports which window shortcuts stay with the IDE while
// the terminal has the focus (terminal.overrideIdeShortcuts): Cmd chords
// on macOS, F-keys with modifiers (Alt+F12 toggles the terminal), and on
// other systems Alt+digit (tool windows).
func keepShortcut(mac bool) func(keymap.Chord) bool {
	return func(c keymap.Chord) bool {
		fkey := len(c.Key) >= 2 && c.Key[0] == 'F' && c.Key[1] >= '1' && c.Key[1] <= '9'
		switch {
		case mac && c.Mods&keymap.Cmd != 0:
			return true
		case fkey && c.Mods&(keymap.Alt|keymap.Ctrl|keymap.Cmd) != 0:
			return true
		case !mac && c.Mods == keymap.Alt && len(c.Key) == 1 && c.Key[0] >= '0' && c.Key[0] <= '9':
			return true
		}
		return false
	}
}

// openRename asks for a new name of the current tab.
func (d *termDock) openRename() {
	t := d.terms().Current()
	if t == nil {
		return
	}
	id := t.Info.ID
	d.rename = atoms.TextField{Hint: "Tab name"}
	d.rename.Editor.SingleLine, d.rename.Editor.Submit = true, true
	d.rename.SetText(t.Info.Name)
	d.rename.Editor.SetCaret(len(t.Info.Name), 0)
	focus := true
	apply := func() {
		d.terms().Rename(id, d.rename.Text())
		d.focusNext = true
	}
	d.v.OpenPopup(&Popup{
		Title: "Rename Terminal Tab", Centered: true,
		Body: func(gtx layout.Context, th *tokens.Theme) layout.Dimensions {
			if focus {
				focus = false
				gtx.Execute(key.FocusCmd{Tag: &d.rename.Editor})
			}
			for {
				ev, ok := d.rename.Editor.Update(gtx)
				if !ok {
					break
				}
				if _, ok := ev.(widget.SubmitEvent); ok {
					d.v.Post(func() {
						d.v.ClosePopup()
						apply()
					})
				}
			}
			return d.rename.Layout(gtx, th)
		},
		Buttons: []*PopupButton{
			{Text: "Rename", Kind: atoms.Primary, OnClick: apply},
			{Text: "Cancel", Kind: atoms.Secondary},
		},
	})
}

func (d *termDock) layout(gtx layout.Context, th *tokens.Theme) layout.Dimensions {
	ts := d.terms()
	if d.addBtn.Clicked(gtx) {
		d.newTab()
	}
	if d.renameBt.Clicked(gtx) {
		d.openRename()
	}
	for id, t := range d.tabs {
		sel, closed := t.Update(gtx)
		switch {
		case closed:
			ts.Close(id)
		case sel:
			ts.Select(id)
			d.focusNext = true
		}
	}
	cur := ts.Current()
	children := []layout.FlexChild{
		layout.Rigid(func(gtx layout.Context) layout.Dimensions { return d.layoutTabs(gtx, th) }),
	}
	if cur == nil {
		children = append(children, layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
			hint := "No terminal. + opens a shell in the project; Services › Open Shell opens one in a compose service."
			return layout.Inset{Left: th.Space.S, Top: th.Space.S}.Layout(gtx, atoms.Muted(hint).Fn(th))
		}))
	} else {
		children = append(children, layout.Flexed(1, func(gtx layout.Context) layout.Dimensions { return d.layoutTerminal(gtx, th, cur) }))
	}
	return layout.Flex{Axis: layout.Vertical}.Layout(gtx, children...)
}

// layoutTabs draws one tab per session and the buttons.
func (d *termDock) layoutTabs(gtx layout.Context, th *tokens.Theme) layout.Dimensions {
	ts := d.terms()
	cur := ts.Current()
	seen := map[string]bool{}
	var children []layout.FlexChild
	for _, x := range ts.Tabs {
		id := x.Info.ID
		seen[id] = true
		t := d.tabs[id]
		if t == nil {
			t = molecules.NewTab("")
			d.tabs[id] = t
		}
		t.Title, t.Tooltip = x.Info.Name, strings.Join(x.Info.Command, " ")
		if x.Info.Title != "" {
			t.Tooltip = x.Info.Title + " · " + t.Tooltip
		}
		t.Glyph = atoms.GlyphComputer
		if x.Info.Kind != terminal.KindShell {
			t.Glyph = atoms.GlyphContainer
		}
		t.Active, t.Modified = x == cur, false
		children = append(children, layout.Rigid(func(gtx layout.Context) layout.Dimensions { return t.Layout(gtx, th) }))
	}
	for id := range d.tabs {
		if !seen[id] {
			delete(d.tabs, id)
			delete(d.views, id)
		}
	}
	children = append(children,
		layout.Rigid(func(gtx layout.Context) layout.Dimensions { return d.addBtn.Layout(gtx, th) }),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			if cur == nil {
				return layout.Dimensions{}
			}
			return d.renameBt.Layout(gtx, th)
		}))
	return layout.Flex{Alignment: layout.Middle}.Layout(gtx, children...)
}

// view returns the organism of a tab.
func (d *termDock) view(id string) *termview.View {
	tv := d.views[id]
	if tv == nil {
		tv = termview.New()
		d.views[id] = tv
	}
	return tv
}

// layoutTerminal draws the current session and forwards its input.
func (d *termDock) layoutTerminal(gtx layout.Context, th *tokens.Theme, cur *controller.TermTab) layout.Dimensions {
	v, ts := d.v, d.terms()
	id := cur.Info.ID
	tv := d.view(id)
	d.shownID = id
	st := ts.Settings
	tv.Screen = cur.Screen
	tv.Mac = v.goos == "darwin"
	tv.OptionAsMeta = st.OptionAsMeta
	tv.EscapeToEditor = st.EscapeToEditor
	tv.FontSize = unit.Sp(st.FontSize)
	if st.FontSize <= 0 {
		tv.FontSize = 0
	}
	tv.Notice = ""
	if !cur.Info.Running {
		tv.Notice = fmt.Sprintf("[process exited with code %d]", cur.Info.ExitCode)
	}
	if d.focusNext {
		d.focusNext = false
		tv.Focus()
	}
	for _, e := range tv.Update(gtx) {
		switch e := e.(type) {
		case termview.KeyEvent:
			ts.SendKey(id, e.Key)
		case termview.TextEvent:
			ts.SendText(id, e.Text)
		case termview.PasteEvent:
			ts.Paste(id, e.Text)
		case termview.CopyEvent:
			ts.Copy(id, api.TerminalPos{Line: e.From.Line, Col: e.From.Col}, api.TerminalPos{Line: e.To.Line, Col: e.To.Col}, func(s string) {
				if s != "" {
					v.clipboard = &s
				}
			})
		case termview.LinkEvent:
			v.ctl.OpenFile(e.Link.File, ptr(controller.AtLine(e.Link.File, e.Link.Line)))
		case termview.ResizeEvent:
			ts.Resize(id, e.Cols, e.Rows)
		case termview.EscapeEvent:
			tv.Blur()
			v.focusEditor = true
		case termview.HideEvent:
			v.HideTool(ToolTerminal)
		case termview.FocusEvent:
			if e.Focused {
				v.activeTool = ToolTerminal
			}
		}
	}
	dims := tv.Layout(gtx, th)
	if tv.Screen != nil {
		ts.View(id, tv.Following(), tv.Top(), tv.Rows())
	} else {
		ts.View(id, true, 0, tv.Rows())
	}
	return dims
}
