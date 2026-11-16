package git

import (
	"image"
	"testing"

	"github.com/ape-dev-de/tiegel/internal/ui/atoms"
	"github.com/ape-dev-de/tiegel/internal/ui/organisms/git/gitsample"
	"github.com/ape-dev-de/tiegel/internal/ui/uitest"
)

func TestLogViewSmoke(t *testing.T) {
	v := NewLogView()
	uitest.Smoke(t, "log empty", area, v.Layout)
	v.SetCommits(gitsample.Commits(0, 40), true)
	uitest.Smoke(t, "log", area, v.Layout)
	uitest.Smoke(t, "log narrow", image.Pt(300, 200), v.Layout)
}

func TestLogViewSelectAndPaging(t *testing.T) {
	h := uitest.NewHarness(image.Pt(900, 400))
	v := NewLogView()
	v.SetCommits(gitsample.Commits(0, 100), true)
	if ev := frame(h, v); len(ev) != 0 {
		t.Fatalf("no paging while far from the end: %v", ev)
	}
	// Header: 4 + small text + 4, divider; first row below.
	h.Click(image.Pt(300, 23+1+12))
	ev := frame(h, v)
	if len(ev) != 1 || ev[0] != (CommitSelected{Hash: v.commits[0].Hash}) || v.SelectedHash() != v.commits[0].Hash {
		t.Fatalf("select: %v", ev)
	}
	// Scroll to the end: exactly one page request until answered.
	v.list.ScrollTo(95)
	ev = frame(h, v)
	if len(ev) != 1 {
		t.Fatalf("paging events %v", ev)
	}
	req, ok := ev[0].(LogPageRequested)
	if !ok || req.Loaded != 100 || req.After != v.commits[99].Hash || !v.Loading() {
		t.Fatalf("request %+v", ev[0])
	}
	if ev := frame(h, v); len(ev) != 0 {
		t.Fatalf("duplicate request: %v", ev)
	}
	v.AppendCommits(gitsample.Commits(100, 50), false)
	if v.Len() != 150 || v.Loading() {
		t.Fatal("append")
	}
	v.list.ScrollTo(140)
	if ev := frame(h, v); len(ev) != 0 {
		t.Fatalf("no more pages: %v", ev)
	}
	// Selection survives a reload.
	sel := v.SelectedHash()
	v.SetCommits(gitsample.Commits(0, 10), false)
	if v.SelectedHash() != sel {
		t.Error("selection lost on reload")
	}
}

func TestRefBadges(t *testing.T) {
	cases := map[string]atoms.BadgeKind{
		"HEAD -> main": atoms.BadgeAccent, "HEAD": atoms.BadgeAccent, "tag: v1.0": atoms.BadgeWarning,
		"origin/main": atoms.BadgeInfo, "feature": atoms.BadgeSuccess,
	}
	for ref, k := range cases {
		if b := refBadge(ref); b.Kind != k || b.Text == "" {
			t.Errorf("%s: %+v", ref, b)
		}
	}
	if refBadge("HEAD -> main").Text != "main" || refBadge("tag: v1.0").Text != "v1.0" {
		t.Error("badge text")
	}
}
