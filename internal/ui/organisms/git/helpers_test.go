package git

import (
	"image"
	"testing"

	"gioui.org/layout"

	"github.com/ape-dev-de/tiegel/internal/gitcli"
	"github.com/ape-dev-de/tiegel/internal/ui/organisms/git/gitsample"
	"github.com/ape-dev-de/tiegel/internal/ui/tokens"
	"github.com/ape-dev-de/tiegel/internal/ui/uitest"
)

var area = image.Pt(1200, 700)

func sampleChanges(t testing.TB) []Change {
	t.Helper()
	ch, err := gitsample.Changes()
	if err != nil {
		t.Fatal(err)
	}
	out := make([]Change, len(ch))
	for i, c := range ch {
		out[i] = Change{Status: c.Status, Diff: c.Diff}
	}
	return out
}

func diffFor(t testing.TB, path string) *gitcli.FileDiff {
	t.Helper()
	for _, c := range sampleChanges(t) {
		if c.Status.Path == path && c.Diff != nil {
			return c.Diff
		}
	}
	t.Fatalf("no diff for %s", path)
	return nil
}

// frame lays out w once in h and returns the events of Update.
func frame(h *uitest.Harness, w interface {
	Update(layout.Context) []Event
	Layout(layout.Context, *tokens.Theme) layout.Dimensions
}) []Event {
	var ev []Event
	h.Frame(func(gtx layout.Context, th *tokens.Theme) layout.Dimensions {
		d := w.Layout(gtx, th)
		ev = w.Update(gtx)
		return d
	})
	return ev
}
