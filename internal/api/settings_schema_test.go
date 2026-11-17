package api

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ape-dev-de/tiegel/internal/core/settings"
)

type specFake struct {
	fakeModule
	specs []settings.Spec
}

func (m specFake) SettingSpecs() []settings.Spec { return m.specs }

func addSpecModule(t *testing.T, a *App) {
	t.Helper()
	m := specFake{fakeModule: fakeModule{id: "qa-tool"}, specs: []settings.Spec{
		{Key: "qa-tool.path", Kind: settings.KindPath, Default: "vendor/bin/tool", Scope: settings.ScopeProject, Label: "Executable", Help: "Path of the tool"},
		{Key: "qa-tool.level", Kind: settings.KindEnum, Default: "low", Options: []string{"low", "high"}, Scope: settings.ScopeGlobal, Label: "Level"},
		{Key: "editor.hijack", Kind: settings.KindBool, Scope: settings.ScopeGlobal, Label: "x"}, // rejected
	}}
	if err := a.AddModule(m); err != nil {
		t.Fatal(err)
	}
	if err := a.AddModule(fakeModule{id: "plain"}); err != nil {
		t.Fatal(err)
	}
}

func TestSettingsSchema(t *testing.T) {
	a := newTestApp(t)
	addSpecModule(t, a)
	var s SettingsSchema
	call(t, a, "settings.schema", nil, &s)
	if s.Project != "" || len(s.Core) < 10 || len(s.Modules) != 2 || len(s.Problems) != 1 || !strings.Contains(s.Problems[0], "editor.hijack") {
		t.Fatalf("schema %+v", s)
	}
	if s.Pages[0] != settings.PageAppearance {
		t.Fatalf("pages %v", s.Pages)
	}
	var tab *SettingInfo
	for i := range s.Core {
		if s.Core[i].Key == settings.KeyEditorTabWidth {
			tab = &s.Core[i]
		}
	}
	if tab == nil || tab.Source != "default" || string(tab.Value) != "4" || tab.Group != settings.PageEditor {
		t.Fatalf("tab %+v", tab)
	}
	if s.Modules[0].ID != "plain" || s.Modules[1].ID != "qa-tool" || len(s.Modules[1].Settings) != 2 || !s.Modules[1].Enabled {
		t.Fatalf("modules %+v", s.Modules)
	}

	// Values and sources.
	var v SettingValue
	call(t, a, "settings.set", SettingsSetParams{Key: "editor.tabWidth", Value: json.RawMessage(`2`)}, &v)
	openProject(t, a, map[string]string{"a.php": "<?php\n"})
	call(t, a, "settings.set", SettingsSetParams{Key: "qa-tool.path", Scope: "project", Value: json.RawMessage(`"bin/tool"`)}, &v)
	call(t, a, "settings.schema", SchemaParams{Module: "qa-tool"}, &s)
	if len(s.Core) != 0 || len(s.Modules) != 1 || s.Project == "" {
		t.Fatalf("module filter %+v", s)
	}
	path := s.Modules[0].Settings[0]
	if path.Source != "project" || string(path.Project) != `"bin/tool"` || path.Global != nil || string(path.Value) != `"bin/tool"` {
		t.Fatalf("path %+v", path)
	}
	call(t, a, "settings.schema", SchemaParams{Module: "core", Query: "TAB width"}, &s)
	if len(s.Core) != 1 || s.Core[0].Source != "global" || string(s.Core[0].Value) != "2" || len(s.Modules) != 0 {
		t.Fatalf("query %+v", s)
	}
	call(t, a, "settings.schema", SchemaParams{Query: "executable"}, &s)
	if len(s.Core) != 0 || len(s.Modules) != 1 || len(s.Modules[0].Settings) != 1 {
		t.Fatalf("query modules %+v", s)
	}
	call(t, a, "settings.schema", SchemaParams{Query: "qa-tool"}, &s)
	if len(s.Modules) != 1 || len(s.Modules[0].Settings) != 2 {
		t.Fatalf("query module id %+v", s)
	}
	wantCode(t, a, "settings.schema", SchemaParams{Module: "nope"}, CodeNotFound)

	// Invalid stored values are reported and skipped.
	_ = settings.Set(a.Settings(), "editor.tabWidth", 99)
	call(t, a, "settings.schema", SchemaParams{Module: "core", Query: "editor.tabWidth"}, &s)
	if s.Core[0].Invalid == "" || s.Core[0].Source != "default" {
		t.Fatalf("invalid %+v", s.Core[0])
	}

	// Effective reads.
	call(t, a, "settings.get", SettingsParams{Key: "qa-tool.path", Effective: true}, &v)
	if v.Source != "project" || string(v.Value) != `"bin/tool"` {
		t.Fatalf("effective %+v", v)
	}
	call(t, a, "settings.get", SettingsParams{Key: "qa-tool.level", Effective: true}, &v)
	if v.Source != "default" || string(v.Value) != `"low"` || !v.Found {
		t.Fatalf("effective default %+v", v)
	}
	call(t, a, "settings.get", SettingsParams{Key: "no.such", Effective: true}, &v)
	if v.Found {
		t.Fatalf("effective undeclared %+v", v)
	}
	if Setting(a, "qa-tool.level", "x") != "low" || Setting(a, "no.such", "d") != "d" || Setting(a, "qa-tool.path", 1) != 1 {
		t.Fatal("Setting helper")
	}
}

func TestSettingsSetValidation(t *testing.T) {
	a := newTestApp(t)
	addSpecModule(t, a)
	var v SettingValue
	bad := []SettingsSetParams{
		{Key: "editor.tabWidth", Value: json.RawMessage(`0`)},
		{Key: "editor.tabWidth", Value: json.RawMessage(`"4"`)},
		{Key: "ui.theme", Value: json.RawMessage(`"pink"`)},
		{Key: "qa-tool.level", Value: json.RawMessage(`"mid"`)},
		{Key: "files.associations", Value: json.RawMessage(`[1]`)},
		{Key: "editor.hijack", Value: json.RawMessage(`true`)}, // rejected spec → undeclared → editor.* client key, allowed
		{Key: "qa-tool.unknown", Value: json.RawMessage(`1`)},  // undeclared outside client namespaces
		{Key: "php.interpreter", Value: json.RawMessage(`"x"`)},
		{Key: "", Value: json.RawMessage(`1`)},
		{Key: "ui.", Value: json.RawMessage(`1`)},
	}
	for i, p := range bad {
		if i == 5 {
			call(t, a, "settings.set", p, &v)
			continue
		}
		wantCode(t, a, "settings.set", p, CodeInvalidParams)
	}
	// Global-only keys reject project values (also deletion).
	openProject(t, a, map[string]string{"a.php": "<?php\n"})
	wantCode(t, a, "settings.set", SettingsSetParams{Key: "ui.theme", Scope: "project", Value: json.RawMessage(`"light"`)}, CodeInvalidParams)
	wantCode(t, a, "settings.set", SettingsSetParams{Key: "ui.theme", Scope: "project"}, CodeInvalidParams)
	// Valid declared keys outside the old client namespaces.
	call(t, a, "settings.set", SettingsSetParams{Key: "qa-tool.level", Value: json.RawMessage(`"high"`)}, &v)
	call(t, a, "settings.set", SettingsSetParams{Key: "files.associations", Scope: "project", Value: json.RawMessage(`["*.tpl=php"]`)}, &v)
	call(t, a, "settings.set", SettingsSetParams{Key: "editor.fontSize", Value: json.RawMessage(`13.5`)}, &v)
	// Reset (null) of a declared key.
	call(t, a, "settings.set", SettingsSetParams{Key: "qa-tool.level"}, &v)
	if a.Settings().Has("qa-tool.level") {
		t.Fatal("not reset")
	}
	// Unknown keys stored by others are preserved.
	_ = settings.Set(a.Settings(), "legacy.thing", 1)
	call(t, a, "settings.set", SettingsSetParams{Key: "editor.tabWidth", Value: json.RawMessage(`8`)}, &v)
	if !a.Settings().Has("legacy.thing") {
		t.Fatal("unknown key dropped")
	}

	// autosave.idleSeconds is applied immediately.
	call(t, a, "settings.set", SettingsSetParams{Key: settings.KeyAutosaveIdle, Value: json.RawMessage(`7`)}, &v)
	if a.saver.IdleDelay() != 7*time.Second {
		t.Fatalf("idle %v", a.saver.IdleDelay())
	}
	call(t, a, "settings.set", SettingsSetParams{Key: settings.KeyAutosaveIdle}, &v)
	if a.saver.IdleDelay() != 0 {
		t.Fatal("idle not reset")
	}
	n := -1
	wantCode(t, a, "autosave.config", AutosaveConfig{IdleSeconds: &n}, CodeInvalidParams)
}

func TestAssociationsAndEditorConfig(t *testing.T) {
	a := newTestApp(t)
	root := openProject(t, a, map[string]string{
		"view.tpl":      "<p>{{ x }}</p>",
		".editorconfig": "root = true\n[*]\nindent_style = tab\ntab_width = 8\ntrim_trailing_whitespace = true\ninsert_final_newline = true\ncharset = utf-8-bom\n",
		"a.php":         "<?php\necho 1;   ",
	})
	var lang LanguageResult
	call(t, a, "files.detectLanguage", DetectLanguageParams{Path: "view.tpl"}, &lang)
	if lang.Language != "plaintext" {
		t.Fatalf("before %q", lang.Language)
	}
	var v SettingValue
	call(t, a, "settings.set", SettingsSetParams{Key: "files.associations", Scope: "project", Value: json.RawMessage(`["*.tpl=php","bad"]`)}, &v)
	call(t, a, "files.detectLanguage", DetectLanguageParams{Path: "view.tpl"}, &lang)
	if lang.Language != "php" {
		t.Fatalf("after %q", lang.Language)
	}
	var open DocOpenResult
	call(t, a, "doc.open", DocOpenParams{Path: "view.tpl"}, &open)
	if open.Language != "php" {
		t.Fatalf("doc lang %q", open.Language)
	}
	// Rename keeps honouring associations.
	call(t, a, "files.rename", RenameParams{Path: "view.tpl", NewName: "other.tpl"}, nil)
	var docs []DocState
	call(t, a, "doc.list", nil, &docs)
	if len(docs) != 1 || docs[0].Language != "php" {
		t.Fatalf("renamed %+v", docs)
	}

	// .editorconfig on open and save.
	call(t, a, "doc.open", DocOpenParams{Path: "a.php"}, &open)
	if open.EditorConfig == nil || open.EditorConfig.IndentStyle != "tab" || open.EditorConfig.TabWidth != 8 || !strings.Contains(open.CharsetWarning, "BOM") {
		t.Fatalf("editorconfig %+v %q", open.EditorConfig, open.CharsetWarning)
	}
	var ec EditorConfigResult
	call(t, a, "files.editorconfig", PathParams{Path: "a.php"}, &ec)
	if !ec.Enabled || ec.IndentSize != 8 || ec.CharsetWarning == "" || len(ec.Files) != 1 {
		t.Fatalf("method %+v", ec)
	}
	var st DocState
	call(t, a, "doc.insert", DocInsertParams{Path: "a.php", At: Pos{Offset: 0}, Text: "  "}, &st)
	var sr DocSaveResult
	call(t, a, "doc.save", DocSaveParams{Path: "a.php"}, &sr)
	data, _ := os.ReadFile(filepath.Join(root, "a.php"))
	if string(data) != "  <?php\necho 1;\n" {
		t.Fatalf("saved %q", data)
	}
	var txt DocText
	call(t, a, "doc.text", DocTextParams{Path: "a.php"}, &txt)
	if txt.Text != string(data) {
		t.Fatalf("buffer %q", txt.Text)
	}

	// Switched off.
	call(t, a, "settings.set", SettingsSetParams{Key: "files.editorconfig", Scope: "project", Value: json.RawMessage(`false`)}, &v)
	call(t, a, "files.editorconfig", PathParams{Path: "a.php"}, &ec)
	if ec.Enabled || ec.IndentStyle != "" {
		t.Fatalf("off %+v", ec)
	}
	call(t, a, "doc.insert", DocInsertParams{Path: "a.php", At: Pos{Offset: 0}, Text: "x "}, &st)
	call(t, a, "doc.save", DocSaveParams{Path: "a.php"}, &sr)
	if data, _ := os.ReadFile(filepath.Join(root, "a.php")); !strings.HasPrefix(string(data), "x   <?php") {
		t.Fatalf("off saved %q", data)
	}

	if !within("/a/b", "/a/b/c") || within("/a/b", "/a/bc") || within("/a/b", "/a") || !within("/a/b", "/a/b") {
		t.Fatal("within")
	}
}
