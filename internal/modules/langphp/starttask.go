package langphp

import (
	"context"
	"errors"
	"sync"

	"github.com/ape-dev-de/tiegel/internal/api"
	"github.com/ape-dev-de/tiegel/internal/core/tasks"
	"github.com/ape-dev-de/tiegel/internal/lsp/supervisor"
)

// startTask lists the start of the language server (process launch and
// initialize handshake) as a background task (D507). It is not
// cancellable: "lsp.stop" is the way to stop a server.
type startTask struct {
	mu sync.Mutex
	t  *tasks.Task
}

// follow begins the task when the supervisor enters "starting" and ends
// it with the next other state (failed on crashed/not-found).
func (s *startTask) follow(app *api.App, st supervisor.Status) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if st.State == supervisor.StateStarting {
		if s.t == nil {
			name := st.Name
			if name == "" {
				name = "language server"
			}
			s.t = app.BeginTask(context.Background(), tasks.Options{Title: "Starting " + name, Module: ID})
		}
		return
	}
	if s.t == nil {
		return
	}
	var err error
	switch st.State {
	case supervisor.StateCrashed, supervisor.StateNotFound:
		reason := st.Reason
		if reason == "" {
			reason = string(st.State)
		}
		err = errors.New(reason)
	}
	s.t.End(err)
	s.t = nil
}

// end finishes a pending start task (module stop).
func (s *startTask) end() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.t.End(nil)
	s.t = nil
}
