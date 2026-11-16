package debug

import (
	"image"
	"strings"
	"testing"

	"gioui.org/io/key"
	"gioui.org/layout"

	"github.com/ape-dev-de/tiegel/internal/dbgp"
	"github.com/ape-dev-de/tiegel/internal/ui/tokens"
	"github.com/ape-dev-de/tiegel/internal/ui/uitest"
)

var area = image.Pt(420, 300)

// collector wraps an organism's Update/Layout and records its events.
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

func one[T Event](t *testing.T, evs []Event) T {
	t.Helper()
	var zero T
	if len(evs) != 1 {
		t.Fatalf("events %#v, want one %T", evs, zero)
	}
	e, ok := evs[0].(T)
	if !ok {
		t.Fatalf("event %#v, want %T", evs[0], zero)
	}
	return e
}

func TestShortcuts(t *testing.T) {
	mac := Shortcuts("darwin")
	want := map[Action]string{ActionResume: "F9", ActionStepOver: "F8", ActionStepInto: "F7", ActionStepOut: "Shift+F8", ActionStop: "Cmd+F2"}
	for _, b := range mac {
		if want[b.Action] != b.Chord.String() {
			t.Errorf("%s: %s, want %s", b.Action, b.Chord, want[b.Action])
		}
		delete(want, b.Action)
	}
	if len(want) != 0 {
		t.Errorf("missing %v", want)
	}
	for _, b := range Shortcuts("linux") {
		if b.Action == ActionStop && b.Chord.String() != "Ctrl+F2" {
			t.Errorf("linux stop %s", b.Chord)
		}
	}
}

func TestStates(t *testing.T) {
	for s := StateNone; s <= StateStopped; s++ {
		if s.String() == "" {
			t.Errorf("state %d without name", s)
		}
	}
	if StateNone.Active() || StateStopped.Active() || !StateBreak.Active() {
		t.Error("Active")
	}
	for st, want := range map[dbgp.Status]State{dbgp.StatusStarting: StateStarting, dbgp.StatusRunning: StateRunning,
		dbgp.StatusBreak: StateBreak, dbgp.StatusStopping: StateStopping, dbgp.StatusStopped: StateStopped, "": StateNone} {
		if got := StateFromStatus(st); got != want {
			t.Errorf("%q → %v", st, got)
		}
	}
}

func TestToolbar(t *testing.T) {
	tb := &DebugToolbar{Shortcuts: Shortcuts("darwin"), Port: 9003}
	for _, st := range []State{StateNone, StateRunning, StateBreak} {
		for _, listening := range []bool{false, true} {
			tb.State, tb.Listening, tb.Muted = st, listening, listening
			uitest.Smoke(t, "toolbar "+st.String(), area, tb.Layout)
		}
	}
	tb.State, tb.Listening = StateNone, true
	if tb.Status() != "listening on :9003" {
		t.Errorf("status %q", tb.Status())
	}
	items := tb.Items()
	if !items[0].Active || !items[2].Disabled || items[6].Disabled != true {
		t.Errorf("idle items %+v", items)
	}
	if !strings.Contains(items[3].Tooltip(), "F8") {
		t.Errorf("step over tooltip %q", items[3].Tooltip())
	}
	d := newDriver(tb)
	if e := one[ActionEvent](t, d.click(14, 14)); e.Action != ActionToggleListen {
		t.Fatalf("listen click: %v", e)
	}
	// Resume sits after the listen button and a separator (4+1+4dp).
	if evs := d.click(28+9+14, 14); len(evs) != 0 {
		t.Fatalf("disabled resume reported %v", evs)
	}
	tb.State = StateBreak
	d.frame()
	if e := one[ActionEvent](t, d.click(28+9+14, 14)); e.Action != ActionResume {
		t.Fatalf("resume click: %v", e)
	}
	if e := one[ActionEvent](t, d.click(28+9+28*3+14, 14)); e.Action != ActionStepOut {
		t.Fatalf("step out click: %v", e)
	}
}

func TestCallStack(t *testing.T) {
	cs := &CallStackList{Frames: FramesFromStack([]dbgp.StackFrame{
		{Level: 0, Where: "App\\Mailer->send", File: "/app/src/Mailer.php", Line: 42},
		{Level: 1, Where: "App\\Queue->run", FileURI: "file:///var/www/src/Queue.php", Line: 17},
		{Level: 2, Where: "{main}", File: "/app/public/index.php", Line: 5},
	})}
	uitest.Smoke(t, "callstack", area, cs.Layout)
	uitest.Smoke(t, "callstack empty", area, (&CallStackList{}).Layout)
	d := newDriver(cs)
	e := one[FrameSelectEvent](t, d.click(100, 24+12))
	if e.Level != 1 || e.Line != 17 || cs.Selected != 1 {
		t.Fatalf("select: %+v selected %d", e, cs.Selected)
	}
	if location("C:\\work\\a.php", 3) != "a.php:3" || location("", 1) != "" || location("/x/b.php", 0) != "b.php" {
		t.Error("location")
	}
}

func sampleVars() []*Variable {
	return []*Variable{
		{Name: "$user", FullName: "$user", Type: "object", ClassName: "App\\User", HasChildren: true, NumChildren: 3},
		{Name: "$list", FullName: "$list", Type: "array", HasChildren: true, NumChildren: 5, PageSize: 2, Children: []*Variable{
			{Name: "0", FullName: "$list[0]", Type: "int", Value: "1"},
			{Name: "1", FullName: "$list[1]", Type: "string", Value: "two"},
		}},
		{Name: "$count", FullName: "$count", Type: "int", Value: "42"},
	}
}

func TestVariableDisplay(t *testing.T) {
	cases := map[string]*Variable{
		`{App\User}`:    {Type: "object", ClassName: "App\\User"},
		"array(3)":      {Type: "array", NumChildren: 3},
		`"a\"b"`:        {Type: "string", Value: `a"b`},
		"null":          {Type: "null"},
		"uninitialized": {Type: "uninitialized"},
		"1.5":           {Type: "float", Value: "1.5"},
	}
	for want, v := range cases {
		if got := v.Display(); got != want {
			t.Errorf("Display = %q, want %q", got, want)
		}
	}
	vs := sampleVars()
	if vs[0].HasMore() || !vs[1].HasMore() || vs[2].HasMore() {
		t.Error("HasMore")
	}
	if Find(vs, "$list[1]") == nil || Find(vs, "$nope") != nil {
		t.Error("Find")
	}
}

func TestVariablesTree(t *testing.T) {
	vt := &VariablesTree{Roots: sampleVars()}
	uitest.Smoke(t, "variables", area, vt.Layout)
	uitest.Smoke(t, "variables empty", area, (&VariablesTree{}).Layout)
	d := newDriver(vt)
	// Expanding $user (no children yet) asks the host for page 0.
	e := one[ExpandEvent](t, d.click(10, 12))
	if e.FullName != "$user" || e.Page != 0 || !vt.Expanded("$user") {
		t.Fatalf("expand: %+v", e)
	}
	vt.Roots[0].Loading = true
	d.frame()
	if len(vt.visible) != 4 || vt.visible[1].kind != rowLoading {
		t.Fatalf("loading row missing: %+v", vt.visible)
	}
	// Collapse again: no event.
	if evs := d.click(10, 12); len(evs) != 0 || vt.Expanded("$user") {
		t.Fatalf("collapse: %v", evs)
	}
	// Expand $list (row 1): children are present, no event; "load more" row.
	if evs := d.click(10, 24+12); len(evs) != 0 {
		t.Fatalf("expand loaded node: %v", evs)
	}
	if len(vt.visible) != 6 || vt.visible[4].kind != rowMore {
		t.Fatalf("rows: %+v", vt.visible)
	}
	e = one[ExpandEvent](t, d.click(100, 4*24+12))
	if e.FullName != "$list" || e.Page != 1 {
		t.Fatalf("load more: %+v", e)
	}
	vt.Roots[1].MergePage(&Variable{Page: 1, PageSize: 2, Children: []*Variable{{Name: "2", FullName: "$list[2]", Type: "int", Value: "3"}}})
	if len(vt.Roots[1].Children) != 3 || vt.Roots[1].Page != 1 {
		t.Fatal("MergePage")
	}
	// Select $count (last row) and copy its value.
	d.frame()
	last := len(vt.visible) - 1
	if evs := d.click(100, last*24+12); len(evs) != 0 || vt.Selected() != "$count" {
		t.Fatalf("select: %v %q", evs, vt.Selected())
	}
	c := one[CopyValueEvent](t, d.click(area.X-14, last*24+12))
	if c.FullName != "$count" || c.Value != "42" {
		t.Fatalf("copy: %+v", c)
	}
	vt.SetExpanded("$list", false)
	d.frame()
	if len(vt.visible) != 3 {
		t.Fatalf("SetExpanded: %d rows", len(vt.visible))
	}
}

func TestVariablesFromProperties(t *testing.T) {
	ps := []*dbgp.Property{
		{Name: "$a", FullName: "$a", Type: "array", HasChildren: true, NumChildren: 1, PageSize: 32,
			Children: []*dbgp.Property{{Key: "k", FullName: "$a['k']", Type: "int", Value: "1"}}},
		nil,
	}
	vs := VariablesFromProperties(ps)
	if len(vs) != 2 || vs[1] != nil || vs[0].Children[0].Name != "k" || vs[0].PageSize != 32 {
		t.Fatalf("%+v", vs)
	}
	var v *Variable
	v = vs[0]
	v.MergePage(nil)
	bp := BreakpointFromDBGp(dbgp.Breakpoint{ID: "7", FileURI: "file:///a.php", Line: 3, Disabled: true}, "")
	if bp.ID != "7" || bp.File != "file:///a.php" || bp.Enabled {
		t.Fatalf("%+v", bp)
	}
	if BreakpointFromDBGp(dbgp.Breakpoint{File: "/a.php"}, "x").ID != "x" {
		t.Fatal("id override")
	}
}

func TestWatches(t *testing.T) {
	w := &WatchesList{Items: []Watch{
		{Expression: "$user->name", Value: "Ada", Type: "string"},
		{Expression: "$nope", Error: "undefined variable"},
	}}
	uitest.Smoke(t, "watches", area, w.Layout)
	uitest.Smoke(t, "watches empty", area, (&WatchesList{}).Layout)
	d := newDriver(w)
	// Add via the + button (right end of the input row).
	w.Input().SetText("  $count + 1 ")
	e := one[WatchAddEvent](t, d.click(area.X-4-14, 4+14))
	if e.Expression != "$count + 1" || w.Input().Text() != "" {
		t.Fatalf("add: %+v", e)
	}
	if evs := d.click(area.X-4-14, 4+14); len(evs) != 0 {
		t.Fatalf("empty add: %v", evs)
	}
	// Remove the second watch (rows start below the input row).
	rowY := 4 + 28 + 4
	r := one[WatchRemoveEvent](t, d.click(area.X-4-10, rowY+24+12))
	if r.Index != 1 {
		t.Fatalf("remove: %+v", r)
	}
	// Double click edits inline; Enter commits.
	d.h.DoubleClick(image.Pt(100, rowY+12))
	d.frame()
	d.frame()
	if w.Editing() != 0 {
		t.Fatalf("editing %d", w.Editing())
	}
	w.editor.SetText("$user->email")
	d.events = nil
	d.h.Router.Queue(key.Event{Name: key.NameReturn, State: key.Press})
	d.frame()
	d.frame()
	ed := one[WatchEditEvent](t, d.events)
	if ed.Index != 0 || ed.Expression != "$user->email" || w.Editing() != -1 {
		t.Fatalf("edit: %+v", ed)
	}
	// Escape cancels.
	w.Edit(1)
	d.frame()
	d.frame()
	d.events = nil
	d.h.Router.Queue(key.Event{Name: key.NameEscape, State: key.Press})
	d.frame()
	if w.Editing() != -1 || len(d.events) != 0 {
		t.Fatalf("escape: editing %d, events %v", w.Editing(), d.events)
	}
	w.Edit(9) // out of range: ignored
	if w.Editing() != -1 {
		t.Fatal("edit out of range")
	}
}

func TestBreakpoints(t *testing.T) {
	b := &BreakpointsList{Items: []Breakpoint{
		{ID: "1", File: "/app/src/Mailer.php", Line: 42, Enabled: true},
		{ID: "2", File: "/app/src/Queue.php", Line: 17, Condition: "$retries > 3", Enabled: true},
		{ID: "3", File: "/app/public/index.php", Line: 5},
	}}
	uitest.Smoke(t, "breakpoints", area, b.Layout)
	uitest.Smoke(t, "breakpoints empty", area, (&BreakpointsList{}).Layout)
	d := newDriver(b)
	j := one[BreakpointJumpEvent](t, d.click(200, 24+12))
	if j.ID != "2" || j.File != "/app/src/Queue.php" || j.Line != 17 || b.Selected != "2" {
		t.Fatalf("jump: %+v", j)
	}
	tg := one[BreakpointToggleEvent](t, d.click(15, 12))
	if tg.ID != "1" || tg.Enabled {
		t.Fatalf("toggle: %+v", tg)
	}
	// Data in: the host did not apply the toggle, the checkbox reverts.
	d.frame()
	if !b.rows["1"].row.IsEnabled() {
		t.Fatal("checkbox not synced from data")
	}
	// Edit (pencil) and remove (×) sit at the right end of the selected row.
	if evs := d.click(area.X-4-20-10, 24+12); len(evs) != 0 || b.Editing() != "2" {
		t.Fatalf("edit: %v editing %q", evs, b.Editing())
	}
	if b.cond.Text() != "$retries > 3" {
		t.Fatalf("condition prefill %q", b.cond.Text())
	}
	b.cond.SetText(" $retries > 5 ")
	d.events = nil
	d.h.Router.Queue(key.Event{Name: key.NameReturn, State: key.Press})
	d.frame()
	d.frame()
	c := one[BreakpointConditionEvent](t, d.events)
	if c.ID != "2" || c.Condition != "$retries > 5" || b.Editing() != "" {
		t.Fatalf("condition: %+v", c)
	}
	b.EditCondition("2")
	d.frame()
	d.frame()
	d.h.Router.Queue(key.Event{Name: key.NameEscape, State: key.Press})
	d.frame()
	if b.Editing() != "" {
		t.Fatal("escape did not cancel")
	}
	rm := one[BreakpointRemoveEvent](t, d.click(area.X-4-10, 24+12))
	if rm.ID != "2" {
		t.Fatalf("remove: %+v", rm)
	}
	b.Items = b.Items[:1]
	d.frame()
	if len(b.rows) != 1 {
		t.Fatalf("stale rows: %d", len(b.rows))
	}
}

func TestSessions(t *testing.T) {
	s := &SessionList{Selected: "a", Items: []Session{
		{ID: "a", Title: "index.php", Detail: "TIEGEL", State: StateBreak},
		{ID: "b", Title: "worker.php", State: StateRunning},
		{ID: "c", Title: "cron.php", State: StateStopped},
	}}
	uitest.Smoke(t, "sessions", area, s.Layout)
	uitest.Smoke(t, "sessions empty", area, (&SessionList{}).Layout)
	d := newDriver(s)
	if e := one[SessionSelectEvent](t, d.click(100, 24+12)); e.ID != "b" || s.Selected != "b" {
		t.Fatalf("select: %+v", e)
	}
	if evs := d.click(100, 24+12); len(evs) != 0 {
		t.Fatalf("reselect: %v", evs)
	}
	stopX := area.X - 4 - 10
	if e := one[SessionStopEvent](t, d.click(stopX, 12)); e.ID != "a" {
		t.Fatalf("stop: %+v", e)
	}
	if evs := d.click(stopX, 2*24+12); len(evs) != 0 {
		t.Fatalf("stopped session stop: %v", evs)
	}
}

func samplePanel() *DebugPanel {
	p := NewDebugPanel()
	p.Toolbar = DebugToolbar{Listening: true, Port: 9003, State: StateBreak, Shortcuts: Shortcuts("darwin")}
	p.Sessions.Items = []Session{{ID: "a", Title: "index.php", State: StateBreak}, {ID: "b", Title: "worker.php", State: StateRunning}}
	p.Sessions.Selected = "a"
	p.Stack.Frames = []Frame{{Level: 0, Where: "App\\Mailer->send", File: "/app/src/Mailer.php", Line: 42}, {Level: 1, Where: "{main}", File: "/app/index.php", Line: 3}}
	p.Variables.Roots = sampleVars()
	p.Watches.Items = []Watch{{Expression: "$count * 2", Value: "84", Type: "int"}}
	p.Breakpoints.Items = []Breakpoint{{ID: "1", File: "/app/src/Mailer.php", Line: 42, Enabled: true}}
	return p
}

func TestPanel(t *testing.T) {
	p := samplePanel()
	big := image.Pt(900, 520)
	if d := uitest.Smoke(t, "panel", big, p.Layout); d.Size != big {
		t.Errorf("size %v", d.Size)
	}
	p.Sessions.Items = p.Sessions.Items[:1]
	uitest.Smoke(t, "panel one session", big, p.Layout)
	uitest.Smoke(t, "panel unbounded", image.Pt(1<<24, 1<<24), NewDebugPanel().Layout)
	d := newDriver(p)
	d.h.Max = big
	d.frame()
	if e := one[ActionEvent](t, d.click(4+14, 4+14)); e.Action != ActionToggleListen {
		t.Fatalf("panel toolbar: %+v", e)
	}
	// The first frame row sits below the toolbar (36+1) and a section header (28).
	if e := one[FrameSelectEvent](t, d.click(100, 37+28+24+12)); e.Level != 1 {
		t.Fatalf("panel frame: %+v", e)
	}
}

func TestRenderPanel(t *testing.T) {
	th := tokens.Dark(nil)
	img, err := uitest.Render(th, image.Pt(900, 520), samplePanel().Layout)
	if err != nil {
		t.Skipf("headless rendering unavailable: %v", err)
	}
	if n := uitest.CountDiffering(img, th.Color.Background); n < 900*520/2 {
		t.Errorf("panel did not paint its surface (%d pixels)", n)
	}
}
