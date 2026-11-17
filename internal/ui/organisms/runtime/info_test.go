package runtime

import (
	"image"
	"strings"
	"testing"
	"time"

	"github.com/ape-dev-de/tiegel/internal/compose"
	"github.com/ape-dev-de/tiegel/internal/docker"
	"github.com/ape-dev-de/tiegel/internal/interp"
	"github.com/ape-dev-de/tiegel/internal/ui/uitest"
)

func TestPickerDockerKinds(t *testing.T) {
	img := FromImage(docker.NewImageInterpreter("php:8.3-cli", "/srv", "/Users/ada/shop", docker.Client{Context: "orbstack"}))
	if img.Kind != KindDocker || img.ID != "docker:php:8.3-cli" || img.Context != "orbstack" || !strings.Contains(img.Detail, "/srv") {
		t.Fatalf("image %+v", img)
	}
	c := FromCandidate("shop", compose.Candidate{Service: "app", Score: 90, Image: "php:8.3-fpm"})
	c.Context, c.State = "", "running"
	p := &InterpreterPicker{Items: []Interpreter{c, img}, Selected: c.ID}
	uitest.Smoke(t, "picker compose running", area, p.Layout)
	for _, s := range []string{"stopped", "missing", "error"} {
		p.Items[0].State = s
		uitest.Smoke(t, "picker compose "+s, area, p.Layout)
	}
	p.Selected = img.ID
	uitest.Smoke(t, "picker image", area, p.Layout)
	p.Info = InterpreterInfoPanel{ID: img.ID, Error: "image not pulled"}
	dims := uitest.Smoke(t, "picker image with info", image.Pt(480, 600), p.Layout)
	d := newDriver(p)
	d.h = uitest.NewHarness(image.Pt(480, 600))
	d.frame()
	if evs := d.click(30, dims.Size.Y-10); len(evs) != 1 || evs[0] != (ProbeRefreshEvent{ID: img.ID}) {
		t.Fatalf("picker info refresh: %v (height %d)", evs, dims.Size.Y)
	}
	p.Info.ID = "other"
	if h := uitest.Smoke(t, "picker info other", image.Pt(480, 600), p.Layout).Size.Y; h >= dims.Size.Y {
		t.Fatalf("info for another interpreter shown: %d >= %d", h, dims.Size.Y)
	}
	if kindGlyph(KindDocker) != kindGlyph(KindCompose) || kindGlyph(KindLocal) == kindGlyph(KindDocker) {
		t.Fatal("glyphs")
	}
	if contextText("") == contextText("orbstack") || stateBadge("running").Kind == stateBadge("stopped").Kind {
		t.Fatal("context/state texts")
	}
}

func TestInterpreterInfoPanel(t *testing.T) {
	when := time.Date(2026, 11, 17, 10, 42, 0, 0, time.UTC)
	info := FromProbe(interp.Info{Version: "8.3.4", SAPI: "cli", OS: "Linux", Xdebug: "3.3.2", IniScanned: []string{"/usr/local/etc/php/conf.d/xdebug.ini"},
		Extensions: []string{"Core", "json", "xdebug"}}, when, true)
	if info.ProbedAt == "" || !info.Cached || len(info.Extensions) != 3 {
		t.Fatalf("convert %+v", info)
	}
	if FromProbe(interp.Info{Version: "8"}, time.Time{}, false).ProbedAt != "" {
		t.Fatal("zero time shown")
	}
	p := &InterpreterInfoPanel{ID: "docker:php:8.3-cli", Info: info}
	uitest.Smoke(t, "info", area, p.Layout)
	uitest.Smoke(t, "info narrow", image.Pt(200, 400), p.Layout)
	p.Info.Xdebug, p.Info.IniFile, p.Error = "", "/usr/local/etc/php/php.ini", "container not running"
	uitest.Smoke(t, "info no xdebug + error", area, p.Layout)
	empty := &InterpreterInfoPanel{}
	uitest.Smoke(t, "info empty", area, empty.Layout)

	// The button sits below the content; find it by clicking along the
	// left edge from the bottom of the drawn panel.
	q := &InterpreterInfoPanel{ID: "local:/usr/bin/php", Error: "not read"}
	dims := uitest.Smoke(t, "info button", area, q.Layout)
	d := newDriver(q)
	evs := d.click(20, dims.Size.Y-10)
	if len(evs) != 1 || evs[0] != (ProbeRefreshEvent{ID: "local:/usr/bin/php"}) {
		t.Fatalf("refresh: %v (height %d)", evs, dims.Size.Y)
	}
	q.Busy = true
	if evs := d.click(20, dims.Size.Y-10); len(evs) != 0 {
		t.Fatalf("busy: %v", evs)
	}
}
