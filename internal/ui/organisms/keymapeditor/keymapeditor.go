// Package keymapeditor provides the keymap page of the settings dialog
// (AP13.3): the preset choice, a searchable table of all bindable actions
// (search by text or by recording a shortcut), and for the selected
// action its shortcuts with "add" (a Recorder capturing the next chord),
// "remove", "no shortcut" and "reset to default", an inline conflict
// warning for a recorded chord, the stored lines that cannot be parsed,
// and "Reset all".
//
// The organism holds no keymap logic: the host fills Rows from
// keymap.list, answers RecordedEvent with keymap.check (Pending when the
// chord conflicts) and turns every event into a keymap.* call (D41).
package keymapeditor

import (
	"strings"

	"gioui.org/layout"
	"gioui.org/unit"

	"github.com/ape-dev-de/tiegel/internal/ui/atoms"
	"github.com/ape-dev-de/tiegel/internal/ui/molecules"
	"github.com/ape-dev-de/tiegel/internal/ui/tokens"
)

// Row is one bindable action (a keymap.list binding).
type Row struct {
	Action  string
	Title   string
	Context string // global, editor, toolWindow
	// Chords are the effective chords, Defaults those of the preset.
	Chords   []string
	Defaults []string
	// Shadowed are chords another action of the context wins.
	Shadowed []string
	// User marks an action the user changed.
	User bool
}

// Other is an action that already uses a recorded chord.
type Other struct {
	Action  string
	Title   string
	Context string
}

// Pending is a recorded chord that conflicts with other actions; the
// editor asks whether to keep both bindings or to remove the chord from
// the others.
type Pending struct {
	Action string
	Chord  string
	Others []Other
}

// Event is reported by Editor.Update.
type Event interface{ isKeymapEvent() }

// PresetEvent: another preset was chosen.
type PresetEvent struct{ Preset string }

// RecordedEvent: a chord was recorded for Action. The host checks it for
// conflicts: none → add it; otherwise it sets Pending.
type RecordedEvent struct{ Action, Chord string }

// AddEvent: add Chord to Action after a conflict warning; Exclusive
// removes it from the other actions.
type AddEvent struct {
	Action, Chord string
	Exclusive     bool
}

// CancelPendingEvent: the conflicting chord is not added.
type CancelPendingEvent struct{}

// RemoveEvent: remove one chord of Action.
type RemoveEvent struct{ Action, Chord string }

// UnbindEvent: Action gets no shortcut at all.
type UnbindEvent struct{ Action string }

// ResetEvent: Action back to the preset's chords.
type ResetEvent struct{ Action string }

// ResetAllEvent: all user bindings and the preset choice are removed.
type ResetAllEvent struct{}

// DropInvalidEvent: remove an unparseable stored line.
type DropInvalidEvent struct{ Line string }

func (PresetEvent) isKeymapEvent()        {}
func (RecordedEvent) isKeymapEvent()      {}
func (AddEvent) isKeymapEvent()           {}
func (CancelPendingEvent) isKeymapEvent() {}
func (RemoveEvent) isKeymapEvent()        {}
func (UnbindEvent) isKeymapEvent()        {}
func (ResetEvent) isKeymapEvent()         {}
func (ResetAllEvent) isKeymapEvent()      {}
func (DropInvalidEvent) isKeymapEvent()   {}

// ContextLabel returns the display name of a binding context.
func ContextLabel(ctx string) string {
	switch ctx {
	case "global":
		return "Global"
	case "editor":
		return "Editor"
	case "toolWindow":
		return "Tool window"
	}
	return ctx
}

// Matches reports whether r matches every word of query (case-insensitive
// in title, action ID, context and chords) and, if chord is set, has
// that chord.
func Matches(r Row, query, chord string) bool {
	if chord != "" && !hasChord(r.Chords, chord) {
		return false
	}
	words := strings.Fields(strings.ToLower(query))
	if len(words) == 0 {
		return true
	}
	hay := strings.ToLower(strings.Join(append([]string{r.Title, r.Action, r.Context, ContextLabel(r.Context)}, r.Chords...), "\x00"))
	for _, w := range words {
		if !strings.Contains(hay, w) {
			return false
		}
	}
	return true
}

func hasChord(cs []string, c string) bool {
	for _, x := range cs {
		if strings.EqualFold(x, c) {
			return true
		}
	}
	return false
}

// Filter returns the rows matching query and chord (see Matches).
func Filter(rows []Row, query, chord string) []Row {
	var out []Row
	for _, r := range rows {
		if Matches(r, query, chord) {
			out = append(out, r)
		}
	}
	return out
}

// Editor is the keymap organism. The host fills the exported data every
// frame; the editor keeps the widget state.
type Editor struct {
	// Preset is the current preset, Presets the choices (default mac,
	// pc).
	Preset  string
	Presets []string
	Rows    []Row
	// Invalid are stored lines that cannot be parsed.
	Invalid []string
	// Pending is a conflicting recorded chord awaiting a decision.
	Pending *Pending
	// Selected is the action shown in the detail part.
	Selected string

	inited    bool
	search    *molecules.SearchField
	presetSel *molecules.Dropdown
	presetFor []string
	findKey   atoms.Button
	clearKey  atoms.IconButton
	keyFilter string
	filterRec Recorder
	addRec    Recorder
	recFor    string
	resetAll  atoms.Button
	list      molecules.ScrollList
	rows      map[string]*molecules.ListRow
	chips     map[string]*atoms.IconButton
	addBtn    atoms.Button
	noneBtn   atoms.Button
	resetBtn  atoms.Button
	keepBoth  atoms.Button
	steal     atoms.Button
	cancel    atoms.Button
	drops     map[string]*atoms.Button
}

func (e *Editor) init() {
	if e.inited {
		return
	}
	e.inited = true
	e.search = molecules.NewSearchField("Search actions or shortcuts")
	e.findKey = atoms.Button{Text: "Find by shortcut", Kind: atoms.Secondary, Glyph: atoms.GlyphSearch}
	e.clearKey = atoms.IconButton{Glyph: atoms.GlyphClose, Description: "Clear shortcut filter"}
	e.filterRec = Recorder{Instant: true, Hint: "Press the shortcut to find…"}
	e.resetAll = atoms.Button{Text: "Reset all", Kind: atoms.Secondary, Glyph: atoms.GlyphRefresh}
	e.rows = map[string]*molecules.ListRow{}
	e.chips = map[string]*atoms.IconButton{}
	e.drops = map[string]*atoms.Button{}
	e.addBtn = atoms.Button{Text: "Add shortcut", Kind: atoms.Secondary, Glyph: atoms.GlyphAdd}
	e.noneBtn = atoms.Button{Text: "No shortcut", Kind: atoms.Ghost}
	e.resetBtn = atoms.Button{Text: "Reset to default", Kind: atoms.Ghost, Glyph: atoms.GlyphRefresh}
	e.keepBoth = atoms.Button{Text: "Keep both", Kind: atoms.Secondary}
	e.steal = atoms.Button{Text: "Remove from other", Kind: atoms.Primary}
	e.cancel = atoms.Button{Text: "Cancel", Kind: atoms.Ghost}
}

func (e *Editor) presets() []string {
	if len(e.Presets) > 0 {
		return e.Presets
	}
	return []string{"mac", "pc"}
}

// Query returns the search text.
func (e *Editor) Query() string {
	e.init()
	return e.search.Text()
}

// SetQuery replaces the search text.
func (e *Editor) SetQuery(q string) {
	e.init()
	e.search.SetText(q)
}

// KeyFilter returns the shortcut filter ("" = none).
func (e *Editor) KeyFilter() string { return e.keyFilter }

// SetKeyFilter sets the shortcut filter.
func (e *Editor) SetKeyFilter(c string) { e.keyFilter = c }

// Recording reports whether a recorder captures keys: the host must then
// leave key events (shortcuts, Escape) to the editor.
func (e *Editor) Recording() bool { return e.addRec.Active() || e.filterRec.Active() }

// RecordingFor returns the action a shortcut is recorded for ("" if
// none).
func (e *Editor) RecordingFor() string {
	if e.addRec.Active() {
		return e.recFor
	}
	return ""
}

// StartRecording starts recording a shortcut for action (also selects
// it).
func (e *Editor) StartRecording(action string) {
	e.init()
	e.filterRec.Cancel()
	e.Selected, e.recFor = action, action
	e.addRec.Start()
}

// StartKeyFilter starts recording the shortcut to search for.
func (e *Editor) StartKeyFilter() {
	e.init()
	e.addRec.Cancel()
	e.filterRec.Start()
}

// CancelRecording stops any recording (e.g. when the page is left).
func (e *Editor) CancelRecording() {
	e.addRec.Cancel()
	e.filterRec.Cancel()
}

// Gesture hands a pointer or gesture input (keymap.KeyMouseBack, …) to an
// active recorder; it reports whether one took it.
func (e *Editor) Gesture(name string) bool {
	return e.addRec.Gesture(name) || e.filterRec.Gesture(name)
}

// Visible returns the rows shown with the current search.
func (e *Editor) Visible() []Row {
	e.init()
	return Filter(e.Rows, e.search.Text(), e.keyFilter)
}

func (e *Editor) row(action string) (Row, bool) {
	for _, r := range e.Rows {
		if r.Action == action {
			return r, true
		}
	}
	return Row{}, false
}

// Update returns the user's changes.
func (e *Editor) Update(gtx layout.Context) []Event {
	e.init()
	var out []Event
	// Recorders first: while one has the focus it gets every key.
	if e.filterRec.Update(gtx) == RecordConfirmed {
		e.keyFilter = e.filterRec.Chord()
	}
	if e.addRec.Update(gtx) == RecordConfirmed && e.recFor != "" {
		out = append(out, RecordedEvent{Action: e.recFor, Chord: e.addRec.Chord()})
	}
	if e.presetSel != nil {
		if i, ok := e.presetSel.Update(gtx); ok && i >= 0 && i < len(e.presetFor) && e.presetFor[i] != e.Preset {
			out = append(out, PresetEvent{Preset: e.presetFor[i]})
		}
	}
	if e.resetAll.Clicked(gtx) {
		out = append(out, ResetAllEvent{})
	}
	e.search.Update(gtx)
	if e.findKey.Clicked(gtx) {
		e.StartKeyFilter()
	}
	if e.clearKey.Clicked(gtx) {
		e.keyFilter = ""
	}
	for _, r := range e.Visible() {
		lr := e.rows[r.Action]
		if lr == nil {
			continue
		}
		if c, double := lr.Update(gtx); c {
			e.Selected = r.Action
			if double {
				e.StartRecording(r.Action)
			}
		}
	}
	if r, ok := e.row(e.Selected); ok {
		for _, c := range r.Chords {
			if b := e.chips[chipKey(r.Action, c)]; b != nil && b.Clicked(gtx) {
				out = append(out, RemoveEvent{Action: r.Action, Chord: c})
			}
		}
		if e.addBtn.Clicked(gtx) {
			e.StartRecording(r.Action)
		}
		if e.noneBtn.Clicked(gtx) {
			out = append(out, UnbindEvent{Action: r.Action})
		}
		if e.resetBtn.Clicked(gtx) {
			out = append(out, ResetEvent{Action: r.Action})
		}
	}
	if p := e.Pending; p != nil {
		switch {
		case e.keepBoth.Clicked(gtx):
			out = append(out, AddEvent{Action: p.Action, Chord: p.Chord})
		case e.steal.Clicked(gtx):
			out = append(out, AddEvent{Action: p.Action, Chord: p.Chord, Exclusive: true})
		case e.cancel.Clicked(gtx):
			out = append(out, CancelPendingEvent{})
		}
	}
	for _, l := range e.Invalid {
		if b := e.drops[l]; b != nil && b.Clicked(gtx) {
			out = append(out, DropInvalidEvent{Line: l})
		}
	}
	return out
}

func chipKey(action, chord string) string { return action + "\x00" + chord }

// Layout draws the editor filling the constraints.
func (e *Editor) Layout(gtx layout.Context, th *tokens.Theme) layout.Dimensions {
	e.init()
	gtx.Constraints.Min = gtx.Constraints.Max
	visible := e.Visible()
	children := []layout.FlexChild{
		layout.Rigid(func(gtx layout.Context) layout.Dimensions { return e.layoutToolbar(gtx, th) }),
		layout.Rigid(layout.Spacer{Height: th.Space.S}.Layout),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions { return e.layoutSearch(gtx, th) }),
	}
	for _, l := range e.Invalid {
		children = append(children, layout.Rigid(func(gtx layout.Context) layout.Dimensions { return e.layoutInvalid(gtx, th, l) }))
	}
	children = append(children,
		layout.Rigid(layout.Spacer{Height: th.Space.S}.Layout),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions { return e.layoutHeader(gtx, th) }),
		layout.Flexed(1, func(gtx layout.Context) layout.Dimensions { return e.layoutRows(gtx, th, visible) }),
		layout.Rigid(atoms.Divider{}.Fn(th)),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions { return e.layoutDetail(gtx, th) }),
	)
	return layout.Flex{Axis: layout.Vertical}.Layout(gtx, children...)
}

func (e *Editor) layoutToolbar(gtx layout.Context, th *tokens.Theme) layout.Dimensions {
	ps := e.presets()
	if e.presetSel == nil || !equal(e.presetFor, ps) {
		opts := make([]molecules.DropdownOption, len(ps))
		for i, p := range ps {
			opts[i] = molecules.DropdownOption{Label: presetLabel(p), Detail: presetDetail(p)}
		}
		e.presetSel = molecules.NewSelect("Preset", opts...)
		e.presetFor = append([]string(nil), ps...)
	}
	e.presetSel.Selected = -1
	for i, p := range ps {
		if p == e.Preset {
			e.presetSel.Selected = i
		}
	}
	return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
		layout.Rigid(atoms.Text("Preset").Fn(th)),
		layout.Rigid(layout.Spacer{Width: th.Space.S}.Layout),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			gtx.Constraints.Max.X = min(gtx.Constraints.Max.X, gtx.Dp(220))
			gtx.Constraints.Min.X = gtx.Constraints.Max.X
			return e.presetSel.Layout(gtx, th)
		}),
		layout.Flexed(1, layout.Spacer{}.Layout),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions { return e.resetAll.Layout(gtx, th) }),
	)
}

func presetLabel(p string) string {
	switch p {
	case "mac":
		return "macOS"
	case "pc":
		return "Windows / Linux"
	}
	return p
}

func presetDetail(p string) string {
	switch p {
	case "mac":
		return "Cmd for commands, Option for words"
	case "pc":
		return "Ctrl for commands, Alt"
	}
	return ""
}

func equal(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func (e *Editor) layoutSearch(gtx layout.Context, th *tokens.Theme) layout.Dimensions {
	return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
		layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
			if e.filterRec.Active() {
				return e.filterRec.Layout(gtx, th)
			}
			return e.search.Layout(gtx, th)
		}),
		layout.Rigid(layout.Spacer{Width: th.Space.S}.Layout),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			if e.keyFilter == "" {
				return e.findKey.Layout(gtx, th)
			}
			return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
				layout.Rigid(func(gtx layout.Context) layout.Dimensions { return e.findKey.Layout(gtx, th) }),
				layout.Rigid(layout.Spacer{Width: th.Space.S}.Layout),
				layout.Rigid(atoms.Badge{Text: e.keyFilter, Kind: atoms.BadgeAccent}.Fn(th)),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions { return e.clearKey.Layout(gtx, th) }),
			)
		}),
	)
}

func (e *Editor) layoutInvalid(gtx layout.Context, th *tokens.Theme, line string) layout.Dimensions {
	b := e.drops[line]
	if b == nil {
		b = &atoms.Button{Text: "Remove", Kind: atoms.Secondary, Glyph: atoms.GlyphDelete}
		e.drops[line] = b
	}
	return layout.Inset{Top: th.Space.S}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
			layout.Flexed(1, molecules.Banner{Kind: molecules.BannerWarning, Title: "Ignored stored shortcut line",
				Detail: line + " — it cannot be read and has no effect."}.Fn(th)),
			layout.Rigid(layout.Spacer{Width: th.Space.S}.Layout),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions { return b.Layout(gtx, th) }),
		)
	})
}

// Column weights of the table: action, ID, context (fixed), shortcuts.
const ctxWidth unit.Dp = 90

func (e *Editor) columns(th *tokens.Theme, title, id, ctx, chords layout.Widget) layout.Widget {
	cell := func(w layout.Widget) layout.Widget {
		return func(gtx layout.Context) layout.Dimensions {
			return layout.Inset{Right: th.Space.S}.Layout(gtx, w)
		}
	}
	return func(gtx layout.Context) layout.Dimensions {
		return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
			layout.Flexed(3, cell(title)),
			layout.Flexed(3, cell(id)),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				gtx.Constraints.Min.X = gtx.Dp(ctxWidth)
				gtx.Constraints.Max.X = gtx.Constraints.Min.X
				return cell(ctx)(gtx)
			}),
			layout.Flexed(3, chords),
		)
	}
}

func (e *Editor) layoutHeader(gtx layout.Context, th *tokens.Theme) layout.Dimensions {
	return atoms.Surface{Kind: atoms.SurfaceAlt}.Layout(gtx, th, func(gtx layout.Context) layout.Dimensions {
		gtx.Constraints.Min.X = gtx.Constraints.Max.X
		return layout.Inset{Left: th.Space.S, Right: th.Space.S, Top: th.Space.XXS, Bottom: th.Space.XXS}.Layout(gtx,
			e.columns(th, atoms.Small("Action").Fn(th), atoms.Small("ID").Fn(th), atoms.Small("Context").Fn(th), atoms.Small("Shortcuts").Fn(th)))
	})
}

func (e *Editor) layoutRows(gtx layout.Context, th *tokens.Theme, rows []Row) layout.Dimensions {
	if len(rows) == 0 {
		msg := "No action matches."
		if len(e.Rows) == 0 {
			msg = "Loading shortcuts…"
		}
		return layout.UniformInset(th.Space.M).Layout(gtx, atoms.Muted(msg).Fn(th))
	}
	return e.list.Layout(gtx, th, len(rows), func(gtx layout.Context, i int) layout.Dimensions {
		r := rows[i]
		lr := e.rows[r.Action]
		if lr == nil {
			lr = &molecules.ListRow{}
			e.rows[r.Action] = lr
		}
		lr.Selected = r.Action == e.Selected
		return lr.Layout(gtx, th, e.columns(th,
			func(gtx layout.Context) layout.Dimensions {
				return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
					layout.Flexed(1, atoms.Text(r.Title).SingleLine().Fn(th)),
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						if !r.User {
							return layout.Dimensions{}
						}
						return layout.Inset{Left: th.Space.XS}.Layout(gtx, atoms.Badge{Text: "custom", Kind: atoms.BadgeAccent}.Fn(th))
					}),
				)
			},
			atoms.Small(r.Action).SingleLine().Fn(th),
			atoms.Small(ContextLabel(r.Context)).SingleLine().Fn(th),
			func(gtx layout.Context) layout.Dimensions {
				return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
					layout.Flexed(1, atoms.Code(strings.Join(r.Chords, ", ")).SingleLine().Fn(th)),
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						if len(r.Shadowed) == 0 {
							return layout.Dimensions{}
						}
						return layout.Inset{Left: th.Space.XS}.Layout(gtx, atoms.Badge{Text: "shadowed", Kind: atoms.BadgeWarning}.Fn(th))
					}),
				)
			},
		))
	})
}

func (e *Editor) layoutDetail(gtx layout.Context, th *tokens.Theme) layout.Dimensions {
	r, ok := e.row(e.Selected)
	return layout.Inset{Top: th.Space.S}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		if !ok {
			return atoms.Muted("Select an action to change its shortcuts (double-click records a new one).").Layout(gtx, th)
		}
		var ws []layout.FlexChild
		add := func(w layout.Widget) {
			if len(ws) > 0 {
				ws = append(ws, layout.Rigid(layout.Spacer{Height: th.Space.S}.Layout))
			}
			ws = append(ws, layout.Rigid(w))
		}
		add(func(gtx layout.Context) layout.Dimensions {
			return layout.Flex{Alignment: layout.Baseline}.Layout(gtx,
				layout.Rigid(atoms.Strong(r.Title).Fn(th)),
				layout.Rigid(layout.Spacer{Width: th.Space.S}.Layout),
				layout.Flexed(1, atoms.Small(r.Action+" · "+ContextLabel(r.Context)+defaultsNote(r)).SingleLine().Fn(th)),
			)
		})
		add(func(gtx layout.Context) layout.Dimensions { return e.layoutChips(gtx, th, r) })
		if e.addRec.Active() && e.recFor == r.Action {
			add(func(gtx layout.Context) layout.Dimensions { return e.addRec.Layout(gtx, th) })
		}
		if p := e.Pending; p != nil && p.Action == r.Action {
			add(func(gtx layout.Context) layout.Dimensions { return e.layoutPending(gtx, th, *p) })
		}
		if len(r.Shadowed) > 0 {
			add(molecules.Banner{Kind: molecules.BannerInfo, Title: "Shadowed: " + strings.Join(r.Shadowed, ", "),
				Detail: "Another action in this context uses the shortcut and wins; remove it there or here."}.Fn(th))
		}
		add(func(gtx layout.Context) layout.Dimensions {
			e.noneBtn.Disabled = len(r.Chords) == 0
			e.resetBtn.Disabled = !r.User
			return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
				layout.Rigid(func(gtx layout.Context) layout.Dimensions { return e.addBtn.Layout(gtx, th) }),
				layout.Rigid(layout.Spacer{Width: th.Space.S}.Layout),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions { return e.noneBtn.Layout(gtx, th) }),
				layout.Rigid(layout.Spacer{Width: th.Space.S}.Layout),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions { return e.resetBtn.Layout(gtx, th) }),
			)
		})
		return layout.Flex{Axis: layout.Vertical}.Layout(gtx, ws...)
	})
}

// defaultsNote tells the preset's chords of a changed action.
func defaultsNote(r Row) string {
	if !r.User {
		return ""
	}
	if len(r.Defaults) == 0 {
		return " · default: none"
	}
	return " · default: " + strings.Join(r.Defaults, ", ")
}

func (e *Editor) layoutChips(gtx layout.Context, th *tokens.Theme, r Row) layout.Dimensions {
	if len(r.Chords) == 0 {
		return atoms.Muted("No shortcut").Layout(gtx, th)
	}
	var cs []layout.FlexChild
	for _, c := range r.Chords {
		b := e.chips[chipKey(r.Action, c)]
		if b == nil {
			b = &atoms.IconButton{Glyph: atoms.GlyphClose, Description: "Remove " + c}
			e.chips[chipKey(r.Action, c)] = b
		}
		shadowed := hasChord(r.Shadowed, c)
		cs = append(cs, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Inset{Right: th.Space.S}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return atoms.Surface{Kind: atoms.SurfaceAlt, Border: true, Rounded: true}.Layout(gtx, th, func(gtx layout.Context) layout.Dimensions {
					return layout.Inset{Left: th.Space.S}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
						label := atoms.Code(c)
						if shadowed {
							label = label.WithColor(th.Color.TextMuted)
						}
						return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
							layout.Rigid(label.SingleLine().Fn(th)),
							layout.Rigid(func(gtx layout.Context) layout.Dimensions { return b.Layout(gtx, th) }),
						)
					})
				})
			})
		}))
	}
	return layout.Flex{Alignment: layout.Middle}.Layout(gtx, cs...)
}

func (e *Editor) layoutPending(gtx layout.Context, th *tokens.Theme, p Pending) layout.Dimensions {
	names := make([]string, len(p.Others))
	for i, o := range p.Others {
		t := o.Title
		if t == "" {
			t = o.Action
		}
		names[i] = t + " (" + ContextLabel(o.Context) + ")"
	}
	return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
		layout.Rigid(molecules.Banner{Kind: molecules.BannerWarning, Title: p.Chord + " is already used",
			Detail: "Also bound to " + strings.Join(names, ", ") + "."}.Fn(th)),
		layout.Rigid(layout.Spacer{Height: th.Space.XS}.Layout),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
				layout.Rigid(func(gtx layout.Context) layout.Dimensions { return e.steal.Layout(gtx, th) }),
				layout.Rigid(layout.Spacer{Width: th.Space.S}.Layout),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions { return e.keepBoth.Layout(gtx, th) }),
				layout.Rigid(layout.Spacer{Width: th.Space.S}.Layout),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions { return e.cancel.Layout(gtx, th) }),
			)
		}),
	)
}
