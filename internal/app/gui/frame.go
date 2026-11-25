package gui

import (
	"fmt"
	"io"
	"strings"

	"gioui.org/io/clipboard"
	"gioui.org/io/key"
	"gioui.org/layout"

	"github.com/ape-dev-de/tiegel/internal/app/gui/native"
	"github.com/ape-dev-de/tiegel/internal/ui/organisms/welcome"
	"github.com/ape-dev-de/tiegel/internal/ui/templates"
	"github.com/ape-dev-de/tiegel/internal/ui/tokens"
)

// newShaper creates the text shaper of a window (system fonts with the
// Go fonts as last fallback, D112).
var newShaper = tokens.NewShaper

// Frame lays out one frame of the window filling gtx.
func (v *View) Frame(gtx layout.Context) layout.Dimensions {
	v.runPosted()
	v.ctl.Drain()
	v.syncKeymap()
	v.applyTheme()
	th := v.th
	if v.clipboard != nil {
		gtx.Execute(clipboard.WriteCmd{Type: "application/text", Data: io.NopCloser(strings.NewReader(*v.clipboard))})
		v.clipboard = nil
	}
	if !v.ctl.Project.Open {
		return v.frameWelcome(gtx)
	}

	v.syncTools()
	for _, h := range v.Registry.frames {
		h(v, gtx)
	}
	v.syncTools()
	if !v.keymapRecording() {
		if v.termd.focused() && v.ctl.Terminals.Settings.OverrideKeys {
			// Keys like Ctrl+R belong to the shell; only the kept
			// shortcuts reach the IDE.
			v.termGlobal.keep = keepShortcut(v.goos == "darwin")
			v.termGlobal.build(v.Registry, ScopeGlobal, nil)
			v.termGlobal.process(gtx, v)
		} else if v.termd.focused() {
			// The terminal's own copy/paste keys stay with it.
			v.termOwn.keep = notTerminalOwnKey(v.goos == "darwin")
			v.termOwn.build(v.Registry, ScopeGlobal, nil)
			v.termOwn.process(gtx, v)
		} else {
			v.global.build(v.Registry, ScopeGlobal, nil)
			v.global.process(gtx, v)
		}
	}
	v.hdr.update(gtx)
	v.handleBench(gtx)
	v.handleTree(gtx)
	v.handleStatus(gtx)
	v.handleProblems(gtx)
	v.handleRecovery(gtx)
	v.offerImportSettings()
	v.offerProjectSetup()
	v.handleOverlay(gtx)
	v.closeStaleEditors()

	slots := templates.Slots{
		Editor: v.layoutEditorArea,
		Status: func(gtx layout.Context) layout.Dimensions { return v.status.Layout(gtx, th) },
	}
	slots.Header = func(gtx layout.Context) layout.Dimensions { return v.hdr.layout(gtx, th) }
	if !native.MenuSupported() {
		slots.Menu = func(gtx layout.Context) layout.Dimensions { return v.menuBar.layout(gtx, th, v) }
	}
	v.toolSlots(&slots)
	v.overlaySlot(&slots)
	d := v.bench.Layout(gtx, th, slots)
	v.pointerNav(gtx)
	v.escapeKeys(gtx)
	v.ctl.PersistToolLayout()
	return d
}

// frameWelcome shows the welcome screen (no project open).
func (v *View) frameWelcome(gtx layout.Context) layout.Dimensions {
	th := v.th
	v.welcome.Recent = v.ctl.RecentProjects
	v.welcome.Error = v.ctl.Status.Notice
	v.welcome.Hints = []string{
		fmt.Sprintf("Find Action    %s", v.Registry.ChordText(ActFindAction, v.goos)),
		fmt.Sprintf("Settings    %s", v.Registry.ChordText(ActSettings, v.goos)),
	}
	for _, e := range v.welcome.Update(gtx) {
		switch e := e.(type) {
		case welcome.OpenEvent:
			v.ctl.Status.Notice = ""
			v.requestProject(e.Path)
		case welcome.BrowseEvent:
			v.openProjectDialog()
		case welcome.RemoveEvent:
			v.ctl.RemoveRecentProject(e.Path)
		case welcome.ImportEvent:
			v.openImportSettings()
		}
	}
	v.global.build(v.Registry, ScopeGlobal, nil)
	if !v.keymapRecording() {
		v.global.process(gtx, v)
	}
	for _, e := range v.bench.Update(gtx) {
		if _, ok := e.(templates.DismissEvent); ok {
			v.closeOverlay()
		}
	}
	v.handleOverlay(gtx)
	slots := templates.Slots{Editor: func(gtx layout.Context) layout.Dimensions { return v.welcome.Layout(gtx, th) }}
	if !native.MenuSupported() {
		slots.Menu = func(gtx layout.Context) layout.Dimensions { return v.menuBar.layout(gtx, th, v) }
	}
	v.overlaySlot(&slots)
	hide := v.bench.HideStripes
	v.bench.HideStripes = true
	d := v.bench.Layout(gtx, th, slots)
	v.bench.HideStripes = hide
	return d
}

// overlaySlot shows the open dialog, popup or chooser.
func (v *View) overlaySlot(slots *templates.Slots) {
	th := v.th
	switch {
	case v.settingsOpen:
		slots.Overlay, slots.OverlayCentered = func(gtx layout.Context) layout.Dimensions { return v.settings.Layout(gtx, th) }, true
	case v.popup != nil:
		p := v.popup
		slots.Overlay, slots.OverlayCentered = func(gtx layout.Context) layout.Dimensions { return p.Layout(gtx, th) }, p.Centered
	case v.chooseMode != chooseNone:
		slots.Overlay = func(gtx layout.Context) layout.Dimensions { return v.chooser.Layout(gtx, th) }
	}
}

// escapeKeys handles Escape (focus the editor) and Shift+Escape (hide
// the active tool window) after all widgets had their chance: popups,
// choosers, the find bar and the editor itself use Escape first.
func (v *View) escapeKeys(gtx layout.Context) {
	if v.settingsOpen || v.popup != nil || v.chooseMode != chooseNone {
		return
	}
	v.escKeys = [2]key.Filter{
		{Name: key.NameEscape},
		{Name: key.NameEscape, Required: key.ModShift},
	}
	for {
		ev, ok := gtx.Event(v.escKeys[0], v.escKeys[1])
		if !ok {
			return
		}
		e, ok := ev.(key.Event)
		if !ok || e.State != key.Press {
			continue
		}
		if e.Modifiers.Contain(key.ModShift) {
			v.hideActiveTool()
		}
		v.focusEditor = true
	}
}
