// Package settings provides the settings dialog (decision D190, D216): a
// tree with "Core" pages (Appearance, Editor, Files, Interpreter, …) and
// "Plugins" pages (one per module, with its on/off switch), all generated
// from declared setting specs, a search across every setting, the source of
// each value (default, global, project) and a reset per value.
//
// The organism knows no setting by name: the host converts the
// settings.schema result into Pages and turns the reported events into
// settings.set calls. Special pages (the interpreter list with path
// mappings, the update check) are drawn from Page.Extra.
package settings

import (
	"fmt"
	"image"
	"math"
	"strconv"
	"strings"

	"gioui.org/layout"
	"gioui.org/unit"

	"github.com/ape-dev-de/tiegel/internal/ui/atoms"
	"github.com/ape-dev-de/tiegel/internal/ui/molecules"
	"github.com/ape-dev-de/tiegel/internal/ui/organisms/keymapeditor"
	"github.com/ape-dev-de/tiegel/internal/ui/organisms/runtime"
	"github.com/ape-dev-de/tiegel/internal/ui/tokens"
)

// Value kinds of a Field (the settings.Kind values).
const (
	KindBool   = "bool"
	KindInt    = "int"
	KindFloat  = "float"
	KindString = "string"
	KindEnum   = "enum"
	KindPath   = "path"
	KindList   = "list"
)

// Scopes and sources.
const (
	ScopeGlobal   = "global"
	ScopeProject  = "project"
	SourceDefault = "default"
)

// Extras are the special page parts the dialog draws itself.
const (
	ExtraInterpreter = "interpreter" // interpreter picker, path mappings, Xdebug hints
	ExtraUpdates     = "updates"     // "Check now" with status
	ExtraFontPreview = "fontPreview" // sample line in the editor font
	// ExtraKeymap replaces the page's fields by the keymap editor
	// (Panel.Keymap), which fills the page.
	ExtraKeymap = "keymap"
)

// Field is one declared setting with its current value.
type Field struct {
	Key   string
	Kind  string
	Label string
	Help  string
	// Group is an optional sub-heading inside the page.
	Group   string
	Options []string
	Min     *int
	Max     *int
	// ProjectScope is true when the setting may have a project value.
	ProjectScope bool
	// Value is the effective value (decoded JSON: bool, float64, string,
	// []any or []string); Source is default, global or project.
	Value  any
	Source string
	// HasGlobal and HasProject tell which scopes store a value.
	HasGlobal  bool
	HasProject bool
	// Invalid describes an ignored stored value.
	Invalid string
	// Choices offers values for a string field as a dropdown (e.g. the
	// installed fonts); ChoiceDefault labels the empty value.
	Choices       []string
	ChoiceDefault string
	// ReadOnly shows the value without an input to change it (no reset
	// either); ReadOnlyHint tells where it is changed.
	ReadOnly     bool
	ReadOnlyHint string
}

// Module describes the plugin of a plugin page.
type Module struct {
	ID        string
	DependsOn []string
	// Dependents are enabled modules that need this one.
	Dependents []string
	Enabled    bool
	Running    bool
	Error      string
}

// Page is one node of the tree.
type Page struct {
	// ID is unique ("core:Editor", "plugin:git").
	ID    string
	Title string
	// Module is set for plugin pages.
	Module *Module
	Fields []Field
	// Extra names a special part drawn after the fields (Extra*).
	Extra string
}

// Plugin reports whether p is a plugin page.
func (p Page) Plugin() bool { return p.Module != nil }

// Event is reported by Panel.Update.
type Event interface{ isSettingsEvent() }

// SetEvent: a value was entered. Value is bool, int, float64, string or
// []string; Scope is global or project.
type SetEvent struct {
	Key   string
	Scope string
	Value any
}

// ResetEvent: the value of Key in Scope should be removed (back to the
// value of the next scope or the default).
type ResetEvent struct {
	Key   string
	Scope string
}

// ChoosePathEvent: the chooser button of a path field was clicked; the
// host shows a file dialog and answers with a SetEvent-equivalent write.
type ChoosePathEvent struct {
	Key   string
	Scope string
}

// ModuleEvent: a plugin switch was toggled.
type ModuleEvent struct {
	ID     string
	Enable bool
}

// CheckNowEvent: "Check now" was clicked (updates page).
type CheckNowEvent struct{}

// RuntimeEvent wraps an event of the interpreter page.
type RuntimeEvent struct{ Event runtime.Event }

// CloseEvent: the dialog was closed.
type CloseEvent struct{}

// KeymapEvent wraps an event of the keymap page.
type KeymapEvent struct{ Event keymapeditor.Event }

func (KeymapEvent) isSettingsEvent() {}

func (SetEvent) isSettingsEvent()        {}
func (ResetEvent) isSettingsEvent()      {}
func (ChoosePathEvent) isSettingsEvent() {}
func (ModuleEvent) isSettingsEvent()     {}
func (CheckNowEvent) isSettingsEvent()   {}
func (RuntimeEvent) isSettingsEvent()    {}
func (CloseEvent) isSettingsEvent()      {}

// FontPreview is the sample line of ExtraFontPreview: Latin with accents,
// PHP syntax, CJK and a symbol from the fallback fonts.
const FontPreview = "<?php $café = fn(int $x) => $x * 2; // 漢字 ✓"

// Size is the dialog size.
var Size = image.Pt(880, 600)

// Panel is the settings organism. The host fills Pages (and the
// interpreter organisms) every frame; the panel keeps the widget state per
// setting key.
type Panel struct {
	Pages []Page
	// Selected is the ID of the shown page ("" = the first).
	Selected string
	// Scope is where edits go: global (default) or project. ScopeProject
	// needs an open project (NoProject false).
	Scope     string
	NoProject bool
	// UpdateStatus is shown next to "Check now".
	UpdateStatus string

	// The interpreter page reuses the runtime organisms; the host owns
	// them and fills their data.
	Interpreter runtime.InterpreterPicker
	Mappings    runtime.PathMappingEditor
	Hints       runtime.XdebugHintsList
	// Keymap is the keymap page (ExtraKeymap); the host fills its data.
	Keymap keymapeditor.Editor

	search   *molecules.SearchField
	scopeSel *molecules.Dropdown
	nav      map[string]*molecules.TreeRow
	coreRow  molecules.TreeRow
	plugRow  molecules.TreeRow
	fields   map[string]*fieldState
	modBoxes map[string]*atoms.Checkbox
	checkNow atoms.Button
	closeBtn atoms.IconButton
	scroll   molecules.ScrollList
	navList  molecules.ScrollList
	inited   bool
}

func (p *Panel) init() {
	if p.inited {
		return
	}
	p.inited = true
	p.search = molecules.NewSearchField("Search settings")
	p.scopeSel = molecules.NewSelect("Scope",
		molecules.DropdownOption{Label: "Global", Detail: "All projects"},
		molecules.DropdownOption{Label: "This project", Detail: "Overrides the global value"})
	p.nav = map[string]*molecules.TreeRow{}
	p.fields = map[string]*fieldState{}
	p.modBoxes = map[string]*atoms.Checkbox{}
	p.coreRow = molecules.TreeRow{Name: "Core", Expandable: true, Expanded: true, Glyph: atoms.GlyphSettings}
	p.plugRow = molecules.TreeRow{Name: "Plugins", Expandable: true, Expanded: true, Glyph: atoms.GlyphList}
	p.checkNow = atoms.Button{Text: "Check now", Kind: atoms.Secondary, Glyph: atoms.GlyphRefresh}
	p.closeBtn = atoms.IconButton{Glyph: atoms.GlyphClose, Description: "Close (Escape)"}
}

// Query returns the search text.
func (p *Panel) Query() string {
	p.init()
	return p.search.Text()
}

// SetQuery replaces the search text.
func (p *Panel) SetQuery(q string) {
	p.init()
	p.search.SetText(q)
}

// Page returns the shown page (search off), or nil without pages.
func (p *Panel) Page() *Page {
	for i := range p.Pages {
		if p.Pages[i].ID == p.Selected {
			return &p.Pages[i]
		}
	}
	if len(p.Pages) > 0 {
		return &p.Pages[0]
	}
	return nil
}

// editScope returns the scope an edit of f goes to.
func (p *Panel) editScope(f Field) string {
	if p.Scope == ScopeProject && !p.NoProject && f.ProjectScope {
		return ScopeProject
	}
	return ScopeGlobal
}

// Matches reports whether f (on page) matches every word of query
// (case-insensitive: key, label, help, group, page title, plugin ID).
func Matches(page Page, f Field, query string) bool {
	words := strings.Fields(strings.ToLower(query))
	if len(words) == 0 {
		return true
	}
	hay := strings.ToLower(strings.Join([]string{f.Key, f.Label, f.Help, f.Group, page.Title}, "\x00"))
	for _, w := range words {
		if !strings.Contains(hay, w) {
			return false
		}
	}
	return true
}

// Results returns the pages with only the fields matching query (pages
// without matches are left out). An empty query returns all pages.
func Results(pages []Page, query string) []Page {
	if strings.TrimSpace(query) == "" {
		return pages
	}
	var out []Page
	for _, pg := range pages {
		r := pg
		r.Fields = nil
		for _, f := range pg.Fields {
			if Matches(pg, f, query) {
				r.Fields = append(r.Fields, f)
			}
		}
		if len(r.Fields) > 0 {
			out = append(out, r)
		}
	}
	return out
}

// FormatValue renders a value of kind for a text field or label. Lists
// have one entry per line: entries may contain commas (keymap chords like
// "Cmd+,", globs like "*.{tpl,phtml}"), so no separator character inside
// a line is safe (D246).
func FormatValue(kind string, v any) string {
	switch kind {
	case KindList:
		var parts []string
		switch l := v.(type) {
		case []string:
			parts = l
		case []any:
			for _, e := range l {
				parts = append(parts, fmt.Sprint(e))
			}
		}
		return strings.Join(parts, "\n")
	case KindInt, KindFloat:
		switch n := v.(type) {
		case float64:
			if kind == KindInt || n == math.Trunc(n) {
				return strconv.FormatInt(int64(n), 10)
			}
			return strconv.FormatFloat(n, 'g', -1, 64)
		case int:
			return strconv.Itoa(n)
		}
	case KindBool:
		if b, ok := v.(bool); ok {
			return strconv.FormatBool(b)
		}
	}
	if v == nil {
		return ""
	}
	return fmt.Sprint(v)
}

// ParseValue converts text entered for f into a value for SetEvent.
func ParseValue(f Field, text string) (any, error) {
	text = strings.TrimSpace(text)
	bound := func(n float64) error {
		if f.Min != nil && n < float64(*f.Min) {
			return fmt.Errorf("at least %d", *f.Min)
		}
		if f.Max != nil && n > float64(*f.Max) {
			return fmt.Errorf("at most %d", *f.Max)
		}
		return nil
	}
	switch f.Kind {
	case KindInt:
		n, err := strconv.Atoi(text)
		if err != nil {
			return nil, fmt.Errorf("not a whole number")
		}
		return n, bound(float64(n))
	case KindFloat:
		n, err := strconv.ParseFloat(strings.ReplaceAll(text, ",", "."), 64)
		if err != nil {
			return nil, fmt.Errorf("not a number")
		}
		return n, bound(n)
	case KindList:
		// One entry per line (see FormatValue); blank lines are dropped.
		out := []string{}
		for _, part := range strings.Split(text, "\n") {
			if s := strings.TrimSpace(part); s != "" {
				out = append(out, s)
			}
		}
		return out, nil
	case KindBool:
		return strconv.ParseBool(text)
	}
	return text, nil
}

// keymapShown reports whether the keymap page is on screen (search off).
func (p *Panel) keymapShown() bool {
	pg := p.Page()
	return pg != nil && pg.Extra == ExtraKeymap && strings.TrimSpace(p.search.Text()) == ""
}

// Recording reports whether the keymap page records a shortcut: the host
// must then leave key events (its shortcuts, Escape) to the dialog.
func (p *Panel) Recording() bool {
	p.init()
	return p.keymapShown() && p.Keymap.Recording()
}

// Update returns the user's changes.
func (p *Panel) Update(gtx layout.Context) []Event {
	p.init()
	var out []Event
	// The keymap page first: its recorders take every key while active.
	if p.keymapShown() {
		for _, e := range p.Keymap.Update(gtx) {
			out = append(out, KeymapEvent{e})
		}
	} else {
		p.Keymap.CancelRecording()
	}
	if p.closeBtn.Clicked(gtx) {
		out = append(out, CloseEvent{})
	}
	p.search.Update(gtx)
	if i, ok := p.scopeSel.Update(gtx); ok {
		p.Scope = ScopeGlobal
		if i == 1 {
			p.Scope = ScopeProject
		}
	}
	p.coreRow.Update(gtx)
	p.plugRow.Update(gtx)
	for i := range p.Pages {
		pg := &p.Pages[i]
		if r := p.nav[pg.ID]; r != nil {
			if c, _, _ := r.Update(gtx); c {
				p.Selected = pg.ID
				p.search.SetText("")
			}
		}
		if m := pg.Module; m != nil {
			b := p.box(m.ID)
			b.State = checkState(m.Enabled)
			if b.Update(gtx) {
				out = append(out, ModuleEvent{ID: m.ID, Enable: b.Checked()})
			}
		}
		for _, f := range pg.Fields {
			if st := p.fields[f.Key]; st != nil {
				out = append(out, st.update(gtx, p, f)...)
			}
		}
	}
	if p.checkNow.Clicked(gtx) {
		out = append(out, CheckNowEvent{})
	}
	if !p.NoProject {
		for _, e := range p.Interpreter.Update(gtx) {
			out = append(out, RuntimeEvent{e})
		}
		for _, e := range p.Mappings.Update(gtx) {
			out = append(out, RuntimeEvent{e})
		}
		for _, e := range p.Hints.Update(gtx) {
			out = append(out, RuntimeEvent{e})
		}
	}
	return out
}

func (p *Panel) box(id string) *atoms.Checkbox {
	b := p.modBoxes[id]
	if b == nil {
		b = &atoms.Checkbox{Label: "Enabled"}
		p.modBoxes[id] = b
	}
	return b
}

func (p *Panel) navRow(id string) *molecules.TreeRow {
	r := p.nav[id]
	if r == nil {
		r = &molecules.TreeRow{Depth: 1}
		p.nav[id] = r
	}
	return r
}

func checkState(on bool) atoms.CheckState {
	if on {
		return atoms.Checked
	}
	return atoms.Unchecked
}

// Layout draws the dialog (Size dp, bounded by the constraints).
func (p *Panel) Layout(gtx layout.Context, th *tokens.Theme) layout.Dimensions {
	p.init()
	size := image.Pt(min(gtx.Dp(unit.Dp(Size.X)), gtx.Constraints.Max.X), min(gtx.Dp(unit.Dp(Size.Y)), gtx.Constraints.Max.Y))
	gtx.Constraints = layout.Exact(size)
	return atoms.Surface{Kind: atoms.SurfacePanel, Border: true, Rounded: true}.Layout(gtx, th, func(gtx layout.Context) layout.Dimensions {
		return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return layout.Inset{Left: th.Space.L, Right: th.Space.S, Top: th.Space.S, Bottom: th.Space.S}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
						layout.Flexed(1, atoms.Heading("Settings").Fn(th)),
						layout.Rigid(func(gtx layout.Context) layout.Dimensions { return p.closeBtn.Layout(gtx, th) }),
					)
				})
			}),
			layout.Rigid(atoms.Divider{}.Fn(th)),
			layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
				return layout.Flex{}.Layout(gtx,
					layout.Rigid(func(gtx layout.Context) layout.Dimensions { return p.layoutNav(gtx, th) }),
					layout.Rigid(atoms.Divider{Vertical: true}.Fn(th)),
					layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
						return layout.UniformInset(th.Space.L).Layout(gtx, func(gtx layout.Context) layout.Dimensions {
							return p.layoutContent(gtx, th)
						})
					}),
				)
			}),
		)
	})
}

// NavWidth is the width of the tree column in dp.
const NavWidth = 220

func (p *Panel) layoutNav(gtx layout.Context, th *tokens.Theme) layout.Dimensions {
	gtx.Constraints = layout.Exact(image.Pt(gtx.Dp(NavWidth), gtx.Constraints.Max.Y))
	query := p.search.Text()
	visible := map[string]bool{}
	for _, pg := range Results(p.Pages, query) {
		visible[pg.ID] = true
	}
	cur := p.Page()
	type row struct {
		r     *molecules.TreeRow
		title string
	}
	var rows []row
	add := func(header *molecules.TreeRow, plugins bool) {
		var kids []row
		for i := range p.Pages {
			pg := &p.Pages[i]
			if pg.Plugin() != plugins || (query != "" && !visible[pg.ID]) {
				continue
			}
			r := p.navRow(pg.ID)
			r.Name = pg.Title
			r.Selected = query == "" && cur != nil && cur.ID == pg.ID
			r.Detail = ""
			r.NameColor = th.Color.Text
			if m := pg.Module; m != nil && !m.Enabled {
				r.Detail = "off"
				r.NameColor = th.Color.TextMuted
			}
			kids = append(kids, row{r: r})
		}
		if len(kids) == 0 {
			return
		}
		rows = append(rows, row{r: header})
		if header.Expanded {
			rows = append(rows, kids...)
		}
	}
	add(&p.coreRow, false)
	add(&p.plugRow, true)
	return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.UniformInset(th.Space.S).Layout(gtx, func(gtx layout.Context) layout.Dimensions { return p.search.Layout(gtx, th) })
		}),
		layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
			return p.navList.Layout(gtx, th, len(rows), func(gtx layout.Context, i int) layout.Dimensions {
				return rows[i].r.Layout(gtx, th)
			})
		}),
	)
}

func (p *Panel) layoutContent(gtx layout.Context, th *tokens.Theme) layout.Dimensions {
	var rows []layout.Widget
	spacer := func(h unit.Dp) { rows = append(rows, layout.Spacer{Height: h}.Layout) }
	if q := p.search.Text(); strings.TrimSpace(q) != "" {
		res := Results(p.Pages, q)
		rows = append(rows, atoms.Strong(fmt.Sprintf("Search: %q", strings.TrimSpace(q))).Fn(th))
		if len(res) == 0 {
			rows = append(rows, atoms.Small("No setting matches.").Fn(th))
		}
		for _, pg := range res {
			title := pg.Title
			if pg.Plugin() {
				title = "Plugins › " + title
			} else {
				title = "Core › " + title
			}
			spacer(th.Space.M)
			rows = append(rows, molecules.SectionHeader{Title: title}.Fn(th))
			spacer(th.Space.S)
			for _, f := range pg.Fields {
				rows = append(rows, p.fieldRow(th, f))
			}
		}
		return p.scroll.Layout(gtx, th, len(rows), func(gtx layout.Context, i int) layout.Dimensions { return rows[i](gtx) })
	}
	pg := p.Page()
	if pg == nil {
		return atoms.Small("No settings.").Layout(gtx, th)
	}
	if pg.Extra == ExtraKeymap {
		// The keymap editor scrolls its own table and fills the page.
		return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
			layout.Rigid(p.pageHeader(th, pg)),
			layout.Rigid(layout.Spacer{Height: th.Space.M}.Layout),
			layout.Flexed(1, func(gtx layout.Context) layout.Dimensions { return p.Keymap.Layout(gtx, th) }),
		)
	}
	rows = append(rows, p.pageHeader(th, pg))
	spacer(th.Space.M)
	if m := pg.Module; m != nil && !m.Enabled {
		rows = append(rows, atoms.Small("Enable the plugin to use and configure it.").Fn(th))
	} else {
		group := ""
		for _, f := range pg.Fields {
			if f.Group != "" && f.Group != group {
				group = f.Group
				spacer(th.Space.S)
				rows = append(rows, molecules.SectionHeader{Title: group}.Fn(th))
				spacer(th.Space.S)
			}
			rows = append(rows, p.fieldRow(th, f))
		}
		if pg.Plugin() && len(pg.Fields) == 0 {
			rows = append(rows, atoms.Small("This plugin has no settings.").Fn(th))
		}
		rows = append(rows, p.extra(th, pg.Extra)...)
	}
	return p.scroll.Layout(gtx, th, len(rows), func(gtx layout.Context, i int) layout.Dimensions { return rows[i](gtx) })
}

// pageHeader: title, scope selector (pages with project settings) and, on
// plugin pages, the on/off switch with state and dependencies.
func (p *Panel) pageHeader(th *tokens.Theme, pg *Page) layout.Widget {
	project := false
	for _, f := range pg.Fields {
		project = project || f.ProjectScope
	}
	return func(gtx layout.Context) layout.Dimensions {
		return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
					layout.Flexed(1, atoms.Strong(pg.Title).Fn(th)),
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						if !project || p.NoProject || (pg.Module != nil && !pg.Module.Enabled) {
							return layout.Dimensions{}
						}
						p.scopeSel.Selected = 0
						if p.Scope == ScopeProject {
							p.scopeSel.Selected = 1
						}
						gtx.Constraints.Max.X = gtx.Dp(180)
						gtx.Constraints.Min.X = gtx.Constraints.Max.X
						return p.scopeSel.Layout(gtx, th)
					}),
				)
			}),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				if pg.Module == nil {
					return layout.Dimensions{}
				}
				return layout.Inset{Top: th.Space.S}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					return p.moduleRow(gtx, th, *pg.Module)
				})
			}),
		)
	}
}

func (p *Panel) moduleRow(gtx layout.Context, th *tokens.Theme, m Module) layout.Dimensions {
	b := p.box(m.ID)
	b.State = checkState(m.Enabled)
	return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
				layout.Rigid(func(gtx layout.Context) layout.Dimensions { return b.Layout(gtx, th) }),
				layout.Rigid(layout.Spacer{Width: th.Space.S}.Layout),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					switch {
					case m.Error != "":
						return atoms.Badge{Text: "error", Kind: atoms.BadgeDanger}.Layout(gtx, th)
					case m.Running:
						return atoms.Badge{Text: "running", Kind: atoms.BadgeSuccess}.Layout(gtx, th)
					case m.Enabled:
						return atoms.Badge{Text: "enabled"}.Layout(gtx, th)
					}
					return atoms.Badge{Text: "off"}.Layout(gtx, th)
				}),
			)
		}),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			var parts []string
			if len(m.DependsOn) > 0 {
				parts = append(parts, "requires "+strings.Join(m.DependsOn, ", "))
			}
			if len(m.Dependents) > 0 {
				parts = append(parts, "needed by "+strings.Join(m.Dependents, ", "))
			}
			if m.Error != "" {
				parts = append(parts, m.Error)
			}
			if len(parts) == 0 {
				return layout.Dimensions{}
			}
			return layout.Inset{Left: th.Size.Checkbox + th.Space.S}.Layout(gtx, atoms.Small(strings.Join(parts, " · ")).Fn(th))
		}),
	)
}

func (p *Panel) extra(th *tokens.Theme, kind string) []layout.Widget {
	switch kind {
	case ExtraFontPreview:
		return []layout.Widget{func(gtx layout.Context) layout.Dimensions {
			gtx.Constraints.Max.X = min(gtx.Constraints.Max.X, gtx.Dp(460))
			gtx.Constraints.Min.X = gtx.Constraints.Max.X
			return atoms.Surface{Kind: atoms.SurfaceEditor, Border: true, Rounded: true}.Layout(gtx, th, func(gtx layout.Context) layout.Dimensions {
				return layout.UniformInset(th.Space.S).Layout(gtx, atoms.Code(FontPreview).SingleLine().Fn(th))
			})
		}}
	case ExtraUpdates:
		return []layout.Widget{func(gtx layout.Context) layout.Dimensions {
			return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
				layout.Rigid(func(gtx layout.Context) layout.Dimensions { return p.checkNow.Layout(gtx, th) }),
				layout.Rigid(layout.Spacer{Width: th.Space.M}.Layout),
				layout.Rigid(atoms.Small(p.UpdateStatus).Fn(th)),
			)
		}}
	case ExtraInterpreter:
		if p.NoProject {
			return []layout.Widget{atoms.Small("Open a project to choose its interpreter.").Fn(th)}
		}
		return []layout.Widget{
			atoms.Small("Local PHP binaries and Docker Compose services of this project.").Fn(th),
			layout.Spacer{Height: th.Space.S}.Layout,
			func(gtx layout.Context) layout.Dimensions {
				gtx.Constraints.Max.X = min(gtx.Constraints.Max.X, gtx.Dp(480))
				return p.Interpreter.Layout(gtx, th)
			}, layout.Spacer{Height: th.Space.L}.Layout,
			molecules.SectionHeader{Title: "Path mappings"}.Fn(th),
			func(gtx layout.Context) layout.Dimensions {
				gtx.Constraints.Max.Y = min(gtx.Constraints.Max.Y, gtx.Dp(160))
				return p.Mappings.Layout(gtx, th)
			}, layout.Spacer{Height: th.Space.M}.Layout,
			molecules.SectionHeader{Title: "Xdebug"}.Fn(th),
			func(gtx layout.Context) layout.Dimensions {
				gtx.Constraints.Max.Y = min(gtx.Constraints.Max.Y, gtx.Dp(140))
				return p.Hints.Layout(gtx, th)
			},
		}
	}
	return nil
}
