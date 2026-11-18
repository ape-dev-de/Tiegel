package gui

import (
	"strings"

	"gioui.org/layout"

	"github.com/ape-dev-de/tiegel/internal/api"
	"github.com/ape-dev-de/tiegel/internal/editor/buffer"
	"github.com/ape-dev-de/tiegel/internal/editor/keymap"
	"github.com/ape-dev-de/tiegel/internal/run"
)

// External tools (AP17.2): run configurations of the core type
// "external-tool" appear under Tools › External Tools, one action each
// (ID api.ExternalToolAction, bindable in the keymap, D414). Starting one
// sends the editor context (file, caret, selection); the backend expands
// the macros. "Configure External Tools…" opens the editor for a new
// tool.

// ActConfigureTools opens the editor of a new external tool.
const ActConfigureTools keymap.Action = "externalTools.configure"

// externalToolsMenu is the submenu of the tools.
const externalToolsMenu = "Tools/External Tools"

type toolsDock struct {
	v *View
	// ids are the registered tool actions; sig identifies the tool set
	// (IDs and names) the actions and the keymap were built for.
	ids []keymap.Action
	sig string
}

func installExternalTools(v *View) *toolsDock {
	d := &toolsDock{v: v}
	v.Registry.AddAction(Action{ID: ActConfigureTools, Title: "Configure External Tools…", Menu: externalToolsMenu, Section: "configure",
		Run: func(*View) { d.configure() }})
	v.Registry.AddFrameHandler(d.frame)
	return d
}

// signature identifies a tool set.
func toolSignature(tools []api.RunConfigInfo) string {
	var sb strings.Builder
	for _, t := range tools {
		sb.WriteString(t.ID + "\x00" + t.Name + "\x00")
	}
	return sb.String()
}

// defs returns the keymap definitions of the current tools.
func (d *toolsDock) defs() []keymap.Def {
	tools := d.v.ctl.Run.Tools()
	cfgs := make([]run.Config, len(tools))
	for i, t := range tools {
		cfgs[i] = t.Config
	}
	return api.ExternalToolDefs(cfgs)
}

// frame mirrors the tools into actions when the configurations changed.
func (d *toolsDock) frame(v *View, _ layout.Context) {
	tools := v.ctl.Run.Tools()
	sig := toolSignature(tools)
	if sig == d.sig {
		return
	}
	d.sig = sig
	for _, id := range d.ids {
		v.Registry.RemoveAction(id)
	}
	d.ids = d.ids[:0]
	for _, t := range tools {
		id, name := api.ExternalToolAction(t.ID), t.Name
		cfgID := t.ID
		v.Registry.AddAction(Action{ID: id, Title: name, Menu: externalToolsMenu, Section: "tools",
			Run: func(v *View) { d.run(cfgID, name) }})
		d.ids = append(d.ids, id)
	}
	// The keymap scheme gets the new actions (bindings of the user).
	v.keys.keymapSeen = ^uint64(0)
	v.syncKeymap()
}

// run starts tool cfgID with the context of the active editor.
func (d *toolsDock) run(cfgID, name string) {
	d.v.ctl.Status.Notice = "External tool: " + name
	d.v.ctl.Run.RunTool(cfgID, d.editorContext())
}

// editorContext returns the file, caret and selection of the active
// editor (nil without one).
func (d *toolsDock) editorContext() *run.MacroContext {
	s := d.v.activeSession()
	if s == nil {
		if t := d.v.ctl.ActiveTab(); t != nil && t.File != "" {
			return &run.MacroContext{File: t.File}
		}
		return nil
	}
	s.doc.Lock()
	defer s.doc.Unlock()
	b := s.doc.Buffer()
	sel := s.ed.Cursor().Primary()
	p := b.OffsetToPos(sel.Head)
	mc := &run.MacroContext{File: s.file, Line: p.Line + 1, Column: buffer.RuneCol(b.Line(p.Line), p.Col) + 1}
	if !sel.Empty() {
		mc.SelectedText = string(b.Slice(sel.Start(), sel.End()))
	}
	return mc
}

// configure opens the configuration editor for a new external tool.
func (d *toolsDock) configure() {
	if d.v.ctl.Project.Root == "" {
		d.v.ctl.Status.Notice = "External tools belong to a project – open one first"
		return
	}
	if t, ok := d.v.rund.typeOf(run.TypeExternalTool); ok {
		d.v.rund.openEditor(run.Config{Type: run.TypeExternalTool}, t)
		return
	}
	d.v.ctl.Run.LoadConfigs()
	d.v.ctl.Status.Notice = "Loading run configuration types – try again"
}
