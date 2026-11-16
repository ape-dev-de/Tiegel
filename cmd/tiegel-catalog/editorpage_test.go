package main

import (
	"context"
	"image"
	"strings"
	"sync/atomic"
	"testing"
	"time"

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

func TestEditorPageUsesTreeSitter(t *testing.T) {
	d := newEditorDemo()
	defer d.hl.Close()
	defer d.yamlHL.Close()
	var calls atomic.Int32
	f := func() { calls.Add(1) }
	redraw.Store(&f)
	defer redraw.Store(nil)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := d.hl.Wait(ctx); err != nil {
		t.Fatal(err)
	}
	if err := d.yamlHL.Wait(ctx); err != nil {
		t.Fatal(err)
	}
	s := d.ed.Buffer().Snapshot()
	if len(d.hl.Spans(s, 0, s.Len())) == 0 {
		t.Fatal("no Tree-sitter spans for the PHP sample")
	}
	ys := d.yaml.Buffer().Snapshot()
	if len(d.yamlHL.Spans(ys, 0, ys.Len())) == 0 {
		t.Fatal("no spans for YAML")
	}
	if parseState(d.hl) != "php" {
		t.Fatalf("state %q", parseState(d.hl))
	}
	uitest.Smoke(t, "editor page", window, func(gtx layout.Context, th *tokens.Theme) layout.Dimensions {
		d.update(gtx)
		return d.ed.Layout(gtx, th)
	})
	if _, ok := d.hl.TakeFolds(); ok {
		t.Fatal("folds not taken by update")
	}
	if calls.Load() == 0 {
		t.Fatal("redraw never requested")
	}
}
