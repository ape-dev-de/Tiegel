package gui

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/ape-dev-de/tiegel/internal/api"
	"github.com/ape-dev-de/tiegel/internal/editor/keymap"
)

// TestWindowShortcutUserBindingWins checks that a user binding wins over
// an earlier registered action's default of the same chord (D247).
func TestWindowShortcutUserBindingWins(t *testing.T) {
	r := NewRegistry()
	r.modelGOOS = "darwin"
	r.AddAction(Action{ID: keymap.NavBack, Title: "Back", Run: func(*View) {}})
	r.AddAction(Action{ID: "app.settings", Title: "Settings", Run: func(*View) {}})
	back := keymap.MustParse("Cmd+[")
	r.SetScheme(keymap.NewScheme(keymap.PresetMac, keymap.Overrides{"app.settings": {back}}))
	var s shortcuts
	s.build(r, ScopeGlobal, nil)
	if a, ok := s.keys.Lookup(back); !ok || a != "app.settings" {
		t.Fatalf("Cmd+[ = %s, %v", a, ok)
	}
	// Without the override the default applies.
	r.SetScheme(keymap.NewScheme(keymap.PresetMac, nil))
	s.build(r, ScopeGlobal, nil)
	if a, _ := s.keys.Lookup(back); a != keymap.NavBack {
		t.Fatalf("default Cmd+[ = %s", a)
	}
}

// TestSyncKeymapKeepsValidOverrides checks that one bad stored override
// line does not drop the valid ones in the window (D248).
func TestSyncKeymapKeepsValidOverrides(t *testing.T) {
	e := newEnv(t, true)
	val, _ := json.Marshal([]string{"navigate.back F5", "app.settings Cmd+Nope"})
	var sv api.SettingValue
	if err := e.ctl.Backend().Call(context.Background(), "settings.set", api.SettingsSetParams{Key: keymap.SettingOverrides, Value: val}, &sv); err != nil {
		t.Fatal(err)
	}
	e.waitFor("keymap reload", func() bool { return e.v.Registry.Scheme().ChordText(ActBack) == "F5" })
	if n := e.ctl.Status.Notice; n == "" {
		t.Error("bad line not reported")
	}
}
