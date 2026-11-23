package gui

import (
	"path/filepath"

	"gioui.org/layout"

	"github.com/ape-dev-de/tiegel/internal/api"
	"github.com/ape-dev-de/tiegel/internal/app/gui/controller"
	"github.com/ape-dev-de/tiegel/internal/editor/keymap"
	"github.com/ape-dev-de/tiegel/internal/ui/atoms"
	"github.com/ape-dev-de/tiegel/internal/ui/editorview"
)

// Composer scripts (AP17.2): a green play arrow in the gutter of the
// project's composer.json at every script key (Run / Modify Run
// Configuration…), the chooser "Run Composer Script…" (Tools menu, Find
// Action, context menus of composer.json). The backend is the composer
// plugin's composer.* namespace; runs appear in the Run tool window.

// ActComposerRun is the action of the script chooser.
const ActComposerRun keymap.Action = "composer.runScript"

type composerDock struct {
	v       *View
	project string
}

func installComposer(v *View) *composerDock {
	d := &composerDock{v: v}
	r := v.Registry
	r.AddFrameHandler(d.frame)
	r.AddGutterProvider(d.markers)
	r.AddGutterClickHandler(d.gutterClick)
	r.AddAction(Action{ID: ActComposerRun, Title: "Run Composer Script…", Menu: "Tools", Section: "composer", Run: func(*View) { d.chooser() }})
	menu := func(_ *View, t ContextTarget) []MenuItem {
		files := append([]string{t.File}, t.Paths...)
		for _, f := range files {
			if f != "" && d.isComposerJSON(f) {
				return []MenuItem{{Title: "Run Composer Script…", Icon: "run", Run: d.chooser}}
			}
		}
		return nil
	}
	r.AddContextMenu(ContextEditor, menu)
	r.AddContextMenu(ContextProjectTree, menu)
	return d
}

func (d *composerDock) ctl() *controller.Composer { return d.v.ctl.Composer }

// frame follows the project.
func (d *composerDock) frame(v *View, _ layout.Context) {
	if root := v.ctl.Project.Root; root != d.project {
		d.project = root
		d.ctl().ProjectChanged()
	}
}

func (d *composerDock) isComposerJSON(file string) bool {
	root := d.v.ctl.PHPRoot()
	return root != "" && filepath.Clean(file) == filepath.Join(root, "composer.json")
}

// markers puts a play arrow at every script key of composer.json.
func (d *composerDock) markers(file string) editorview.MarkerProvider {
	at := d.ctl().ScriptsAt(file)
	if len(at) == 0 {
		return nil
	}
	return editorview.MarkerFunc(func(line int) []editorview.Marker {
		if _, ok := at[line+1]; !ok {
			return nil
		}
		return []editorview.Marker{{Kind: editorview.MarkerIcon, Glyph: atoms.GlyphRun, Tone: editorview.ToneSuccess}}
	})
}

// gutterClick opens the run menu of the script on the clicked line.
func (d *composerDock) gutterClick(file string, ev editorview.GutterClickEvent) bool {
	if ev.Area != editorview.GutterIcons || ev.Secondary || ev.Mods != 0 {
		return false
	}
	s, ok := d.ctl().ScriptsAt(file)[ev.Line+1]
	if !ok {
		return false
	}
	d.v.ShowMenu("composer "+s.Name, d.scriptMenu(s))
	return true
}

// scriptMenu are the run entries of a script.
func (d *composerDock) scriptMenu(s api.ComposerScript) []MenuItem {
	name := s.Name
	return []MenuItem{
		{Title: "Run '" + name + "'", Icon: "run", Run: func() { d.ctl().Run(name) }},
		{Title: "Modify Run Configuration…", Icon: "edit", Run: func() { d.modifyConfig(name) }},
	}
}

// modifyConfig opens the configuration editor with an unsaved
// configuration of the script.
func (d *composerDock) modifyConfig(script string) {
	cfg, ok := d.ctl().ConfigFor(script)
	if ok {
		if t, ok := d.v.rund.typeOf(cfg.Type); ok {
			d.v.rund.openEditor(cfg, t)
			return
		}
	}
	d.v.ctl.Run.LoadConfigs()
	d.v.ctl.Status.Notice = "Loading run configuration types – try again (is the composer plugin enabled?)"
}

// chooser lists the scripts (fresh from composer.json) and runs the
// chosen one.
func (d *composerDock) chooser() {
	if d.v.ctl.Project.Root == "" {
		d.v.ctl.Status.Notice = "Run Composer Script: no project open"
		return
	}
	d.ctl().Load(func(err error) {
		if err != nil {
			d.v.ctl.Status.Notice = "Composer: " + err.Error()
			return
		}
		res := d.ctl().Result
		if !res.Exists || len(res.Scripts) == 0 {
			d.v.ctl.Status.Notice = "Run Composer Script: composer.json has no scripts"
			return
		}
		items := make([]MenuItem, 0, len(res.Scripts))
		for _, s := range res.Scripts {
			name, title := s.Name, s.Name
			if s.Description != "" {
				title += " – " + s.Description
			}
			items = append(items, MenuItem{Title: title, Icon: "run", Run: func() { d.ctl().Run(name) }})
		}
		d.v.ShowMenu("Run Composer Script ("+res.Command+")", items)
	})
}
