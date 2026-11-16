package api

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/ape-dev-de/tiegel/internal/core/history"
)

func TestSettingsGetSet(t *testing.T) {
	a := newTestApp(t)
	var v SettingValue
	call(t, a, "settings.get", SettingsParams{Key: "ui.theme"}, &v)
	if v.Found || v.Scope != "global" {
		t.Fatalf("get missing: %+v", v)
	}
	call(t, a, "settings.set", SettingsSetParams{Key: "ui.theme", Value: json.RawMessage(`"light"`)}, &v)
	call(t, a, "settings.get", SettingsParams{Key: "ui.theme"}, &v)
	if !v.Found || string(v.Value) != `"light"` {
		t.Fatalf("get: %+v", v)
	}
	call(t, a, "settings.set", SettingsSetParams{Key: "ui.theme"}, &v)
	call(t, a, "settings.get", SettingsParams{Key: "ui.theme"}, &v)
	if v.Found {
		t.Fatal("null value should delete")
	}
	wantCode(t, a, "settings.set", SettingsSetParams{Key: "php.interpreter", Value: json.RawMessage(`"x"`)}, CodeInvalidParams)
	wantCode(t, a, "settings.set", SettingsSetParams{Key: "ui.x", Value: json.RawMessage(`{`)}, CodeInvalidParams)
	wantCode(t, a, "settings.get", SettingsParams{Key: "ui.x", Scope: "project"}, CodeNoProject)
	wantCode(t, a, "settings.get", SettingsParams{Key: "ui.x", Scope: "nope"}, CodeInvalidParams)
	wantCode(t, a, "settings.get", SettingsParams{}, CodeInvalidParams)

	openProject(t, a, map[string]string{"a.php": "<?php\n"})
	call(t, a, "settings.set", SettingsSetParams{Key: "ui.recentFiles", Scope: "project", Value: json.RawMessage(`["a.php"]`)}, &v)
	call(t, a, "settings.get", SettingsParams{Key: "ui.recentFiles", Scope: "project"}, &v)
	if !v.Found || v.Scope != "project" {
		t.Fatalf("project get: %+v", v)
	}
}

func TestAutosaveTriggerAndConfig(t *testing.T) {
	a := newTestApp(t)
	root := openProject(t, a, map[string]string{"a.php": "<?php\n"})
	var open DocOpenResult
	call(t, a, "doc.open", DocOpenParams{Path: "a.php"}, &open)
	var st DocState
	call(t, a, "doc.insert", DocInsertParams{Path: "a.php", At: Pos{Offset: 6}, Text: "echo 1;\n"}, &st)

	var res SaveAllResult
	call(t, a, "autosave.trigger", AutosaveTriggerParams{}, &res)
	if len(res.Saved) != 1 || res.Saved[0] != "a.php" {
		t.Fatalf("saved %v", res.Saved)
	}
	data, _ := os.ReadFile(filepath.Join(root, "a.php"))
	if string(data) != "<?php\necho 1;\n" {
		t.Fatalf("disk %q", data)
	}
	var revs []history.Revision
	call(t, a, "history.list", PathParams{Path: "a.php"}, &revs)
	if len(revs) != 1 || revs[0].Label != "autosave: focusLost" {
		t.Fatalf("history %+v", revs)
	}
	wantCode(t, a, "autosave.trigger", AutosaveTriggerParams{Trigger: "nope"}, CodeInvalidParams)

	var cfg AutosaveConfig
	call(t, a, "autosave.config", AutosaveConfig{}, &cfg)
	if cfg.IdleSeconds == nil || *cfg.IdleSeconds != 0 {
		t.Fatalf("default idle %+v", cfg)
	}
	ten := 10
	call(t, a, "autosave.config", AutosaveConfig{IdleSeconds: &ten}, &cfg)
	if *cfg.IdleSeconds != 10 {
		t.Fatalf("idle %d", *cfg.IdleSeconds)
	}
	neg := -1
	wantCode(t, a, "autosave.config", AutosaveConfig{IdleSeconds: &neg}, CodeInvalidParams)
	zero := 0
	call(t, a, "autosave.config", AutosaveConfig{IdleSeconds: &zero}, &cfg)
}
