package keymap

import (
	"testing"
	"time"
)

func TestModTrackerDoublePress(t *testing.T) {
	var tr ModTracker
	t0 := time.Unix(0, 0)
	at := func(ms int) time.Time { return t0.Add(time.Duration(ms) * time.Millisecond) }
	up := MustParse("Alt+Up")

	tr.Modifier(Alt, true, at(0))
	if tr.Resolve(up) != up {
		t.Fatal("single press resolved to a double chord")
	}
	tr.Modifier(Alt, false, at(80))
	tr.Modifier(Alt, true, at(200))
	if tr.Armed() != Alt {
		t.Fatalf("armed = %v", tr.Armed())
	}
	want := MustParse("Alt+Alt+Up")
	for i := 0; i < 3; i++ { // repeatable while held
		if got := tr.Resolve(up); got != want {
			t.Fatalf("resolve %d = %v", i, got)
		}
		tr.Key()
	}
	// Key repeat of the held modifier (Windows) keeps it armed.
	tr.Modifier(Alt, true, at(900))
	if tr.Armed() != Alt {
		t.Fatal("auto-repeat disarmed")
	}
	// Another modifier while armed keeps the double press.
	tr.Modifier(Shift, true, at(950))
	tr.Modifier(Cmd, true, at(960))
	if got := tr.Resolve(MustParse("Alt+Shift+Down")); got != MustParse("Shift+Alt+Alt+Down") {
		t.Fatalf("with shift = %v", got)
	}
	if got := tr.Resolve(MustParse("Ctrl+Down")); got != MustParse("Ctrl+Down") {
		t.Fatalf("other modifier resolved: %v", got)
	}
	if got := tr.Resolve(Chord{Key: KeyMouseBack}); got.Double != 0 {
		t.Fatal("gesture resolved")
	}
	tr.Modifier(Alt, false, at(1000))
	if tr.Armed() != 0 || tr.Resolve(up) != up {
		t.Fatal("release did not disarm")
	}
}

func TestModTrackerRejects(t *testing.T) {
	t0 := time.Unix(0, 0)
	at := func(ms int) time.Time { return t0.Add(time.Duration(ms) * time.Millisecond) }
	cases := map[string]func(tr *ModTracker){
		"too slow": func(tr *ModTracker) {
			tr.Modifier(Ctrl, true, at(0))
			tr.Modifier(Ctrl, false, at(10))
			tr.Modifier(Ctrl, true, at(10+int(DoubleModDelay/time.Millisecond)+1))
		},
		"key between": func(tr *ModTracker) {
			tr.Modifier(Ctrl, true, at(0))
			tr.Key()
			tr.Modifier(Ctrl, false, at(10))
			tr.Modifier(Ctrl, true, at(20))
		},
		"key after release": func(tr *ModTracker) {
			tr.Modifier(Ctrl, true, at(0))
			tr.Modifier(Ctrl, false, at(10))
			tr.Key()
			tr.Modifier(Ctrl, true, at(20))
		},
		"other modifier": func(tr *ModTracker) {
			tr.Modifier(Ctrl, true, at(0))
			tr.Modifier(Ctrl, false, at(10))
			tr.Modifier(Alt, true, at(20))
			tr.Modifier(Ctrl, true, at(30))
		},
		"shift ignored": func(tr *ModTracker) {
			tr.Modifier(Shift, true, at(0))
			tr.Modifier(Shift, false, at(10))
			tr.Modifier(Shift, true, at(20))
		},
		"two modifiers at once": func(tr *ModTracker) {
			tr.Modifier(Ctrl|Alt, true, at(0))
			tr.Modifier(Ctrl|Alt, false, at(10))
			tr.Modifier(Ctrl|Alt, true, at(20))
		},
		"clock backwards": func(tr *ModTracker) {
			tr.Modifier(Ctrl, true, at(100))
			tr.Modifier(Ctrl, false, at(110))
			tr.Modifier(Ctrl, true, at(50))
		},
		"reset": func(tr *ModTracker) {
			tr.Modifier(Ctrl, true, at(0))
			tr.Modifier(Ctrl, false, at(10))
			tr.Modifier(Ctrl, true, at(20))
			tr.Reset()
		},
		"stale release": func(tr *ModTracker) {
			tr.Modifier(Ctrl, true, at(0))
			tr.Modifier(Ctrl, false, at(10))
			tr.Modifier(Ctrl, false, at(15)) // second release without a press
			tr.Modifier(Ctrl, true, at(20))
		},
	}
	for name, run := range cases {
		var tr ModTracker
		run(&tr)
		if tr.Armed() != 0 {
			t.Errorf("%s: armed %v", name, tr.Armed())
		}
	}
	// A custom delay.
	tr := ModTracker{Delay: 50 * time.Millisecond}
	tr.Modifier(Cmd, true, at(0))
	tr.Modifier(Cmd, false, at(10))
	tr.Modifier(Cmd, true, at(100))
	if tr.Armed() != 0 {
		t.Error("custom delay ignored")
	}
	tr.Modifier(Cmd, false, at(110))
	tr.Modifier(Cmd, true, at(150))
	if tr.Armed() != Cmd {
		t.Error("custom delay: no double press")
	}
	tr.Reset()
	if tr.Delay != 50*time.Millisecond {
		t.Error("reset dropped the delay")
	}
}
