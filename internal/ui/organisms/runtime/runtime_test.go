package runtime

import (
	"image"
	"strings"
	"testing"

	"gioui.org/io/key"
	"gioui.org/layout"

	"github.com/ape-dev-de/tiegel/internal/compose"
	"github.com/ape-dev-de/tiegel/internal/interp"
	"github.com/ape-dev-de/tiegel/internal/ui/tokens"
	"github.com/ape-dev-de/tiegel/internal/ui/uitest"
)

var area = image.Pt(480, 320)

type organism interface {
	Update(gtx layout.Context) []Event
	Layout(gtx layout.Context, th *tokens.Theme) layout.Dimensions
}

type driver struct {
	h      *uitest.Harness
	o      organism
	events []Event
}

func newDriver(o organism) *driver {
	d := &driver{h: uitest.NewHarness(area), o: o}
	d.frame()
	return d
}

func (d *driver) frame() {
	d.h.Frame(func(gtx layout.Context, th *tokens.Theme) layout.Dimensions {
		d.events = append(d.events, d.o.Update(gtx)...)
		return d.o.Layout(gtx, th)
	})
}

func (d *driver) click(x, y int) []Event {
	d.events = nil
	d.h.Click(image.Pt(x, y))
	d.frame()
	d.frame()
	return d.events
}

func samplePicker() *InterpreterPicker {
	l := &interp.Local{Path: "/opt/homebrew/Cellar/php/8.3.4/bin/php", Found: "/opt/homebrew/bin/php", PHPVersion: "8.3.4"}
	return &InterpreterPicker{Items: []Interpreter{
		FromLocal(l),
		FromCandidate("shop", compose.Candidate{Service: "php", Score: 115, Image: "php:8.3-fpm",
			Reasons: []string{"official php image", "service name \"php\"", "mounts the project directory"}}),
		FromCandidate("shop", compose.Candidate{Service: "worker", Score: 40, Image: "shop/worker"}),
	}}
}

func TestInterpreterPicker(t *testing.T) {
	p := samplePicker()
	if p.Items[0].ID != "local:/opt/homebrew/Cellar/php/8.3.4/bin/php" || p.Items[1].ID != "compose:shop:php" {
		t.Fatalf("ids %q %q", p.Items[0].ID, p.Items[1].ID)
	}
	if p.Best() != 1 {
		t.Fatalf("best %d", p.Best())
	}
	uitest.Smoke(t, "picker none", area, p.Layout)
	p.Selected = "compose:shop:php"
	uitest.Smoke(t, "picker compose", area, p.Layout)
	p.Open(true)
	uitest.Smoke(t, "picker open", area, p.Layout)
	p.Open(false)

	d := newDriver(p)
	if evs := d.click(100, 14); len(evs) != 0 {
		t.Fatalf("open: %v", evs)
	}
	// Third option: field 28 + 4 gap + 4 pad + 2 rows of 24 + half a row.
	evs := d.click(100, 28+4+4+2*24+12)
	if len(evs) != 1 || evs[0] != (SelectInterpreterEvent{ID: "compose:shop:worker"}) || p.Selected != "compose:shop:worker" {
		t.Fatalf("select: %v (selected %q)", evs, p.Selected)
	}
	if (&InterpreterPicker{}).Best() != -1 {
		t.Fatal("best of empty")
	}
}

func TestPathMappingEditor(t *testing.T) {
	e := &PathMappingEditor{Items: MappingsFromCompose(compose.Mappings{
		{Host: "/Users/ada/shop", Remote: "/var/www/html"},
		{Host: "/Users/ada/shop/vendor", Remote: "relative/bad"},
	})}
	uitest.Smoke(t, "mappings", area, e.Layout)
	uitest.Smoke(t, "mappings empty", area, (&PathMappingEditor{}).Layout)
	if !RemoteValid("/app") || RemoteValid("app") || RemoteValid("/a\\b") || !RemoteValid("") {
		t.Fatal("RemoteValid")
	}
	d := newDriver(e)
	if e.rows[0].host.Text() != "/Users/ada/shop" || !e.rows[1].remote.Invalid {
		t.Fatal("data not synced into fields")
	}
	// Remove button: last column (36dp) of the second row (header 24+1? rows ≥ 28+4).
	rowH := 28 + 4
	header := 24
	evs := d.click(area.X-18, header+rowH+rowH/2)
	if len(evs) != 1 || evs[0] != (MappingRemoveEvent{Index: 1}) {
		t.Fatalf("remove: %v", evs)
	}
	// Add button below the table (bottom of the area).
	evs = d.click(40, area.Y-14)
	if len(evs) != 1 || evs[0] != (MappingAddEvent{}) {
		t.Fatalf("add: %v", evs)
	}
	// Edit the remote path of row 0 and press Enter.
	d.click(300, header+rowH/2)
	e.rows[0].remote.SetText("/srv/app ")
	d.events = nil
	d.h.Router.Queue(key.Event{Name: key.NameReturn, State: key.Press})
	d.frame()
	d.frame()
	if len(d.events) != 1 || d.events[0] != (MappingChangeEvent{Index: 0, Host: "/Users/ada/shop", Remote: "/srv/app"}) {
		t.Fatalf("change: %v", d.events)
	}
	// Focus loss commits too.
	e.rows[0].host.SetText("/Users/ada/other")
	d.click(40, header+rowH+rowH/2) // focus row 1
	found := false
	for _, ev := range d.events {
		if ev == (MappingChangeEvent{Index: 0, Host: "/Users/ada/other", Remote: "/srv/app"}) {
			found = true
		}
	}
	if !found {
		t.Fatalf("blur commit: %v", d.events)
	}
	// Unfocused rows follow the data again.
	e.Items[0] = Mapping{Host: "/h", Remote: "/r"}
	d.frame()
	if e.rows[0].host.Text() != "/h" || e.rows[0].remote.Text() != "/r" {
		t.Fatal("data not re-synced")
	}
	if got := ToCompose([]Mapping{{Host: "/a", Remote: "/b"}, {Host: "/c"}}); len(got) != 1 || got[0].Remote != "/b" {
		t.Fatalf("ToCompose %v", got)
	}
}

func TestHints(t *testing.T) {
	l := &XdebugHintsList{Items: HintsFromCompose([]compose.Hint{
		{Severity: compose.SeverityWarning, Code: compose.HintNoHostGateway, Message: "host.docker.internal does not resolve on Linux; add extra_hosts to the service so the container can reach the IDE"},
		{Severity: compose.SeverityInfo, Code: compose.HintNoPathMapping, Message: "no path mapping"},
	})}
	l.Items = append(l.Items, Hint{Severity: SeverityError, Code: "x", Message: "broken"})
	if l.Items[0].Severity != SeverityWarning || l.Items[1].Severity != SeverityInfo {
		t.Fatal("severity conversion")
	}
	for _, s := range []Severity{SeverityInfo, SeverityWarning, SeverityError} {
		if s.String() == "" {
			t.Fatal("severity name")
		}
	}
	uitest.Smoke(t, "hints", area, l.Layout)
	uitest.Smoke(t, "hints narrow", image.Pt(160, 300), l.Layout)
	uitest.Smoke(t, "hints empty", area, (&XdebugHintsList{}).Layout)
	d := newDriver(l)
	evs := d.click(100, 10)
	if len(evs) != 1 || evs[0] != (HintSelectEvent{Code: compose.HintNoHostGateway}) {
		t.Fatalf("hint click: %v", evs)
	}
}

func TestStatusbarVersion(t *testing.T) {
	s := &StatusbarVersion{Version: "8.3", Interpreter: "compose:php", Source: "composer.json: require php ^8.3"}
	if s.Text() != "PHP 8.3 · compose:php" {
		t.Fatalf("text %q", s.Text())
	}
	if !strings.HasPrefix(s.Tooltip(), "composer.json") {
		t.Fatalf("tooltip %q", s.Tooltip())
	}
	d := uitest.Smoke(t, "statusbar", area, s.Layout)
	if d.Size.Y != 24 || d.Size.X >= area.X {
		t.Errorf("size %v", d.Size)
	}
	s.Override = true
	if !strings.Contains(s.Tooltip(), "override") {
		t.Error("override tooltip")
	}
	uitest.Smoke(t, "statusbar override", area, s.Layout)
	wide := uitest.Smoke(t, "statusbar override", area, s.Layout).Size.X
	s.Hint, s.HintBadge = "container not running: compose service \"php\" is stopped", "stopped"
	if !strings.HasSuffix(s.Tooltip(), "click to change\n"+s.Hint) {
		t.Errorf("hint tooltip %q", s.Tooltip())
	}
	if d := uitest.Smoke(t, "statusbar hint", area, s.Layout); d.Size.X <= wide {
		t.Errorf("hint badge not drawn: %v <= %d", d.Size, wide)
	}
	s.Hint = ""
	if (&StatusbarVersion{}).Text() != "PHP ?" || !strings.Contains((&StatusbarVersion{}).Tooltip(), "unknown") {
		t.Error("empty widget")
	}
	dr := newDriver(s)
	evs := dr.click(10, 10)
	if len(evs) != 1 || evs[0] != (OverrideEvent{}) {
		t.Fatalf("click: %v", evs)
	}
}

func TestFromInterpreter(t *testing.T) {
	l := interp.NewLocal("/usr/bin/php")
	it := FromInterpreter(l, "8.2.1")
	if it.Kind != KindLocal || it.Version != "8.2.1" || it.ID != l.ID() {
		t.Fatalf("%+v", it)
	}
	l.Found = ""
	if FromLocal(l).Label == "" {
		t.Fatal("label")
	}
}
