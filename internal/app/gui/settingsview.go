package gui

import (
	"context"
	"encoding/json"

	"github.com/ape-dev-de/tiegel/internal/app/gui/controller"
	"github.com/ape-dev-de/tiegel/internal/app/gui/native"
	settingsui "github.com/ape-dev-de/tiegel/internal/ui/organisms/settings"
)

// Core pages with special parts drawn by the dialog (D216). The page
// names are the Spec.Group values of the core settings. The interpreter
// part lives on the php-runtime page ("Project › PHP Interpreter", D525).
var corePageExtras = map[string]string{
	"Editor":  settingsui.ExtraFontPreview,
	"Updates": settingsui.ExtraUpdates,
	"Keymap":  settingsui.ExtraKeymap, // the keymap editor replaces the raw specs
}

// Settings navigation (D525): sections in this order; plugins name their
// section and page title (module.PageProvider), others keep their ID as
// title under "Plugins".
const (
	sectionProject = "Project"
	sectionGeneral = "General"
	sectionPHP     = "PHP"
	sectionDocker  = "Docker & Compose"
	sectionTests   = "Tests"
	sectionQA      = "Quality Tools"
	sectionVCS     = "Version Control"
	sectionAI      = "AI"
	sectionPlugins = "Plugins"
)

// sectionOrder is the order of the sections in the navigation.
var sectionOrder = []string{sectionProject, sectionGeneral, sectionPHP, sectionDocker, sectionTests, sectionQA, sectionVCS, sectionAI, sectionPlugins}

// projectKeys are shown on the "Project" page whatever page declares them.
var projectKeys = map[string]bool{"project.phpRoot": true, "project.setupDismissed": true}

// keyInterpreter is the project interpreter; the dialog writes it through
// interp.select (it is read-only for settings.set).
const keyInterpreter = "php-runtime.interpreter"

// settingsPages builds the dialog tree from the settings schema: the
// Project section first (PHP root, setup, interpreter), core pages in
// schema order (pages without settings and without a special part are
// left out), then the plugin pages grouped by section, and the plugin
// overview.
func settingsPages(c *controller.Controller) []settingsui.Page {
	s := c.Schema
	if s == nil {
		return nil
	}
	var pages []settingsui.Page
	project := settingsui.Page{ID: "core:Project", Title: "Project", Section: sectionProject, Extra: settingsui.ExtraProject}
	byGroup := map[string][]settingsui.Field{}
	for _, sp := range s.Core {
		f := settingsField(c, sp)
		if sp.Key == controller.KeyEditorFont {
			f.Choices, f.ChoiceDefault = c.MonoFonts, "System default"
		}
		if projectKeys[sp.Key] {
			project.Fields = append(project.Fields, f)
			continue
		}
		byGroup[sp.Group] = append(byGroup[sp.Group], f)
	}
	var core []settingsui.Page
	for _, g := range s.Pages {
		fields, extra := byGroup[g], corePageExtras[g]
		if len(fields) == 0 && extra == "" {
			continue
		}
		if extra == settingsui.ExtraKeymap {
			fields = nil // changed through keymap.* only (keymapview.go)
		}
		core = append(core, settingsui.Page{ID: "core:" + g, Title: g, Section: sectionGeneral, Fields: fields, Extra: extra})
	}
	var plugins []settingsui.Page
	for _, m := range s.Modules {
		mod := &settingsui.Module{ID: m.ID, DependsOn: m.DependsOn, Dependents: c.Dependents(m.ID), Enabled: m.Enabled, Running: m.Running, Error: m.Error}
		section, title := m.Section, m.Title
		if section == "" {
			section = sectionPlugins
		}
		if title == "" {
			title = m.ID
		}
		pg := settingsui.Page{ID: "plugin:" + m.ID, Title: title, Section: section, Module: mod}
		if m.ID == "php-runtime" {
			pg.Extra = settingsui.ExtraInterpreter
		}
		for _, sp := range m.Settings {
			f := settingsField(c, sp)
			if projectKeys[sp.Key] {
				project.Fields = append(project.Fields, f)
				continue
			}
			f.Group = sp.Group
			pg.Fields = append(pg.Fields, f)
		}
		plugins = append(plugins, pg)
	}
	pages = append(pages, project)
	all := append(core, plugins...)
	known := map[string]bool{}
	for _, sec := range sectionOrder {
		known[sec] = true
		for _, pg := range all {
			if pg.Section == sec {
				pages = append(pages, pg)
			}
		}
	}
	for _, pg := range all { // sections the dialog does not know yet
		if !known[pg.Section] {
			pages = append(pages, pg)
		}
	}
	pages = append(pages, settingsui.Page{ID: "plugins", Title: "All Plugins", Section: sectionPlugins, Extra: settingsui.ExtraPlugins})
	return pages
}

func settingsField(c *controller.Controller, sp controller.SettingSpec) settingsui.Field {
	f := settingsui.Field{
		Key: sp.Key, Kind: sp.Kind, Label: sp.Label, Help: sp.Help, Options: sp.Options, Min: sp.Min, Max: sp.Max,
		ProjectScope: sp.Scope == "project", Source: sp.Source, Invalid: sp.Invalid,
		HasGlobal: len(sp.Global) > 0, HasProject: len(sp.Project) > 0,
		ReadOnly: sp.ReadOnly, ReadOnlyHint: sp.ReadOnlyHint,
	}
	if len(sp.Value) > 0 {
		_ = json.Unmarshal(sp.Value, &f.Value)
	}
	if sp.Key == keyInterpreter && c.Project.Open {
		// Chosen from the candidates; written through interp.select.
		f.ReadOnly, f.ReadOnlyHint = false, ""
	}
	if sp.Candidates != "" && !f.ReadOnly && (c.Project.Open || sp.Scope == "global") {
		f.CandidateSource = sp.Candidates
		c.EnsureCandidates(sp.Key)
		list, loading := c.Candidates(sp.Key)
		f.CandidatesLoading = loading
		for _, cand := range list {
			f.Candidates = append(f.Candidates, settingsui.Candidate{Value: cand.Value, Label: cand.Label, Detail: cand.Detail, Recommended: cand.Recommended})
		}
	}
	return f
}

// handleSettingsEvent applies an event of the settings dialog.
func (v *View) handleSettingsEvent(e settingsui.Event) {
	c := v.ctl
	switch e := e.(type) {
	case settingsui.CloseEvent:
		v.closeOverlay()
	case settingsui.ModuleEvent:
		c.SetModuleEnabled(e.ID, e.Enable)
	case settingsui.SetEvent:
		if e.Key == controller.KeyEditorFontSize {
			v.zoomDelta = 0
		}
		if e.Key == keyInterpreter {
			if id, ok := e.Value.(string); ok && id != "" {
				c.SelectInterpreter(id)
				v.mappings = nil
			}
			return
		}
		c.SetSettingValue(e.Key, e.Scope, e.Value)
	case settingsui.ResetEvent:
		c.ResetSetting(e.Key, e.Scope)
	case settingsui.ChoosePathEvent:
		v.choosePath(e)
	case settingsui.CheckNowEvent:
		c.CheckUpdate()
	case settingsui.RefreshCandidatesEvent:
		c.RefreshCandidates(e.Key)
	case settingsui.ProjectSetupEvent:
		v.openProjectSetup()
	case settingsui.RuntimeEvent:
		v.handleRuntimeEvent(e.Event)
	case settingsui.KeymapEvent:
		v.handleKeymapEvent(e.Event)
	}
}

// choosePath lets the user pick a directory for a path setting (native
// dialog where available; otherwise the path is typed).
func (v *View) choosePath(e settingsui.ChoosePathEvent) {
	if !native.FolderDialogSupported() || v.noNativeDialog {
		v.ctl.Status.Notice = "Type the path into the field"
		return
	}
	post := v.Post
	start := v.ctl.Project.Root
	go func() {
		dir, ok, err := native.ChooseFolder(context.Background(), "Choose Path", start)
		post(func() {
			switch {
			case err != nil:
				v.ctl.Status.Notice = "Choose: " + err.Error()
			case ok:
				v.ctl.SetSettingValue(e.Key, e.Scope, dir)
			}
		})
	}()
}
