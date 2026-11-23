package langphp

import (
	"testing"

	"github.com/ape-dev-de/tiegel/internal/lsp/supervisor"
	"github.com/ape-dev-de/tiegel/internal/modules/modtest"
)

func TestStartTask(t *testing.T) {
	a := modtest.NewApp(t, nil, nil)
	var s startTask
	s.follow(a, supervisor.Status{State: supervisor.StateOff}) // no task: nothing
	s.follow(a, supervisor.Status{State: supervisor.StateStarting, Name: "Phpactor"})
	s.follow(a, supervisor.Status{State: supervisor.StateStarting, Name: "Phpactor"}) // once
	l := a.Tasks().List()
	if len(l) != 1 || l[0].Title != "Starting Phpactor" || l[0].Module != ID || l[0].Cancellable {
		t.Fatalf("tasks %+v", l)
	}
	s.follow(a, supervisor.Status{State: supervisor.StateRunning})
	if l := a.Tasks().List(); len(l) != 0 {
		t.Fatalf("running: %+v", l)
	}
	s.follow(a, supervisor.Status{State: supervisor.StateStarting})
	s.follow(a, supervisor.Status{State: supervisor.StateCrashed})
	if l := a.Tasks().List(); len(l) != 1 || l[0].Title != "Starting language server" || l[0].Error != "crashed" {
		t.Fatalf("crashed: %+v", l)
	}
	s.follow(a, supervisor.Status{State: supervisor.StateStarting})
	s.end()
	s.end()
}
