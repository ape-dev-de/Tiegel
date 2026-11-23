package git

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/ape-dev-de/tiegel/internal/api"
	"github.com/ape-dev-de/tiegel/internal/core/bus"
	"github.com/ape-dev-de/tiegel/internal/core/module"
	"github.com/ape-dev-de/tiegel/internal/core/tasks"
	"github.com/ape-dev-de/tiegel/internal/modules/modtest"
)

// withTask lists the call as a task and passes the cancellable task
// context to the handler.
func TestWithTaskCancel(t *testing.T) {
	a := modtest.NewApp(t, []module.Module{New(Options{})}, nil)
	m := &Module{app: a}
	started := make(chan struct{})
	h := withTask(m, true, fetchTitle, func(ctx context.Context, _ FetchParams) (int, error) {
		close(started)
		<-ctx.Done()
		return 0, ctx.Err()
	})
	done := make(chan error, 1)
	go func() { _, err := h(context.Background(), FetchParams{Remote: "origin"}); done <- err }()
	<-started
	l := a.Tasks().List()
	if len(l) != 1 || l[0].Title != "Fetching origin" || l[0].Module != ID || !l[0].Cancellable {
		t.Fatalf("tasks %+v", l)
	}
	modtest.Call(t, a, "tasks.cancel", api.TaskIDParams{ID: l[0].ID}, nil)
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("err %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("handler not cancelled")
	}
	if l := a.Tasks().List(); len(l) != 0 {
		t.Fatalf("cancelled task listed: %+v", l)
	}
}

func TestCommitIsTask(t *testing.T) {
	a, dir := setup(t)
	rec := modtest.Record(t, a, api.TopicTasksChanged)
	write(t, dir, "a.txt", "changed\n")
	modtest.Call(t, a, "git.commit", GitCommitParams{Message: "c", Stage: []string{"a.txt"}}, nil)
	rec.Wait(t, api.TopicTasksChanged, func(e bus.Event) bool {
		ts := e.Payload.(tasks.ChangedEvent).Tasks
		return len(ts) == 1 && ts[0].Title == "Committing" && !ts[0].Cancellable
	})
}

func TestTaskTitles(t *testing.T) {
	cases := map[string]string{
		commitTitle(true):                           "Committing and pushing",
		pushTitle(PushParams{}):                     "Pushing",
		pushTitle(PushParams{Branch: "main"}):       "Pushing main",
		fetchTitle(FetchParams{}):                   "Fetching",
		mergeTitle(MergeParams{Branch: "dev"}):      "Merging dev",
		mergeTitle(MergeParams{Abort: true}):        "Aborting merge",
		mergeTitle(MergeParams{Continue: true}):     "Continuing merge",
		rebaseTitle(RebaseParams{Onto: "main"}):     "Rebasing onto main",
		rebaseTitle(RebaseParams{Abort: true}):      "Aborting rebase",
		rebaseTitle(RebaseParams{Continue: true}):   "Continuing rebase",
		titled[PullParams]("Pulling")(PullParams{}): "Pulling",
	}
	for got, want := range cases {
		if got != want {
			t.Errorf("title %q, want %q", got, want)
		}
	}
}
