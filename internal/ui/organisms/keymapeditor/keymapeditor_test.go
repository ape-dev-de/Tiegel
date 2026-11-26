package keymapeditor

import (
	"image"
	"reflect"
	"testing"

	"gioui.org/io/key"
	"gioui.org/layout"

	"github.com/ape-dev-de/tiegel/internal/ui/tokens"
	"github.com/ape-dev-de/tiegel/internal/ui/uitest"
)

func sampleRows() []Row {
	return []Row{
		{Action: "navigate.back", Title: "Back", Context: "global", Chords: []string{"Cmd+[", "MouseBack"}, Defaults: []string{"Cmd+[", "MouseBack"}, Shadowed: []string{"Cmd+["}},
		{Action: "edit.duplicateLines", Title: "Duplicate Line or Selection", Context: "editor", Chords: []string{"Cmd+D"}, Defaults: []string{"Cmd+D"}},
		{Action: "app.settings", Title: "Settings…", Context: "global", Chords: []string{"Cmd+,", "Cmd+["}, Defaults: []string{"Cmd+,"}, User: true},
		{Action: "git.showDiff", Title: "Show Diff", Context: "toolWindow", Chords: []string{"Cmd+D"}, Defaults: []string{"Cmd+D"}},
		{Action: "tab.close", Title: "Close Tab", Context: "global", Defaults: []string{"Cmd+W"}, User: true},
	}
}

func TestFilter(t *testing.T) {
	rows := sampleRows()
	ids := func(rs []Row) []string {
		var out []string
		for _, r := range rs {
			out = append(out, r.Action)
		}
		return out
	}
	cases := []struct {
		query, chord string
		want         []string
	}{
		{"", "", []string{"navigate.back", "edit.duplicateLines", "app.settings", "git.showDiff", "tab.close"}},
		{"DUPLICATE line", "", []string{"edit.duplicateLines"}},
		{"tool window", "", []string{"git.showDiff"}},
		{"cmd+d", "", []string{"edit.duplicateLines", "git.showDiff"}},
		{"", "cmd+[", []string{"navigate.back", "app.settings"}},
		{"settings", "Cmd+[", []string{"app.settings"}},
		{"", "F12", nil},
	}
	for _, c := range cases {
		if got := ids(Filter(rows, c.query, c.chord)); !reflect.DeepEqual(got, c.want) {
			t.Errorf("%q/%q: %v", c.query, c.chord, got)
		}
	}
	if ContextLabel("toolWindow") != "Tool window" || ContextLabel("x") != "x" || ContextLabel("editor") != "Editor" || ContextLabel("global") != "Global" {
		t.Fatal("context labels")
	}
	if presetLabel("mac") != "macOS" || presetLabel("pc") != "Windows / Linux" || presetLabel("x") != "x" || presetDetail("x") != "" {
		t.Fatal("preset labels")
	}
}

// recorderHost lays out a recorder alone and collects its results.
type recorderHost struct {
	r   Recorder
	got []RecordResult
}

func (h *recorderHost) widget(gtx layout.Context, th *tokens.Theme) layout.Dimensions {
	if res := h.r.Update(gtx); res != RecordIdle {
		h.got = append(h.got, res)
	}
	if !h.r.Active() {
		return layout.Dimensions{}
	}
	return h.r.Layout(gtx, th)
}

func press(h *uitest.Harness, name key.Name, mods key.Modifiers) {
	h.Router.Queue(key.Event{Name: name, Modifiers: mods, State: key.Press})
}

func TestRecorderCapturesAndConfirms(t *testing.T) {
	h := uitest.NewHarness(image.Pt(500, 80))
	rh := &recorderHost{}
	rh.r.Start()
	h.Frame(rh.widget)
	h.Frame(rh.widget)
	press(h, key.NameCommand, key.ModCommand) // modifier alone: ignored
	press(h, "D", key.ModCommand)
	h.Frame(rh.widget)
	if rh.r.Chord() != "Cmd+D" || !rh.r.Active() || !reflect.DeepEqual(rh.got, []RecordResult{RecordCaptured}) {
		t.Fatalf("captured %q %v", rh.r.Chord(), rh.got)
	}
	// A second chord replaces the first; Tab is captured too.
	press(h, key.NameTab, key.ModShift)
	h.Frame(rh.widget)
	if rh.r.Chord() != "Shift+Tab" {
		t.Fatalf("tab %q", rh.r.Chord())
	}
	press(h, key.NameReturn, 0)
	h.Frame(rh.widget)
	if rh.r.Active() || rh.r.Chord() != "Shift+Tab" || rh.got[len(rh.got)-1] != RecordConfirmed {
		t.Fatalf("confirm %v %q", rh.got, rh.r.Chord())
	}

	// Plain Enter with nothing captured is a chord; Escape cancels.
	rh.got = nil
	rh.r.Start()
	h.Frame(rh.widget)
	press(h, key.NameReturn, 0)
	h.Frame(rh.widget)
	if rh.r.Chord() != "Enter" {
		t.Fatalf("enter chord %q", rh.r.Chord())
	}
	press(h, key.NameEscape, 0)
	h.Frame(rh.widget)
	if rh.r.Active() || rh.r.Chord() != "" || rh.got[len(rh.got)-1] != RecordCancelled {
		t.Fatalf("cancel %v", rh.got)
	}
	// Inactive: nothing happens.
	press(h, "X", 0)
	h.Frame(rh.widget)
	if rh.r.Update(layout.Context{}) != RecordIdle {
		t.Fatal("inactive update")
	}
}

func TestRecorderDoubleShiftGestureBlur(t *testing.T) {
	h := uitest.NewHarness(image.Pt(500, 80))
	rh := &recorderHost{}
	rh.r.Start()
	h.Frame(rh.widget)
	for _, st := range []key.State{key.Press, key.Release, key.Press} {
		h.Router.Queue(key.Event{Name: key.NameShift, Modifiers: key.ModShift, State: st})
	}
	h.Frame(rh.widget)
	if rh.r.Chord() != "DoubleShift" {
		t.Fatalf("double shift %q", rh.r.Chord())
	}
	// Shift used for a capital letter does not arm DoubleShift.
	h.Router.Queue(key.Event{Name: key.NameShift, Modifiers: key.ModShift, State: key.Release})
	h.Router.Queue(key.Event{Name: key.NameShift, Modifiers: key.ModShift, State: key.Press})
	press(h, "A", key.ModShift)
	h.Router.Queue(key.Event{Name: key.NameShift, Modifiers: key.ModShift, State: key.Release})
	h.Router.Queue(key.Event{Name: key.NameShift, Modifiers: key.ModShift, State: key.Press})
	h.Frame(rh.widget)
	if rh.r.Chord() != "Shift+A" {
		t.Fatalf("shift+a %q", rh.r.Chord())
	}
	// Gestures come from the host.
	if !rh.r.Gesture("MouseBack") {
		t.Fatal("gesture refused")
	}
	h.Frame(rh.widget)
	if rh.r.Chord() != "MouseBack" {
		t.Fatalf("gesture %q", rh.r.Chord())
	}
	// Unsupported keys are reported, not captured.
	press(h, "Hyper", 0)
	h.Frame(rh.widget)
	if rh.r.Chord() != "MouseBack" || rh.r.err == "" {
		t.Fatalf("unsupported %q %q", rh.r.Chord(), rh.r.err)
	}
	uitest.Smoke(t, "error", image.Pt(500, 80), func(gtx layout.Context, th *tokens.Theme) layout.Dimensions { return rh.r.Layout(gtx, th) })
	// Focus loss confirms the captured chord.
	h.Frame(func(gtx layout.Context, th *tokens.Theme) layout.Dimensions {
		gtx.Execute(key.FocusCmd{})
		return rh.widget(gtx, th)
	})
	h.Frame(rh.widget)
	if rh.r.Active() || rh.got[len(rh.got)-1] != RecordConfirmed || rh.r.Chord() != "MouseBack" {
		t.Fatalf("blur %v %q", rh.got, rh.r.Chord())
	}
	if rh.r.Gesture("MouseForward") {
		t.Fatal("inactive recorder took a gesture")
	}
	// Focus loss without a chord cancels; Instant confirms at once.
	rh.got = nil
	rh.r.Start()
	h.Frame(rh.widget)
	h.Frame(func(gtx layout.Context, th *tokens.Theme) layout.Dimensions {
		gtx.Execute(key.FocusCmd{})
		return rh.widget(gtx, th)
	})
	h.Frame(rh.widget)
	if len(rh.got) == 0 || rh.got[len(rh.got)-1] != RecordCancelled {
		t.Fatalf("blur without chord %v", rh.got)
	}
	rh.r = Recorder{Instant: true}
	rh.r.Start()
	h.Frame(rh.widget)
	press(h, "F", key.ModCommand)
	h.Frame(rh.widget)
	if rh.r.Active() || rh.r.Chord() != "Cmd+F" {
		t.Fatalf("instant %q", rh.r.Chord())
	}
}

// editorHost runs an Editor and collects its events.
type editorHost struct {
	e   Editor
	evs []Event
}

func (h *editorHost) widget(gtx layout.Context, th *tokens.Theme) layout.Dimensions {
	h.evs = append(h.evs, h.e.Update(gtx)...)
	return h.e.Layout(gtx, th)
}

var editorSize = image.Pt(900, 560)

func TestEditorSmoke(t *testing.T) {
	h := &editorHost{e: Editor{Preset: "mac", Rows: sampleRows(), Invalid: []string{"x Hyper+Q"}}}
	uitest.Smoke(t, "empty selection", editorSize, h.widget)
	h.e.Selected = "app.settings"
	uitest.Smoke(t, "custom", editorSize, h.widget)
	h.e.Selected = "navigate.back"
	h.e.Pending = &Pending{Action: "navigate.back", Chord: "Cmd+D", Others: []Other{{Action: "edit.duplicateLines", Title: "Duplicate", Context: "editor"}, {Action: "x"}}}
	h.e.StartRecording("navigate.back")
	uitest.Smoke(t, "recording + pending", editorSize, h.widget)
	h.e.Selected = "tab.close"
	h.e.SetKeyFilter("Cmd+D")
	h.e.StartKeyFilter()
	uitest.Smoke(t, "no shortcut, key filter", editorSize, h.widget)
	h.e.SetQuery("nothing matches")
	uitest.Smoke(t, "no match", editorSize, h.widget)
	h.e.Rows = nil
	uitest.Smoke(t, "loading", editorSize, h.widget)
	if h.e.Query() != "nothing matches" || h.e.KeyFilter() != "Cmd+D" {
		t.Fatal("query state")
	}
}

func TestEditorRecordsAndFilters(t *testing.T) {
	h := &editorHost{e: Editor{Preset: "mac", Rows: sampleRows()}}
	hr := uitest.NewHarness(editorSize)
	hr.Frame(h.widget)

	// Record a shortcut for an action.
	h.e.StartRecording("edit.duplicateLines")
	if !h.e.Recording() || h.e.RecordingFor() != "edit.duplicateLines" || h.e.Selected != "edit.duplicateLines" {
		t.Fatal("recording state")
	}
	hr.Frame(h.widget)
	press(hr, "K", key.ModCommand|key.ModShift)
	hr.Frame(h.widget)
	press(hr, key.NameReturn, 0)
	hr.Frame(h.widget)
	want := []Event{RecordedEvent{Action: "edit.duplicateLines", Chord: "Cmd+Shift+K"}}
	if !reflect.DeepEqual(h.evs, want) || h.e.Recording() {
		t.Fatalf("recorded %+v", h.evs)
	}

	// Search by shortcut: the recorded chord becomes the filter.
	h.e.StartKeyFilter()
	hr.Frame(h.widget)
	press(hr, "D", key.ModCommand)
	hr.Frame(h.widget)
	if h.e.KeyFilter() != "Cmd+D" || len(h.e.Visible()) != 2 || h.e.Recording() {
		t.Fatalf("key filter %q %d", h.e.KeyFilter(), len(h.e.Visible()))
	}
	// Gestures reach an active recorder only.
	if h.e.Gesture("MouseBack") {
		t.Fatal("gesture without recording")
	}
	h.e.StartKeyFilter()
	if !h.e.Gesture("MouseBack") {
		t.Fatal("gesture refused")
	}
	hr.Frame(h.widget)
	if h.e.KeyFilter() != "MouseBack" || len(h.e.Visible()) != 1 {
		t.Fatalf("gesture filter %q", h.e.KeyFilter())
	}
	h.e.StartRecording("tab.close")
	h.e.CancelRecording()
	if h.e.Recording() || h.e.RecordingFor() != "" {
		t.Fatal("cancel")
	}
}

// clickScan clicks a grid of points inside r until stop reports true.
func clickScan(hr *uitest.Harness, w uitest.Widget, r image.Rectangle, step int, stop func() bool) bool {
	for y := r.Min.Y; y < r.Max.Y; y += step {
		for x := r.Min.X; x < r.Max.X; x += step {
			hr.Click(image.Pt(x, y))
			hr.Frame(w)
			if stop() {
				return true
			}
		}
	}
	return false
}

func TestEditorButtons(t *testing.T) {
	h := &editorHost{e: Editor{Preset: "mac", Rows: sampleRows(), Invalid: []string{"x Hyper+Q"}}}
	hr := uitest.NewHarness(editorSize)
	hr.Frame(h.widget)

	// Clicking a row selects it.
	if !clickScan(hr, h.widget, image.Rect(40, 60, 41, 400), 4, func() bool { return h.e.Selected == "edit.duplicateLines" }) {
		t.Fatal("row not selectable")
	}
	// The invalid line's Remove button.
	has := func(match func(Event) bool) func() bool {
		return func() bool {
			for _, e := range h.evs {
				if match(e) {
					return true
				}
			}
			return false
		}
	}
	if !clickScan(hr, h.widget, image.Rect(780, 30, 900, 140), 8, has(func(e Event) bool { return e == DropInvalidEvent{Line: "x Hyper+Q"} })) {
		t.Fatalf("drop invalid: %+v", h.evs)
	}
	// Detail of a custom action: remove chip, no shortcut, reset.
	h.e.Selected = "app.settings"
	bottom := image.Rect(0, 420, 700, editorSize.Y)
	for name, ev := range map[string]Event{
		"remove": RemoveEvent{Action: "app.settings", Chord: "Cmd+,"},
		"none":   UnbindEvent{Action: "app.settings"},
		"reset":  ResetEvent{Action: "app.settings"},
	} {
		h.evs = nil
		if !clickScan(hr, h.widget, bottom, 6, has(func(e Event) bool { return e == ev })) {
			t.Fatalf("%s: %+v", name, h.evs)
		}
	}
	// Add shortcut starts the recorder.
	h.e.CancelRecording()
	if !clickScan(hr, h.widget, bottom, 6, func() bool { return h.e.Recording() }) {
		t.Fatal("add shortcut")
	}
	h.e.CancelRecording()

	// Conflict decision.
	h.e.Pending = &Pending{Action: "app.settings", Chord: "Cmd+D", Others: []Other{{Action: "edit.duplicateLines", Title: "Duplicate", Context: "editor"}}}
	for name, ev := range map[string]Event{
		"steal":  AddEvent{Action: "app.settings", Chord: "Cmd+D", Exclusive: true},
		"keep":   AddEvent{Action: "app.settings", Chord: "Cmd+D"},
		"cancel": CancelPendingEvent{},
	} {
		h.evs = nil
		if !clickScan(hr, h.widget, image.Rect(0, 300, 500, editorSize.Y), 6, has(func(e Event) bool { return e == ev })) {
			t.Fatalf("%s: %+v", name, h.evs)
		}
		h.e.CancelRecording()
	}
	h.e.Pending = nil

	// Reset all, the preset dropdown and "Find by shortcut".
	h.evs = nil
	if !clickScan(hr, h.widget, image.Rect(700, 0, 900, 40), 6, has(func(e Event) bool { return e == ResetAllEvent{} })) {
		t.Fatalf("reset all %+v", h.evs)
	}
	h.evs = nil
	picked := has(func(e Event) bool { return e == PresetEvent{Preset: "pc"} })
	for y := 30; y < 200 && !picked(); y += 4 {
		h.e.presetSel.Open = true
		hr.Frame(h.widget)
		hr.Click(image.Pt(150, y))
		hr.Frame(h.widget)
	}
	if !picked() {
		t.Fatalf("preset %+v", h.evs)
	}
	if !clickScan(hr, h.widget, image.Rect(600, 30, 900, 80), 6, func() bool { return h.e.filterRec.Active() }) {
		t.Fatal("find by shortcut")
	}
	h.e.CancelRecording()
	h.e.SetKeyFilter("Cmd+D")
	if !clickScan(hr, h.widget, image.Rect(600, 30, 900, 80), 6, func() bool { return h.e.KeyFilter() == "" }) {
		t.Fatal("clear key filter")
	}
}

// Option pressed twice and held records a double-modifier chord.
func TestRecorderDoubleModifier(t *testing.T) {
	h := uitest.NewHarness(image.Pt(500, 80))
	rh := &recorderHost{}
	rh.r.Start()
	h.Frame(rh.widget)
	for _, st := range []key.State{key.Press, key.Release, key.Press} {
		h.Router.Queue(key.Event{Name: key.NameAlt, State: st})
	}
	press(h, key.NameUpArrow, key.ModAlt)
	h.Frame(rh.widget)
	if rh.r.Chord() != "Alt+Alt+Up" {
		t.Fatalf("double alt %q", rh.r.Chord())
	}
	h.Router.Queue(key.Event{Name: key.NameAlt, State: key.Release})
	press(h, key.NameUpArrow, key.ModAlt)
	h.Frame(rh.widget)
	if rh.r.Chord() != "Alt+Up" {
		t.Fatalf("after release %q", rh.r.Chord())
	}
}
