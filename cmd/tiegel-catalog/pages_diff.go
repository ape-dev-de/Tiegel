package main

import (
	"fmt"
	"strings"

	"gioui.org/layout"

	"github.com/ape-dev-de/tiegel/internal/editor/buffer"
	"github.com/ape-dev-de/tiegel/internal/editor/highlight"
	"github.com/ape-dev-de/tiegel/internal/editor/highlight/tshighlight"
	"github.com/ape-dev-de/tiegel/internal/ui/atoms"
	"github.com/ape-dev-de/tiegel/internal/ui/editorview"
	"github.com/ape-dev-de/tiegel/internal/ui/molecules"
	"github.com/ape-dev-de/tiegel/internal/ui/organisms/diff"
	"github.com/ape-dev-de/tiegel/internal/ui/organisms/diff/diffsample"
	"github.com/ape-dev-de/tiegel/internal/ui/tokens"
)

// diffPages are the pages of the file comparison components (AP10.6):
// the ChangeBands molecule and the diff organisms with sample data from
// diffsample (computed with gitcli.CompareTexts like git.fileDiff).
func diffPages() []*page {
	return []*page{changeBandsPage(), diffWindowPage(), blameGutterPage(), fileHistoryPage()}
}

// logDiff appends diff events to an event log.
func logDiff(l *eventLog, ev []diff.Event) {
	for _, e := range ev {
		s := strings.TrimPrefix(fmt.Sprintf("%T %+v", e, e), "diff.")
		if len(s) > 160 {
			s = s[:160] + "…"
		}
		l.last = append(l.last, s)
	}
	if n := len(l.last); n > 4 {
		l.last = l.last[n-4:]
	}
}

// tsHighlighter creates Tree-sitter highlighters that redraw the catalog
// when a background parse finishes.
func tsHighlighter(b *buffer.Buffer, path string) highlight.Highlighter {
	return tshighlight.New(b, tshighlight.Options{Path: path, Invalidate: requestRedraw})
}

func changeBandsPage() *page {
	return &page{
		Level: "Molecules", Name: "ChangeBands", Glyph: atoms.GlyphSwap,
		Summary: "Strip between the columns of a side-by-side diff: curved bands connect each change block's lines on the left with its lines on the right.",
		Variants: func(th *tokens.Theme) []variant {
			band := func(k editorview.DecorationKind, lt, lb, rt, rb int, em bool) molecules.Band {
				fill, _, edge := editorview.DecorationColors(th, k)
				return molecules.Band{LeftTop: lt, LeftBottom: lb, RightTop: rt, RightBottom: rb, Fill: fill, Edge: tokens.WithAlpha(edge, 0xB0), Emphasis: em}
			}
			cb := molecules.ChangeBands{Bands: []molecules.Band{
				band(editorview.DecorationAdded, 20, 20, 10, 50, false),
				band(editorview.DecorationModified, 70, 90, 80, 130, true),
				band(editorview.DecorationDeleted, 140, 180, 160, 160, false),
			}}
			cells := []molecules.AnnotationCell{
				{Text: "2026-08-21  Aiko Tanaka", Bar: th.Color.Accent, Tint: tokens.WithAlpha(th.Color.Accent, 0x30)},
				{Text: "2026-03-01  Mara Lindqvist", Bar: th.Color.Border, Muted: true},
			}
			return []variant{
				{"Added (insertion point left), modified (current, emphasized), deleted", fixedSize(48, 200, func(gtx layout.Context) layout.Dimensions {
					return cb.Layout(gtx, th)
				})},
				{"AnnotationCell (blame column): fresh and old commit", column(0, fixedSize(240, 22, func(gtx layout.Context) layout.Dimensions {
					return cells[0].Layout(gtx, th)
				}), fixedSize(240, 22, func(gtx layout.Context) layout.Dimensions {
					return cells[1].Layout(gtx, th)
				}))},
			}
		},
	}
}

func diffWindowPage() *page {
	w := diff.NewDiffWindow()
	w.NewHighlighter = tsHighlighter
	shared := buffer.NewString(diffsample.New)
	w.SetContent(diffsample.Content(), shared)
	ro := diff.NewDiffWindow()
	ro.NewHighlighter = tsHighlighter
	c := diffsample.Content()
	c.Left.Label, c.Right.Label = "a1b2c3d4", "HEAD (4f2a9c1e)"
	ro.SetContent(c, nil)
	ro.SetUnified(true)
	var log eventLog
	return &page{
		Level: "Organisms", Name: "DiffWindow", Glyph: atoms.GlyphSwap,
		Summary: "Side-by-side or unified comparison on two editors: synchronized scrolling, bands between the columns, word highlights, F7/Shift+F7 navigation, ignore whitespace, revert block (») on an editable work tree side.",
		Variants: func(th *tokens.Theme) []variant {
			return []variant{
				{"HEAD ↔ work tree (right side editable; » reverts a block)", fixedSize(0, 440, func(gtx layout.Context) layout.Dimensions {
					ev := w.Update(gtx)
					logDiff(&log, ev)
					for _, e := range ev {
						if r, ok := e.(diff.RevertRequested); ok {
							_ = w.ApplyRevert(r)
						}
					}
					return w.Layout(gtx, th)
				})},
				{"Events", log.widget(th)},
				{"Two revisions, unified (read-only)", fixedSize(0, 300, func(gtx layout.Context) layout.Dimensions {
					logDiff(&log, ro.Update(gtx))
					return ro.Layout(gtx, th)
				})},
			}
		},
	}
}

func blameGutterPage() *page {
	ed := editorview.New(buffer.NewString(diffsample.New))
	ed.ReadOnly = true
	ed.Highlighter = tsHighlighter(ed.Buffer(), diffsample.Path)
	g := diff.NewBlameGutter()
	g.SetBlame(diffsample.Blame())
	ed.SetGutterColumn(g)
	var log eventLog
	return &page{
		Level: "Organisms", Name: "BlameGutter", Glyph: atoms.GlyphHistory,
		Summary: "Annotation column for the editor (git.blame): date and author per change, age bar and tint from the newest to the oldest commit; a click reports the commit.",
		Variants: func(th *tokens.Theme) []variant {
			return []variant{
				{"Editor with annotations (click a date)", fixedSize(0, 420, func(gtx layout.Context) layout.Dimensions {
					for {
						ev, ok := ed.Update(gtx)
						if !ok {
							break
						}
						if e, ok := g.HandleEditorEvent(ev); ok {
							logDiff(&log, []diff.Event{e})
						}
					}
					return ed.Layout(gtx, th)
				})},
				{"Events", log.widget(th)},
			}
		},
	}
}

func fileHistoryPage() *page {
	const total = 120
	v := diff.NewFileHistoryView()
	v.Diff.NewHighlighter = tsHighlighter
	v.SetHistory(diffsample.Path, diffsample.History(0, 40), true)
	var log eventLog
	return &page{
		Level: "Organisms", Name: "FileHistoryView", Glyph: atoms.GlyphHistory,
		Summary: "History of one file (git.fileHistory, follows renames) with the LogView; selecting a commit requests its diff against the parent.",
		Variants: func(th *tokens.Theme) []variant {
			return []variant{
				{diffsample.Path, fixedSize(0, 520, func(gtx layout.Context) layout.Dimensions {
					ev := v.Update(gtx)
					logDiff(&log, ev)
					for _, e := range ev {
						switch e := e.(type) {
						case diff.HistoryDiffRequested:
							c := diffsample.Content()
							c.Left.Label, c.Right.Label = shortRev(e.Base), shortRev(e.Commit)
							v.Diff.SetContent(c, nil)
						case diff.HistoryPageRequested:
							n := min(40, total-e.Loaded)
							v.AppendHistory(diffsample.History(e.Loaded, n), e.Loaded+n < total)
						}
					}
					return v.Layout(gtx, th)
				})},
				{"Events", log.widget(th)},
			}
		},
	}
}

func shortRev(h string) string {
	if len(h) > 8 {
		return h[:8]
	}
	return h
}
