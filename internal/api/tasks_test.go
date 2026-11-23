package api

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/ape-dev-de/tiegel/internal/core/bus"
	"github.com/ape-dev-de/tiegel/internal/core/tasks"
)

func TestTasksAPI(t *testing.T) {
	a := newTestApp(t)
	var mu sync.Mutex
	var events []tasks.ChangedEvent
	a.Bus().Subscribe(TopicTasksChanged, func(e bus.Event) {
		mu.Lock()
		events = append(events, e.Payload.(tasks.ChangedEvent))
		mu.Unlock()
	})
	var list []tasks.Info
	call(t, a, "tasks.list", nil, &list)
	if len(list) != 0 {
		t.Fatalf("initial %+v", list)
	}
	tk := a.BeginTask(context.Background(), tasks.Options{Title: "Fetching", Module: "git", Cancellable: true})
	fixed := a.BeginTask(context.Background(), tasks.Options{Title: "Starting server", Module: "lang-php"})
	tk.Progress(1, 4, "origin")
	call(t, a, "tasks.list", nil, &list)
	if len(list) != 2 || list[0].ID != tk.ID() || list[0].Done != 1 || list[0].Total != 4 || list[0].Text != "origin" || !list[0].Cancellable {
		t.Fatalf("list %+v", list)
	}
	wantCode(t, a, "tasks.cancel", TaskIDParams{}, CodeInvalidParams)
	wantCode(t, a, "tasks.cancel", TaskIDParams{ID: "zzz"}, CodeNotFound)
	wantCode(t, a, "tasks.cancel", TaskIDParams{ID: fixed.ID()}, CodeFailed)
	var res TaskCancelResult
	call(t, a, "tasks.cancel", TaskIDParams{ID: tk.ID()}, &res)
	if len(res.Tasks) != 2 || !res.Tasks[0].Cancelling {
		t.Fatalf("cancel result %+v", res)
	}
	select {
	case <-tk.Context().Done():
	case <-time.After(time.Second):
		t.Fatal("cancel did not reach the task")
	}
	tk.End(tk.Context().Err())
	fixed.End(nil)
	deadline := time.Now().Add(3 * time.Second)
	for {
		mu.Lock()
		n := len(events)
		empty := n > 0 && len(events[n-1].Tasks) == 0
		mu.Unlock()
		if empty {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("no final empty tasks.changed event (%d events)", n)
		}
		time.Sleep(5 * time.Millisecond)
	}
	var nilApp *App
	nilApp.BeginTask(context.Background(), tasks.Options{}).End(nil)
}

// qa.run registers a background task while the analyzers run.
func TestQARunIsTask(t *testing.T) {
	a := newTestApp(t)
	avail := true
	fakeQA(a, &avail)
	openProject(t, a, map[string]string{"a.php": "<?php\n// TODO\n", "b.php": "<?php\n"})
	seen := make(chan tasks.Info, 16)
	a.Bus().Subscribe(TopicTasksChanged, func(e bus.Event) {
		for _, ti := range e.Payload.(tasks.ChangedEvent).Tasks {
			select {
			case seen <- ti:
			default:
			}
		}
	})
	call(t, a, "qa.run", QARunParams{Tool: "qa-find"}, nil)
	select {
	case ti := <-seen:
		if ti.Module != "qa" || ti.Title != "Inspecting with Find" || !ti.Cancellable {
			t.Fatalf("task %+v", ti)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("qa.run published no task")
	}
	if l := a.Tasks().List(); len(l) != 0 {
		t.Fatalf("task left after qa.run: %+v", l)
	}
}
