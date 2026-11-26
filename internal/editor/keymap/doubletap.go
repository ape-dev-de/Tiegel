package keymap

import "time"

// DoubleModDelay is the longest pause between releasing a modifier and
// pressing it again for the pair to count as a double press.
const DoubleModDelay = 400 * time.Millisecond

// ModTracker detects double-pressed modifiers for chords such as
// "Alt+Alt+Up": the modifier is pressed, released and pressed again
// within Delay without another key in between, and stays held. While it
// is held, every key press resolves to the double chord, so the key can
// be repeated ("Option Option ↑ ↑ ↑" clones three carets). The tracker is
// pure state; the caller feeds it the modifier and key events with their
// time stamps.
type ModTracker struct {
	// Delay overrides DoubleModDelay when positive.
	Delay time.Duration

	cand   Mods      // modifier pressed (and possibly released) once
	candUp time.Time // release of cand; zero while it is held
	armed  Mods      // double-pressed modifier that is still held
}

func (t *ModTracker) delay() time.Duration {
	if t.Delay > 0 {
		return t.Delay
	}
	return DoubleModDelay
}

// Modifier records a press (pressed) or release of modifier m at now.
// Only Ctrl, Alt and Cmd take part; other values are ignored.
func (t *ModTracker) Modifier(m Mods, pressed bool, now time.Time) {
	if m&doubleMods == 0 || m&(m-1) != 0 {
		return
	}
	if pressed {
		if t.armed != 0 && m != t.armed {
			t.cand = 0 // another modifier while armed keeps the double
			return
		}
		if t.cand == m && !t.candUp.IsZero() && now.Sub(t.candUp) <= t.delay() && !now.Before(t.candUp) {
			t.armed, t.cand, t.candUp = m, 0, time.Time{}
			return
		}
		t.cand, t.candUp = m, time.Time{}
		return
	}
	switch {
	case m == t.armed:
		t.armed, t.cand, t.candUp = 0, 0, time.Time{}
	case m == t.cand && t.candUp.IsZero():
		t.candUp = now
	case m == t.cand:
		t.cand = 0
	}
}

// Key records the press of a non-modifier key: a modifier pressed once
// before no longer starts a double press. An armed modifier stays armed.
func (t *ModTracker) Key() { t.cand, t.candUp = 0, time.Time{} }

// Reset forgets all state (e.g. when the focus is lost).
func (t *ModTracker) Reset() { *t = ModTracker{Delay: t.Delay} }

// Armed returns the double-pressed modifier that is held (0 if none).
func (t *ModTracker) Armed() Mods { return t.armed }

// Resolve returns c as a double chord when the armed modifier is part of
// c's modifiers, otherwise c unchanged.
func (t *ModTracker) Resolve(c Chord) Chord {
	if t.armed != 0 && c.Mods&t.armed != 0 && !IsGesture(c) {
		c.Double = t.armed
	}
	return c
}
