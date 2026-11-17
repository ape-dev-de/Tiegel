//go:build cgo

package phpindex

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/ape-dev-de/tiegel/internal/api"
	"github.com/ape-dev-de/tiegel/internal/core/settings"
	"github.com/ape-dev-de/tiegel/internal/modules/modtest"
)

func TestSettingsMigrationAndRestart(t *testing.T) {
	a, _ := newApp(t, Options{}, nil)
	// A value stored under the old key (before the module namespace).
	if err := settings.Set(a.Settings(), "index.libraries", false); err != nil {
		t.Fatal(err)
	}
	modtest.Project(t, a, files)
	var st Status
	modtest.Call(t, a, "index.status", StatusParams{Wait: true}, &st)
	if st.Libraries || st.Stats.Libraries != 0 {
		t.Fatalf("legacy value ignored: %+v", st)
	}
	if a.Settings().Has("index.libraries") || !a.Settings().Has(SettingLibraries) {
		t.Fatal("not migrated")
	}

	// A write through settings.set (the dialog) restarts the index.
	modtest.Call(t, a, "settings.set", api.SettingsSetParams{Key: SettingLibraries, Scope: "project", Value: json.RawMessage(`true`)}, nil)
	deadline := time.Now().Add(10 * time.Second)
	for {
		modtest.Call(t, a, "index.status", StatusParams{Wait: true}, &st)
		if st.Libraries && st.Stats.Libraries == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("not restarted: %+v", st)
		}
		time.Sleep(20 * time.Millisecond)
	}
	var cfg Config
	modtest.Call(t, a, "index.config", nil, &cfg)
	if !cfg.Libraries {
		t.Fatalf("config %+v", cfg)
	}
}
