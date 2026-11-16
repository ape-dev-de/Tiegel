package gui

import (
	"fmt"
	"image"
	"path"
	"strings"

	"gioui.org/io/key"
	"gioui.org/layout"
	"gioui.org/unit"
	"gioui.org/widget"

	"github.com/ape-dev-de/tiegel/internal/app/gui/controller"
	"github.com/ape-dev-de/tiegel/internal/ui/atoms"
	"github.com/ape-dev-de/tiegel/internal/ui/molecules"
	"github.com/ape-dev-de/tiegel/internal/ui/organisms/palette"
	"github.com/ape-dev-de/tiegel/internal/ui/tokens"
)

// LSP features in the UI (D160): the context actions popup (Alt+Enter)
// and Rename Symbol (Shift+F6: name input, preview list, apply).

// showCodeActions offers the context actions in a popup list.
func (d *navDock) showCodeActions(as *controller.CodeActions) {
	items := make([]palette.Item, 0, len(as.Items))
	for _, a := range as.Items {
		g := atoms.GlyphInfo
		switch {
		case a.Disabled != "":
			g = atoms.GlyphRemove
		case a.Preferred:
			g = atoms.GlyphDone
		case strings.HasPrefix(a.Kind, "quickfix"):
			g = atoms.GlyphWarning
		case strings.HasPrefix(a.Kind, "refactor"):
			g = atoms.GlyphEdit
		}
		detail := a.Kind
		if a.Disabled != "" {
			detail = a.Disabled
		}
		items = append(items, palette.Item{Label: a.Title, Detail: detail, Glyph: g})
	}
	filter := func(q string) ([]palette.Item, []int) {
		var out []palette.Item
		var idx []int
		for i, it := range items {
			if q == "" || strings.Contains(strings.ToLower(it.Label), strings.ToLower(q)) {
				out = append(out, it)
				idx = append(idx, i)
			}
		}
		return out, idx
	}
	_, idx := filter("")
	d.v.OpenChooser(ChooserSpec{Title: "Context Actions", Hint: "Filter", Items: items,
		OnQuery: func(q string) []palette.Item {
			var out []palette.Item
			out, idx = filter(q)
			return out
		},
		OnChoose: func(i int, _ string) {
			if i < 0 || i >= len(idx) {
				d.n.CloseCodeActions()
				return
			}
			d.n.ApplyCodeAction(idx[i])
		}})
}

// renameDock shows Rename Symbol.
type renameDock struct {
	d     *navDock
	field atoms.TextField
	list  molecules.ScrollList
	rows  []renameRow
	shown *controller.RenameState
	stage int
}

type renameRow struct {
	header bool
	text   string
	detail string
}

// sync opens the popup of the current rename stage.
func (r *renameDock) sync() {
	v := r.d.v
	rs := r.d.n.Rename
	if rs == nil {
		if r.shown != nil && v.popup != nil && v.popup.Title != "" && strings.HasPrefix(v.popup.Title, "Rename") {
			v.ClosePopup()
		}
		r.shown = nil
		return
	}
	if rs == r.shown && rs.Stage == r.stage {
		return
	}
	r.shown, r.stage = rs, rs.Stage
	switch rs.Stage {
	case controller.RenameInput:
		r.field = atoms.TextField{Hint: "New name", Mono: true}
		r.field.Editor.SingleLine, r.field.Editor.Submit = true, true
		r.field.SetText(rs.Placeholder)
		r.field.Editor.SetCaret(len(rs.Placeholder), 0)
		focus := true
		v.OpenPopup(&Popup{
			Title: "Rename " + rs.Placeholder, Detail: "Rename the symbol in all files (language server).", Centered: true,
			Body: func(gtx layout.Context, th *tokens.Theme) layout.Dimensions {
				if focus {
					focus = false
					gtx.Execute(key.FocusCmd{Tag: &r.field.Editor})
				}
				for {
					ev, ok := r.field.Editor.Update(gtx)
					if !ok {
						break
					}
					if _, ok := ev.(widget.SubmitEvent); ok {
						v.Post(r.preview)
					}
				}
				return r.field.Layout(gtx, th)
			},
			Buttons: []*PopupButton{
				{Text: "Preview", Kind: atoms.Primary, OnClick: r.preview},
				{Text: "Cancel", Kind: atoms.Secondary, OnClick: r.d.n.CancelRename},
			},
			OnClose: r.d.n.CancelRename,
		})
	case controller.RenamePreview:
		r.rows = r.rows[:0]
		for _, f := range rs.Preview {
			title := f.Path
			if f.NewPath != "" {
				title += " → " + f.NewPath
			}
			r.rows = append(r.rows, renameRow{header: true, text: path.Base(title), detail: fmt.Sprintf("%s · %s", path.Dir(f.Path), plural(len(f.Edits), "change"))})
			for _, e := range f.Edits {
				r.rows = append(r.rows, renameRow{text: fmt.Sprintf("%d:%d", e.Range.Start.Line, e.Range.Start.Col), detail: e.NewText})
			}
		}
		v.OpenPopup(&Popup{
			Title:    fmt.Sprintf("Rename %s to %s", rs.Placeholder, rs.NewName),
			Detail:   fmt.Sprintf("%s in %s", plural(rs.Changes(), "change"), plural(len(rs.Preview), "file")),
			Centered: true, Width: 560, BodyHeight: 260,
			Body: r.layoutPreview,
			Buttons: []*PopupButton{
				{Text: "Rename", Kind: atoms.Primary, OnClick: r.d.n.ApplyRename},
				{Text: "Cancel", Kind: atoms.Secondary, OnClick: r.d.n.CancelRename},
			},
			OnClose: r.d.n.CancelRename,
		})
	}
}

func (r *renameDock) preview() {
	rs := r.d.n.Rename
	if rs == nil {
		return
	}
	name := strings.TrimSpace(r.field.Text())
	if name == "" || name == rs.Placeholder {
		r.d.n.CancelRename()
		return
	}
	// The popup closes (button or Enter); the preview opens a new one.
	r.shown = nil
	if r.d.v.popup != nil {
		r.d.v.popup.OnClose = nil
		r.d.v.ClosePopup()
	}
	r.d.n.PreviewRename(name)
}

func (r *renameDock) layoutPreview(gtx layout.Context, th *tokens.Theme) layout.Dimensions {
	return r.list.Layout(gtx, th, len(r.rows), func(gtx layout.Context, i int) layout.Dimensions {
		row := r.rows[i]
		h := gtx.Dp(th.Size.RowH)
		w := gtx.Constraints.Max.X
		gtx.Constraints = layout.Exact(image.Pt(w, h))
		indent := unit.Dp(0)
		if !row.header {
			indent = th.Space.L
		}
		return layout.Inset{Left: indent}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					if row.header {
						return atoms.Strong(row.text).SingleLine().Layout(gtx, th)
					}
					return atoms.Muted(row.text).SingleLine().Layout(gtx, th)
				}),
				layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
					gtx.Constraints.Min.X = 0
					if row.header {
						return layout.Inset{Left: th.Space.S}.Layout(gtx, atoms.Small(row.detail).SingleLine().Fn(th))
					}
					return layout.Inset{Left: th.Space.S}.Layout(gtx, atoms.Label{Text: row.detail, Role: atoms.RoleCode, MaxLines: 1}.Fn(th))
				}),
			)
		})
	})
}
