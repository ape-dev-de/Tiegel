package git

import (
	"strings"

	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/unit"

	"github.com/ape-dev-de/tiegel/internal/gitcli"
	"github.com/ape-dev-de/tiegel/internal/ui/atoms"
	"github.com/ape-dev-de/tiegel/internal/ui/molecules"
	"github.com/ape-dev-de/tiegel/internal/ui/tokens"
)

// logPrefetch is how many rows before the end a page request is sent.
const logPrefetch = 20

// Column widths of the log.
const (
	logHashW   = unit.Dp(72)
	logAuthorW = unit.Dp(140)
	logDateW   = unit.Dp(124)
)

// LogView is the commit history: a virtualized list with short hash, ref
// badges, subject, author and date. Selecting a commit emits
// CommitSelected; scrolling near the end while more commits are available
// emits LogPageRequested once until the host answers with AppendCommits.
//
// Data in: SetCommits, AppendCommits. Events out: CommitSelected,
// LogPageRequested.
type LogView struct {
	// DateFormat is the time layout of the date column (default
	// "2006-01-02 15:04").
	DateFormat string

	commits  []gitcli.Commit
	rows     []molecules.ListRow
	more     bool
	loading  bool
	selected int
	list     molecules.ScrollList
	q        queue
}

// NewLogView returns an empty log.
func NewLogView() *LogView { return &LogView{selected: -1} }

// SetCommits replaces the list; more tells whether further pages exist.
func (v *LogView) SetCommits(c []gitcli.Commit, more bool) {
	sel := v.SelectedHash()
	v.commits = append(v.commits[:0], c...)
	v.rows = make([]molecules.ListRow, len(v.commits))
	v.more, v.loading = more, false
	v.selected = -1
	for i := range v.commits {
		if v.commits[i].Hash == sel && sel != "" {
			v.selected = i
		}
	}
}

// AppendCommits adds the next page (answer to LogPageRequested).
func (v *LogView) AppendCommits(c []gitcli.Commit, more bool) {
	v.commits = append(v.commits, c...)
	v.rows = append(v.rows, make([]molecules.ListRow, len(c))...)
	v.more, v.loading = more, false
}

// Len returns the number of commits shown.
func (v *LogView) Len() int { return len(v.commits) }

// Loading reports whether a page request is outstanding.
func (v *LogView) Loading() bool { return v.loading }

// SelectedHash returns the hash of the selected commit ("" if none).
func (v *LogView) SelectedHash() string {
	if v.selected < 0 || v.selected >= len(v.commits) {
		return ""
	}
	return v.commits[v.selected].Hash
}

// Update returns the events since the last call.
func (v *LogView) Update(gtx layout.Context) []Event { return v.q.drain() }

func (v *LogView) emit(gtx layout.Context, e Event) {
	v.q.emit(e)
	gtx.Execute(op.InvalidateCmd{})
}

// Layout draws the log filling the maximum constraints.
func (v *LogView) Layout(gtx layout.Context, th *tokens.Theme) layout.Dimensions {
	gtx.Constraints.Min = gtx.Constraints.Max
	return atoms.Surface{Kind: atoms.SurfacePanel}.Fill(gtx, th, func(gtx layout.Context) layout.Dimensions {
		return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
			layout.Rigid(func(gtx layout.Context) layout.Dimensions { return v.layoutHeader(gtx, th) }),
			layout.Rigid(atoms.Divider{}.Fn(th)),
			layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
				n := len(v.commits)
				extra := 0
				if v.more || v.loading {
					extra = 1
				}
				if n == 0 && extra == 0 {
					return layout.UniformInset(th.Space.L).Layout(gtx, atoms.Muted("No commits").Fn(th))
				}
				d := v.list.Layout(gtx, th, n+extra, func(gtx layout.Context, i int) layout.Dimensions {
					if i >= n {
						return layout.Inset{Left: th.Space.M, Top: th.Space.XS, Bottom: th.Space.XS}.Layout(gtx,
							atoms.Small("Loading more commits…").Fn(th))
					}
					return v.layoutRow(gtx, th, i)
				})
				if v.more && !v.loading && v.list.LastVisible() >= n-logPrefetch {
					v.loading = true
					after := ""
					if n > 0 {
						after = v.commits[n-1].Hash
					}
					v.emit(gtx, LogPageRequested{Loaded: n, After: after})
				}
				return d
			}),
		)
	})
}

func (v *LogView) layoutHeader(gtx layout.Context, th *tokens.Theme) layout.Dimensions {
	return atoms.Surface{Kind: atoms.SurfaceAlt}.Fill(gtx, th, func(gtx layout.Context) layout.Dimensions {
		gtx.Constraints.Min.X = gtx.Constraints.Max.X
		return layout.Inset{Left: th.Space.S, Right: th.Space.S, Top: th.Space.XS, Bottom: th.Space.XS}.Layout(gtx,
			func(gtx layout.Context) layout.Dimensions {
				return logColumns(gtx, th,
					atoms.Small("Hash").Fn(th), atoms.Small("Subject").Fn(th),
					atoms.Small("Author").Fn(th), atoms.Small("Date").Fn(th))
			})
	})
}

// logColumns lays out the four log columns with fixed widths except the
// subject.
func logColumns(gtx layout.Context, th *tokens.Theme, hash, subject, author, date layout.Widget) layout.Dimensions {
	cell := func(w unit.Dp, c layout.Widget) layout.FlexChild {
		return layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			px := min(gtx.Dp(w), gtx.Constraints.Max.X)
			gtx.Constraints.Min.X, gtx.Constraints.Max.X = px, px
			d := c(gtx)
			d.Size.X = px
			return d
		})
	}
	return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
		cell(logHashW, hash),
		layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
			return layout.Inset{Right: th.Space.S}.Layout(gtx, subject)
		}),
		cell(logAuthorW, author),
		cell(logDateW, date),
	)
}

func (v *LogView) layoutRow(gtx layout.Context, th *tokens.Theme, i int) layout.Dimensions {
	c := &v.commits[i]
	row := &v.rows[i]
	if clicked, _ := row.Update(gtx); clicked && v.selected != i {
		v.selected = i
		v.emit(gtx, CommitSelected{Hash: c.Hash})
	}
	row.Selected = i == v.selected
	format := v.DateFormat
	if format == "" {
		format = "2006-01-02 15:04"
	}
	date := ""
	if !c.AuthorDate.IsZero() {
		date = c.AuthorDate.Local().Format(format)
	}
	short := c.Hash
	if len(short) > 8 {
		short = short[:8]
	}
	return row.Layout(gtx, th, func(gtx layout.Context) layout.Dimensions {
		gtx.Constraints.Min.X = gtx.Constraints.Max.X
		return logColumns(gtx, th,
			atoms.Label{Text: short, Role: atoms.RoleCode, Color: th.Color.TextMuted, MaxLines: 1}.Fn(th),
			func(gtx layout.Context) layout.Dimensions {
				children := make([]layout.FlexChild, 0, len(c.Refs)+1)
				for _, r := range c.Refs {
					b := refBadge(r)
					children = append(children, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						return layout.Inset{Right: th.Space.XS}.Layout(gtx, b.Fn(th))
					}))
				}
				children = append(children, layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
					l := atoms.Text(c.Subject).SingleLine()
					if c.IsMerge() {
						l = atoms.Muted(c.Subject).SingleLine()
					}
					return l.Layout(gtx, th)
				}))
				return layout.Flex{Alignment: layout.Middle}.Layout(gtx, children...)
			},
			atoms.Muted(c.AuthorName).SingleLine().Fn(th),
			atoms.Small(date).SingleLine().Fn(th),
		)
	})
}

// refBadge turns a decoration ("HEAD -> main", "tag: v1.0",
// "origin/main") into a badge.
func refBadge(ref string) atoms.Badge {
	switch {
	case strings.HasPrefix(ref, "HEAD -> "):
		return atoms.Badge{Text: strings.TrimPrefix(ref, "HEAD -> "), Kind: atoms.BadgeAccent, Solid: true}
	case ref == "HEAD":
		return atoms.Badge{Text: "HEAD", Kind: atoms.BadgeAccent}
	case strings.HasPrefix(ref, "tag: "):
		return atoms.Badge{Text: strings.TrimPrefix(ref, "tag: "), Kind: atoms.BadgeWarning}
	case strings.Contains(ref, "/"):
		return atoms.Badge{Text: ref, Kind: atoms.BadgeInfo}
	}
	return atoms.Badge{Text: ref, Kind: atoms.BadgeSuccess}
}
