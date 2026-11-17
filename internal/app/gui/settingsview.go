package gui

import (
	"context"
	"encoding/json"

	"github.com/ape-dev-de/tiegel/internal/app/gui/controller"
	"github.com/ape-dev-de/tiegel/internal/app/gui/native"
	settingsui "github.com/ape-dev-de/tiegel/internal/ui/organisms/settings"
)

// Core pages with special parts drawn by the dialog (D216). The page
// names are the Spec.Group values of the core settings.
var corePageExtras = map[string]string{
	"Editor":      settingsui.ExtraFontPreview,
	"Interpreter": settingsui.ExtraInterpreter,
	"Updates":     settingsui.ExtraUpdates,
}

// settingsPages builds the dialog tree from the settings schema: core
// pages in schema order (pages without settings and without a special
// part are left out), then one page per plugin.
func settingsPages(c *controller.Controller) []settingsui.Page {
	s := c.Schema
	if s == nil {
		return nil
	}
	var pages []settingsui.Page
	byGroup := map[string][]settingsui.Field{}
	for _, sp := range s.Core {
		f := settingsField(sp)
		if sp.Key == controller.KeyEditorFont {
			f.Choices, f.ChoiceDefault = c.MonoFonts, "System default"
		}
		byGroup[sp.Group] = append(byGroup[sp.Group], f)
	}
	for _, g := range s.Pages {
		fields, extra := byGroup[g], corePageExtras[g]
		if len(fields) == 0 && extra == "" {
			continue
		}
		pages = append(pages, settingsui.Page{ID: "core:" + g, Title: g, Fields: fields, Extra: extra})
	}
	for _, m := range s.Modules {
		mod := &settingsui.Module{ID: m.ID, DependsOn: m.DependsOn, Dependents: c.Dependents(m.ID), Enabled: m.Enabled, Running: m.Running, Error: m.Error}
		pg := settingsui.Page{ID: "plugin:" + m.ID, Title: m.ID, Module: mod}
		for _, sp := range m.Settings {
			pg.Fields = append(pg.Fields, settingsField(sp))
		}
		pages = append(pages, pg)
	}
	return pages
}

func settingsField(sp controller.SettingSpec) settingsui.Field {
	f := settingsui.Field{
		Key: sp.Key, Kind: sp.Kind, Label: sp.Label, Help: sp.Help, Options: sp.Options, Min: sp.Min, Max: sp.Max,
		ProjectScope: sp.Scope == "project", Source: sp.Source, Invalid: sp.Invalid,
		HasGlobal: len(sp.Global) > 0, HasProject: len(sp.Project) > 0,
	}
	if len(sp.Value) > 0 {
		_ = json.Unmarshal(sp.Value, &f.Value)
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
		c.SetSettingValue(e.Key, e.Scope, e.Value)
	case settingsui.ResetEvent:
		c.ResetSetting(e.Key, e.Scope)
	case settingsui.ChoosePathEvent:
		v.choosePath(e)
	case settingsui.CheckNowEvent:
		c.CheckUpdate()
	case settingsui.RuntimeEvent:
		v.handleRuntimeEvent(e.Event)
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
