package git

import (
	"fmt"
	"strings"

	"gioui.org/io/key"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/widget"

	"github.com/ape-dev-de/tiegel/internal/gitcli"
	"github.com/ape-dev-de/tiegel/internal/ui/atoms"
	"github.com/ape-dev-de/tiegel/internal/ui/molecules"
	"github.com/ape-dev-de/tiegel/internal/ui/tokens"
)

// BranchList shows local and remote branches with a search field and the
// actions Checkout, New branch and Delete. It is meant for a popup or a
// side panel; the host positions it.
//
// Data in: SetBranches. Events out: CheckoutRequested (button or double
// click), CreateBranchRequested (inline form; the start point is the
// selected branch), DeleteBranchRequested (local, not checked out).
type BranchList struct {
	branches []gitcli.Branch
	rows     []molecules.ListRow
	view     []int // visible branch indexes (local first, then remote)
	headers  [2]int
	selected int // branch index, -1 = none

	search   *molecules.SearchField
	list     molecules.ScrollList
	checkout atoms.Button
	create   atoms.Button
	del      atoms.Button
	// New branch form.
	creating   bool
	name       atoms.TextField
	doCheckout atoms.Checkbox
	confirm    atoms.Button
	cancel     atoms.Button
	nameErr    string

	q queue
}

// NewBranchList returns an empty branch list.
func NewBranchList() *BranchList {
	b := &BranchList{selected: -1, search: molecules.NewSearchField("Search branches")}
	b.checkout = atoms.Button{Kind: atoms.Secondary, Text: "Checkout"}
	b.create = atoms.Button{Kind: atoms.Ghost, Text: "New branch", Glyph: atoms.GlyphAdd}
	b.del = atoms.Button{Kind: atoms.Ghost, Text: "Delete", Glyph: atoms.GlyphDelete}
	b.name.Hint = "new-branch-name"
	b.name.Editor.SingleLine, b.name.Editor.Submit = true, true
	b.doCheckout = atoms.Checkbox{Label: "Check out", State: atoms.Checked}
	b.confirm = atoms.Button{Kind: atoms.Primary, Text: "Create"}
	b.cancel = atoms.Button{Kind: atoms.Ghost, Text: "Cancel"}
	return b
}

// SetBranches replaces the branches; the selection is kept by name.
func (b *BranchList) SetBranches(br []gitcli.Branch) {
	sel := b.Selected()
	b.branches = append(b.branches[:0], br...)
	b.rows = make([]molecules.ListRow, len(b.branches))
	b.selected = -1
	for i := range b.branches {
		if sel != nil && b.branches[i].Ref == sel.Ref {
			b.selected = i
		}
	}
	b.filter()
}

// Selected returns the selected branch or nil.
func (b *BranchList) Selected() *gitcli.Branch {
	if b.selected < 0 || b.selected >= len(b.branches) {
		return nil
	}
	return &b.branches[b.selected]
}

// SetQuery sets the search text.
func (b *BranchList) SetQuery(q string) {
	b.search.SetText(q)
	b.filter()
}

// Visible returns the names of the branches matching the search, local
// ones first.
func (b *BranchList) Visible() []string {
	out := make([]string, 0, len(b.view))
	for _, i := range b.view {
		if i >= 0 {
			out = append(out, b.branches[i].Name)
		}
	}
	return out
}

// filter rebuilds the visible rows: -1 and -2 are the "Local" and
// "Remote" section headers.
func (b *BranchList) filter() {
	q := strings.ToLower(strings.TrimSpace(b.search.Text()))
	b.view = b.view[:0]
	for pass, remote := range []bool{false, true} {
		head := len(b.view)
		b.view = append(b.view, -1-pass)
		n := 0
		for i := range b.branches {
			br := &b.branches[i]
			if br.Remote != remote || (q != "" && !strings.Contains(strings.ToLower(br.Name), q)) {
				continue
			}
			b.view = append(b.view, i)
			n++
		}
		b.headers[pass] = n
		if n == 0 {
			b.view = b.view[:head]
		}
	}
}

// Update returns the events since the last call.
func (b *BranchList) Update(gtx layout.Context) []Event {
	b.update(gtx)
	return b.q.drain()
}

func (b *BranchList) emit(gtx layout.Context, e Event) {
	b.q.emit(e)
	gtx.Execute(op.InvalidateCmd{})
}

// validBranchName performs a light check of git's ref name rules.
func validBranchName(n string) error {
	switch {
	case n == "":
		return fmt.Errorf("enter a name")
	case strings.HasPrefix(n, "-") || strings.HasPrefix(n, "/") || strings.HasSuffix(n, "/") ||
		strings.HasSuffix(n, ".") || strings.HasSuffix(n, ".lock"):
		return fmt.Errorf("invalid start or end")
	case strings.Contains(n, "..") || strings.Contains(n, "//") || strings.Contains(n, "@{"):
		return fmt.Errorf("must not contain \"..\", \"//\" or \"@{\"")
	case strings.ContainsAny(n, " ~^:?*[\\\t"):
		return fmt.Errorf("must not contain spaces or ~^:?*[\\")
	}
	return nil
}

func (b *BranchList) update(gtx layout.Context) {
	if changed, _ := b.search.Update(gtx); changed {
		b.filter()
	}
	sel := b.Selected()
	b.checkout.Disabled = sel == nil || sel.Current
	b.del.Disabled = sel == nil || sel.Current || sel.Remote
	if b.checkout.Clicked(gtx) && sel != nil {
		b.emit(gtx, CheckoutRequested{Branch: sel.Name, Remote: sel.Remote})
	}
	if b.del.Clicked(gtx) && sel != nil {
		b.emit(gtx, DeleteBranchRequested{Name: sel.Name})
	}
	if b.create.Clicked(gtx) {
		b.creating = !b.creating
		b.nameErr = ""
		if b.creating {
			gtx.Execute(key.FocusCmd{Tag: &b.name.Editor})
		}
	}
	if !b.creating {
		return
	}
	b.doCheckout.Update(gtx)
	submit := false
	for {
		e, ok := b.name.Editor.Update(gtx)
		if !ok {
			break
		}
		switch e.(type) {
		case widget.SubmitEvent:
			submit = true
		case widget.ChangeEvent:
			b.nameErr = ""
		}
	}
	if b.cancel.Clicked(gtx) {
		b.creating = false
		return
	}
	if b.confirm.Clicked(gtx) || submit {
		name := strings.TrimSpace(b.name.Text())
		if err := validBranchName(name); err != nil {
			b.nameErr = err.Error()
			return
		}
		start := ""
		if sel != nil {
			start = sel.Name
		}
		b.emit(gtx, CreateBranchRequested{Name: name, Start: start, Checkout: b.doCheckout.Checked()})
		b.creating = false
		b.name.SetText("")
	}
}

// Layout draws the list filling the maximum constraints.
func (b *BranchList) Layout(gtx layout.Context, th *tokens.Theme) layout.Dimensions {
	b.update(gtx)
	gtx.Constraints.Min = gtx.Constraints.Max
	return atoms.Surface{Kind: atoms.SurfacePanel}.Fill(gtx, th, func(gtx layout.Context) layout.Dimensions {
		return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return layout.UniformInset(th.Space.S).Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					return b.search.Layout(gtx, th)
				})
			}),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				if !b.creating {
					return layout.Dimensions{}
				}
				return b.layoutForm(gtx, th)
			}),
			layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
				if len(b.view) == 0 {
					msg := "No branches"
					if b.search.Text() != "" {
						msg = "No branch matches"
					}
					return layout.UniformInset(th.Space.L).Layout(gtx, atoms.Muted(msg).Fn(th))
				}
				return b.list.Layout(gtx, th, len(b.view), func(gtx layout.Context, i int) layout.Dimensions {
					return b.layoutRow(gtx, th, b.view[i])
				})
			}),
			layout.Rigid(atoms.Divider{}.Fn(th)),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return layout.UniformInset(th.Space.S).Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
						layout.Rigid(func(gtx layout.Context) layout.Dimensions { return b.checkout.Layout(gtx, th) }),
						layout.Rigid(layout.Spacer{Width: th.Space.S}.Layout),
						layout.Rigid(func(gtx layout.Context) layout.Dimensions { return b.create.Layout(gtx, th) }),
						layout.Flexed(1, layout.Spacer{}.Layout),
						layout.Rigid(func(gtx layout.Context) layout.Dimensions { return b.del.Layout(gtx, th) }),
					)
				})
			}),
		)
	})
}

func (b *BranchList) layoutForm(gtx layout.Context, th *tokens.Theme) layout.Dimensions {
	start := "HEAD"
	if sel := b.Selected(); sel != nil {
		start = sel.Name
	}
	return layout.Inset{Left: th.Space.S, Right: th.Space.S, Bottom: th.Space.S}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		return atoms.Surface{Kind: atoms.SurfaceAlt, Border: true, Rounded: true}.Layout(gtx, th, func(gtx layout.Context) layout.Dimensions {
			return layout.UniformInset(th.Space.S).Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
					layout.Rigid(atoms.Small("New branch from "+start).Fn(th)),
					layout.Rigid(layout.Spacer{Height: th.Space.XS}.Layout),
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						b.name.Invalid = b.nameErr != ""
						return b.name.Layout(gtx, th)
					}),
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						if b.nameErr == "" {
							return layout.Dimensions{}
						}
						return layout.Inset{Top: th.Space.XXS}.Layout(gtx,
							atoms.Label{Text: b.nameErr, Role: atoms.RoleSmall, Color: th.Color.Danger}.Fn(th))
					}),
					layout.Rigid(layout.Spacer{Height: th.Space.S}.Layout),
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
							layout.Rigid(func(gtx layout.Context) layout.Dimensions { return b.doCheckout.Layout(gtx, th) }),
							layout.Flexed(1, layout.Spacer{}.Layout),
							layout.Rigid(func(gtx layout.Context) layout.Dimensions { return b.cancel.Layout(gtx, th) }),
							layout.Rigid(layout.Spacer{Width: th.Space.XS}.Layout),
							layout.Rigid(func(gtx layout.Context) layout.Dimensions { return b.confirm.Layout(gtx, th) }),
						)
					}),
				)
			})
		})
	})
}

func (b *BranchList) layoutRow(gtx layout.Context, th *tokens.Theme, i int) layout.Dimensions {
	if i < 0 {
		title := "Local"
		if i == -2 {
			title = "Remote"
		}
		return layout.Inset{Left: th.Space.M, Top: th.Space.S, Bottom: th.Space.XS}.Layout(gtx,
			atoms.Small(strings.ToUpper(title)).Fn(th))
	}
	br := &b.branches[i]
	row := &b.rows[i]
	if clicked, double := row.Update(gtx); clicked {
		b.selected = i
		if double && !br.Current {
			b.emit(gtx, CheckoutRequested{Branch: br.Name, Remote: br.Remote})
		}
		gtx.Execute(op.InvalidateCmd{})
	}
	row.Selected = i == b.selected
	glyph, gcol := atoms.GlyphBranch, th.Color.TextMuted
	if br.Remote {
		glyph = atoms.GlyphCloud
	}
	if br.Current {
		gcol = th.Color.Accent
	}
	return row.Layout(gtx, th, func(gtx layout.Context) layout.Dimensions {
		gtx.Constraints.Min.X = gtx.Constraints.Max.X
		children := []layout.FlexChild{
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return layout.Inset{Right: th.Space.S}.Layout(gtx, atoms.Icon{Glyph: glyph, Color: gcol}.Fn(th))
			}),
			layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
				l := atoms.Text(br.Name).SingleLine()
				if br.Current {
					l = atoms.Strong(br.Name).SingleLine()
				}
				return l.Layout(gtx, th)
			}),
		}
		badge := func(bd atoms.Badge) layout.FlexChild {
			return layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return layout.Inset{Left: th.Space.XS}.Layout(gtx, bd.Fn(th))
			})
		}
		if br.Current {
			children = append(children, badge(atoms.Badge{Text: "current", Kind: atoms.BadgeAccent}))
		}
		if br.Ahead > 0 {
			children = append(children, badge(atoms.Badge{Text: fmt.Sprintf("↑%d", br.Ahead), Kind: atoms.BadgeSuccess}))
		}
		if br.Behind > 0 {
			children = append(children, badge(atoms.Badge{Text: fmt.Sprintf("↓%d", br.Behind), Kind: atoms.BadgeWarning}))
		}
		if br.UpstreamGone {
			children = append(children, badge(atoms.Badge{Text: "gone", Kind: atoms.BadgeDanger}))
		}
		return layout.Flex{Alignment: layout.Middle}.Layout(gtx, children...)
	})
}
