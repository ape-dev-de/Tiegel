package gui

import (
	"testing"

	"github.com/ape-dev-de/tiegel/internal/editor/keymap"
	"github.com/ape-dev-de/tiegel/internal/ui/editorview"
)

// TestEditorDisplaySettings covers soft wrap, whitespace and the right
// margin from the settings, .editorconfig and the View toggles (M12.3).
func TestEditorDisplaySettings(t *testing.T) {
	e := newEnvWith(t, true, map[string]string{
		"README.md":     "# Title\n",
		".editorconfig": "root = true\n[*.php]\nmax_line_length = 100\n",
	}, nil)
	if p := e.ctl.Prefs; p.SoftWrap != "files" || p.ShowWhitespace != "none" || p.RightMargin != 120 || !p.ShowRightMargin {
		t.Fatalf("defaults %+v", p)
	}
	md := e.openEditor("README.md")
	if !md.ed.SoftWrap || md.ed.RightMargin != 120 {
		t.Fatalf("markdown: wrap %v margin %d", md.ed.SoftWrap, md.ed.RightMargin)
	}
	php := e.openEditor("index.php")
	if php.ed.SoftWrap || php.ed.RightMargin != 100 || php.ed.Whitespace != editorview.WhitespaceNone {
		t.Fatalf("php: wrap %v margin %d ws %d", php.ed.SoftWrap, php.ed.RightMargin, php.ed.Whitespace)
	}

	// View › Soft-Wrap toggles only the active editor.
	e.v.runAction(keymap.ViewSoftWrap)
	e.frame()
	if !php.ed.SoftWrap || !php.ed.Wrapped() {
		t.Fatal("soft wrap toggle did not wrap the php editor")
	}
	e.v.runAction(keymap.ViewSoftWrap)
	e.frame()
	if php.ed.SoftWrap {
		t.Fatal("second toggle did not unwrap")
	}

	// View › Show Whitespaces flips the setting.
	e.v.runAction(keymap.ViewShowWhitespace)
	e.settle()
	if php.ed.Whitespace != editorview.WhitespaceAll {
		t.Fatalf("whitespace %d", php.ed.Whitespace)
	}
	e.v.runAction(keymap.ViewShowWhitespace)
	e.settle()
	if php.ed.Whitespace != editorview.WhitespaceNone {
		t.Fatalf("whitespace after second toggle %d", php.ed.Whitespace)
	}

	// Settings from the dialog apply live; the margin can be hidden.
	e.ctl.SetSettingValue("editor.showRightMargin", "global", false)
	e.ctl.SetSettingValue("editor.softWrap", "global", "all")
	e.settle()
	if php.ed.RightMargin != 0 || !php.ed.SoftWrap {
		t.Fatalf("after settings: margin %d wrap %v", php.ed.RightMargin, php.ed.SoftWrap)
	}
	e.ctl.SetSettingValue("editor.rightMargin", "project", 80)
	e.ctl.SetSettingValue("editor.showRightMargin", "global", true)
	e.settle()
	md = e.openEditor("README.md") // inactive editors update when drawn
	if e.ctl.Prefs.RightMargin != 80 || md.ed.RightMargin != 80 {
		t.Fatalf("project margin %d / %d", e.ctl.Prefs.RightMargin, md.ed.RightMargin)
	}
}
