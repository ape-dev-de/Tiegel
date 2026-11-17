package keymap

import (
	"fmt"

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

// Load reads preset and overrides from a settings store; a missing
// preset gives the OS default. Malformed stored values are reported
// together with the usable defaults.
func Load(st *settings.Store, goos string) (Preset, Overrides, error) {
	p := PresetFor(goos)
	var errs []error
	if st == nil {
		return p, Overrides{}, nil
	}
	if s, ok, err := settings.Lookup[string](st, SettingPreset); err != nil {
		errs = append(errs, fmt.Errorf("%s: %w", SettingPreset, err))
	} else if ok {
		if Preset(s).Valid() {
			p = Preset(s)
		} else {
			errs = append(errs, fmt.Errorf("%s: unknown preset %q", SettingPreset, s))
		}
	}
	ov := Overrides{}
	if lines, ok, err := settings.Lookup[[]string](st, SettingOverrides); err != nil {
		errs = append(errs, fmt.Errorf("%s: %w", SettingOverrides, err))
	} else if ok {
		parsed, perr := ParseOverrides(lines)
		if perr != nil {
			errs = append(errs, perr)
		} else {
			ov = parsed
		}
	}
	if len(errs) > 0 {
		return p, ov, fmt.Errorf("keymap settings: %w", errs[0])
	}
	return p, ov, nil
}

// Store writes preset and overrides (an empty override set removes the
// key). An empty preset removes the stored preset (OS default).
func Store(st *settings.Store, p Preset, ov Overrides) error {
	if p == "" {
		if err := st.Delete(SettingPreset); err != nil {
			return fmt.Errorf("keymap: %w", err)
		}
	} else if err := settings.Set(st, SettingPreset, string(p)); err != nil {
		return fmt.Errorf("keymap: %w", err)
	}
	if len(ov) == 0 {
		if err := st.Delete(SettingOverrides); err != nil {
			return fmt.Errorf("keymap: %w", err)
		}
		return nil
	}
	if err := settings.Set(st, SettingOverrides, ov.Format()); err != nil {
		return fmt.Errorf("keymap: %w", err)
	}
	return nil
}
