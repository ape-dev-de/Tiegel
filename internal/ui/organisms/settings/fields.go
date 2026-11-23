package settings

import (
	"fmt"
	"strings"

	"gioui.org/layout"
	"gioui.org/widget"

	"github.com/ape-dev-de/tiegel/internal/ui/atoms"
	"github.com/ape-dev-de/tiegel/internal/ui/molecules"
	"github.com/ape-dev-de/tiegel/internal/ui/tokens"
)

// fieldState is the widget state of one setting, generated from its kind:
// bool → checkbox, enum (or a string with Choices) → dropdown, int, float,
// string → text field (committed on Enter or focus loss), list →
// multi-line text field with one entry per line (committed on focus loss),
// path → text field with a chooser button. A field with candidates
// (D521) is a dropdown of the discovered values plus "Custom…", which
// switches to the text field ("Choices" switches back).
type fieldState struct {
	check  atoms.Checkbox
	sel    *molecules.Dropdown
	selKey string   // options the dropdown was built from
	values []string // dropdown index → value
	text   *atoms.TextField
	synced string // text last taken from the value (or committed)
	focus  bool
	err    string
	choose atoms.Button
	reset  atoms.IconButton

	// Candidate mode.
	cand     *molecules.Dropdown
	candKey  string
	candVals []any // dropdown index → value; nil entry = "Custom…"
	custom   bool  // free input instead of the candidates
	refresh  atoms.IconButton
	choices  atoms.Button
}

// customLabel is the last entry of a candidate dropdown.
const customLabel = "Custom…"

// candidateMode reports whether f is shown as a candidate dropdown.
func candidateMode(f Field) bool {
	return f.CandidateSource != "" && f.Kind != KindBool && f.Kind != KindEnum && !f.ReadOnly
}

// syncCandidates (re)builds the candidate dropdown: candidates
// (recommended first, as delivered), the current value when it is not
// among them, and "Custom…".
func (st *fieldState) syncCandidates(f Field) {
	cur := FormatValue(f.Kind, f.Value)
	key := cur + "\x01"
	for _, c := range f.Candidates {
		key += FormatValue(f.Kind, c.Value) + "\x00" + c.Label + "\x00" + c.Detail + "\x00"
		if c.Recommended {
			key += "*"
		}
	}
	if f.CandidatesLoading {
		key += "\x02"
	}
	if st.cand != nil && key == st.candKey {
		return
	}
	st.candKey = key
	var opts []molecules.DropdownOption
	st.candVals = st.candVals[:0]
	found := false
	for _, c := range f.Candidates {
		o := molecules.DropdownOption{Label: c.Label, Detail: c.Detail}
		if c.Recommended {
			o.Badge, o.BadgeKind = "recommended", atoms.BadgeAccent
		}
		opts = append(opts, o)
		st.candVals = append(st.candVals, c.Value)
		found = found || FormatValue(f.Kind, c.Value) == cur
	}
	if !found {
		label, detail := cur, "current value"
		if cur == "" {
			label, detail = "Default", "not set"
		}
		if f.Kind == KindList {
			label = strings.ReplaceAll(label, "\n", " + ")
		}
		opts = append(opts, molecules.DropdownOption{Label: label, Detail: detail})
		st.candVals = append(st.candVals, f.Value)
	}
	custom := molecules.DropdownOption{Label: customLabel, Detail: "type a value"}
	if f.CandidatesLoading {
		custom.Detail = "looking for more…"
	}
	opts = append(opts, custom)
	st.candVals = append(st.candVals, nil)
	if st.cand == nil {
		st.cand = molecules.NewSelect(f.Label)
	}
	st.cand.Options = opts
}

// free reports whether the field takes free input: chosen with "Custom…"
// or nothing was found.
func (st *fieldState) free(f Field) bool { return st.custom || len(f.Candidates) == 0 }

// candidateIndex returns the dropdown index of the current value.
func (st *fieldState) candidateIndex(f Field) int {
	cur := FormatValue(f.Kind, f.Value)
	for i, v := range st.candVals {
		if v != nil && FormatValue(f.Kind, v) == cur {
			return i
		}
	}
	return -1
}

// candidateValue converts a candidate value for a SetEvent ([]any of a
// list becomes []string).
func candidateValue(kind string, v any) any {
	if l, ok := v.([]any); ok && kind == KindList {
		out := make([]string, 0, len(l))
		for _, e := range l {
			out = append(out, fmt.Sprint(e))
		}
		return out
	}
	return v
}

func (p *Panel) field(f Field) *fieldState {
	st := p.fields[f.Key]
	if st == nil {
		st = &fieldState{
			choose:  atoms.Button{Text: "Choose…", Kind: atoms.Secondary, Glyph: atoms.GlyphFolderOpen},
			reset:   atoms.IconButton{Glyph: atoms.GlyphHistory, Description: "Reset to the inherited value"},
			refresh: atoms.IconButton{Glyph: atoms.GlyphRefresh, Description: "Look for values again"},
			choices: atoms.Button{Text: "Choices", Kind: atoms.Secondary, Glyph: atoms.GlyphList},
		}
		p.fields[f.Key] = st
	}
	if candidateMode(f) {
		st.syncCandidates(f)
	}
	switch {
	case f.Kind == KindBool:
	case f.Kind == KindEnum || (f.Kind == KindString && len(f.Choices) > 0):
		st.syncSelect(f)
	default:
		if st.text == nil {
			st.text = atoms.NewTextField(placeholder(f))
			st.text.Mono = f.Kind == KindPath || f.Kind == KindList
			if f.Kind == KindList {
				// Enter starts a new entry; the value is committed when
				// the field loses focus.
				st.text.Editor.SingleLine, st.text.Editor.Submit = false, false
			}
		}
	}
	return st
}

func placeholder(f Field) string {
	switch f.Kind {
	case KindList:
		return "one entry per line"
	case KindPath:
		return "path"
	}
	return ""
}

// syncSelect (re)builds the dropdown options when the choices changed.
func (st *fieldState) syncSelect(f Field) {
	vals := f.Options
	if f.Kind == KindString {
		vals = append([]string{""}, f.Choices...)
		cur := FormatValue(f.Kind, f.Value)
		if indexOf(vals, cur) < 0 {
			vals = append(vals, cur)
		}
	}
	key := ""
	for _, v := range vals {
		key += v + "\x00"
	}
	if st.sel != nil && key == st.selKey {
		return
	}
	st.selKey, st.values = key, append([]string(nil), vals...)
	opts := make([]molecules.DropdownOption, len(vals))
	for i, v := range vals {
		opts[i] = molecules.DropdownOption{Label: v}
		if v == "" && f.Kind == KindString {
			opts[i] = molecules.DropdownOption{Label: orDefault(f.ChoiceDefault, "Default"), Detail: "Not set"}
		}
	}
	if st.sel == nil {
		st.sel = molecules.NewSelect(f.Label)
	}
	st.sel.Options = opts
}

func orDefault(s, def string) string {
	if s == "" {
		return def
	}
	return s
}

func indexOf(list []string, v string) int {
	for i, x := range list {
		if x == v {
			return i
		}
	}
	return -1
}

// storedIn reports whether f has a value in scope.
func storedIn(f Field, scope string) bool {
	if scope == ScopeProject {
		return f.HasProject
	}
	return f.HasGlobal
}

func (st *fieldState) update(gtx layout.Context, p *Panel, f Field) []Event {
	scope := p.editScope(f)
	var out []Event
	if f.ReadOnly {
		return nil
	}
	if st.reset.Clicked(gtx) && storedIn(f, scope) {
		out = append(out, ResetEvent{Key: f.Key, Scope: scope})
	}
	if candidateMode(f) {
		if st.refresh.Clicked(gtx) {
			out = append(out, RefreshCandidatesEvent{Key: f.Key})
		}
		if st.custom && st.choices.Clicked(gtx) {
			st.custom = false
		}
		if !st.free(f) && st.cand != nil {
			if i, ok := st.cand.Update(gtx); ok && i < len(st.candVals) {
				if st.candVals[i] == nil {
					st.custom = true
				} else {
					out = append(out, SetEvent{Key: f.Key, Scope: scope, Value: candidateValue(f.Kind, st.candVals[i])})
				}
			}
			return out
		}
	}
	switch {
	case f.Kind == KindBool:
		st.check.State = checkState(f.Value == true)
		if st.check.Update(gtx) {
			out = append(out, SetEvent{Key: f.Key, Scope: scope, Value: st.check.Checked()})
		}
	case st.sel != nil:
		if i, ok := st.sel.Update(gtx); ok && i < len(st.values) {
			out = append(out, SetEvent{Key: f.Key, Scope: scope, Value: st.values[i]})
		}
	case st.text != nil:
		if f.Kind == KindPath && st.choose.Clicked(gtx) {
			out = append(out, ChoosePathEvent{Key: f.Key, Scope: scope})
		}
		commit := false
		for {
			ev, ok := st.text.Editor.Update(gtx)
			if !ok {
				break
			}
			switch ev.(type) {
			case widget.SubmitEvent:
				commit = true
			case widget.ChangeEvent:
				st.err, st.text.Invalid = "", false
			}
		}
		focused := gtx.Focused(&st.text.Editor)
		if st.focus && !focused {
			commit = true
		}
		st.focus = focused
		if commit && st.text.Text() != st.synced {
			v, err := ParseValue(f, st.text.Text())
			if err != nil {
				st.err, st.text.Invalid = err.Error(), true
				break
			}
			st.synced = st.text.Text()
			out = append(out, SetEvent{Key: f.Key, Scope: scope, Value: v})
		}
	}
	return out
}

// fieldRow lays out label, source badge, reset button, the input and help.
func (p *Panel) fieldRow(th *tokens.Theme, f Field) layout.Widget {
	st := p.field(f)
	scope := p.editScope(f)
	return func(gtx layout.Context) layout.Dimensions {
		return layout.Inset{Bottom: th.Space.M}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
						layout.Rigid(func(gtx layout.Context) layout.Dimensions {
							if f.Kind == KindBool {
								return layout.Dimensions{} // the checkbox carries the label
							}
							return layout.Inset{Right: th.Space.S}.Layout(gtx, atoms.Text(f.Label).Fn(th))
						}),
						layout.Rigid(func(gtx layout.Context) layout.Dimensions {
							if f.Kind != KindBool {
								return layout.Dimensions{}
							}
							st.check.Label = f.Label
							st.check.State = checkState(f.Value == true)
							st.check.Disabled = f.ReadOnly
							return layout.Inset{Right: th.Space.S}.Layout(gtx, func(gtx layout.Context) layout.Dimensions { return st.check.Layout(gtx, th) })
						}),
						layout.Rigid(func(gtx layout.Context) layout.Dimensions { return sourceBadge(f).Layout(gtx, th) }),
						layout.Rigid(func(gtx layout.Context) layout.Dimensions {
							if !storedIn(f, scope) || f.ReadOnly {
								return layout.Dimensions{}
							}
							return layout.Inset{Left: th.Space.XS}.Layout(gtx, func(gtx layout.Context) layout.Dimensions { return st.reset.Layout(gtx, th) })
						}),
						layout.Rigid(func(gtx layout.Context) layout.Dimensions {
							if f.ProjectScope || p.NoProject || p.Scope != ScopeProject {
								return layout.Dimensions{}
							}
							return layout.Inset{Left: th.Space.S}.Layout(gtx, atoms.Small("global only").Fn(th))
						}),
					)
				}),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					if f.Kind == KindBool {
						return layout.Dimensions{}
					}
					return layout.Inset{Top: th.Space.XS}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
						return p.input(gtx, th, st, f)
					})
				}),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					msg := f.Help
					if f.ReadOnly && f.ReadOnlyHint != "" {
						msg = strings.TrimSpace(msg + " Read-only here: " + f.ReadOnlyHint + ".")
					}
					switch {
					case st.err != "":
						msg = st.err
					case f.Invalid != "":
						msg = "Stored value ignored: " + f.Invalid
					}
					if msg == "" {
						return layout.Dimensions{}
					}
					gtx.Constraints.Max.X = min(gtx.Constraints.Max.X, gtx.Dp(520))
					lbl := atoms.Small(msg)
					if st.err != "" || f.Invalid != "" {
						lbl.Color = th.Color.Danger
					}
					return layout.Inset{Top: th.Space.XXS}.Layout(gtx, lbl.Fn(th))
				}),
			)
		})
	}
}

func (p *Panel) input(gtx layout.Context, th *tokens.Theme, st *fieldState, f Field) layout.Dimensions {
	gtx.Constraints.Max.X = min(gtx.Constraints.Max.X, gtx.Dp(360))
	gtx.Constraints.Min.X = gtx.Constraints.Max.X
	if st.text != nil {
		st.text.Disabled = f.ReadOnly
	}
	if st.sel != nil {
		st.sel.Disabled = f.ReadOnly
		st.sel.Selected = indexOf(st.values, FormatValue(f.Kind, f.Value))
		return st.sel.Layout(gtx, th)
	}
	if candidateMode(f) && st.cand != nil {
		refresh := layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Inset{Left: th.Space.XS}.Layout(gtx, func(gtx layout.Context) layout.Dimensions { return st.refresh.Layout(gtx, th) })
		})
		if !st.free(f) {
			st.cand.Selected = st.candidateIndex(f)
			return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
				layout.Flexed(1, func(gtx layout.Context) layout.Dimensions { return st.cand.Layout(gtx, th) }),
				refresh,
			)
		}
		return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
			layout.Flexed(1, func(gtx layout.Context) layout.Dimensions { return p.textInput(gtx, th, st, f) }),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				if len(f.Candidates) == 0 {
					return layout.Dimensions{}
				}
				return layout.Inset{Left: th.Space.XS}.Layout(gtx, func(gtx layout.Context) layout.Dimensions { return st.choices.Layout(gtx, th) })
			}),
			refresh,
		)
	}
	return p.textInput(gtx, th, st, f)
}

// textInput is the text field (with the path chooser for paths).
func (p *Panel) textInput(gtx layout.Context, th *tokens.Theme, st *fieldState, f Field) layout.Dimensions {
	if !gtx.Focused(&st.text.Editor) && st.err == "" {
		if s := FormatValue(f.Kind, f.Value); s != st.synced || st.text.Text() != s {
			st.text.SetText(s)
			st.synced = s
		}
	}
	if f.Kind != KindPath {
		return st.text.Layout(gtx, th)
	}
	return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
		layout.Flexed(1, func(gtx layout.Context) layout.Dimensions { return st.text.Layout(gtx, th) }),
		layout.Rigid(layout.Spacer{Width: th.Space.S}.Layout),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions { return st.choose.Layout(gtx, th) }),
	)
}

func sourceBadge(f Field) atoms.Badge {
	switch f.Source {
	case ScopeProject:
		return atoms.Badge{Text: "project", Kind: atoms.BadgeAccent}
	case ScopeGlobal:
		return atoms.Badge{Text: "global", Kind: atoms.BadgeInfo}
	}
	return atoms.Badge{Text: "default"}
}
