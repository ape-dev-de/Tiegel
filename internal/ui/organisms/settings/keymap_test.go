package settings

import (
	"image"
	"testing"

	"gioui.org/io/key"
	"gioui.org/layout"

	"github.com/ape-dev-de/tiegel/internal/ui/organisms/keymapeditor"
	"github.com/ape-dev-de/tiegel/internal/ui/tokens"
	"github.com/ape-dev-de/tiegel/internal/ui/uitest"
)

// TestKeymapPage checks that the keymap page shows the keymap editor,
// forwards its events and reports recording only while it is shown.
func TestKeymapPage(t *testing.T) {
	pages := append(samplePages(), Page{ID: "core:Keymap", Title: "Keymap", Extra: ExtraKeymap})
	p := &Panel{Pages: pages, Selected: "core:Keymap"}
	p.Keymap.Preset = "mac"
	p.Keymap.Rows = []keymapeditor.Row{{Action: "app.settings", Title: "Settings…", Context: "global", Chords: []string{"Cmd+,"}}}
	var evs []Event
	w := func(gtx layout.Context, th *tokens.Theme) layout.Dimensions {
		evs = append(evs, p.Update(gtx)...)
		return p.Layout(gtx, th)
	}
	uitest.Smoke(t, "keymap page", image.Pt(1000, 700), w)

	h := uitest.NewHarness(image.Pt(1000, 700))
	h.Frame(w)
	p.Keymap.StartRecording("app.settings")
	if !p.Recording() {
		t.Fatal("recording not reported")
	}
	h.Frame(w)
	h.Router.Queue(key.Event{Name: "K", Modifiers: key.ModCommand, State: key.Press}, key.Event{Name: key.NameReturn, State: key.Press})
	h.Frame(w)
	want := KeymapEvent{keymapeditor.RecordedEvent{Action: "app.settings", Chord: "Cmd+K"}}
	if len(evs) != 1 || evs[0] != want {
		t.Fatalf("events %+v", evs)
	}
	// Leaving the page (or searching) stops a recording.
	p.Keymap.StartRecording("app.settings")
	p.SetQuery("tab")
	if p.Recording() {
		t.Fatal("recording reported while searching")
	}
	h.Frame(w)
	p.SetQuery("")
	if p.Keymap.Recording() {
		t.Fatal("recording survived the search")
	}
}
