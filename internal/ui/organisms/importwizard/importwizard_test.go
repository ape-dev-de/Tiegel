package importwizard

import (
	"image"
	"reflect"
	"testing"

	"gioui.org/layout"

	"github.com/ape-dev-de/tiegel/internal/ui/atoms"
	"github.com/ape-dev-de/tiegel/internal/ui/tokens"
	"github.com/ape-dev-de/tiegel/internal/ui/uitest"
)

func sample() *View {
	return &View{
		Source: "RefIDE2025.2", Project: "/p/shop", Notes: []string{"note"},
		Areas: []Area{
			{ID: "editor", Label: "Editor", Count: 2, Total: 3, Selected: true},
			{ID: "recentProjects", Label: "Recent projects", Count: 1, Total: 1},
			{ID: "keymap", Label: "Keymap"},
		},
		Items: []Item{
			{Area: "editor", Name: "Editor font", Status: StatusReady, Detail: "Fira Code, 14 pt", Source: "options/editor-font.xml"},
			{Area: "editor", Name: "Soft wrap", Status: StatusSkipped, Detail: "already set"},
			{Area: "editor", Name: "Console font", Status: StatusUnsupported},
			{Area: "recentProjects", Name: "Recent projects", Status: StatusFailed, Detail: "boom"},
		},
	}
}

func TestWizard(t *testing.T) {
	v := sample()
	var evs []Event
	w := func(gtx layout.Context, th *tokens.Theme) layout.Dimensions {
		evs = append(evs, v.Update(gtx)...)
		return v.Layout(gtx, th)
	}
	uitest.Smoke(t, "preview", image.Pt(720, 480), w)
	if !reflect.DeepEqual(v.selected(), []string{"editor"}) {
		t.Errorf("selected = %v", v.selected())
	}
	// The empty area cannot be selected; Done disables all boxes.
	if len(v.checks) != 3 || !v.checks[2].Disabled || v.checks[0].Disabled {
		t.Errorf("checkboxes = %+v", v.checks)
	}
	v.Done, v.Error = true, "1 failed"
	uitest.Smoke(t, "done", image.Pt(720, 480), w)
	if !v.checks[0].Disabled {
		t.Error("done: checkboxes enabled")
	}
	v.Done, v.Busy, v.Error = false, true, ""
	uitest.Smoke(t, "busy", image.Pt(720, 480), w)
	uitest.Smoke(t, "empty", image.Pt(720, 480), (&View{}).Layout)
	if len(evs) != 0 {
		t.Errorf("events without input: %v", evs)
	}
}

func TestBadges(t *testing.T) {
	for _, tc := range []struct {
		status string
		done   bool
		kind   atoms.BadgeKind
	}{
		{StatusReady, false, atoms.BadgeAccent}, {StatusReady, true, atoms.BadgeNeutral},
		{StatusImported, true, atoms.BadgeSuccess}, {StatusUnsupported, false, atoms.BadgeWarning},
		{StatusFailed, true, atoms.BadgeDanger}, {StatusSkipped, false, atoms.BadgeNeutral},
	} {
		if b := badge(tc.status, tc.done); b.Kind != tc.kind || b.Text == "" {
			t.Errorf("%s/%v = %+v", tc.status, tc.done, b)
		}
	}
	if joinText("", "b") != "b" || joinText("a", "b") != "a · b" {
		t.Error("joinText")
	}
}
