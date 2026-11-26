package agentchat

import (
	"image"
	"image/png"
	"os"
	"path/filepath"
	"testing"

	"gioui.org/io/key"
	"gioui.org/layout"

	"github.com/ape-dev-de/tiegel/internal/ui/tokens"
	"github.com/ape-dev-de/tiegel/internal/ui/uitest"
)

func TestChatLayoutAndShots(t *testing.T) {
	c := &Chat{}
	Sample(c)
	c.Entries[1].Key = 1
	w := func(gtx layout.Context, th *tokens.Theme) layout.Dimensions { return c.Layout(gtx, th) }
	uitest.Smoke(t, "chat", image.Pt(420, 900), w)
	uitest.Smoke(t, "narrow", image.Pt(220, 300), w)
	empty := &Chat{Notice: &NoticeBox{Error: true, Title: "opencode is not installed", Detail: "npm install -g opencode-ai", Action: "Install…"}}
	uitest.Smoke(t, "empty", image.Pt(420, 500), empty.Layout)
	busy := &Chat{Busy: true, State: "working…", Entries: c.Entries[:1]}
	uitest.Smoke(t, "busy", image.Pt(420, 500), busy.Layout)
	dir := os.Getenv("TIEGEL_SHOTS")
	if dir == "" {
		return
	}
	sh := tokens.NewBundledShaper()
	for _, th := range []*tokens.Theme{tokens.Dark(sh), tokens.Light(sh)} {
		for name, ch := range map[string]*Chat{"agentchat": c, "agentchat-empty": empty} {
			img, err := uitest.Render(th, image.Pt(420, 900), ch.Layout)
			if err != nil {
				t.Skipf("no renderer: %v", err)
			}
			f, err := os.Create(filepath.Join(dir, name+"-"+th.Mode.String()+".png"))
			if err != nil {
				t.Fatal(err)
			}
			_ = png.Encode(f, img)
			f.Close()
		}
	}
}

// find returns the first point whose frame reports ev when clicked.
func clickEvents(t *testing.T, c *Chat, size image.Point, at image.Point) []Event {
	t.Helper()
	h := uitest.NewHarness(size)
	var evs []Event
	w := func(gtx layout.Context, th *tokens.Theme) layout.Dimensions {
		evs = append(evs, c.Update(gtx)...)
		return c.Layout(gtx, th)
	}
	h.Frame(w)
	h.Click(at)
	h.Frame(w)
	h.Frame(w)
	return evs
}

func TestChatEvents(t *testing.T) {
	size := image.Pt(420, 900)
	// Scan the window for clickable targets and collect the events.
	seen := map[string]bool{}
	for y := 4; y < size.Y; y += 8 {
		for _, x := range []int{30, 120, 200, 260, 330, 370, 400} {
			c := &Chat{}
			Sample(c)
			for _, e := range clickEvents(t, c, size, image.Pt(x, y)) {
				switch e.(type) {
				case SettingsEvent:
					seen["settings"] = true
				case NewSessionEvent:
					seen["new"] = true
				case AnswerEvent:
					seen["answer"] = true
				case DiffEvent:
					seen["diff"] = true
				case RevertEvent:
					seen["revert"] = true
				case KeepAllEvent:
					seen["keep"] = true
				}
			}
		}
	}
	for _, want := range []string{"settings", "new", "answer", "diff", "revert", "keep"} {
		if !seen[want] {
			t.Errorf("no %s event found (seen %v)", want, seen)
		}
	}
}

func TestSendAndCancel(t *testing.T) {
	c := &Chat{}
	h := uitest.NewHarness(image.Pt(420, 600))
	var evs []Event
	w := func(gtx layout.Context, th *tokens.Theme) layout.Dimensions {
		evs = append(evs, c.Update(gtx)...)
		return c.Layout(gtx, th)
	}
	h.Frame(w)
	c.Input().SetText("  hello agent ")
	c.submit(c.input.Text())
	h.Frame(w)
	if len(evs) != 1 || evs[0] != (SendEvent{Text: "hello agent"}) || c.input.Text() != "" {
		t.Fatalf("%v %q", evs, c.input.Text())
	}
	// Busy: nothing is sent; Escape in the focused input cancels.
	c.Busy = true
	c.submit("x")
	h.Frame(func(gtx layout.Context, th *tokens.Theme) layout.Dimensions {
		gtx.Execute(key.FocusCmd{Tag: c.Input()})
		return w(gtx, th)
	})
	h.Router.Queue(key.Event{Name: key.NameEscape, State: key.Press})
	h.Frame(w)
	h.Frame(w)
	if len(evs) != 2 || evs[1] != (CancelEvent{}) {
		t.Fatalf("%v", evs)
	}
	c.Busy = false
	c.Notice = &NoticeBox{Title: "x", Action: "Install…"}
	c.Models = []Choice{{Value: "a"}}
	h.Frame(w)
}
