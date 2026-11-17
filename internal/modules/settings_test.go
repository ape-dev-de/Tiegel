package modules

import (
	"strings"
	"testing"

	"github.com/ape-dev-de/tiegel/internal/api"
	"github.com/ape-dev-de/tiegel/internal/core/module"
	"github.com/ape-dev-de/tiegel/internal/modules/modtest"
)

// TestSettingSpecs (D190): every compiled-in module declares its settings
// in its own namespace, no declaration is rejected, and no core key lives
// in a module namespace.
func TestSettingSpecs(t *testing.T) {
	opts := modtest.Options(t)
	opts.Extensions = []func(*api.App) error{Install}
	a, err := api.New(opts)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	var s api.SettingsSchema
	modtest.Call(t, a, "settings.schema", nil, &s)
	if len(s.Problems) != 0 {
		t.Fatalf("rejected module settings: %v", s.Problems)
	}
	// Modules migrated to declared settings (M12).
	declares := map[string]bool{"git": true, "debug-xdebug": true, "php-features": true, "syntax": true, "php-index": true}
	ids := map[string]bool{}
	for _, m := range s.Modules {
		ids[m.ID] = true
		if declares[m.ID] && len(m.Settings) == 0 {
			t.Errorf("module %s declares no settings", m.ID)
		}
		for _, st := range m.Settings {
			if !strings.HasPrefix(st.Key, m.ID+".") {
				t.Errorf("module %s: key %s outside its namespace", m.ID, st.Key)
			}
			if st.Label == "" {
				t.Errorf("%s: no label", st.Key)
			}
		}
	}
	for _, c := range s.Core {
		for id := range ids {
			if strings.HasPrefix(c.Key, id+".") {
				t.Errorf("core key %s lies in the namespace of module %s", c.Key, id)
			}
		}
	}
	// The same holds for the plain declarations.
	for _, m := range All(Options{}) {
		sp, ok := m.(module.SettingsProvider)
		if !ok {
			continue
		}
		if _, errs := module.CheckSpecs(m.ID(), sp.SettingSpecs()); len(errs) > 0 {
			t.Errorf("%s: %v", m.ID(), errs)
		}
	}
}
