package debug

import (
	"fmt"
	"image"
	"runtime"

	"gioui.org/layout"

	"github.com/ape-dev-de/tiegel/internal/editor/keymap"
	"github.com/ape-dev-de/tiegel/internal/ui/atoms"
	"github.com/ape-dev-de/tiegel/internal/ui/molecules"
	"github.com/ape-dev-de/tiegel/internal/ui/tokens"
)

// DebugToolbar holds the debugger controls: listener toggle, resume, step
// over/into/out, stop and mute breakpoints, followed by a status text.
// Buttons are enabled according to State.
type DebugToolbar struct {
	// Listening reports whether the DBGp listener is running.
	Listening bool
	// Port is shown in the status while listening (0: hidden).
	Port  int
	State State
	// Muted reports whether breakpoints are muted.
	Muted bool
	// Shortcuts are shown in tooltips; nil means Shortcuts(runtime.GOOS).
	Shortcuts []keymap.Binding

	bar molecules.Toolbar
}

func (d *DebugToolbar) shortcut(a Action) string {
	bs := d.Shortcuts
	if bs == nil {
		bs = Shortcuts(runtime.GOOS)
	}
	for _, b := range bs {
		if b.Action == a {
			return b.Chord.String()
		}
	}
	return ""
}

// Items returns the toolbar items for the current state.
func (d *DebugToolbar) Items() []molecules.ToolbarItem {
	paused := d.State == StateBreak
	listen := "Start listening for debug connections"
	if d.Listening {
		listen = "Stop listening for debug connections"
	}
	item := func(a Action, g *atoms.Glyph, label string, enabled bool) molecules.ToolbarItem {
		return molecules.ToolbarItem{ID: string(a), Glyph: g, Label: label, Shortcut: d.shortcut(a), Disabled: !enabled}
	}
	items := []molecules.ToolbarItem{
		item(ActionToggleListen, atoms.GlyphListen, listen, true),
		{Separator: true},
		item(ActionResume, atoms.GlyphRun, "Resume", paused),
		item(ActionStepOver, atoms.GlyphStepOver, "Step over", paused),
		item(ActionStepInto, atoms.GlyphStepInto, "Step into", paused),
		item(ActionStepOut, atoms.GlyphStepOut, "Step out", paused),
		item(ActionStop, atoms.GlyphStop, "Stop", d.State.Active()),
		{Separator: true},
		item(ActionMuteBreakpoints, atoms.GlyphMute, "Mute breakpoints", true),
	}
	items[0].Active = d.Listening
	items[len(items)-1].Active = d.Muted
	return items
}

// Update returns the actions clicked since the last call.
func (d *DebugToolbar) Update(gtx layout.Context) []Event {
	d.bar.Items = d.Items()
	var out []Event
	for _, id := range d.bar.Update(gtx) {
		out = append(out, ActionEvent{Action: Action(id)})
	}
	return out
}

// Status returns the status text shown after the buttons.
func (d *DebugToolbar) Status() string {
	switch {
	case d.State.Active():
		return d.State.String()
	case d.Listening && d.Port > 0:
		return fmt.Sprintf("listening on :%d", d.Port)
	case d.Listening:
		return "listening"
	}
	return "not listening"
}

// Layout draws the toolbar at its intrinsic width.
func (d *DebugToolbar) Layout(gtx layout.Context, th *tokens.Theme) layout.Dimensions {
	d.Update(gtx)
	gtx.Constraints.Min = image.Point{}
	kind := atoms.BadgeNeutral
	switch {
	case d.State == StateBreak:
		kind = atoms.BadgeWarning
	case d.State.Active():
		kind = atoms.BadgeSuccess
	case d.Listening:
		kind = atoms.BadgeInfo
	}
	return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
		layout.Rigid(func(gtx layout.Context) layout.Dimensions { return d.bar.Layout(gtx, th) }),
		layout.Rigid(layout.Spacer{Width: th.Space.S}.Layout),
		layout.Rigid(atoms.Badge{Text: d.Status(), Kind: kind}.Fn(th)),
	)
}
