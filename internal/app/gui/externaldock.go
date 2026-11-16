package gui

import (
	"fmt"

	"gioui.org/layout"

	"github.com/ape-dev-de/tiegel/internal/app/gui/controller"
	"github.com/ape-dev-de/tiegel/internal/ui/atoms"
	"github.com/ape-dev-de/tiegel/internal/ui/molecules"
)

// External changes (D141): a document with unsaved changes whose file
// changed on disk gets a banner above its editor – Keep mine (write the
// buffer over the file), Take disk (reload, one undo step) or Show Diff
// (disk ↔ editor in a diff tab). A file deleted or renamed on disk gets a
// banner with Save Again and Close. Clean documents reload silently
// (doc.reloaded). The same flow starts when an explicit save is refused
// with -32004 externalChange.

// extButtons are the banner buttons of one file.
type extButtons struct {
	keep, take, diff, recreate, close atoms.Button
}

func newExtButtons() *extButtons {
	return &extButtons{
		keep:     atoms.Button{Text: "Keep mine", Kind: atoms.Primary},
		take:     atoms.Button{Text: "Take disk", Kind: atoms.Secondary},
		diff:     atoms.Button{Text: "Show Diff", Kind: atoms.Ghost, Glyph: atoms.GlyphSwap},
		recreate: atoms.Button{Text: "Save Again", Kind: atoms.Primary},
		close:    atoms.Button{Text: "Close", Kind: atoms.Secondary},
	}
}

func (d *workDock) installExternal() {
	d.v.Registry.AddEditorBanner(d.externalBanner)
	d.v.Registry.AddTabDecorator(d.decorateTab)
}

// externalTexts returns the banner title and detail of a change.
func externalTexts(e *controller.ExternalChange) (title, detail string) {
	if e.Deleted() {
		title = "File deleted on disk"
		if e.RenamedTo != "" {
			title = "File renamed on disk to " + e.RenamedTo
		}
		return title, "The editor keeps its content. Save Again writes it to " + e.Path + "; Close discards it."
	}
	title = "File changed on disk"
	detail = "Another program changed " + e.Path + " while the editor has unsaved changes."
	if e.Added+e.Removed > 0 {
		detail += fmt.Sprintf(" (%d line(s) only on disk, %d only in the editor).", e.Added, e.Removed)
	}
	return title, detail + " Keep mine overwrites the file; Take disk replaces the editor content (undoable)."
}

// externalBanner is the editor banner of a file with a pending external
// change.
func (d *workDock) externalBanner(v *View, t *controller.Tab) layout.Widget {
	w := v.ctl.Work
	e := w.ExternalOf(t.File)
	if e == nil {
		return nil
	}
	b := d.ext[t.File]
	if b == nil {
		b = newExtButtons()
		d.ext[t.File] = b
	}
	file := t.File
	return func(gtx layout.Context) layout.Dimensions {
		th := v.th
		var btns []*atoms.Button
		if e.Deleted() {
			if b.recreate.Clicked(gtx) {
				v.Post(func() { w.RecreateDeleted(file) })
			}
			if b.close.Clicked(gtx) {
				v.Post(func() { w.CloseDiscard(file) })
			}
			btns = []*atoms.Button{&b.recreate, &b.close}
		} else {
			if b.keep.Clicked(gtx) {
				v.Post(func() { w.ResolveExternal(file, controller.ResolveKeepMine) })
			}
			if b.take.Clicked(gtx) {
				v.Post(func() { w.ResolveExternal(file, controller.ResolveTakeDisk) })
			}
			if b.diff.Clicked(gtx) {
				v.Post(func() { w.OpenDiff(controller.DiffSpec{Path: v.ctl.Rel(file), External: true}) })
			}
			btns = []*atoms.Button{&b.keep, &b.take, &b.diff}
		}
		if e.Busy {
			gtx = gtx.Disabled()
		}
		title, detail := externalTexts(e)
		kind := molecules.BannerWarning
		if e.Deleted() {
			kind = molecules.BannerError
		}
		children := []layout.FlexChild{layout.Flexed(1, molecules.Banner{Kind: kind, Title: title, Detail: detail}.Fn(th))}
		for _, btn := range btns {
			children = append(children, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return layout.Inset{Left: th.Space.XS}.Layout(gtx, func(gtx layout.Context) layout.Dimensions { return btn.Layout(gtx, th) })
			}))
		}
		return layout.Inset{Left: th.Space.S, Right: th.Space.S, Top: th.Space.XS, Bottom: th.Space.XS}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			return layout.Flex{Alignment: layout.Middle}.Layout(gtx, children...)
		})
	}
}
