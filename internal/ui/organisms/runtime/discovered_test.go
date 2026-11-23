package runtime

import (
	"image"
	"testing"

	"gioui.org/layout"

	"github.com/ape-dev-de/tiegel/internal/ui/tokens"
	"github.com/ape-dev-de/tiegel/internal/ui/uitest"
)

func TestDiscoveredList(t *testing.T) {
	l := &DiscoveredList{Items: []Discovered{
		{ID: "compose:x:app", Label: "PHP (compose x/app)", Kind: KindCompose, Where: "../../docker-compose.yaml", Mapping: "project → /var/www/project", Score: 80, Reasons: []string{"mounts the PHP root"}},
		{ID: "docker:php:8.3", Label: "Docker image php:8.3", Kind: KindDocker},
	}}
	var evs []Event
	w := func(gtx layout.Context, th *tokens.Theme) layout.Dimensions {
		evs = append(evs, l.Update(gtx)...)
		return l.Layout(gtx, th)
	}
	h := uitest.NewHarness(image.Pt(560, 300))
	h.Frame(w)
	for y := 2; y < 60 && len(evs) == 0; y += 4 {
		h.Click(image.Pt(530, y))
		h.Frame(w)
	}
	if len(evs) != 1 || evs[0].(UseDiscoveredEvent).ID != "compose:x:app" {
		t.Fatalf("events %+v", evs)
	}
	uitest.Smoke(t, "loading", image.Pt(560, 300), (&DiscoveredList{Loading: true}).Layout)
}
