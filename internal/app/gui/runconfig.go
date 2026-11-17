package gui

import (
	"fmt"
	"strconv"
	"strings"

	"gioui.org/layout"

	"github.com/ape-dev-de/tiegel/internal/api"
	"github.com/ape-dev-de/tiegel/internal/core/settings"
	"github.com/ape-dev-de/tiegel/internal/run"
	"github.com/ape-dev-de/tiegel/internal/ui/atoms"
	"github.com/ape-dev-de/tiegel/internal/ui/molecules"
	"github.com/ape-dev-de/tiegel/internal/ui/tokens"
)

// Run configuration editor: a popup with the name and one input per field
// of the configuration type (fields declared like settings specs): text
// for strings and paths, one entry per line for lists, a checkbox for
// booleans, a select for enums. Suggestions (composer scripts,
// interpreters) are listed below their field.

type cfgField struct {
	f     run.Field
	text  atoms.TextField
	check atoms.Checkbox
	sel   *molecules.Dropdown
	hint  string
}

type runConfigEditor struct {
	d     *runDock
	cfg   run.Config
	typ   api.RunConfigTypeInfo
	name  atoms.TextField
	items []*cfgField
}

// openEditor shows the editor for cfg (empty ID: a new configuration of
// type t).
func (d *runDock) openEditor(cfg run.Config, t api.RunConfigTypeInfo) {
	e := &runConfigEditor{d: d, cfg: cfg, typ: t}
	e.name.Editor.SingleLine = true
	e.name.Hint = "Name"
	e.name.SetText(cfg.Name)
	if cfg.Name == "" {
		e.name.SetText(t.Label)
	}
	for _, f := range t.Fields {
		it := &cfgField{f: f}
		it.text.Hint = f.Label
		it.text.Mono = true
		it.text.Editor.SingleLine = f.Kind != settings.KindList
		val, ok := cfg.Values[f.Key]
		if !ok {
			val = f.Default
		}
		switch f.Kind {
		case settings.KindBool:
			b, _ := val.(bool)
			it.check = atoms.Checkbox{Label: f.Label}
			if b {
				it.check.State = atoms.Checked
			}
		case settings.KindEnum:
			opts := make([]molecules.DropdownOption, len(f.Options))
			selected := -1
			for i, o := range f.Options {
				opts[i] = molecules.DropdownOption{Label: o}
				if s, _ := val.(string); s == o {
					selected = i
				}
			}
			it.sel = molecules.NewSelect(f.Label, opts...)
			it.sel.Selected = selected
		case settings.KindList:
			it.text.SetText(strings.Join(run.Config{Values: map[string]any{"v": val}}.Strings("v"), "\n"))
		default:
			if val != nil {
				it.text.SetText(fmt.Sprint(val))
			}
		}
		if s := t.Suggestions[f.Key]; len(s) > 0 {
			it.hint = "Suggestions: " + strings.Join(s, ", ")
		}
		e.items = append(e.items, it)
	}
	title := "New " + t.Label + " Configuration"
	if cfg.ID != "" {
		title = "Edit Run Configuration"
	}
	pop := &Popup{Title: title, Width: 560, Body: e.layout}
	pop.Buttons = append(pop.Buttons,
		&PopupButton{Text: "Save and Run", Kind: atoms.Primary, OnClick: func() { e.save(true) }},
		&PopupButton{Text: "Save", Kind: atoms.Secondary, OnClick: func() { e.save(false) }},
		&PopupButton{Text: "Cancel", Kind: atoms.Ghost})
	d.v.OpenPopup(pop)
	d.editor = e
}

// editorOpen returns the configuration editor while its popup is shown.
func (d *runDock) editorOpen() (*runConfigEditor, bool) {
	return d.editor, d.editor != nil && d.v.PopupOpen()
}

// values reads the inputs; empty optional fields are left out so their
// defaults apply.
func (e *runConfigEditor) values() (map[string]any, error) {
	vals := map[string]any{}
	for _, it := range e.items {
		f := it.f
		switch f.Kind {
		case settings.KindBool:
			vals[f.Key] = it.check.Checked()
		case settings.KindEnum:
			if i := it.sel.Selected; i >= 0 && i < len(f.Options) {
				vals[f.Key] = f.Options[i]
			}
		case settings.KindList:
			var list []string
			for _, l := range strings.Split(it.text.Text(), "\n") {
				if l = strings.TrimSpace(l); l != "" {
					list = append(list, l)
				}
			}
			if len(list) > 0 {
				vals[f.Key] = list
			}
		case settings.KindInt:
			if s := strings.TrimSpace(it.text.Text()); s != "" {
				n, err := strconv.Atoi(s)
				if err != nil {
					return nil, fmt.Errorf("%s: not a number", f.Label)
				}
				vals[f.Key] = n
			}
		default:
			if s := strings.TrimSpace(it.text.Text()); s != "" {
				vals[f.Key] = s
			}
		}
	}
	return vals, nil
}

func (e *runConfigEditor) save(andRun bool) {
	vals, err := e.values()
	if err != nil {
		e.d.v.ctl.Status.Notice = "Run configuration: " + err.Error()
		return
	}
	cfg := run.Config{ID: e.cfg.ID, Name: strings.TrimSpace(e.name.Text()), Type: e.typ.ID, Values: vals}
	r := e.d.runs()
	r.SaveConfig(cfg, func(saved run.Config) {
		r.SelectConfig(saved.ID)
		if andRun {
			r.RunConfig(saved.ID)
		}
	})
}

func (e *runConfigEditor) layout(gtx layout.Context, th *tokens.Theme) layout.Dimensions {
	for _, it := range e.items {
		if it.sel != nil {
			it.sel.Update(gtx)
		}
		if it.f.Kind == settings.KindBool {
			it.check.Update(gtx)
		}
	}
	row := func(w layout.Widget) layout.FlexChild {
		return layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Inset{Bottom: th.Space.S}.Layout(gtx, w)
		})
	}
	labeled := func(label string, w layout.Widget, hint string) layout.Widget {
		return func(gtx layout.Context) layout.Dimensions {
			children := []layout.FlexChild{
				layout.Rigid(atoms.Small(label).Fn(th)),
				layout.Rigid(layout.Spacer{Height: th.Space.XXS}.Layout),
				layout.Rigid(w),
			}
			if hint != "" {
				children = append(children, layout.Rigid(atoms.Small(hint).WithColor(th.Color.TextMuted).Fn(th)))
			}
			return layout.Flex{Axis: layout.Vertical}.Layout(gtx, children...)
		}
	}
	rows := []layout.FlexChild{
		row(labeled("Name", func(gtx layout.Context) layout.Dimensions { return e.name.Layout(gtx, th) }, e.typ.Description)),
	}
	for _, it := range e.items {
		label := it.f.Label
		if it.f.Required {
			label += " *"
		}
		hint := it.f.Help
		if it.hint != "" {
			hint = strings.TrimSpace(hint + " " + it.hint)
		}
		var w layout.Widget
		switch {
		case it.f.Kind == settings.KindBool:
			w = func(gtx layout.Context) layout.Dimensions { return it.check.Layout(gtx, th) }
			label = ""
		case it.sel != nil:
			w = func(gtx layout.Context) layout.Dimensions { return it.sel.Layout(gtx, th) }
		default:
			w = func(gtx layout.Context) layout.Dimensions { return it.text.Layout(gtx, th) }
		}
		rows = append(rows, row(labeled(label, w, hint)))
	}
	return layout.Flex{Axis: layout.Vertical}.Layout(gtx, rows...)
}
