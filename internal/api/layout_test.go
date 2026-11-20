package api

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestLayoutMethods(t *testing.T) {
	a := newTestApp(t)
	var st LayoutState
	wantCode(t, a, "layout.get", LayoutParams{}, CodeNoProject)
	wantCode(t, a, "layout.reset", struct{}{}, CodeNoProject)
	wantCode(t, a, "layout.save", LayoutSaveParams{Name: " "}, CodeInvalidParams)
	wantCode(t, a, "layout.save", LayoutSaveParams{Name: strings.Repeat("x", 81)}, CodeInvalidParams)
	wantCode(t, a, "layout.save", LayoutSaveParams{Name: "x", Layout: json.RawMessage(`[1]`)}, CodeInvalidParams)
	// Saving an explicit layout works without a project.
	call(t, a, "layout.save", LayoutSaveParams{Name: "Wide", Layout: json.RawMessage(`{"corners":{"left":"bottom","right":"bottom"}}`)}, &st)
	if !st.Found || st.Name != "Wide" || len(st.Names) != 1 {
		t.Fatalf("save %+v", st)
	}
	wantCode(t, a, "layout.save", LayoutSaveParams{Name: "Current"}, CodeNoProject)

	openProject(t, a, map[string]string{"a.php": "<?php\n"})
	call(t, a, "layout.get", LayoutParams{}, &st)
	if st.Found || len(st.Names) != 1 || st.Names[0] != "Wide" {
		t.Fatalf("fresh project %+v", st)
	}
	// Without a stored layout, "save" stores the defaults.
	call(t, a, "layout.save", LayoutSaveParams{Name: "Default"}, &st)
	if string(st.Layout) != `{}` || len(st.Names) != 2 || st.Names[0] != "Default" {
		t.Fatalf("save defaults %+v", st)
	}
	call(t, a, "layout.apply", LayoutParams{Name: "Wide"}, &st)
	call(t, a, "layout.get", LayoutParams{}, &st)
	if !st.Found || !strings.Contains(string(st.Layout), `"bottom"`) {
		t.Fatalf("applied %+v", st)
	}
	var sv SettingValue
	call(t, a, "settings.get", SettingsParams{Key: SettingToolLayout, Scope: "project"}, &sv)
	if !sv.Found {
		t.Fatal("apply should write the project setting")
	}
	// The project's layout saved under a new name.
	call(t, a, "layout.save", LayoutSaveParams{Name: "Copy"}, &st)
	call(t, a, "layout.get", LayoutParams{Name: "Copy"}, &st)
	if !st.Found || !strings.Contains(string(st.Layout), `"bottom"`) || len(st.Names) != 3 {
		t.Fatalf("copy %+v", st)
	}
	wantCode(t, a, "layout.get", LayoutParams{Name: "nope"}, CodeNotFound)
	wantCode(t, a, "layout.apply", LayoutParams{Name: "nope"}, CodeNotFound)
	wantCode(t, a, "layout.apply", LayoutParams{}, CodeInvalidParams)
	call(t, a, "layout.reset", struct{}{}, &st)
	call(t, a, "layout.get", LayoutParams{}, &st)
	if st.Found {
		t.Fatalf("reset %+v", st)
	}
	call(t, a, "layout.delete", LayoutParams{Name: "Copy"}, &st)
	if len(st.Names) != 2 {
		t.Fatalf("delete %+v", st)
	}
	wantCode(t, a, "layout.delete", LayoutParams{Name: "Copy"}, CodeNotFound)
	wantCode(t, a, "layout.delete", LayoutParams{}, CodeInvalidParams)
}
