package gui

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/ape-dev-de/tiegel/internal/core/tasks"
	"github.com/ape-dev-de/tiegel/internal/ui/organisms/statusbar"
)

// A running backend task appears in the footer; the popup lists it and
// cancelling it ends the task's context and removes the entry.
func TestBackgroundTasksStatus(t *testing.T) {
	e := newEnv(t, true)
	e.settle()
	d := e.v.tasks
	has := func(title string) bool {
		return slices.ContainsFunc(d.t.List, func(x tasks.Info) bool { return x.Title == title })
	}
	push := e.app.BeginTask(context.Background(), tasks.Options{Title: "Pushing main", Module: "git", Cancellable: true})
	push.Progress(1, 4, "origin")
	e.waitFor("task in controller", func() bool {
		i := slices.IndexFunc(d.t.List, func(x tasks.Info) bool { return x.ID == push.ID() })
		return i >= 0 && d.t.List[i].Done == 1
	})
	lsp := e.app.BeginTask(context.Background(), tasks.Options{Title: "Starting Phpactor", Module: "lang-php"})
	e.waitFor("second task", func() bool { return has("Starting Phpactor") })
	e.frame()

	// The footer entry shows the newest task and "+N".
	items := d.status()
	if len(items) != 1 || items[0].ID != statusTasksID || items[0].Widget != &d.ind {
		t.Fatalf("status items %+v", items)
	}
	if !slices.ContainsFunc(e.v.status.Right, func(it statusbar.Item) bool { return it.ID == statusTasksID }) {
		t.Fatal("tasks entry not in the status bar")
	}
	if s := d.summary(); !strings.HasPrefix(s, "Starting Phpactor") || !strings.Contains(s, "+") {
		t.Fatalf("summary %q", s)
	}
	if !strings.Contains(d.ind.Tooltip, "Pushing main 25%") {
		t.Fatalf("tooltip %q", d.ind.Tooltip)
	}

	// The popup lists all tasks, newest first.
	d.open()
	e.frame()
	if !e.v.PopupOpen() || d.popup == nil || len(d.list.Tasks) < 2 || d.list.Tasks[0].Title != "Starting Phpactor" {
		t.Fatalf("popup %v %+v", e.v.PopupOpen(), d.list.Tasks)
	}
	i := slices.IndexFunc(d.list.Tasks, func(x statusbar.Task) bool { return x.ID == push.ID() })
	if i < 0 || !d.list.Tasks[i].Cancellable || d.list.Tasks[i].Percent() != "25%" {
		t.Fatalf("push row %+v", d.list.Tasks)
	}

	// Cancel (the popup's button calls Tasks.Cancel).
	d.t.Cancel(push.ID())
	e.frame()
	if j := slices.IndexFunc(d.list.Tasks, func(x statusbar.Task) bool { return x.ID == push.ID() }); j < 0 || !d.list.Tasks[j].Cancelling {
		t.Fatalf("not marked cancelling: %+v", d.list.Tasks)
	}
	select {
	case <-push.Context().Done():
	default:
		e.settle()
		if push.Context().Err() == nil {
			t.Fatal("cancel did not reach the backend task")
		}
	}
	push.End(push.Context().Err())
	e.waitFor("cancelled task gone", func() bool { return !has("Pushing main") })

	// A failed task stays with its error; dismissing removes it.
	lsp.End(errors.New("crashed"))
	e.waitFor("failed task", func() bool {
		i := slices.IndexFunc(d.t.List, func(x tasks.Info) bool { return x.ID == lsp.ID() })
		return i >= 0 && d.t.List[i].Error == "crashed"
	})
	e.frame()
	d.t.Cancel(lsp.ID())
	e.waitFor("dismissed", func() bool { return !has("Starting Phpactor") })
	e.frame()

	// Closing the popup forgets it; without tasks the entry disappears.
	e.v.ClosePopup()
	e.frame()
	if d.popup != nil {
		t.Fatal("popup still tracked")
	}
	e.waitFor("no tasks", func() bool { return len(d.t.List) == 0 })
	e.frame()
	if len(d.status()) != 0 || d.summary() != "" {
		t.Fatalf("status without tasks %+v", d.status())
	}
}
