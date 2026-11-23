package gui

import (
	"fmt"
	"strings"

	"gioui.org/layout"

	"github.com/ape-dev-de/tiegel/internal/app/gui/controller"
	"github.com/ape-dev-de/tiegel/internal/ui/organisms/statusbar"
	"github.com/ape-dev-de/tiegel/internal/ui/tokens"
)

// Background tasks in the footer (D508): one status bar entry shows the
// most recent running task (title, progress bar, percentage, "+N" for
// the others); a click opens the "Background Tasks" popup with every
// task and its cancel button. It replaces the former "Indexing n/m"
// entry and the git busy text. The state lives in controller.Tasks.

const statusTasksID = "tasks"

type tasksDock struct {
	v    *View
	t    *controller.Tasks
	ind  statusbar.TaskIndicator
	list statusbar.TaskList
	// popup is the open task popup (nil: closed).
	popup *Popup
	root  string
	seen  uint64
}

func installTasks(v *View) *tasksDock {
	d := &tasksDock{v: v, t: controller.NewTasks(v.ctl)}
	d.list.Empty = "No background tasks"
	d.seen = ^uint64(0)
	v.Registry.AddFrameHandler(d.frame)
	v.Registry.AddStatusProvider(d.status)
	return d
}

// toTask converts a controller task for the status bar organisms.
func toTask(t controller.TaskInfo) statusbar.Task {
	return statusbar.Task{ID: t.ID, Title: t.Title, Module: t.Module, Text: t.Text, Value: float32(t.Fraction()),
		Cancellable: t.Cancellable, Cancelling: t.Cancelling, Error: t.Error}
}

// frame reloads the list after a project switch, follows the controller
// state and opens the popup on a click.
func (d *tasksDock) frame(v *View, gtx layout.Context) {
	if root := v.ctl.Project.Root; root != d.root {
		d.root = root
		d.t.Load()
	}
	if d.seen != d.t.Version {
		d.seen = d.t.Version
		d.sync()
	}
	if d.popup != nil && v.popup != d.popup {
		d.popup = nil // closed (Escape, click outside, another overlay)
	}
	if d.ind.Clicked(gtx) {
		d.open()
	}
}

// sync copies the controller list into the widgets: the indicator shows
// the newest running task (or the newest failed one).
func (d *tasksDock) sync() {
	l := d.t.List
	d.list.Tasks = d.list.Tasks[:0]
	for i := len(l) - 1; i >= 0; i-- { // newest first
		d.list.Tasks = append(d.list.Tasks, toTask(l[i]))
	}
	if len(l) == 0 {
		return
	}
	show := l[len(l)-1]
	if run := d.t.Running(); len(run) > 0 {
		show = run[len(run)-1]
	}
	d.ind.Task, d.ind.More = toTask(show), len(l)-1
	titles := make([]string, 0, len(l))
	for _, t := range d.list.Tasks {
		s := t.Title
		if p := t.Percent(); p != "" {
			s += " " + p
		}
		if t.Error != "" {
			s += " – failed: " + t.Error
		}
		titles = append(titles, s)
	}
	d.ind.Tooltip = strings.Join(titles, "\n") + "\n(click to show all background tasks)"
}

// status is the footer entry (none without tasks).
func (d *tasksDock) status() []statusbar.Item {
	if len(d.t.List) == 0 {
		return nil
	}
	return []statusbar.Item{{ID: statusTasksID, Widget: &d.ind}}
}

// open shows the task list with a cancel button per task.
func (d *tasksDock) open() {
	p := &Popup{Title: "Background Tasks", Width: 480,
		Body: func(gtx layout.Context, th *tokens.Theme) layout.Dimensions {
			for _, id := range d.list.Update(gtx) {
				d.t.Cancel(id)
			}
			return d.list.Layout(gtx, th)
		},
	}
	d.v.OpenPopup(p)
	d.popup = p
}

// summary describes the task state in one line (tests, accessibility).
func (d *tasksDock) summary() string {
	if len(d.t.List) == 0 {
		return ""
	}
	s := d.ind.Task.Title
	if p := d.ind.Task.Percent(); p != "" {
		s += " " + p
	}
	if d.ind.More > 0 {
		s += fmt.Sprintf(" +%d", d.ind.More)
	}
	return s
}
