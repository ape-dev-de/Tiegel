package keymap

import (
	"errors"
	"fmt"
	"slices"

	"github.com/ape-dev-de/tiegel/internal/core/settings"
)

// Setting keys of the keymap (global scope).
const (
	// SettingPreset holds the preset ("mac" or "pc"); unset means the
	// preset of the OS (PresetFor).
	SettingPreset = "keymap.preset"
	// SettingOverrides holds the user bindings in the form of
	// Overrides.Format: a list of "<action> <chord>…" strings.
	SettingOverrides = "keymap.overrides"
)

// SettingSpecs declares the keymap settings for the settings schema
// (D190). goos selects the default preset.
func SettingSpecs(goos string) []settings.Spec {
	opts := make([]string, len(Presets))
	for i, p := range Presets {
		opts[i] = string(p)
	}
	return []settings.Spec{
		{Key: SettingPreset, Kind: settings.KindEnum, Default: string(PresetFor(goos)), Scope: settings.ScopeGlobal,
			Label: "Keymap", Help: "Default shortcuts: mac (Cmd for commands, Option for words) or pc (Ctrl, Alt).",
			Group: "Keymap", Options: opts},
		{Key: SettingOverrides, Kind: settings.KindList, Default: []string{}, Scope: settings.ScopeGlobal,
			Label: "Custom shortcuts", Help: "One entry per action: \"<action> <chord> <chord>…\"; an action alone removes its shortcuts.",
			Group: "Keymap"},
	}
}

// State is the stored keymap configuration as read by LoadState.
type State struct {
	// Preset is the effective preset (the OS default unless PresetSet).
	Preset Preset
	// PresetSet is true when a valid preset is stored explicitly; only
	// then SaveState writes it, so an unset preset keeps following the OS.
	PresetSet bool
	// Overrides are the parsed override lines.
	Overrides Overrides
	// Invalid are stored override lines that could not be parsed. They
	// are kept verbatim by SaveState (unless the action got a valid
	// override or was reset), so a typo in one line never erases the
	// others (D248).
	Invalid []string
}

// LoadState reads the keymap settings. Malformed stored values are
// reported in the error; the result still holds everything usable.
func LoadState(st *settings.Store, goos string) (State, error) {
	s := State{Preset: PresetFor(goos), Overrides: Overrides{}}
	if st == nil {
		return s, nil
	}
	var errs []error
	if p, ok, err := settings.Lookup[string](st, SettingPreset); err != nil {
		errs = append(errs, fmt.Errorf("%s: %w", SettingPreset, err))
	} else if ok {
		if Preset(p).Valid() {
			s.Preset, s.PresetSet = Preset(p), true
		} else {
			errs = append(errs, fmt.Errorf("%s: unknown preset %q", SettingPreset, p))
		}
	}
	if lines, ok, err := settings.Lookup[[]string](st, SettingOverrides); err != nil {
		errs = append(errs, fmt.Errorf("%s: %w", SettingOverrides, err))
	} else if ok {
		ov, bad, perr := parseOverrides(lines)
		s.Overrides, s.Invalid = ov, bad
		if perr != nil {
			errs = append(errs, perr)
		}
	}
	if len(errs) > 0 {
		return s, fmt.Errorf("keymap settings: %w", errors.Join(errs...))
	}
	return s, nil
}

// Drop removes the override of a and any invalid stored line of a.
func (s *State) Drop(a Action) {
	if _, ok := s.Overrides[a]; ok {
		s.Overrides = s.Overrides.Clone()
		delete(s.Overrides, a)
	}
	s.Invalid = slices.DeleteFunc(slices.Clone(s.Invalid), func(l string) bool { return overrideAction(l) == a })
}

// Lines returns the stored form of the state's overrides: the valid
// overrides (Overrides.Format) followed by the invalid lines of actions
// without a valid override.
func (s State) Lines() []string {
	out := s.Overrides.Format()
	for _, l := range s.Invalid {
		if _, ok := s.Overrides[overrideAction(l)]; !ok {
			out = append(out, l)
		}
	}
	return out
}

// SaveState writes s: the preset only when PresetSet (otherwise the
// stored preset is removed), the override lines of Lines (none removes
// the key).
func SaveState(st *settings.Store, s State) error {
	if s.PresetSet && s.Preset != "" {
		if err := settings.Set(st, SettingPreset, string(s.Preset)); err != nil {
			return fmt.Errorf("keymap: %w", err)
		}
	} else if err := st.Delete(SettingPreset); err != nil {
		return fmt.Errorf("keymap: %w", err)
	}
	lines := s.Lines()
	if len(lines) == 0 {
		if err := st.Delete(SettingOverrides); err != nil {
			return fmt.Errorf("keymap: %w", err)
		}
		return nil
	}
	if err := settings.Set(st, SettingOverrides, lines); err != nil {
		return fmt.Errorf("keymap: %w", err)
	}
	return nil
}

// Load reads preset and overrides from a settings store; a missing
// preset gives the OS default. Malformed stored values are reported
// together with everything usable (valid override lines are kept).
func Load(st *settings.Store, goos string) (Preset, Overrides, error) {
	s, err := LoadState(st, goos)
	return s.Preset, s.Overrides, err
}

// Store writes preset and overrides (an empty override set removes the
// key). An empty preset removes the stored preset (OS default).
func Store(st *settings.Store, p Preset, ov Overrides) error {
	return SaveState(st, State{Preset: p, PresetSet: p != "", Overrides: ov})
}
