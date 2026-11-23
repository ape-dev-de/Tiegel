package controller

import (
	"context"
	"encoding/json"

	"github.com/ape-dev-de/tiegel/internal/api"
	"github.com/ape-dev-de/tiegel/internal/core/tasks"
)

// Background tasks (D508): the status bar shows the backend's task list
// (tasks.list, kept current by tasks.changed events) and cancels tasks
// with tasks.cancel.

// TaskInfo is one background task (see tasks.Info).
type TaskInfo = tasks.Info

// Tasks is the client state of the background tasks. Fields are read and
// written on the UI goroutine.
type Tasks struct {
	c *Controller
	// List holds the tasks, oldest first.
	List []TaskInfo
	// Version increases with every change of List.
	Version uint64

	unsub func()
	// gen discards list results older than the newest event.
	gen uint64
}

// NewTasks returns the task state of c and subscribes to the change
// events. Call Close when done.
func NewTasks(c *Controller) *Tasks {
	t := &Tasks{c: c}
	unsub, err := c.be.Subscribe(c.ctx, []string{api.TopicTasksChanged}, func(e api.Event) {
		var ev tasks.ChangedEvent
		if json.Unmarshal(e.Payload, &ev) != nil {
			return
		}
		c.post(func() {
			t.gen++
			t.set(ev.Tasks)
		})
	})
	if err != nil {
		unsub = func() {}
	}
	t.unsub = unsub
	return t
}

// Close ends the event subscription.
func (t *Tasks) Close() { t.unsub() }

func (t *Tasks) set(l []TaskInfo) {
	if l == nil {
		l = []TaskInfo{}
	}
	t.List = l
	t.Version++
}

// Load fetches the current list (after a project switch, when events of
// another project were not delivered).
func (t *Tasks) Load() {
	gen := t.gen
	t.c.Go(func(ctx context.Context) func() {
		var l []TaskInfo
		if err := t.c.call(ctx, "tasks.list", nil, &l); err != nil {
			return nil
		}
		return func() {
			if t.gen == gen {
				t.set(l)
			}
		}
	})
}

// Running returns the tasks that have not failed.
func (t *Tasks) Running() []TaskInfo {
	out := make([]TaskInfo, 0, len(t.List))
	for _, x := range t.List {
		if x.Error == "" {
			out = append(out, x)
		}
	}
	return out
}

// Cancel requests cancellation of task id (a failed task is dismissed).
// The entry shows "cancelling" until the backend reports the end.
func (t *Tasks) Cancel(id string) {
	for i := range t.List {
		if t.List[i].ID == id && t.List[i].Error == "" {
			t.List[i].Cancelling = true
			t.Version++
		}
	}
	t.c.Go(func(ctx context.Context) func() {
		var res api.TaskCancelResult
		err := t.c.call(ctx, "tasks.cancel", api.TaskIDParams{ID: id}, &res)
		return func() {
			if err != nil && api.ErrorCode(err) != api.CodeNotFound {
				t.c.notice(err)
			}
		}
	})
}
