package api

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ape-dev-de/tiegel/internal/core/settings"
)

// TestSettingsSetRejectsReadOnly checks that settings.set refuses a
// ReadOnly spec (set and reset) with the hint, and that settings.schema
// exposes the flag (D249).
func TestSettingsSetRejectsReadOnly(t *testing.T) {
	a := newTestApp(t)
	m := specFake{fakeModule: fakeModule{id: "rt"}, specs: []settings.Spec{
		{Key: "rt.interpreter", Kind: settings.KindString, Default: "", Scope: settings.ScopeProject, Label: "Interpreter",
			ReadOnly: true, ReadOnlyHint: "use interpreter.select"},
		{Key: "rt.other", Kind: settings.KindString, Default: "", Scope: settings.ScopeGlobal, Label: "Other"},
	}}
	if err := a.AddModule(m); err != nil {
		t.Fatal(err)
	}
	for _, p := range []SettingsSetParams{
		{Key: "rt.interpreter", Value: json.RawMessage(`"docker"`)},
		{Key: "rt.interpreter"},
	} {
		err := tryCall(a, "settings.set", p, nil)
		if err == nil || !strings.Contains(err.Error(), "use interpreter.select") {
			t.Fatalf("%+v: %v", p, err)
		}
	}
	var v SettingValue
	call(t, a, "settings.set", SettingsSetParams{Key: "rt.other", Value: json.RawMessage(`"x"`)}, &v)
	var s SettingsSchema
	call(t, a, "settings.schema", SchemaParams{Module: "rt"}, &s)
	if len(s.Modules) != 1 || !s.Modules[0].Settings[0].ReadOnly || s.Modules[0].Settings[1].ReadOnly {
		t.Fatalf("schema %+v", s.Modules)
	}
	// Backwards compatible JSON: the flag is omitted when false.
	raw, _ := json.Marshal(settings.Spec{Key: "a.b", Kind: settings.KindBool, Scope: settings.ScopeGlobal})
	if strings.Contains(string(raw), "readOnly") {
		t.Fatalf("json %s", raw)
	}
}

// TestAutosaveIdleAcceptsLongDelays checks that idle delays above an
// hour, accepted before the spec had a maximum, still work (D249).
func TestAutosaveIdleAcceptsLongDelays(t *testing.T) {
	a := newTestApp(t)
	var v SettingValue
	call(t, a, "settings.set", SettingsSetParams{Key: settings.KeyAutosaveIdle, Value: json.RawMessage(`7200`)}, &v)
	if a.saver.IdleDelay() != 2*time.Hour {
		t.Fatalf("idle %v", a.saver.IdleDelay())
	}
	n := 10800
	var cfg AutosaveConfig
	call(t, a, "autosave.config", AutosaveConfig{IdleSeconds: &n}, &cfg)
	if *cfg.IdleSeconds != n || Setting(a, settings.KeyAutosaveIdle, 0) != n {
		t.Fatalf("config %d", *cfg.IdleSeconds)
	}
}

// TestInvalidStoredSettingIsVisible checks that a stored value failing its
// spec is listed in settings.schema problems and logged once (D249).
func TestInvalidStoredSettingIsVisible(t *testing.T) {
	var logBuf bytes.Buffer
	a, err := New(Options{ConfigDir: filepath.Join(t.TempDir(), "config"), GOOS: "linux", Interp: interpOptions(nil),
		Logger: slog.New(slog.NewTextHandler(&logBuf, nil))})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = a.Close() })
	if err := settings.Set(a.Settings(), settings.KeyEditorTabWidth, 99); err != nil {
		t.Fatal(err)
	}
	if got := Setting(a, settings.KeyEditorTabWidth, 4); got != 4 {
		t.Fatalf("effective %d", got)
	}
	_ = Setting(a, settings.KeyEditorTabWidth, 4)
	if n := strings.Count(logBuf.String(), "stored setting ignored"); n != 1 {
		t.Fatalf("logged %d times: %s", n, logBuf.String())
	}
	var s SettingsSchema
	call(t, a, "settings.schema", SchemaParams{Module: "core"}, &s)
	found := false
	for _, p := range s.Problems {
		found = found || strings.HasPrefix(p, settings.KeyEditorTabWidth+": stored value ignored")
	}
	if !found {
		t.Fatalf("problems %q", s.Problems)
	}
}
