package api

import (
	"github.com/ape-dev-de/tiegel/internal/core/hooks"
	"github.com/ape-dev-de/tiegel/internal/interp"
	"github.com/ape-dev-de/tiegel/internal/run"
)

// Run infrastructure (AP15.1): the kernel's run.* service runs processes
// in the project's interpreter. php-runtime resolves interpreters through
// HookRunInterpreter (no module import), plugins contribute configuration
// types through HookRunConfigTypes, and HookRunBefore lets the kernel
// (autosave) and plugins veto or prepare a run.

// Topics of the run service.
const (
	// TopicRunStarted: a run started (run.Info).
	TopicRunStarted = "run.started"
	// TopicRunOutput: new output lines of a run, batched (run.Output).
	TopicRunOutput = "run.output"
	// TopicRunExited: a run finished: exit code, duration (run.Info).
	TopicRunExited = "run.exited"
	// TopicRunRemoved: a finished run was forgotten (RunIDParams).
	TopicRunRemoved = "run.removed"
	// TopicRunConfigsChanged: the run configurations or the selection
	// changed ([]RunConfigInfo).
	TopicRunConfigsChanged = "run.configsChanged"
)

// RunRequest is the payload of the run.before guard.
type RunRequest struct {
	Name   string `json:"name"`
	Config string `json:"config,omitempty"`
	Type   string `json:"type,omitempty"`
	// Display is the command before interpreter wrapping.
	Display string `json:"display"`
	// Rerun is the run being rerun ("" for a new start).
	Rerun string `json:"rerun,omitempty"`
}

// RunInterpreterQuery is the value of the run.interpreter filter: the
// run service asks for the interpreter a spec runs in. A provider
// (php-runtime) fills Interpreter (and Mappings for the selected one) or
// explains in Err why it cannot; without a provider the run falls back
// to the host.
type RunInterpreterQuery struct {
	// ID is the requested interpreter ("" = the project's selected one).
	ID string
	// Interpreter is the answer; it may start containers (user action).
	Interpreter interp.Interpreter
	// Mappings are the host/container path mappings to use (empty: the
	// interpreter's own).
	Mappings []MappingInfo
	// Err describes why no interpreter was found.
	Err string
}

// Hook points of the run infrastructure.
var (
	// HookRunBefore runs before every start and rerun; returning an error
	// aborts it. The kernel saves all dirty documents here (fail closed).
	HookRunBefore = hooks.NewGuard[RunRequest]("run.before")
	// HookRunConfigTypes collects the run configuration types: the kernel
	// contributes the core types (PHP script, command, Composer script),
	// plugins append theirs (e.g. phpunit) to a copy of the slice. The
	// first type with an ID wins.
	HookRunConfigTypes = hooks.NewFilter[[]run.ConfigType]("run.configTypes")
	// HookRunInterpreter resolves the interpreter of a run (provided by
	// php-runtime).
	HookRunInterpreter = hooks.NewFilter[RunInterpreterQuery]("run.interpreter")
)
