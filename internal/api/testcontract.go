package api

import (
	"time"

	"github.com/ape-dev-de/tiegel/internal/core/hooks"
	"github.com/ape-dev-de/tiegel/internal/teamcity"
)

// Test runner contract (AP15.2–15.4). The kernel knows no test framework:
// a test plugin registers the methods of the "test" namespace
// (test.status, test.discover, test.run, test.rerunFailed, test.results,
// test.lastStatus, test.goto, test.create) and runs tests through the
// run infrastructure (run.start with a spec of its configuration type).
// The types below are what clients (GUI, headless, serve) program
// against; results come from TeamCity service messages (internal/teamcity).

// Topics of the test namespace.
const (
	// TopicTestStarted: a test run started (TestRun without tree).
	TopicTestStarted = "test.started"
	// TopicTestUpdated: results of a running test run changed (TestRun
	// with tree; throttled).
	TopicTestUpdated = "test.updated"
	// TopicTestFinished: a test run ended (TestRun with tree).
	TopicTestFinished = "test.finished"
)

// TestScope selects what a test run executes. Empty: everything (the
// configuration's default test suites). Combinations narrow down: Path
// with Class and Method runs exactly that method.
type TestScope struct {
	// Suite is a test suite of the configuration file.
	Suite string `json:"suite,omitempty"`
	// Path is a test file or directory (absolute or project relative).
	Path string `json:"path,omitempty"`
	// Class is a test class FQN (without leading backslash).
	Class string `json:"class,omitempty"`
	// Method is a test method of Class.
	Method string `json:"method,omitempty"`
	// DataSet is one data set of Method ("#0", "\"name\"" or just name).
	DataSet string `json:"dataSet,omitempty"`
	// Filter is a raw filter expression of the framework.
	Filter string `json:"filter,omitempty"`
	// Tests are test IDs ("Ns\Cls::method[ with data set …]") to run
	// (rerun failed).
	Tests []string `json:"tests,omitempty"`
}

// TestRunParams are the params of "test.run".
type TestRunParams struct {
	TestScope
	// Debug starts the debugger listener and runs with the Xdebug
	// trigger set.
	Debug bool `json:"debug,omitempty"`
	// Interpreter overrides the plugin's interpreter setting.
	Interpreter string `json:"interpreter,omitempty"`
	// Config runs a stored run configuration of the plugin's type (ID or
	// name) instead of a scope.
	Config string `json:"config,omitempty"`
}

// TestRunIDParams address a test run ("" = the latest).
type TestRunIDParams struct {
	RunID string `json:"runId,omitempty"`
	// Debug (test.rerunFailed) reruns under the debugger.
	Debug bool `json:"debug,omitempty"`
}

// TestRun is a test run with its results.
type TestRun struct {
	// RunID is the run of the run infrastructure (run.output, run.stop).
	RunID string `json:"runId"`
	Name  string `json:"name"`
	// Framework is the display name of the test framework.
	Framework string `json:"framework"`
	// State is the run state (running, exited, stopped).
	State    string    `json:"state"`
	ExitCode int       `json:"exitCode"`
	Started  time.Time `json:"started"`
	// DurationMs is the wall time of the run.
	DurationMs int64           `json:"durationMs"`
	Counts     teamcity.Counts `json:"counts"`
	Scope      TestScope       `json:"scope"`
	Debug      bool            `json:"debug,omitempty"`
	// Tree are the results (omitted in test.started).
	Tree []teamcity.Node `json:"tree,omitempty"`
	// Error explains a run that ended without results (e.g. the
	// executable failed to start).
	Error string `json:"error,omitempty"`
}

// TestStatus is the result of "test.status": what the plugin found in
// the project.
type TestStatus struct {
	Framework string `json:"framework"`
	// Available is true when the test runner was found.
	Available bool `json:"available"`
	// Executable is the runner script (project relative where possible).
	Executable string `json:"executable,omitempty"`
	// Parallel is the parallel runner, if installed and enabled.
	Parallel string `json:"parallel,omitempty"`
	// Version is the installed version (from Composer metadata).
	Version string `json:"version,omitempty"`
	// Config is the configuration file in use ("" = none found).
	Config string `json:"config,omitempty"`
	// Suites are the test suites of the configuration file.
	Suites []string `json:"suites,omitempty"`
	// TestDirs are the directories searched for tests.
	TestDirs []string `json:"testDirs,omitempty"`
	// Hint explains what is missing; Install names the plugin whose
	// install recipe adds the runner (install.plan / install.run).
	Hint    string `json:"hint,omitempty"`
	Install string `json:"install,omitempty"`
}

// TestDiscoverParams are the params of "test.discover".
type TestDiscoverParams struct {
	// Path is a file or directory ("" = the test directories).
	Path string `json:"path,omitempty"`
}

// TestItem is a discovered test class or method.
type TestItem struct {
	// ID is "Ns\Cls" or "Ns\Cls::method".
	ID string `json:"id"`
	// Kind is "class" or "method".
	Kind   string `json:"kind"`
	Name   string `json:"name"`
	Class  string `json:"class"`
	Method string `json:"method,omitempty"`
	// File is the absolute path, Line the 1-based line of the name.
	File string `json:"file"`
	Line int    `json:"line"`
	// DataProviders name the data provider methods of a method.
	DataProviders []string `json:"dataProviders,omitempty"`
	// Status is the result of the last run that executed the item ("" =
	// never run in this session).
	Status   teamcity.Status `json:"status,omitempty"`
	Children []TestItem      `json:"children,omitempty"`
}

// TestDiscovery is the result of "test.discover".
type TestDiscovery struct {
	Classes []TestItem `json:"classes"`
	// Files is the number of PHP files parsed.
	Files int `json:"files"`
	// Errors lists files that could not be parsed.
	Errors []string `json:"errors,omitempty"`
}

// TestLastStatusParams are the params of "test.lastStatus".
type TestLastStatusParams struct {
	// IDs are test IDs ("Ns\Cls" or "Ns\Cls::method"); empty: all known.
	IDs []string `json:"ids,omitempty"`
}

// TestGotoParams are the params of "test.goto".
type TestGotoParams struct {
	// Path is the file in the editor.
	Path string `json:"path"`
}

// TestTarget is a candidate of "test.goto".
type TestTarget struct {
	File   string `json:"file"`
	Class  string `json:"class"`
	Exists bool   `json:"exists"`
}

// TestGotoResult is the result of "test.goto": the tests of a class or
// the class under test of a test.
type TestGotoResult struct {
	// Direction is "test" (Path is a class, Targets are its tests) or
	// "subject" (Path is a test, Targets are the tested classes).
	Direction string `json:"direction"`
	// Class is the class declared in Path.
	Class string `json:"class"`
	// Targets are existing files first.
	Targets []TestTarget `json:"targets"`
	// Create proposes a new test (Direction "test" only).
	Create *TestCreatePlan `json:"create,omitempty"`
}

// TestCreatePlan is a proposed test file.
type TestCreatePlan struct {
	File    string `json:"file"`
	Class   string `json:"class"`
	Content string `json:"content"`
	// Exists is true when File already exists (nothing is written).
	Exists bool `json:"exists"`
}

// TestCreateParams are the params of "test.create".
type TestCreateParams struct {
	// Path is the class to test.
	Path string `json:"path"`
	// File overrides the proposed test file (absolute or project
	// relative).
	File string `json:"file,omitempty"`
	// DryRun only returns the plan.
	DryRun bool `json:"dryRun,omitempty"`
}

// DebugListenerQuery is the value of the debug.listener filter: a run
// that should be debugged (run.start with debug) asks the debugger to
// listen. A provider (debug-xdebug) starts its listener if needed and
// fills Addr; without a provider Err stays empty and Addr too.
type DebugListenerQuery struct {
	Addr string
	// Err explains why the listener could not start.
	Err string
}

// HookDebugListener makes the debugger listen (provided by debug-xdebug,
// used by run.start with debug).
var HookDebugListener = hooks.NewFilter[DebugListenerQuery]("debug.listener")
