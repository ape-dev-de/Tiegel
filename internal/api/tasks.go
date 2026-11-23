package api

import (
	"context"
	"errors"

	"github.com/ape-dev-de/tiegel/internal/core/tasks"
)

// Background tasks (D505–D507): the App owns a tasks.Registry; modules
// register their long-running operations with BeginTask and clients list
// and cancel them with tasks.list / tasks.cancel. Changes are published
// as TopicTasksChanged (tasks.ChangedEvent, the complete list, throttled
// to at most ten events per second).

// TopicTasksChanged is published when tasks begin, progress or end.
const TopicTasksChanged = tasks.Topic

// TaskIDParams are the params of "tasks.cancel".
type TaskIDParams struct {
	ID string `json:"id"`
}

// TaskCancelResult is the result of "tasks.cancel": the remaining tasks.
type TaskCancelResult struct {
	Tasks []tasks.Info `json:"tasks"`
}

// Tasks returns the App's background task registry.
func (a *App) Tasks() *tasks.Registry { return a.tasks }

// BeginTask registers a background task. The returned task's context
// derives from ctx (use context.WithoutCancel for tasks that outlive the
// request) and ends on tasks.cancel; call End when the work is done.
func (a *App) BeginTask(ctx context.Context, o tasks.Options) *tasks.Task {
	if a == nil {
		return (*tasks.Registry)(nil).Begin(ctx, o)
	}
	return a.tasks.Begin(ctx, o)
}

func (a *App) registerTasks() {
	a.tasks = tasks.New(func(e tasks.ChangedEvent) { a.bus.Publish(TopicTasksChanged, e) })
	mustHandle(a, "tasks.list", "Running background tasks (indexing, git, Composer, QA, language server) with progress", a.tasksList)
	mustHandle(a, "tasks.cancel", "Cancel a running background task (or dismiss a failed one)", a.tasksCancel)
}

func (a *App) tasksList(context.Context, struct{}) ([]tasks.Info, error) {
	return a.tasks.List(), nil
}

func (a *App) tasksCancel(_ context.Context, p TaskIDParams) (TaskCancelResult, error) {
	if p.ID == "" {
		return TaskCancelResult{}, Errorf(CodeInvalidParams, "id is required")
	}
	if err := a.tasks.Cancel(p.ID); err != nil {
		if errors.Is(err, tasks.ErrNotFound) {
			return TaskCancelResult{}, Errorf(CodeNotFound, "%w", err)
		}
		return TaskCancelResult{}, Errorf(CodeFailed, "%w", err)
	}
	return TaskCancelResult{Tasks: a.tasks.List()}, nil
}
