package keymap

import (
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/ape-dev-de/tiegel/internal/core/settings"
)

// TestParseOverridesSkipsOnlyBadLines checks that one bad line does not
// discard the valid ones (D248).
func TestParseOverridesSkipsOnlyBadLines(t *testing.T) {
	lines := []string{"navigate.back F5", "app.settings Cmd+Nope", "tab.close", "x Hyper+Y F1"}
	ov, err := ParseOverrides(lines)
	if !errors.Is(err, ErrBadChord) || !strings.Contains(err.Error(), "app.settings") || !strings.Contains(err.Error(), `"x"`) {
		t.Fatalf("err %v", err)
	}
	if len(ov) != 2 || ov[NavBack][0] != MustParse("F5") || len(ov["tab.close"]) != 0 {
		t.Fatalf("overrides %v", ov)
	}
	if bad := InvalidOverrides(lines); !slices.Equal(bad, []string{"app.settings Cmd+Nope", "x Hyper+Y F1"}) {
		t.Fatalf("invalid %q", bad)
	}
}

func TestStateSaveKeepsInvalidLinesAndOSPreset(t *testing.T) {
	st, err := settings.OpenGlobal(t.TempDir(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := settings.Set(st, SettingOverrides, []string{"navigate.back F5", "app.settings Cmd+Nope"}); err != nil {
		t.Fatal(err)
	}
	s, err := LoadState(st, "linux")
	if err == nil || s.PresetSet || s.Preset != PresetPC || len(s.Overrides) != 1 || len(s.Invalid) != 1 {
		t.Fatalf("state %+v, %v", s, err)
	}
	s.Overrides = s.Overrides.Clone()
	s.Overrides["navigate.forward"] = []Chord{MustParse("F6")}
	if err := SaveState(st, s); err != nil {
		t.Fatal(err)
	}
	if st.Has(SettingPreset) {
		t.Fatal("OS preset stored")
	}
	lines, _, _ := settings.Lookup[[]string](st, SettingOverrides)
	if !slices.Equal(lines, []string{"navigate.back F5", "navigate.forward F6", "app.settings Cmd+Nope"}) {
		t.Fatalf("lines %q", lines)
	}
	s.Drop("app.settings")
	s.Drop(NavBack)
	if got := s.Lines(); !slices.Equal(got, []string{"navigate.forward F6"}) {
		t.Fatalf("after drop %q", got)
	}
	// An invalid stored preset is reported and treated as unset.
	if err := settings.Set(st, SettingPreset, "emacs"); err != nil {
		t.Fatal(err)
	}
	if s, err := LoadState(st, "darwin"); err == nil || s.PresetSet || s.Preset != PresetMac {
		t.Fatalf("bad preset %+v %v", s, err)
	}
}
