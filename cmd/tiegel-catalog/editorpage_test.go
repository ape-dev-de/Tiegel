package main

import (
	"image"
	"strings"
	"testing"

	"gioui.org/io/key"
	"gioui.org/layout"

	"github.com/ape-dev-de/tiegel/internal/ui/tokens"
	"github.com/ape-dev-de/tiegel/internal/ui/uitest"
)

func TestEditorPageIsInteractive(t *testing.T) {
	c := newCatalog(false)
	if !c.selectPage("Editor") {
		t.Fatal("no editor page")
	}
	d := c.pages[c.selected].editor
	if d == nil {
		t.Fatal("editor page without state")
	}
	h := uitest.NewHarness(window)
	w := func(gtx layout.Context, _ *tokens.Theme) layout.Dimensions { return c.Layout(gtx) }
	h.Frame(w)
	// Click into the editor text (the card starts below the page header).
	h.Click(image.Pt(700, 300))
	h.Frame(w)
	h.Frame(w)
	if !d.ed.Focused() {
		t.Fatal("click did not focus the editor")
	}
	st := h.Router.EditorState()
	h.Router.Queue(key.EditEvent{Range: st.Selection.Range, Text: "Z"})
	h.Frame(w)
	h.Frame(w)
	if !strings.Contains(d.ed.Buffer().String(), "Z") || d.ed.Buffer().String() == samplePHP {
		t.Fatal("typing did not reach the editor")
	}
	if !strings.HasPrefix(d.status, "Edited") {
		t.Fatalf("status %q", d.status)
	}
	if len(d.markers(29)) == 0 || len(d.markers(0)) != 0 {
		t.Fatal("demo markers")
	}
}
