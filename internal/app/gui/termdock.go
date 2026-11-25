package gui

import (
	"fmt"
	"image"
	"image/color"
	"regexp"
	"strings"

	"gioui.org/io/key"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/unit"
	"gioui.org/widget"

	"github.com/ape-dev-de/tiegel/internal/api"
	"github.com/ape-dev-de/tiegel/internal/app/gui/controller"
	"github.com/ape-dev-de/tiegel/internal/editor/keymap"
	"github.com/ape-dev-de/tiegel/internal/terminal"
	"github.com/ape-dev-de/tiegel/internal/ui/atoms"
	"github.com/ape-dev-de/tiegel/internal/ui/molecules"
	"github.com/ape-dev-de/tiegel/internal/ui/organisms/findbar"
	"github.com/ape-dev-de/tiegel/internal/ui/organisms/termview"
	"github.com/ape-dev-de/tiegel/internal/ui/tokens"
)

// Terminal tool window (AP15.5): one tab per terminal session with the
// termview organism; Alt+F12 toggles it (and focuses the terminal).
// Sessions are the kernel's terminal.* service; a shell inside a compose
// service or container comes from the Services window (with the kind a
// plugin lists in terminal.kinds).

// Terminal tool window and action IDs.
const (
	ToolTerminal = "terminal"

	ActTerminalView   keymap.Action = "view.terminal"
	ActTerminalNew    keymap.Action = "terminal.new"
	ActTerminalClose  keymap.Action = "terminal.closeTab"
	ActTerminalRename keymap.Action = "terminal.renameTab"
	ActTerminalFind   keymap.Action = "terminal.find"
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

	// find is the find bar of the shown tab (Cmd+F in the terminal).
	find struct {
		open    bool
		bar     findbar.Bar
		id      string // tab the matches belong to
		query   string
		opts    findbar.Options
		matches []terminal.Match
		dropped int // scrollback drop count of the matches
		current int
		seq     int // drops stale answers
		reveal  bool
	}
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
				d.closeTab(t.Info.ID)
			}
		}},
		{ID: ActTerminalRename, Title: "Rename Terminal Tab…", Menu: "View", Section: "terminal", Run: func(*View) { d.openRename() }},
		{ID: ActTerminalFind, Title: "Find in Terminal…", Menu: "View", Section: "terminal", Run: func(*View) { d.openFind() }},
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
// The session kind comes from the backend (terminal.kinds, D372).
func (d *termDock) openServiceShell(service, container string) {
	if container != "" {
		d.terms().OpenIn(api.TerminalTargetContainer, container)
		return
	}
	d.terms().OpenIn(api.TerminalTargetService, service)
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
	// A program wrote the clipboard (OSC 52, only when allowed).
	if t := v.ctl.Terminals; t.Clipboard != nil {
		v.clipboard, t.Clipboard = t.Clipboard, nil
	}
}

// tabTitle is the title of a tab: the name the user gave it, else the
// program running in the foreground, else the title the program set,
// else the default name.
func tabTitle(i terminal.Info) string {
	switch {
	case i.Renamed:
		return i.Name
	case i.Busy && i.Process != "":
		return i.Process
	case i.Title != "":
		return i.Title
	}
	return i.Name
}

// closeTab closes a tab, asking first while a program other than the
// shell runs in it (terminal.confirmClose).
func (d *termDock) closeTab(id string) {
	ts := d.terms()
	t := ts.Tab(id)
	if t == nil {
		return
	}
	if !t.Info.Busy || !ts.Settings.ConfirmClose {
		ts.Close(id)
		return
	}
	proc := t.Info.Process
	d.v.OpenPopup(&Popup{
		Title:  "Close Terminal Tab?",
		Detail: fmt.Sprintf("%q is still running in %q. Closing the tab ends it.", proc, tabTitle(t.Info)),
		Buttons: []*PopupButton{
			{Text: "Terminate", Kind: atoms.Primary, OnClick: func() { ts.Close(id) }},
			{Text: "Cancel", Kind: atoms.Secondary},
		},
	})
}

// openFind shows the find bar for the current tab.
func (d *termDock) openFind() {
	if d.terms().Current() == nil {
		return
	}
	d.v.ShowTool(ToolTerminal)
	d.find.open = true
	d.find.bar.Open(d.find.query, false)
}

// closeFind hides the find bar and returns the focus to the terminal.
func (d *termDock) closeFind() {
	d.find.open = false
	d.find.matches, d.find.query = nil, ""
	d.find.seq++
	d.focusNext = true
}

// findPattern turns the bar's query into the backend's options (whole
// word becomes a regular expression).
func findPattern(q string, o findbar.Options) terminal.FindOptions {
	f := terminal.FindOptions{Query: q, CaseSensitive: o.CaseSensitive, Regex: o.Regex}
	if o.WholeWord && q != "" {
		if !o.Regex {
			q = regexp.QuoteMeta(q)
		}
		f.Query, f.Regex = `\b(?:`+q+`)\b`, true
	}
	return f
}

// search runs the query against the shown tab.
func (d *termDock) search(id string) {
	d.find.seq++
	d.find.id = id
	seq := d.find.seq
	if d.find.query == "" {
		d.find.matches = nil
		d.find.bar.Matches, d.find.bar.Current, d.find.bar.Invalid = 0, 0, false
		return
	}
	d.terms().Find(id, findPattern(d.find.query, d.find.opts), func(res terminal.FindResult, err error) {
		if seq != d.find.seq {
			return
		}
		d.find.bar.Invalid = err != nil
		d.find.matches, d.find.dropped = res.Matches, res.Dropped
		// Start at the newest match (the bottom, like a terminal).
		d.find.current = len(res.Matches) - 1
		d.find.reveal = true
	})
}

// layoutFind draws the find bar and handles its events.
func (d *termDock) layoutFind(gtx layout.Context, th *tokens.Theme, id string) layout.Dimensions {
	f := &d.find
	for _, e := range f.bar.Update(gtx) {
		switch e := e.(type) {
		case findbar.QueryEvent:
			f.query, f.opts = e.Query, e.Options
			d.search(id)
		case findbar.NextEvent, findbar.PrevEvent:
			if n := len(f.matches); n > 0 {
				step := -1 // next goes up (older output)
				if _, ok := e.(findbar.PrevEvent); ok {
					step = 1
				}
				f.current = ((f.current+step)%n + n) % n
				f.reveal = true
			} else {
				d.search(id)
			}
		case findbar.CloseEvent:
			d.closeFind()
		}
	}
	if f.id != id && f.query != "" {
		f.id = id
		d.search(id)
	}
	f.bar.Matches = len(f.matches)
	f.bar.Current = 0
	if f.current >= 0 && f.current < len(f.matches) {
		f.bar.Current = len(f.matches) - f.current
	}
	return f.bar.Layout(gtx, th)
}

// hexColor formats a color for terminal.colors.
func hexColor(c color.NRGBA) string { return fmt.Sprintf("#%02x%02x%02x", c.R, c.G, c.B) }

// syncColors tells the backend the theme colors (programs query the
// background to pick a light or dark scheme).
func (d *termDock) syncColors(th *tokens.Theme) {
	tp := th.Terminal
	p := api.TerminalColorsParams{FG: hexColor(tp.Foreground), BG: hexColor(tp.Background), Cursor: hexColor(tp.Cursor)}
	for _, c := range tp.ANSI {
		p.ANSI = append(p.ANSI, hexColor(c))
	}
	d.terms().SetColors(p)
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
// on macOS except the terminal's own Cmd+C/V/A/F, F-keys with modifiers
// (Alt+F12 toggles the terminal), and on other systems Alt+digit (tool
// windows).
func keepShortcut(mac bool) func(keymap.Chord) bool {
	return func(c keymap.Chord) bool {
		fkey := len(c.Key) >= 2 && c.Key[0] == 'F' && c.Key[1] >= '1' && c.Key[1] <= '9'
		switch {
		case terminalOwnKey(mac, c):
			return false
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

// terminalOwnKey reports the chords the terminal view handles itself
// (copy, paste, select all, find): they never run window shortcuts
// while the terminal has the focus, whatever terminal.overrideIdeShortcuts
// says (otherwise Cmd+V pastes into the editor).
func terminalOwnKey(mac bool, c keymap.Chord) bool {
	if len(c.Key) != 1 || !strings.Contains("CVAF", c.Key) {
		return false
	}
	if mac {
		return c.Mods == keymap.Cmd
	}
	return c.Mods == keymap.Ctrl|keymap.Shift
}

// notTerminalOwnKey keeps every window shortcut but the terminal's own
// keys (terminal.overrideIdeShortcuts off).
func notTerminalOwnKey(mac bool) func(keymap.Chord) bool {
	return func(c keymap.Chord) bool { return !terminalOwnKey(mac, c) }
}

// editAction runs an Edit menu action on the terminal when it has the
// focus; it reports whether it did.
func (d *termDock) editAction(a keymap.Action) bool {
	if !d.focused() {
		return false
	}
	tv := d.views[d.shownID]
	switch a {
	case keymap.Paste:
		tv.Paste()
	case keymap.Copy:
		tv.Copy()
	case keymap.SelectAll:
		tv.SelectAll()
	case keymap.Find:
		d.openFind()
	default:
		return false
	}
	d.v.Invalidate()
	return true
}

// pressed focuses the terminal after a click anywhere in its window
// (tab row, empty area), unless an input of the window takes the keys.
func (d *termDock) pressed() {
	if d.find.open || d.focused() {
		return
	}
	d.focusNext = true
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
	d.syncColors(th)
	for id, t := range d.tabs {
		sel, closed := t.Update(gtx)
		switch {
		case closed:
			d.closeTab(id)
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
			return layout.Inset{Left: th.Space.M, Right: th.Space.M, Top: th.Space.S}.Layout(gtx, atoms.Muted(hint).Fn(th))
		}))
	} else {
		if d.find.open {
			children = append(children, layout.Rigid(func(gtx layout.Context) layout.Dimensions { return d.layoutFind(gtx, th, cur.Info.ID) }))
		}
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
		t.Title, t.Tooltip = tabTitle(x.Info), strings.Join(x.Info.Command, " ")
		if x.Info.Title != "" {
			t.Tooltip = x.Info.Title + " · " + t.Tooltip
		}
		if x.Info.Busy && x.Info.Process != "" {
			t.Tooltip = x.Info.Process + " · " + t.Tooltip
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
	// Icon buttons as in panel headers (D464): ActionButton squares
	// with an XXS gap.
	d.addBtn.Size, d.renameBt.Size = th.Size.ActionButton, th.Size.ActionButton
	gap := layout.Rigid(layout.Spacer{Width: th.Space.XXS}.Layout)
	children = append(children, gap,
		layout.Rigid(func(gtx layout.Context) layout.Dimensions { return d.addBtn.Layout(gtx, th) }))
	if cur != nil {
		children = append(children, gap,
			layout.Rigid(func(gtx layout.Context) layout.Dimensions { return d.renameBt.Layout(gtx, th) }))
	}
	// One row of the panel header height, padded like the headers, the
	// content vertically centered.
	h := gtx.Dp(th.Size.PanelHeaderH)
	pad := gtx.Dp(th.Space.XS)
	width := gtx.Constraints.Max.X
	cgtx := gtx
	cgtx.Constraints = layout.Constraints{Max: image.Pt(max(0, width-2*pad), h)}
	rec := op.Record(gtx.Ops)
	dims := layout.Flex{Alignment: layout.Middle}.Layout(cgtx, children...)
	call := rec.Stop()
	t := op.Offset(image.Pt(pad, (h-dims.Size.Y)/2)).Push(gtx.Ops)
	call.Add(gtx.Ops)
	t.Pop()
	return layout.Dimensions{Size: image.Pt(width, h)}
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
	tv.LineHeight = float32(st.LineHeight)
	tv.CopyOnSelect = st.CopyOnSelect
	tv.Matches, tv.Current = nil, -1
	if f := &d.find; f.open && f.id == id && len(f.matches) > 0 && cur.Screen != nil {
		// Line indexes move when the scrollback drops lines.
		shift := cur.Screen.Dropped - f.dropped
		tv.Matches = make([]terminal.Match, len(f.matches))
		for i, m := range f.matches {
			m.Line, m.EndLine = m.Line-shift, m.EndLine-shift
			tv.Matches[i] = m
		}
		tv.Current = f.current
		if f.reveal && f.current >= 0 && f.current < len(tv.Matches) {
			f.reveal = false
			tv.Reveal(tv.Matches[f.current].Line)
		}
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
			// An input method replaced earlier text: erase it first.
			ts.SendText(id, strings.Repeat("\x7f", e.Erase)+e.Text)
		case termview.MouseEvent:
			ts.SendMouse(id, e.Mouse)
		case termview.OpenURLEvent:
			ts.OpenURL(e.URL)
		case termview.FindEvent:
			d.openFind()
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
			ts.SendFocus(id, e.Focused)
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
