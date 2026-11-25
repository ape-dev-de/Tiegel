package gui

import (
	"fmt"
	"path"
	"strings"
	"time"

	"gioui.org/io/key"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/text"

	"github.com/ape-dev-de/tiegel/internal/api"
	"github.com/ape-dev-de/tiegel/internal/app/gui/controller"
	"github.com/ape-dev-de/tiegel/internal/ui/atoms"
	"github.com/ape-dev-de/tiegel/internal/ui/molecules"
	"github.com/ape-dev-de/tiegel/internal/ui/tokens"
)

// Live search of the Find in Files dialog (D537): the dialog searches
// while typing (after a short pause) and lists the matches below the
// options with a code preview of the selected one. Up/Down select, Enter
// opens the selected match, "Open in Find Window" shows all matches in
// the Find tool window. The search is the normal search.inProject stream
// of CodeNav, so the Find window shows the same results.

// liveDelay is the pause after the last change before the live search
// starts.
const liveDelay = 200 * time.Millisecond

// liveMaxRows caps the matches listed in the dialog (the Find window
// shows all).
const liveMaxRows = 500

// liveMatch is one row of the live result list.
type liveMatch struct {
	path, file string
	open       bool
	m          api.SearchMatch
}

// liveState is the live search part of findDock.
type liveState struct {
	// key are the dialog options of the last change; changed is when
	// they changed, pending until the search started.
	key     string
	changed time.Time
	pending bool
	// st is the search started by the dialog (nil: none yet).
	st *controller.FindState

	rows     []liveMatch
	rowsSeen int // files of st already turned into rows
	selected int
	reveal   bool
	list     molecules.ScrollList
	listRows []molecules.ListRow
}

// liveKey identifies the search options (without replacement).
func liveKey(p controller.FindParams) string {
	return fmt.Sprintf("%q|%v|%v|%v|%q|%q|%q", p.Pattern, p.Regex, p.CaseSensitive, p.WholeWord, p.Mask, p.Scope, p.Dir)
}

// liveReset starts over when the dialog opens: a prefilled pattern
// searches at once.
func (f *findDock) liveReset() {
	f.live.key, f.live.pending, f.live.changed = "", false, time.Time{}
	f.live.st, f.live.rows, f.live.rowsSeen, f.live.selected = nil, nil, 0, 0
}

// liveUpdate notes changed options and starts the search after the
// pause.
func (f *findDock) liveUpdate(gtx layout.Context) {
	p := f.params()
	p.Replace, p.Replacement = false, ""
	l := &f.live
	if k := liveKey(p); k != l.key {
		first := l.key == ""
		l.key, l.pending = k, true
		l.changed = gtx.Now
		if first {
			l.changed = gtx.Now.Add(-liveDelay)
		}
	}
	if l.pending {
		if p.Pattern == "" {
			l.pending = false
			if l.st != nil {
				f.d.n.CancelFind()
			}
			l.st, l.rows, l.rowsSeen, l.selected = nil, nil, 0, 0
		} else if due := l.changed.Add(liveDelay); !gtx.Now.Before(due) {
			l.pending = false
			f.d.n.StartFind(p)
			l.st, l.rows, l.rowsSeen, l.selected = f.d.n.Find, nil, 0, 0
			l.list.ScrollTo(0)
		} else {
			gtx.Execute(op.InvalidateCmd{At: due})
		}
	}
	// Rows of the newly arrived files.
	if st := l.st; st != nil && st == f.d.n.Find {
		for _, fm := range st.Files[l.rowsSeen:] {
			for _, m := range fm.Matches {
				if len(l.rows) >= liveMaxRows {
					break
				}
				l.rows = append(l.rows, liveMatch{path: fm.Path, file: fm.File, open: fm.Open, m: m})
			}
		}
		l.rowsSeen = len(st.Files)
	}
}

// liveKeys moves the selection with Up/Down/PageUp/PageDown while the
// pattern field has the focus (before the field sees the keys).
func (f *findDock) liveKeys(gtx layout.Context) {
	l := &f.live
	for {
		ev, ok := gtx.Event(
			key.Filter{Focus: &f.pattern.Editor, Name: key.NameUpArrow},
			key.Filter{Focus: &f.pattern.Editor, Name: key.NameDownArrow},
			key.Filter{Focus: &f.pattern.Editor, Name: key.NamePageUp},
			key.Filter{Focus: &f.pattern.Editor, Name: key.NamePageDown},
		)
		if !ok {
			return
		}
		e, ok := ev.(key.Event)
		if !ok || e.State != key.Press || len(l.rows) == 0 {
			continue
		}
		step := 1
		switch e.Name {
		case key.NameUpArrow:
			step = -1
		case key.NamePageUp:
			step = -10
		case key.NamePageDown:
			step = 10
		}
		l.selected = min(max(l.selected+step, 0), len(l.rows)-1)
		l.reveal = true
	}
}

// openSelected opens the selected match in the editor; it reports
// whether there was one.
func (f *findDock) openSelected() bool {
	l := &f.live
	if l.selected < 0 || l.selected >= len(l.rows) {
		return false
	}
	r := l.rows[l.selected]
	v := f.d.v
	file := r.file
	if file == "" {
		file = v.ctl.Abs(r.path)
	}
	v.ClosePopup()
	v.ctl.GoTo(v.currentLocation(), controller.At(file, r.m.Start))
	v.focusEditor = true
	return true
}

// liveStatus is the line above the result list.
func (f *findDock) liveStatus() string {
	l := &f.live
	st := l.st
	switch {
	case l.pending && f.pattern.Text() != "":
		return "…"
	case st == nil:
		return "Type to search"
	case st.Err != "":
		return firstLine(st.Err)
	}
	s := fmt.Sprintf("%s in %s", plural(st.Matches, "match"), plural(len(st.Files), "file"))
	if len(l.rows) < st.Matches {
		s += fmt.Sprintf(" (first %d shown)", len(l.rows))
	}
	switch {
	case st.Running:
		s += " – searching…"
	case st.Summary != nil && st.Summary.Limited:
		s += " – limit reached"
	}
	return s
}

// layoutLive draws the status line, the result list and the preview.
func (f *findDock) layoutLive(gtx layout.Context, th *tokens.Theme) layout.Dimensions {
	return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Inset{Bottom: th.Space.XS}.Layout(gtx, atoms.Small(f.liveStatus()).Fn(th))
		}),
		layout.Flexed(0.5, func(gtx layout.Context) layout.Dimensions {
			return atoms.Surface{Kind: atoms.SurfaceEditor, Border: true}.Layout(gtx, th, f.layoutLiveList(th))
		}),
		layout.Rigid(layout.Spacer{Height: th.Space.S}.Layout),
		layout.Flexed(0.5, func(gtx layout.Context) layout.Dimensions {
			return atoms.Surface{Kind: atoms.SurfaceEditor, Border: true}.Layout(gtx, th, f.layoutLivePreview(th))
		}),
	)
}

func (f *findDock) layoutLiveList(th *tokens.Theme) layout.Widget {
	return func(gtx layout.Context) layout.Dimensions {
		l := &f.live
		gtx.Constraints.Min = gtx.Constraints.Max
		if len(l.listRows) < len(l.rows) {
			l.listRows = append(l.listRows, make([]molecules.ListRow, len(l.rows)-len(l.listRows))...)
		}
		for i := range l.rows {
			if c, double := l.listRows[i].Update(gtx); c {
				l.selected = i
				if double {
					f.d.v.Post(func() { f.openSelected() })
				}
			}
		}
		if l.reveal {
			l.reveal = false
			if l.selected < l.list.FirstVisible() || l.selected > l.list.LastVisible() {
				l.list.ScrollTo(l.selected - 3)
			}
		}
		if len(l.rows) == 0 {
			return layout.Dimensions{Size: gtx.Constraints.Max}
		}
		return l.list.Layout(gtx, th, len(l.rows), func(gtx layout.Context, i int) layout.Dimensions {
			r := l.rows[i]
			row := &l.listRows[i]
			row.Selected = i == l.selected
			return row.Layout(gtx, th, func(gtx layout.Context) layout.Dimensions {
				return liveRow(gtx, th, r)
			})
		})
	}
}

// liveRow draws a match: the line with the match emphasized, then the
// file name and line number.
func liveRow(gtx layout.Context, th *tokens.Theme, r liveMatch) layout.Dimensions {
	before, match, after := splitMatch(r.m)
	where := fmt.Sprintf("%s %d", path.Base(r.path), r.m.Line)
	return layout.Flex{Alignment: layout.Baseline}.Layout(gtx,
		layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
			gtx.Constraints.Min.X = 0
			return layout.Flex{Alignment: layout.Baseline}.Layout(gtx,
				layout.Rigid(atoms.Label{Text: before, Role: atoms.RoleCode, MaxLines: 1}.Fn(th)),
				layout.Rigid(atoms.Label{Text: match, Role: atoms.RoleCode, Color: th.Color.Accent, MaxLines: 1}.Fn(th)),
				layout.Flexed(1, atoms.Label{Text: after, Role: atoms.RoleCode, MaxLines: 1}.Fn(th)),
			)
		}),
		layout.Rigid(layout.Spacer{Width: th.Space.M}.Layout),
		layout.Rigid(atoms.Label{Text: where, Role: atoms.RoleSmall, MaxLines: 1, Alignment: text.End}.Fn(th)),
	)
}

// splitMatch cuts the line of m into the text before the match (at most
// 40 characters), the match and the rest; leading blanks are dropped.
func splitMatch(m api.SearchMatch) (before, match, after string) {
	lt := m.LineText
	// Col is 1-based in runes of the line; a cut-down long line starts
	// later (PreviewStart), then the match is looked up.
	s := -1
	if m.PreviewStart == 0 {
		if r := []rune(lt); m.Col-1 <= len(r) && m.Col >= 1 {
			s = len(string(r[:m.Col-1]))
		}
	} else {
		s = strings.Index(lt, m.Text)
	}
	e := s + len(m.Text)
	if s < 0 || e > len(lt) || !strings.HasPrefix(lt[s:], m.Text) {
		return strings.TrimSpace(lt), "", ""
	}
	before, match, after = strings.TrimLeft(lt[:s], " \t"), lt[s:e], lt[e:]
	if r := []rune(before); len(r) > 40 {
		before = "…" + string(r[len(r)-40:])
	}
	return before, match, after
}

func (f *findDock) layoutLivePreview(th *tokens.Theme) layout.Widget {
	return func(gtx layout.Context) layout.Dimensions {
		gtx.Constraints.Min = gtx.Constraints.Max
		l := &f.live
		if l.selected < 0 || l.selected >= len(l.rows) {
			return layout.Dimensions{Size: gtx.Constraints.Max}
		}
		r := l.rows[l.selected]
		t := f.d.n.FindText(r.path, r.open)
		if t.Loading || t.Err != "" {
			msg := "Loading…"
			if t.Err != "" {
				msg = t.Err
			}
			return layout.UniformInset(th.Space.S).Layout(gtx, atoms.Muted(msg).Fn(th))
		}
		return layoutPreviewLines(gtx, th, t.Text, r.m)
	}
}

// layoutPreviewLines shows the lines around the match, the match line
// tinted and the match emphasized.
func layoutPreviewLines(gtx layout.Context, th *tokens.Theme, src string, m api.SearchMatch) layout.Dimensions {
	size := gtx.Constraints.Max
	lineH := max(1, gtx.Sp(th.Text.Code)*3/2)
	n := max(1, size.Y/lineH)
	first := max(1, m.Line-n/2)
	lines := strings.Split(src, "\n")
	// Byte offset of the first shown line.
	off := 0
	for i := 0; i < first-1 && i < len(lines); i++ {
		off += len(lines[i]) + 1
	}
	// One digit of slack: the label truncates when it is exactly full.
	digits := len(fmt.Sprint(min(len(lines), first+n))) + 1
	var children []layout.FlexChild
	for no := first; no < first+n && no <= len(lines); no++ {
		text := strings.TrimRight(lines[no-1], "\r")
		cl := molecules.CodeLine{No: no, Columns: 1, Digits: digits, Text: strings.ReplaceAll(text, "\t", "    ")}
		if no == m.Line {
			cl.Tone = molecules.ToneChanged
			if s, e := m.Start-off, m.End-off; s >= 0 && e <= len(text) && s < e {
				// Tabs before the match widen it by 3 each.
				pad := 3 * strings.Count(text[:s], "\t")
				cl.Spans = []molecules.Span{{Start: s + pad, End: e + pad + 3*strings.Count(text[s:e], "\t")}}
			}
		}
		off += len(lines[no-1]) + 1
		children = append(children, layout.Rigid(cl.Fn(th)))
	}
	defer clip.Rect{Max: size}.Push(gtx.Ops).Pop()
	layout.Flex{Axis: layout.Vertical}.Layout(gtx, children...)
	return layout.Dimensions{Size: size}
}
