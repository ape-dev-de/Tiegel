package statusbar

import (
	"image"
	"slices"
	"testing"

	"gioui.org/layout"

	"github.com/ape-dev-de/tiegel/internal/ui/tokens"
	"github.com/ape-dev-de/tiegel/internal/ui/uitest"
)

func TestTaskPercent(t *testing.T) {
	for v, want := range map[float32]string{-1: "", 0: "0%", 0.424: "42%", 1: "100%", 3: "100%"} {
		if got := (Task{Value: v}).Percent(); got != want {
			t.Errorf("Percent(%v) = %q, want %q", v, got, want)
		}
	}
}

func TestTaskIndicator(t *testing.T) {
	ind := &TaskIndicator{Task: Task{ID: "t1", Title: "Indexing PHP files", Value: 0.5}, More: 2, Tooltip: "2 more"}
	var clicks int
	w := func(gtx layout.Context, th *tokens.Theme) layout.Dimensions {
		if ind.Clicked(gtx) {
			clicks++
		}
		return ind.Layout(gtx, th)
	}
	d := uitest.Smoke(t, "indicator", image.Pt(600, 30), w)
	if d.Size.X < 64 || d.Size.X > 600 {
		t.Fatalf("size %v", d.Size)
	}
	h := uitest.NewHarness(image.Pt(600, 30))
	h.Frame(w)
	h.Click(image.Pt(10, d.Size.Y/2))
	h.Frame(w)
	if clicks != 1 {
		t.Fatalf("clicks %d", clicks)
	}
	// Indeterminate, failed and cancelling variants lay out too.
	for _, task := range []Task{
		{Title: "Fetching", Value: -1},
		{Title: "Pushing", Value: -1, Error: "rejected"},
		{Title: "Pulling", Value: 0.1, Cancelling: true},
	} {
		ind.Task, ind.More = task, 0
		uitest.Smoke(t, task.Title, image.Pt(600, 30), w)
	}
}

func TestTaskList(t *testing.T) {
	l := &TaskList{Empty: "No background tasks", Tasks: []Task{
		{ID: "a", Title: "Indexing PHP files", Module: "php-index", Value: 0.3, Text: "src/A.php", Cancellable: true},
		{ID: "b", Title: "Starting Phpactor", Module: "lang-php", Value: -1},
		{ID: "c", Title: "Pushing", Module: "git", Value: -1, Error: "rejected"},
		{ID: "d", Title: "Fetching", Module: "git", Value: -1, Cancellable: true, Cancelling: true},
	}}
	var got []string
	w := func(gtx layout.Context, th *tokens.Theme) layout.Dimensions {
		got = append(got, l.Update(gtx)...)
		return l.Layout(gtx, th)
	}
	size := image.Pt(420, 400)
	d := uitest.Smoke(t, "list", size, w)
	if d.Size.Y == 0 {
		t.Fatal("empty list layout")
	}
	if len(l.buttons) != 3 { // a (cancel), c (dismiss), d (disabled cancel)
		t.Fatalf("buttons %d", len(l.buttons))
	}
	h := uitest.NewHarness(size)
	h.Frame(w)
	// The cancel button of the first row sits at the right end of its row.
	h.Click(image.Pt(size.X-12, 14))
	h.Frame(w)
	h.Frame(w)
	if !slices.Equal(got, []string{"a"}) {
		t.Fatalf("clicked %v", got)
	}
	// Buttons of removed tasks are dropped.
	l.Tasks = l.Tasks[:1]
	h.Frame(w)
	if len(l.buttons) != 1 {
		t.Fatalf("buttons after removal %d", len(l.buttons))
	}
	l.Tasks = nil
	uitest.Smoke(t, "empty", size, w)
}
