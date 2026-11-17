package keymap

import (
	"testing"

	"github.com/ape-dev-de/tiegel/internal/core/settings"
)

func TestKeymapSpecsRegisteredAsCore(t *testing.T) {
	if coreSpecsErr != nil {
		t.Fatal(coreSpecsErr)
	}
	found := map[string]bool{}
	for _, s := range settings.CoreSpecs() {
		found[s.Key] = true
	}
	for _, k := range []string{SettingPreset, SettingOverrides} {
		if !found[k] {
			t.Errorf("%s not registered as core setting", k)
		}
	}
}
