//go:build cgo

package phpfeatures

import (
	"encoding/json"
	"testing"

	"github.com/ape-dev-de/tiegel/internal/api"
	"github.com/ape-dev-de/tiegel/internal/core/bus"
	"github.com/ape-dev-de/tiegel/internal/modules/modtest"
)

func TestBackgroundSettingOff(t *testing.T) {
	a, _ := newApp(t)
	rec := modtest.Record(t, a, api.TopicPHPDiagnostics)
	modtest.Project(t, a, map[string]string{
		"composer.json": `{"require":{"php":"^8.0"}}`,
		"enum.php":      enumSrc,
	})
	modtest.Call(t, a, "settings.set", api.SettingsSetParams{Key: SettingBackground, Scope: "project", Value: json.RawMessage(`false`)}, nil)
	modtest.Call(t, a, "doc.open", api.DocOpenParams{Path: "enum.php"}, nil)
	ev := rec.Wait(t, api.TopicPHPDiagnostics, nil).Payload.(Diagnostics)
	if len(ev.Diagnostics) != 0 || ev.Path != "enum.php" {
		t.Fatalf("off: %+v", ev)
	}
	// php.check still works.
	var res CheckResult
	modtest.Call(t, a, "php.check", CheckParams{Path: "enum.php"}, &res)
	if len(res.Diagnostics) == 0 {
		t.Fatal("php.check must ignore the setting")
	}
	// Back on: the next edit reports the finding.
	modtest.Call(t, a, "settings.set", api.SettingsSetParams{Key: SettingBackground, Scope: "project"}, nil)
	modtest.Call(t, a, "doc.insert", api.DocInsertParams{Path: "enum.php", At: api.Pos{Offset: 0}, Text: "\n"}, nil)
	rec.Wait(t, api.TopicPHPDiagnostics, func(e bus.Event) bool { return len(e.Payload.(Diagnostics).Diagnostics) > 0 })
}
