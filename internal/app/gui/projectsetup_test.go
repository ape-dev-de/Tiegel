package gui

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/ape-dev-de/tiegel/internal/app/gui/controller"
	rtui "github.com/ape-dev-de/tiegel/internal/ui/organisms/runtime"
	settingsui "github.com/ape-dev-de/tiegel/internal/ui/organisms/settings"
)

// TestSettingsCandidatesAndSetup: the settings dialog shows the Project
// section first and human titles, offers discovered values as a choice
// list (choosing one writes it), lists compose services found around the
// project with "Use", and the setup sheet applies proposals only on Apply.
func TestSettingsCandidatesAndSetup(t *testing.T) {
	e := newEnvWith(t, true, map[string]string{
		"vendor/bin/phpunit":  "#!/bin/sh\n",
		"phpunit.xml.dist":    "<phpunit/>",
		"docker/compose.yaml": "services:\n  php:\n    image: php:8.3-fpm\n    volumes:\n      - ..:/app\n",
	}, nil)
	c := e.ctl
	e.v.openSettings()
	e.waitFor("settings schema", func() bool { return c.Schema != nil })
	pages := settingsPages(c)
	if pages[0].ID != "core:Project" || pages[0].Section != "Project" || pages[len(pages)-1].Extra != settingsui.ExtraPlugins {
		t.Fatalf("first/last page %+v / %+v", pages[0], pages[len(pages)-1])
	}
	byID := map[string]settingsui.Page{}
	for _, pg := range pages {
		byID[pg.ID] = pg
	}
	if pg := byID["plugin:php-runtime"]; pg.Title != "PHP Interpreter" || pg.Section != "Project" || pg.Extra != settingsui.ExtraInterpreter {
		t.Fatalf("php-runtime page %+v", pg)
	}
	if pg := byID["plugin:docker"]; pg.Section != "Docker & Compose" {
		t.Fatalf("docker page %+v", pg)
	}
	if pg := byID["plugin:phpunit"]; pg.Title != "PHPUnit" || pg.Section != "Tests" {
		t.Fatalf("phpunit page %+v", pg)
	}
	if _, ok := byID["core:Interpreter"]; ok {
		t.Fatal("empty core Interpreter page still listed")
	}

	// Candidates of phpunit.executable are loaded and offered.
	e.waitFor("phpunit candidates", func() bool {
		list, loading := c.Candidates("phpunit.executable")
		return !loading && len(list) > 0
	})
	var exe settingsui.Field
	for _, f := range settingsPages(c) {
		for _, fl := range f.Fields {
			if fl.Key == "phpunit.executable" {
				exe = fl
			}
		}
	}
	if exe.CandidateSource == "" || len(exe.Candidates) == 0 || !exe.Candidates[0].Recommended || !strings.HasSuffix(exe.Candidates[0].Label, "vendor/bin/phpunit") {
		t.Fatalf("field %+v", exe)
	}
	e.v.settings.Selected = "plugin:phpunit"
	e.settle()
	e.v.handleSettingsEvent(settingsui.SetEvent{Key: "phpunit.executable", Scope: settingsui.ScopeProject, Value: exe.Candidates[0].Value})
	e.waitFor("chosen candidate stored", func() bool {
		var v string
		return json.Unmarshal(c.EffectiveSetting("phpunit.executable"), &v) == nil && v == exe.Candidates[0].Value
	})
	e.v.handleSettingsEvent(settingsui.RefreshCandidatesEvent{Key: "phpunit.executable"})
	e.settle()

	// The compose file in docker/ is discovered, not configured: Use.
	e.v.settings.Selected = "plugin:php-runtime"
	e.waitFor("discovered", func() bool { return !c.Discovered.Loading && len(c.Discovered.Items) > 0 })
	e.settle()
	if len(e.v.settings.Discovered.Items) == 0 || !strings.Contains(e.v.settings.Discovered.Items[0].Where, "docker/compose.yaml") {
		t.Fatalf("discovered list %+v", e.v.settings.Discovered.Items)
	}
	id := c.Discovered.Items[0].ID
	e.v.handleRuntimeEvent(rtUse(id))
	e.waitFor("compose interpreter selected", func() bool {
		for _, i := range c.Interpreters {
			if i.ID == id && i.Selected {
				return true
			}
		}
		return false
	})
	e.v.closeOverlay()

	// Project Setup…: the sheet shows the remaining proposals; Apply
	// writes the chosen ones and closes it.
	e.v.openProjectSetup()
	e.waitFor("setup proposal", func() bool { return !c.Setup.Busy })
	e.settle()
	if e.v.popup == nil || e.v.popup.Title != "Project Setup" {
		t.Fatalf("popup %+v", e.v.popup)
	}
	var cfg *controller.SetupItem
	for i := range c.Setup.Items {
		if c.Setup.Items[i].Key == "phpunit.configuration" {
			cfg = &c.Setup.Items[i]
		}
	}
	if cfg == nil {
		t.Fatalf("no phpunit.configuration proposal in %+v", c.Setup.Items)
	}
	c.ApplySetup([]controller.SetupValue{{Key: cfg.Key, Value: cfg.Proposed.Value}})
	e.waitFor("setup applied", func() bool { return !c.Setup.Open && !c.Setup.Busy })
	e.settle()
	if e.v.popup != nil {
		t.Fatal("sheet still open after Apply")
	}
	// Dismissing stores the choice; the offer stays off.
	c.DismissSetup()
	e.settle()
	if c.Setup.Offer || !c.Setup.Dismissed {
		t.Fatalf("dismiss %+v", c.Setup)
	}
	e.v.noSetupOffer = false
	c.Setup.Offer = true
	e.frame()
	if e.v.popup == nil {
		t.Fatal("offer not shown")
	}
	e.v.ClosePopup()
	e.frame()
}

func rtUse(id string) rtui.Event { return rtui.UseDiscoveredEvent{ID: id} }
