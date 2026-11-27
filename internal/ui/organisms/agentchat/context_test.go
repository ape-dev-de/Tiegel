package agentchat

import (
	"image"
	"image/png"
	"os"
	"path/filepath"
	"testing"

	"gioui.org/layout"

	"github.com/ape-dev-de/tiegel/internal/ui/tokens"
	"github.com/ape-dev-de/tiegel/internal/ui/uitest"
)

// contextChat is a session whose first message carries knowledge
// context (Sample).
func contextChat() *Chat {
	c := &Chat{}
	Sample(c)
	c.Entries = c.Entries[:3]
	c.Changes = nil
	return c
}

// The context of a user message is collapsed below it; clicking the
// header opens the hits, clicking a hit asks to open its page.
func TestContextDisclosure(t *testing.T) {
	size := image.Pt(420, 600)
	c := contextChat()
	h := uitest.NewHarness(size)
	var evs []Event
	w := func(gtx layout.Context, th *tokens.Theme) layout.Dimensions {
		evs = append(evs, c.Update(gtx)...)
		return c.Layout(gtx, th)
	}
	h.Frame(w)
	// Find the header: the click opens the row.
	opened := false
	for y := 4; y < size.Y && !opened; y += 4 {
		h.Click(image.Pt(120, y))
		h.Frame(w)
		h.Frame(w)
		opened = c.rows[0] != nil && c.rows[0].open
	}
	if !opened {
		t.Fatal("context header not found")
	}
	var got *OpenContextEvent
	for y := 4; y < size.Y && got == nil; y += 4 {
		evs = nil
		h.Click(image.Pt(160, y))
		h.Frame(w)
		h.Frame(w)
		for _, e := range evs {
			if oe, ok := e.(OpenContextEvent); ok {
				got = &oe
			}
		}
		if !c.rows[0].open {
			// The click hit the header again: reopen.
			c.rows[0].open = true
		}
	}
	if got == nil || got.File == "" || got.Anchor == "" {
		t.Fatalf("no open event: %+v", got)
	}
}

func TestContextShots(t *testing.T) {
	c := contextChat()
	w := func(gtx layout.Context, th *tokens.Theme) layout.Dimensions { return c.Layout(gtx, th) }
	uitest.Smoke(t, "context-closed", image.Pt(420, 600), w)
	c.rows[0].open = true
	uitest.Smoke(t, "context-open", image.Pt(420, 600), w)
	uitest.Smoke(t, "context-narrow", image.Pt(220, 400), w)
	dir := os.Getenv("TIEGEL_SHOTS")
	if dir == "" {
		return
	}
	sh := tokens.NewBundledShaper()
	for _, th := range []*tokens.Theme{tokens.Dark(sh), tokens.Light(sh)} {
		img, err := uitest.Render(th, image.Pt(420, 560), w)
		if err != nil {
			t.Skipf("no renderer: %v", err)
		}
		f, err := os.Create(filepath.Join(dir, "agentchat-context-"+th.Mode.String()+".png"))
		if err != nil {
			t.Fatal(err)
		}
		_ = png.Encode(f, img)
		f.Close()
	}
}
