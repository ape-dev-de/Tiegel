package molecules

import (
	"image"
	"image/color"

	"gioui.org/io/pointer"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/widget"

	"github.com/ape-dev-de/tiegel/internal/ui/atoms"
	"github.com/ape-dev-de/tiegel/internal/ui/internal/draw"
	"github.com/ape-dev-de/tiegel/internal/ui/tokens"
)

// FileStatus is the VCS state of a file, used to color its name.
type FileStatus int

// File states.
const (
	StatusNone FileStatus = iota
	StatusModified
	StatusAdded
	StatusDeleted
	StatusUntracked
	StatusIgnored
	StatusConflict
)

// FileStatuses lists all states (for the catalog and tests).
var FileStatuses = []FileStatus{StatusNone, StatusModified, StatusAdded, StatusDeleted,
	StatusUntracked, StatusIgnored, StatusConflict}

// String returns the status name.
func (s FileStatus) String() string {
	return [...]string{"none", "modified", "added", "deleted", "untracked", "ignored", "conflict"}[s]
}

// Color returns the name color for status s.
func (s FileStatus) Color(th *tokens.Theme) color.NRGBA {
	g := th.Git
	switch s {
	case StatusModified:
		return g.FileModified
	case StatusAdded:
		return g.FileAdded
	case StatusDeleted:
		return g.FileDeleted
	case StatusUntracked:
		return g.FileUntracked
	case StatusIgnored:
		return g.FileIgnored
	case StatusConflict:
		return g.FileConflict
	}
	return th.Color.Text
}

// FileRow is one row of a file tree or file list: indentation, expand
// arrow (directories), icon, name colored by VCS status and an optional
// muted detail text.
type FileRow struct {
	// count counts clicks for double clicks (atoms.DoubleClickInterval).
	count atoms.ClickCounter
	Name  string
	// Detail is shown muted after the name (e.g. a relative directory).
	Detail   string
	Depth    int
	Dir      bool
	Expanded bool
	Status   FileStatus
	Selected bool
	// Glyph overrides the default file/folder icon.
	Glyph *atoms.Glyph

	row    widget.Clickable
	toggle widget.Clickable
	double bool
}

// DoubleClicked reports whether the last Update saw a double click on the
// row (files: open them).
func (r *FileRow) DoubleClicked() bool { return r.double }

// Update processes input. clicked reports a click on the row (selection);
// toggled reports that a directory's arrow was clicked or the row was
// double-clicked, in which case Expanded has already been flipped.
func (r *FileRow) Update(gtx layout.Context) (clicked, toggled bool) {
	r.count.Update(gtx)
	r.double = false
	for {
		if _, ok := r.toggle.Update(gtx); !ok {
			break
		}
		r.count.Next() // the arrow's press is no row click
		if r.Dir {
			r.Expanded = !r.Expanded
			toggled = true
		}
	}
	for {
		_, ok := r.row.Update(gtx)
		if !ok {
			break
		}
		clicked = true
		n := r.count.Next()
		if n >= 2 {
			r.double = true
		}
		if n >= 2 && r.Dir {
			r.Expanded = !r.Expanded
			toggled = true
		}
	}
	return clicked, toggled
}

// Layout draws the row across the full available width.
func (r *FileRow) Layout(gtx layout.Context, th *tokens.Theme) layout.Dimensions {
	return r.count.Layout(gtx, func(gtx layout.Context) layout.Dimensions { return r.layoutRow(gtx, th) })
}

func (r *FileRow) layoutRow(gtx layout.Context, th *tokens.Theme) layout.Dimensions {
	r.Update(gtx)
	return r.row.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		h := gtx.Dp(th.Size.RowH)
		wide := Wide(gtx)
		w := draw.Width(gtx, gtx.Dp(240))
		if wide {
			w = gtx.Constraints.Min.X
		}

		indent := gtx.Dp(th.Space.XS) + r.Depth*gtx.Dp(th.Size.IndentStep)
		padR := gtx.Dp(th.Space.S)
		cgtx := gtx
		cgtx.Constraints = layout.Constraints{Max: image.Pt(max(0, w-indent-padR), h)}
		// text holds the name and the detail; it fills the row unless the
		// row takes its natural width (horizontal scrolling).
		text := layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
			gtx.Constraints.Min.X = 0
			return layout.Flex{Alignment: layout.Baseline}.Layout(gtx,
				layout.Rigid(atoms.Label{Text: r.Name, Color: r.Status.Color(th), MaxLines: 1}.Fn(th)),
				layout.Flexed(1, r.layoutDetail(th)),
			)
		})
		if wide {
			cgtx.Constraints.Max.X = draw.Unbounded
			text = layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return layout.Flex{Alignment: layout.Baseline}.Layout(gtx,
					layout.Rigid(atoms.Label{Text: r.Name, Color: r.Status.Color(th), MaxLines: 1}.Fn(th)),
					layout.Rigid(r.layoutDetail(th)),
				)
			})
		}
		m := op.Record(gtx.Ops)
		d := layout.Flex{Alignment: layout.Middle}.Layout(cgtx,
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return r.layoutArrow(gtx, th)
			}),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return layout.Inset{Left: th.Space.XXS, Right: th.Space.XS + th.Space.XXS}.Layout(gtx,
					atoms.Icon{Glyph: r.glyph(), Color: r.iconColor(th)}.Fn(th))
			}),
			text,
		)
		call := m.Stop()
		if wide {
			w = max(w, indent+d.Size.X+padR)
		}
		size := image.Pt(w, h)
		switch {
		case r.Selected:
			draw.Rect(gtx.Ops, image.Rectangle{Max: size}, th.Color.Selection)
		case r.row.Hovered():
			draw.Rect(gtx.Ops, image.Rectangle{Max: size}, th.Color.SurfaceHover)
		}
		t := op.Offset(image.Pt(indent, (h-d.Size.Y)/2)).Push(gtx.Ops)
		call.Add(gtx.Ops)
		t.Pop()
		return layout.Dimensions{Size: size}
	})
}

// layoutDetail draws the muted detail after the name.
func (r *FileRow) layoutDetail(th *tokens.Theme) layout.Widget {
	return func(gtx layout.Context) layout.Dimensions {
		if r.Detail == "" {
			return layout.Dimensions{}
		}
		gtx.Constraints.Min.X = 0
		return layout.Inset{Left: th.Space.S}.Layout(gtx, atoms.Small(r.Detail).SingleLine().Fn(th))
	}
}

func (r *FileRow) layoutArrow(gtx layout.Context, th *tokens.Theme) layout.Dimensions {
	if !r.Dir {
		return layout.Spacer{Width: th.Size.Icon}.Layout(gtx)
	}
	return r.toggle.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		g := atoms.GlyphChevronRight
		if r.Expanded {
			g = atoms.GlyphChevronDown
		}
		d := atoms.Icon{Glyph: g, Color: th.Color.TextMuted}.Layout(gtx, th)
		pointer.CursorPointer.Add(gtx.Ops)
		return d
	})
}

func (r *FileRow) glyph() *atoms.Glyph {
	switch {
	case r.Glyph != nil:
		return r.Glyph
	case r.Dir && r.Expanded:
		return atoms.GlyphFolderOpen
	case r.Dir:
		return atoms.GlyphFolder
	}
	return atoms.GlyphFile
}

func (r *FileRow) iconColor(th *tokens.Theme) color.NRGBA {
	if r.Dir {
		return tokens.Mix(th.Color.TextMuted, th.Color.Accent, 0.45)
	}
	return th.Color.TextMuted
}
