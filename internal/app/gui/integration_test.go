package gui

import (
	"encoding/json"
	"testing"

	"github.com/ape-dev-de/tiegel/internal/app/gui/controller"
	"github.com/ape-dev-de/tiegel/internal/editor/keymap"
	"github.com/ape-dev-de/tiegel/internal/ui/organisms/statusbar"
	"github.com/ape-dev-de/tiegel/internal/ui/organisms/toolbar"
	"github.com/ape-dev-de/tiegel/internal/ui/templates"
)

// The redesigned shell (M18): header toolbar, icon stripes with split
// slots, maximize, breadcrumb footer, file icons. TIEGEL_SHOTS=<dir>
// writes the screenshots.
func TestShellIntegration(t *testing.T) {
	e := newGitEnvWith(t, map[string]string{
		"tests/ShopTest.php":     "<?php\n",
		"vendor/autoload.php":    "<?php\n",
		"resources/app.css":      "body {}\n",
		"docker-compose.yml":     "services: {}\n",
		"README.md":              "# Shop\n",
		"src/Model/Customer.php": "<?php\nclass Customer {}\n",
	})
	e.waitFor("tool layout", func() bool { return e.ctl.ToolsReady() })
	e.ctl.OpenFile("src/Shop.php", nil)
	e.waitFor("tab", func() bool { return e.ctl.ActiveTab() != nil })
	e.ctl.OpenFile("src/Model/Customer.php", nil)
	e.waitFor("tab 2", func() bool { return len(e.ctl.Tabs) == 2 })
	e.waitFor("git", func() bool { return e.ctl.Vcs.Info.Branch == "main" })
	e.settle()

	// Header: project, branch, configs, listen toggle.
	b := e.v.hdr.bar
	if len(b.Projects) != 1 || b.Current != e.ctl.Project.Root || b.Branch != "main" {
		t.Fatalf("header: projects %+v current %q branch %q", b.Projects, b.Current, b.Branch)
	}
	// Stripes: Structure sits in the split group of the left stripe and
	// carries the shortcut of its toggle action.
	var structure templates.StripeButton
	for _, sb := range e.v.bench.Stripes[templates.SideLeft] {
		if sb.ID == ToolStructure {
			structure = sb
		}
		if sb.Glyph == nil {
			t.Errorf("stripe button %s without icon", sb.ID)
		}
	}
	if structure.Group != templates.GroupSplit || structure.Shortcut != "Cmd+7" {
		t.Fatalf("structure button %+v", structure)
	}
	// Project above Structure.
	e.v.ShowTool(ToolProject)
	e.v.ShowTool(ToolStructure)
	e.v.ShowTool(toolProblems)
	e.ctl.Tools.MoveTo(ToolTerminal, controller.SideBottom, controller.GroupSplit)
	e.v.ShowTool(ToolTerminal)
	e.settle()
	if !e.v.ToolShown(ToolProject) || !e.v.ToolShown(ToolStructure) || !e.v.ToolShown(toolProblems) || !e.v.ToolShown(ToolTerminal) {
		t.Fatal("two tool windows per side expected")
	}
	// Footer breadcrumb: project › src › Model › Customer.php.
	if p := e.v.status.Path; len(p) != 4 || p[1].Text != "src" || p[3].Text != "Customer.php" {
		t.Fatalf("breadcrumb %+v", p)
	}
	e.v.crumbClicked(2)
	if e.v.tree.Selected() != "src/Model" || !e.v.tree.Expanded("src/Model") {
		t.Fatalf("crumb reveal: selected %q", e.v.tree.Selected())
	}
	e.settle()
	e.shot("integration-workbench")

	// Branch popup from the keyboard action.
	e.v.RunAction(ActBranches)
	e.waitFor("branches", func() bool { return len(e.ctl.Vcs.Branches) > 0 })
	e.settle()
	if b.Opened() != toolbar.PopupBranch {
		t.Fatal("Branches… should open the header popup")
	}
	e.shot("integration-branch-popup")
	b.Close()
	b.Show(toolbar.PopupProject)
	e.settle()
	e.shot("integration-project-popup")
	b.Close()

	// Maximize a panel; it is part of the stored layout.
	e.v.bench.Maximized = toolProblems // as the header button does
	e.v.handleBenchEvent(templates.MaximizeEvent{ID: toolProblems, Maximized: true})
	e.settle()
	if e.ctl.Tools.MaxPanel != toolProblems || e.v.bench.Maximized != toolProblems {
		t.Fatalf("maximize: %q %q", e.ctl.Tools.MaxPanel, e.v.bench.Maximized)
	}
	e.shot("integration-maximized")
	raw, _ := json.Marshal(e.ctl.Tools)
	var f struct {
		Groups    map[string]string `json:"groups"`
		Maximized string            `json:"maximized"`
	}
	_ = json.Unmarshal(raw, &f)
	if f.Maximized != toolProblems || f.Groups[ToolTerminal] != "split" {
		t.Fatalf("stored layout %s", raw)
	}
	e.v.HideTool(toolProblems)
	e.settle()
	if e.ctl.Tools.MaxPanel != "" {
		t.Fatal("hiding the maximized panel restores")
	}
}

// Header events map to commands; plugin quick actions come from the
// action model.
func TestHeaderQuickActionsAndEvents(t *testing.T) {
	e := newEnv(t, true)
	ran := 0
	const id keymap.Action = "agent.claude"
	e.v.Registry.AddAction(Action{ID: id, Title: "Agent", Icon: "agent", Run: func(*View) { ran++ }})
	e.v.Registry.RegisterToolbarAction(id)
	e.frame()
	// The agent plugin's button comes first, then the test's group.
	q := e.v.hdr.bar.Quick
	if len(q) != 3 || q[0].ID != string(ActAgentOpen) || !q[1].Separator || q[2].ID != string(id) || q[2].Glyph == nil {
		t.Fatalf("quick actions %+v", q)
	}
	e.v.hdr.handle(toolbar.QuickActionClicked{ID: string(id)})
	if ran != 1 {
		t.Fatal("quick action not run")
	}
	e.v.Registry.UnregisterToolbarAction(id)
	e.frame()
	if q := e.v.hdr.bar.Quick; len(q) != 1 || q[0].ID != string(ActAgentOpen) {
		t.Fatal("quick action not removed")
	}
	// No repository: no branch switcher, no Branches… action.
	if e.v.hdr.bar.Branch != "" || e.v.Registry.HasHandler(string(ActBranches)) {
		t.Fatal("branch switcher without repository")
	}
	e.v.hdr.handle(toolbar.OpenSettings{})
	e.frame()
	if !e.v.settingsOpen {
		t.Fatal("settings not opened")
	}
	e.v.closeOverlay()
	// "Move to" reaches the split slots.
	e.v.openToolMenu(toolProblems)
	e.frame()
	e.chooseLabel("Move to Right Bottom")
	if e.ctl.Tools.SideOf(toolProblems) != controller.SideRight || e.ctl.Tools.GroupOf(toolProblems) != controller.GroupSplit {
		t.Fatal("move to a split slot")
	}
	e.v.hdr.handle(toolbar.MergeBranch{Name: "x"})
	if e.v.popup != nil {
		t.Fatal("merge without a repository asks for confirmation")
	}
	// Crumbs are reported as CrumbEvent (no panic in the status handler).
	e.v.status.Path = []statusbar.Crumb{{Text: "p"}}
	e.frame()
}
