package main

import (
	_ "embed"
	"fmt"
	"image"
	"strings"

	"gioui.org/layout"
	"gioui.org/unit"

	"github.com/ape-dev-de/tiegel/internal/editor/buffer"
	"github.com/ape-dev-de/tiegel/internal/editor/highlight"
	"github.com/ape-dev-de/tiegel/internal/editor/search"
	"github.com/ape-dev-de/tiegel/internal/ui/atoms"
	"github.com/ape-dev-de/tiegel/internal/ui/editorview"
	"github.com/ape-dev-de/tiegel/internal/ui/tokens"
)

//go:embed sample.php
var samplePHP string

// editorDemo is the state of the Editor catalog page.
type editorDemo struct {
	ed          *editorview.Editor
	yaml        *editorview.Editor
	breakpoints map[int]bool
	status      string
}

func newEditorDemo() *editorDemo {
	d := &editorDemo{breakpoints: map[int]bool{29: true}, status: "Click into the editor."}
	ed := editorview.New(buffer.NewString(samplePHP))
	ed.Highlighter = highlight.PHPDemo
	ed.SetFoldRanges(highlight.BraceFolds(ed.Buffer().Snapshot()))
	ed.SetGutterMarkers(editorview.MarkerFunc(d.markers))
	at := strings.Index(samplePHP, "SmtpTransport")
	ed.SetDiagnostics([]editorview.Diagnostic{
		{Start: at, End: at + len("SmtpTransport"), Severity: editorview.SeverityError, Message: "Undefined class SmtpTransport"},
	})
	if q, err := search.Compile("msg", search.Options{WholeWord: true}); err == nil {
		ed.SetSearch(q)
	}
	d.ed = ed

	y := editorview.New(buffer.NewString("services:\n  php:\n    image: php:8.3-fpm\n    # xdebug enabled\n    environment:\n      XDEBUG_MODE: debug\n"))
	y.LineComment = editorview.LineComments["yaml"]
	y.ReadOnly = true
	d.yaml = y
	return d
}

// markers combines the demo breakpoints with fake VCS changes.
func (d *editorDemo) markers(line int) []editorview.Marker {
	var out []editorview.Marker
	if d.breakpoints[line] {
		out = append(out, editorview.Marker{Kind: editorview.MarkerBreakpoint})
	}
	switch {
	case line >= 29 && line <= 32:
		out = append(out, editorview.Marker{Kind: editorview.MarkerGitAdded})
	case line == 41:
		out = append(out, editorview.Marker{Kind: editorview.MarkerGitModified})
	case line == 45:
		out = append(out, editorview.Marker{Kind: editorview.MarkerGitDeleted})
	case line == 15:
		out = append(out, editorview.Marker{Kind: editorview.MarkerIcon, Glyph: atoms.GlyphInfo, Tone: editorview.ToneInfo})
	}
	return out
}

func (d *editorDemo) update(gtx layout.Context) {
	for {
		ev, ok := d.ed.Update(gtx)
		if !ok {
			break
		}
		switch ev := ev.(type) {
		case editorview.GutterClickEvent:
			if ev.Area == editorview.GutterIcons || ev.Area == editorview.GutterNumbers {
				d.breakpoints[ev.Line] = !d.breakpoints[ev.Line]
			}
			d.status = fmt.Sprintf("Gutter click: line %d", ev.Line+1)
		case editorview.ActionEvent:
			d.status = fmt.Sprintf("Action for the host: %s", ev.Action)
		case editorview.FocusEvent:
			if !ev.Focused {
				d.status = "Focus lost – the IDE would autosave now."
			}
		case editorview.ChangeEvent:
			p := d.ed.Buffer().OffsetToPos(d.ed.Cursor().Primary().Head)
			d.status = fmt.Sprintf("Edited – %d lines, caret %d:%d", d.ed.Buffer().LineCount(), p.Line+1, p.Col+1)
		}
	}
}

// sized gives w an exact size: full width, h dp high.
func sized(h unit.Dp, w layout.Widget) layout.Widget {
	return func(gtx layout.Context) layout.Dimensions {
		gtx.Constraints = layout.Exact(image.Pt(gtx.Constraints.Max.X, gtx.Dp(h)))
		return w(gtx)
	}
}

func editorPage() *page {
	d := newEditorDemo()
	return &page{
		editor: d,
		Level:  "Organisms", Name: "Editor", Glyph: atoms.GlyphFileCode,
		Summary: "Code editor: virtual rendering, multi-cursor, folding, gutter markers, diagnostics, search and bracket highlights, IME.",
		Variants: func(th *tokens.Theme) []variant {
			return []variant{
				{"PHP (demo highlighter) – " + d.status, sized(460, func(gtx layout.Context) layout.Dimensions {
					d.update(gtx)
					return d.ed.Layout(gtx, th)
				})},
				{"Read-only YAML", sized(140, func(gtx layout.Context) layout.Dimensions {
					return d.yaml.Layout(gtx, th)
				})},
			}
		},
	}
}
