package diff

import (
	"slices"
	"time"

	"gioui.org/layout"

	"github.com/ape-dev-de/tiegel/internal/ui/editorview"
	"github.com/ape-dev-de/tiegel/internal/ui/molecules"
	"github.com/ape-dev-de/tiegel/internal/ui/tokens"
)

// BlameCommit describes a commit shown in the annotation column.
type BlameCommit struct {
	Hash    string
	Author  string
	Date    time.Time
	Summary string
	// Uncommitted marks lines that are not committed yet.
	Uncommitted bool
}

// BlameGutter is the annotation column ("blame") for an editorview.Editor:
// date and author of the commit that last changed each line, with an age
// bar and tint that fade from the newest to the oldest commit. Install it
// with Editor.SetGutterColumn; clicks into the column arrive as
// editorview.GutterClickEvent and are translated by HandleEditorEvent.
//
// Data in: SetBlame. Events out (via HandleEditorEvent): CommitClicked.
type BlameGutter struct {
	// DateFormat is the time layout (default "2006-01-02").
	DateFormat string
	// AuthorChars is the maximum author length shown (default 14).
	AuthorChars int

	commits []BlameCommit
	age     []float32 // per commit: 0 newest … 1 oldest
	lines   []int     // per document line: commit index (-1 none)
}

// NewBlameGutter returns an empty annotation column.
func NewBlameGutter() *BlameGutter { return &BlameGutter{} }

// SetBlame sets the annotations: commits and, per 0-based document line,
// the hash of its commit (unknown hashes and missing lines stay empty).
func (g *BlameGutter) SetBlame(commits []BlameCommit, lineHashes []string) {
	g.commits = slices.Clone(commits)
	idx := make(map[string]int, len(commits))
	for i, c := range g.commits {
		idx[c.Hash] = i
	}
	g.lines = make([]int, len(lineHashes))
	for i, h := range lineHashes {
		g.lines[i] = -1
		if c, ok := idx[h]; ok {
			g.lines[i] = c
		}
	}
	// Age rank of the committed commits, newest first.
	var order []int
	for i, c := range g.commits {
		if !c.Uncommitted {
			order = append(order, i)
		}
	}
	slices.SortStableFunc(order, func(a, b int) int {
		return g.commits[b].Date.Compare(g.commits[a].Date)
	})
	g.age = make([]float32, len(g.commits))
	for rank, i := range order {
		if len(order) > 1 {
			g.age[i] = float32(rank) / float32(len(order)-1)
		}
	}
}

// CommitAt returns the commit of a 0-based line.
func (g *BlameGutter) CommitAt(line int) (BlameCommit, bool) {
	if line < 0 || line >= len(g.lines) || g.lines[line] < 0 {
		return BlameCommit{}, false
	}
	return g.commits[g.lines[line]], true
}

// HandleEditorEvent translates a click into the annotation column of the
// editor into CommitClicked (uncommitted lines are ignored).
func (g *BlameGutter) HandleEditorEvent(ev editorview.Event) (Event, bool) {
	gc, ok := ev.(editorview.GutterClickEvent)
	if !ok || gc.Area != editorview.GutterAnnotation || gc.Secondary {
		return nil, false
	}
	c, ok := g.CommitAt(gc.Line)
	if !ok || c.Uncommitted {
		return nil, false
	}
	return CommitClicked{Hash: c.Hash, Line: gc.Line}, true
}

// GutterWidth implements editorview.GutterColumn.
func (g *BlameGutter) GutterWidth(gtx layout.Context, th *tokens.Theme) int {
	chars := g.authorChars() + 12
	return gtx.Sp(th.Text.Small)*55*chars/100 + gtx.Dp(th.Space.M)
}

func (g *BlameGutter) authorChars() int {
	if g.AuthorChars > 0 {
		return g.AuthorChars
	}
	return 14
}

// Text returns the annotation of a line ("" when it continues the
// previous line's commit or has none).
func (g *BlameGutter) Text(line int) string {
	c, ok := g.CommitAt(line)
	if !ok {
		return ""
	}
	if line > 0 && g.lines[line-1] == g.lines[line] {
		return ""
	}
	if c.Uncommitted {
		return "Not committed yet"
	}
	format := g.DateFormat
	if format == "" {
		format = "2006-01-02"
	}
	author := []rune(c.Author)
	if n := g.authorChars(); len(author) > n {
		author = append(author[:n-1], '…')
	}
	return c.Date.Local().Format(format) + "  " + string(author)
}

// LayoutGutterLine implements editorview.GutterColumn.
func (g *BlameGutter) LayoutGutterLine(gtx layout.Context, th *tokens.Theme, line int, active bool) {
	c, ok := g.CommitAt(line)
	if !ok {
		return
	}
	cell := molecules.AnnotationCell{Text: g.Text(line), Muted: !active}
	if c.Uncommitted {
		cell.Bar = th.Git.Modified
	} else {
		a := g.age[g.lines[line]]
		cell.Bar = tokens.Mix(th.Color.Accent, th.Color.Border, a)
		fresh := (1 - a) * (1 - a)
		cell.Tint = tokens.WithAlpha(th.Color.Accent, uint8(0x30*fresh))
	}
	cell.Layout(gtx, th)
}
