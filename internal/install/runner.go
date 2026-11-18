package install

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"sync"
	"time"

	"github.com/ape-dev-de/tiegel/internal/core/proctree"
)

// Run states.
const (
	StateRunning   = "running"
	StateSucceeded = "succeeded"
	StateFailed    = "failed"
	StateCancelled = "cancelled"
)

// Line is one line of output of a run.
type Line struct {
	RunID  string `json:"runId"`
	Plugin string `json:"plugin"`
	// Stream is "stdout" or "stderr".
	Stream string `json:"stream"`
	Text   string `json:"text"`
}

// Result describes a run (finished or not).
type Result struct {
	RunID    string `json:"runId"`
	Plugin   string `json:"plugin"`
	Tool     string `json:"tool"`
	State    string `json:"state"`
	Display  string `json:"display"`
	ExitCode int    `json:"exitCode"`
	Error    string `json:"error,omitempty"`
	// Output is the tail of the combined output.
	Output   []string  `json:"output"`
	Started  time.Time `json:"started"`
	Finished time.Time `json:"finished,omitempty"`
}

// maxOutput bounds the output kept per run.
const maxOutput = 1000

// Errors of the Runner.
var (
	ErrNotAvailable = errors.New("install: plan cannot run")
	ErrUnknownRun   = errors.New("install: unknown run")
	ErrBusy         = errors.New("install: an installation of this plugin is already running")
)

// Runner executes confirmed plans, one at a time per plugin.
type Runner struct {
	// OnLine receives every output line (must not block).
	OnLine func(Line)
	// OnDone receives the result of every finished run.
	OnDone func(Result)

	mu   sync.Mutex
	seq  int
	runs map[string]*run
}

type run struct {
	cancel context.CancelFunc
	done   chan struct{}

	mu  sync.Mutex
	res Result
}

func (r *run) snapshot() Result {
	r.mu.Lock()
	defer r.mu.Unlock()
	res := r.res
	res.Output = append([]string{}, r.res.Output...)
	return res
}

// Start runs plan in the background and returns its run ID. The caller is
// responsible for having obtained the user's confirmation. Output arrives
// through OnLine; Wait or OnDone deliver the result.
func (rn *Runner) Start(ctx context.Context, plan Plan) (string, error) {
	if !plan.Available || len(plan.Command) == 0 {
		return "", fmt.Errorf("%w: %s", ErrNotAvailable, plan.Hint)
	}
	rn.mu.Lock()
	if rn.runs == nil {
		rn.runs = map[string]*run{}
	}
	for _, r := range rn.runs {
		r.mu.Lock()
		busy := r.res.Plugin == plan.Plugin && r.res.State == StateRunning
		r.mu.Unlock()
		if busy {
			rn.mu.Unlock()
			return "", fmt.Errorf("%w (%s)", ErrBusy, plan.Plugin)
		}
	}
	rn.seq++
	id := "install-" + strconv.Itoa(rn.seq)
	// The run outlives the request that started it; only Cancel stops it.
	rctx, cancel := context.WithCancel(context.WithoutCancel(ctx))
	r := &run{cancel: cancel, done: make(chan struct{}), res: Result{
		RunID: id, Plugin: plan.Plugin, Tool: plan.Tool, State: StateRunning, Display: plan.Display, Output: []string{}, Started: time.Now(),
	}}
	rn.runs[id] = r
	rn.mu.Unlock()

	cmd := exec.CommandContext(rctx, plan.Command[0], plan.Command[1:]...)
	cmd.Dir = plan.Dir
	cmd.Env = append(os.Environ(), plan.env...)
	// Composer must not wait for answers nobody can give.
	cmd.Env = append(cmd.Env, "COMPOSER_NO_INTERACTION=1")
	cmd.Stdin = nil
	// Wrappers (npm.cmd, composer.bat, scripts) run the real work in a
	// grandchild: cancelling kills the whole tree, not only the child.
	tree := proctree.New(cmd)
	cmd.Cancel = tree.Kill
	// cmd.Wait owns the output copying (writers, not StdoutPipe), so
	// WaitDelay bounds the wait for pipes a leftover process keeps open.
	cmd.WaitDelay = 2 * time.Second
	outR, outW := io.Pipe()
	errR, errW := io.Pipe()
	cmd.Stdout, cmd.Stderr = outW, errW
	if err := cmd.Start(); err != nil {
		cancel()
		rn.finish(r, StateFailed, -1, fmt.Errorf("install: starting %s: %w", plan.Display, err))
		return id, nil
	}
	tree.Attach()
	go rn.wait(rctx, r, cmd, tree, outR, errR, outW, errW)
	return id, nil
}

func (rn *Runner) wait(ctx context.Context, r *run, cmd *exec.Cmd, tree *proctree.Tree, stdout, stderr io.Reader, outW, errW io.Closer) {
	var wg sync.WaitGroup
	wg.Add(2)
	go rn.pump(r, "stdout", stdout, &wg)
	go rn.pump(r, "stderr", stderr, &wg)
	err := cmd.Wait()
	tree.Release()
	_ = outW.Close()
	_ = errW.Close()
	wg.Wait()
	code := 0
	if cmd.ProcessState != nil {
		code = cmd.ProcessState.ExitCode()
	}
	switch {
	case ctx.Err() != nil:
		rn.finish(r, StateCancelled, code, errors.New("cancelled"))
	case err != nil:
		rn.finish(r, StateFailed, code, fmt.Errorf("install: %w", err))
	default:
		rn.finish(r, StateSucceeded, 0, nil)
	}
}

func (rn *Runner) pump(r *run, stream string, rd io.Reader, wg *sync.WaitGroup) {
	defer wg.Done()
	sc := bufio.NewScanner(rd)
	sc.Buffer(make([]byte, 64<<10), 1<<20)
	for sc.Scan() {
		text := sc.Text()
		r.mu.Lock()
		r.res.Output = append(r.res.Output, text)
		if len(r.res.Output) > maxOutput {
			r.res.Output = r.res.Output[len(r.res.Output)-maxOutput:]
		}
		ln := Line{RunID: r.res.RunID, Plugin: r.res.Plugin, Stream: stream, Text: text}
		r.mu.Unlock()
		if rn.OnLine != nil {
			rn.OnLine(ln)
		}
	}
}

func (rn *Runner) finish(r *run, state string, code int, err error) {
	r.mu.Lock()
	r.res.State, r.res.ExitCode, r.res.Finished = state, code, time.Now()
	if err != nil {
		r.res.Error = err.Error()
	}
	r.mu.Unlock()
	r.cancel()
	// OnDone runs before done closes, so a returned Wait implies the
	// callback has run.
	if rn.OnDone != nil {
		rn.OnDone(r.snapshot())
	}
	close(r.done)
}

func (rn *Runner) get(id string) (*run, error) {
	rn.mu.Lock()
	defer rn.mu.Unlock()
	r, ok := rn.runs[id]
	if !ok {
		return nil, fmt.Errorf("%w %q", ErrUnknownRun, id)
	}
	return r, nil
}

// Cancel stops a running installation (no-op once finished).
func (rn *Runner) Cancel(id string) error {
	r, err := rn.get(id)
	if err != nil {
		return err
	}
	r.cancel()
	return nil
}

// Status returns the state of a run.
func (rn *Runner) Status(id string) (Result, error) {
	r, err := rn.get(id)
	if err != nil {
		return Result{}, err
	}
	return r.snapshot(), nil
}

// Wait blocks until the run finished or ctx ends.
func (rn *Runner) Wait(ctx context.Context, id string) (Result, error) {
	r, err := rn.get(id)
	if err != nil {
		return Result{}, err
	}
	select {
	case <-r.done: // finished runs win over an expired ctx
		return r.snapshot(), nil
	default:
	}
	select {
	case <-r.done:
		return r.snapshot(), nil
	case <-ctx.Done():
		return r.snapshot(), fmt.Errorf("install: waiting for %s: %w", id, ctx.Err())
	}
}

// CancelAll stops all running installations (on shutdown).
func (rn *Runner) CancelAll() {
	rn.mu.Lock()
	defer rn.mu.Unlock()
	for _, r := range rn.runs {
		r.cancel()
	}
}
