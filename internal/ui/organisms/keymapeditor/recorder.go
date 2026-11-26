package keymapeditor

import (
	"image"
	"time"

	"gioui.org/io/event"
	"gioui.org/io/key"
	"gioui.org/layout"
	"gioui.org/op/clip"

	"github.com/ape-dev-de/tiegel/internal/editor/keymap"
	"github.com/ape-dev-de/tiegel/internal/ui/atoms"
	"github.com/ape-dev-de/tiegel/internal/ui/editorview"
	"github.com/ape-dev-de/tiegel/internal/ui/tokens"
)

// DoubleShiftDelay is the longest pause between the two Shift presses of
// a DoubleShift chord (as in the window, navDock).
const DoubleShiftDelay = 400 * time.Millisecond

// RecordResult is what Recorder.Update observed.
type RecordResult int

// Results of Recorder.Update.
const (
	// RecordIdle: nothing happened (or the recorder is inactive).
	RecordIdle RecordResult = iota
	// RecordCaptured: a chord was captured; waiting for confirmation.
	RecordCaptured
	// RecordConfirmed: recording ended with Chord (Enter, focus loss, or
	// every capture in Instant mode).
	RecordConfirmed
	// RecordCancelled: Escape (or focus loss without a chord).
	RecordCancelled
)

// modMask lets every modifier combination through the key filters.
const modMask = key.ModShift | key.ModCtrl | key.ModAlt | key.ModCommand | key.ModSuper

// Recorder is a field that captures the next key chord while it has the
// keyboard focus: modifiers plus a key (a modifier pressed twice and held
// gives a double-modifier chord such as "Alt+Alt+Up"), DoubleShift (Shift pressed twice
// within DoubleShiftDelay), and pointer or gesture inputs the host routes
// in through Gesture (mouse back/forward, swipes). Escape cancels; Enter
// or focus loss confirms the captured chord (plain Enter is recorded as a
// chord when nothing was captured yet).
//
// While a recorder is active the host must not run its own shortcuts or
// Escape handling (the events would never reach the field): check Active.
type Recorder struct {
	// Instant confirms every captured chord at once (search by
	// shortcut).
	Instant bool
	// Hint replaces the default prompt.
	Hint string

	active    bool
	wantFocus bool
	focused   bool
	chord     string
	gesture   string
	shiftDown bool // Shift pressed with no other key so far
	shiftUp   time.Time
	shiftSkip bool
	mods      keymap.ModTracker // double-pressed Ctrl/Alt/Cmd ("Alt+Alt+Up")
	err       string
}

// Start begins recording (the field takes the focus in the next Update).
func (r *Recorder) Start() {
	*r = Recorder{Instant: r.Instant, Hint: r.Hint, active: true, wantFocus: true}
}

// Cancel stops recording without a result.
func (r *Recorder) Cancel() {
	r.active, r.chord, r.gesture = false, "", ""
}

// Active reports whether the recorder captures keys.
func (r *Recorder) Active() bool { return r.active }

// Chord returns the captured chord (keymap.ParseChord syntax).
func (r *Recorder) Chord() string { return r.chord }

// Gesture records a pointer or gesture input (keymap.KeyMouseBack, …)
// delivered by the host; it reports whether the recorder took it.
func (r *Recorder) Gesture(name string) bool {
	if !r.active {
		return false
	}
	r.gesture = name
	return true
}

// Update processes the key events of the focused field.
func (r *Recorder) Update(gtx layout.Context) RecordResult {
	if !r.active {
		return RecordIdle
	}
	if r.wantFocus {
		r.wantFocus = false
		gtx.Execute(key.FocusCmd{Tag: r})
	}
	res := RecordIdle
	if g := r.gesture; g != "" {
		r.gesture = ""
		if res = r.capture(g); res == RecordConfirmed {
			return res
		}
	}
	for {
		ev, ok := gtx.Event(
			key.FocusFilter{Target: r},
			key.Filter{Focus: r, Optional: modMask},
			// Tab moves the focus unless named explicitly.
			key.Filter{Focus: r, Name: key.NameTab, Optional: modMask},
		)
		if !ok {
			return res
		}
		switch e := ev.(type) {
		case key.FocusEvent:
			if e.Focus {
				r.focused = true
				continue
			}
			if r.focused {
				r.focused = false
				if r.chord != "" && !r.Instant {
					return r.confirm()
				}
				return r.cancel()
			}
		case key.Event:
			switch got := r.key(gtx.Now, e); got {
			case RecordConfirmed, RecordCancelled:
				return got
			case RecordCaptured:
				res = got
			}
		}
	}
}

func isModifier(n key.Name) bool {
	switch n {
	case key.NameShift, key.NameCtrl, key.NameAlt, key.NameCommand, key.NameSuper:
		return true
	}
	return false
}

// trackedMods are the modifier keys that can be doubled in a chord.
var trackedMods = map[key.Name]keymap.Mods{
	key.NameCtrl: keymap.Ctrl, key.NameAlt: keymap.Alt,
	key.NameCommand: keymap.Cmd, key.NameSuper: keymap.Cmd,
}

func (r *Recorder) key(now time.Time, e key.Event) RecordResult {
	if e.Name == key.NameShift {
		switch e.State {
		case key.Press:
			if !r.shiftUp.IsZero() && now.Sub(r.shiftUp) <= DoubleShiftDelay {
				r.shiftUp, r.shiftSkip, r.shiftDown = time.Time{}, true, false
				return r.capture(keymap.KeyDoubleShift)
			}
			r.shiftDown = true
		case key.Release:
			if r.shiftSkip || !r.shiftDown {
				r.shiftSkip, r.shiftDown, r.shiftUp = false, false, time.Time{}
				return RecordIdle
			}
			r.shiftDown, r.shiftUp = false, now
		}
		return RecordIdle
	}
	if m, ok := trackedMods[e.Name]; ok {
		r.mods.Modifier(m, e.State == key.Press, now)
		return RecordIdle
	}
	if e.State != key.Press || isModifier(e.Name) {
		return RecordIdle
	}
	r.shiftDown, r.shiftUp = false, time.Time{}
	c := r.mods.Resolve(editorview.ChordOf(e))
	r.mods.Key()
	if c.Mods == 0 {
		switch c.Key {
		case keymap.KeyEscape:
			return r.cancel()
		case keymap.KeyEnter:
			if r.chord != "" && !r.Instant {
				return r.confirm()
			}
		}
	}
	return r.capture(c.String())
}

func (r *Recorder) capture(s string) RecordResult {
	c, err := keymap.ParseChord(s)
	if err != nil {
		r.err = "Unsupported key: " + s
		return RecordIdle
	}
	r.chord, r.err = c.String(), ""
	if r.Instant {
		return r.confirm()
	}
	return RecordCaptured
}

func (r *Recorder) confirm() RecordResult {
	r.active = false
	return RecordConfirmed
}

func (r *Recorder) cancel() RecordResult {
	r.active, r.chord = false, ""
	return RecordCancelled
}

// Layout draws the field: the captured chord (or the prompt) and how to
// finish. It must be laid out while Active to keep the focus.
func (r *Recorder) Layout(gtx layout.Context, th *tokens.Theme) layout.Dimensions {
	return atoms.Surface{Kind: atoms.SurfaceEditor, Border: true, Rounded: true}.Layout(gtx, th, func(gtx layout.Context) layout.Dimensions {
		return layout.UniformInset(th.Space.XS).Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			d := layout.Flex{Alignment: layout.Middle}.Layout(gtx,
				layout.Rigid(atoms.Badge{Text: "Recording", Kind: atoms.BadgeAccent}.Fn(th)),
				layout.Rigid(layout.Spacer{Width: th.Space.S}.Layout),
				layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
					switch {
					case r.err != "":
						return atoms.Small(r.err).WithColor(th.Color.Warning).SingleLine().Layout(gtx, th)
					case r.chord != "":
						return atoms.Code(r.chord).SingleLine().Layout(gtx, th)
					}
					return atoms.Muted(r.prompt()).SingleLine().Layout(gtx, th)
				}),
				layout.Rigid(atoms.Small(r.finishHint()).SingleLine().Fn(th)),
			)
			area := clip.Rect(image.Rectangle{Max: d.Size}).Push(gtx.Ops)
			event.Op(gtx.Ops, r)
			area.Pop()
			return d
		})
	})
}

func (r *Recorder) prompt() string {
	if r.Hint != "" {
		return r.Hint
	}
	return "Press a shortcut…"
}

func (r *Recorder) finishHint() string {
	if r.Instant {
		return "Esc cancels"
	}
	return "Enter confirms · Esc cancels"
}
