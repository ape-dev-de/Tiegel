package gui

import (
	"image"
	"strings"
	"testing"

	"github.com/ape-dev-de/tiegel/internal/app/gui/controller"
)

func menuTitles(items []MenuItem) []string {
	var out []string
	for _, it := range items {
		out = append(out, it.Title)
		for _, ch := range it.Children {
			out = append(out, "  "+ch.Title)
		}
	}
	return out
}

func findItem(items []MenuItem, prefix string) *MenuItem {
	for i := range items {
		if strings.HasPrefix(items[i].Title, prefix) {
			return &items[i]
		}
		if it := findItem(items[i].Children, prefix); it != nil {
			return it
		}
	}
	return nil
}

func TestLSPStatusMenuAndToolWindow(t *testing.T) {
	e := newEnv(t, true)
	e.settle()
	d := e.v.lspd
	var shown []MenuItem
	e.v.MenuPresenter = func(_ *View, _ string, items []MenuItem, _ image.Point) { shown = items }

	// The real backend: no server installed in the test environment.
	e.waitFor("lsp state", func() bool { return e.ctl.LSP.Status.State == controller.LSPNotFound && len(e.ctl.LSP.Servers) > 0 })
	d.openMenu()
	titles := strings.Join(menuTitles(shown), "\n")
	for _, w := range []string{"Phpactor: not-found", "Automatic", "Phpactor (not installed)", "Off (use the index)", "Restart", "Show Log",
		"Searched Locations", "Choose Path of Phpactor…", "Install Phpactor…"} {
		if !strings.Contains(titles, w) {
			t.Fatalf("menu lacks %q:\n%s", w, titles)
		}
	}
	if strings.Contains(titles, "Intelephense") {
		t.Fatal("disabled plugin offered") // lsp-intelephense is opt-in
	}
	// Show Log opens the tool window; it renders the searched locations.
	findItem(shown, "Show Log").Run()
	e.settle()
	if !e.v.ToolShown(ToolLSP) {
		t.Fatal("tool window not shown")
	}
	if lines := d.lines(); len(lines) == 0 || lines[0].Text != "Searched locations" {
		t.Fatalf("lines %+v", lines)
	}
	// Install… shows the plan (curl is missing in the test env: a hint).
	findItem(shown, "Install Phpactor").Run()
	e.waitFor("install popup", func() bool { return e.v.popup != nil })
	if p := e.v.popup; !strings.Contains(p.Title, "Install Phpactor") || !strings.Contains(p.Detail, "phpactor.phar") || !strings.Contains(p.Detail, "curl was not found") || len(p.Buttons) != 1 {
		t.Fatalf("popup %+v", p)
	}
	e.v.ClosePopup()
	// Choose Path opens an input.
	findItem(shown, "Choose Path").Run()
	if e.v.custom == nil || !e.v.custom.NoList {
		t.Fatal("no path input")
	}
	e.v.custom.OnChoose(-1, "  ")
	e.v.closeOverlay()

	// Turn off via the menu: state off, menu offers Turn On.
	findItem(shown, "Off (use the index)").Run()
	e.waitFor("off", func() bool { return e.ctl.LSP.Status.State == controller.LSPOff })
	d.openMenu()
	if findItem(shown, "Turn On") == nil {
		t.Fatalf("no Turn On:\n%s", strings.Join(menuTitles(shown), "\n"))
	}
	e.settle()

	// The "needs a language server" notice opens the same menu.
	shown = nil
	e.ctl.Status.Notice = controller.NoticeNeedsLSP("Completion")
	if !d.noticeClick() || shown == nil || e.ctl.Status.Notice != "" {
		t.Fatal("notice click")
	}
	e.ctl.Status.Notice = "something else"
	if d.noticeClick() {
		t.Fatal("unrelated notice opened the menu")
	}
}

func TestInstallDetail(t *testing.T) {
	got := installDetail(controller.InstallPlan{Tool: "X", Display: "composer require --dev x/x", Scope: "project", Dir: "/p",
		ChangesComposerJSON: true, Effects: []string{"adds x/x"}, Note: "note", Hint: "hint"})
	for _, w := range []string{"note", "Command: composer require --dev x/x", "Target: project (in /p)", "composer.json and composer.lock", "• adds x/x", "hint"} {
		if !strings.Contains(got, w) {
			t.Fatalf("detail lacks %q: %s", w, got)
		}
	}
	if got := installDetail(controller.InstallPlan{Display: "composer global require x", Scope: "global"}); !strings.Contains(got, "is not changed") {
		t.Fatal(got)
	}
}
