package run

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/ape-dev-de/tiegel/internal/core/proctree"
)

// Run states.
const (
	StateRunning = "running"
	// StateExited: the process ended by itself (see ExitCode).
	StateExited = "exited"
	// StateStopped: the process was stopped (Stop, StopAll).
	StateStopped = "stopped"
)

// Output streams of a Line.
const (
	StreamStdout = "stdout"
	StreamStderr = "stderr"
	// StreamSystem marks lines of Tiegel itself (start, stop, errors).
	StreamSystem = "system"
)

// Errors of the Manager.
var (
	ErrUnknownRun = errors.New("run: unknown run")
	ErrRunning    = errors.New("run: the run is still active")
)

// Info describes a run (active or finished).
type Info struct {
	ID string `json:"id"`
	// Name, Config and Type come from the spec.
	Name   string `json:"name"`
	Config string `json:"config,omitempty"`
	Type   string `json:"type,omitempty"`
	// Display is the executed command line, Where the target.
	Display string `json:"display"`
	Where   string `json:"where,omitempty"`
	Dir     string `json:"dir,omitempty"`
	// Project is the project root the run belongs to.
	Project string `json:"project,omitempty"`
	State   string `json:"state"`
	// ExitCode is the exit status (-1: killed or unknown).
	ExitCode int       `json:"exitCode"`
	Error    string    `json:"error,omitempty"`
	Started  time.Time `json:"started"`
	// Finished is zero while the run is active.
	Finished time.Time `json:"finished,omitempty"`
	// DurationMs is the run time so far (or in total).
	DurationMs int64 `json:"durationMs"`
	// Lines is the sequence number of the last output line.
	Lines int `json:"lines"`
	// Replaces is the run this one reruns (its tab is reused).
	Replaces string `json:"replaces,omitempty"`
	Spec     Spec   `json:"spec"`
}

// Active reports whether the run has not finished.
func (i Info) Active() bool { return i.State == StateRunning }

// Line is one line of output.
type Line struct {
	// Seq numbers the lines of a run from 1.
	Seq    int    `json:"seq"`
	Stream string `json:"stream"`
	Text   string `json:"text"`
	// Links are the file locations in Text (host files).
	Links []Link `json:"links,omitempty"`
}

// Output is a batch of new lines of a run (event run.output).
type Output struct {
	RunID string `json:"runId"`
	Lines []Line `json:"lines"`
}

// Page is a part of a run's buffered output.
type Page struct {
	RunID string `json:"runId"`
	State string `json:"state"`
	Lines []Line `json:"lines"`
	// Last is the sequence number of the last line produced so far; pass
	// it as Since to get only newer lines.
	Last int `json:"last"`
	// Dropped tells that older lines than the first returned one were
	// discarded (buffer limit).
	Dropped bool `json:"dropped,omitempty"`
}

// Defaults of the Manager.
const (
	DefaultMaxLines      = 10000
	DefaultMaxRuns       = 30
	DefaultFlushInterval = 40 * time.Millisecond
	// maxLineBytes splits longer lines.
	maxLineBytes = 64 << 10
	// stopTimeout bounds the container-side stop before the local kill.
	stopTimeout = 10 * time.Second
)

// Manager starts and tracks runs. Several runs may be active at once.
// The zero value is ready to use. Callbacks are called from background
// goroutines; they must not block for long and must not call back into
// the Manager synchronously.
type Manager struct {
	// OnStart, OnOutput and OnExit report runs starting, new output
	// (batched) and finished runs. For one run, all OnOutput calls happen
	// before OnExit.
	OnStart  func(Info)
	OnOutput func(Output)
	OnExit   func(Info)
	// MaxLines bounds the buffered lines per run (DefaultMaxLines).
	MaxLines int
	// MaxRuns bounds the finished runs kept (DefaultMaxRuns); the oldest
	// finished ones are forgotten first.
	MaxRuns int
	// FlushInterval batches output events (DefaultFlushInterval).
	FlushInterval time.Duration
	// Exists checks link targets (nil: FileExists).
	Exists func(string) bool
	// Now is the clock (nil: time.Now).
	Now func() time.Time

	mu   sync.Mutex
	seq  int
	runs map[string]*proc
}

type proc struct {
	id       string
	launcher Launcher
	linker   Linker
	done     chan struct{}

	mu       sync.Mutex
	info     Info
	lines    []Line
	pending  []Line
	dropped  bool
	stopping bool
	pid      string
	tree     *proctree.Tree
	launch   *Launch
	stopOnce sync.Once
}

func (m *Manager) now() time.Time {
	if m.Now != nil {
		return m.Now()
	}
	return time.Now()
}

func (m *Manager) maxLines() int {
	if m.MaxLines > 0 {
		return m.MaxLines
	}
	return DefaultMaxLines
}

// StartOptions qualify a start.
type StartOptions struct {
	// Project is the project root the run belongs to.
	Project string
	// LinkDirs are the bases for relative paths in the output (after
	// the working directory).
	LinkDirs []string
	// Replaces is the run a rerun replaces.
	Replaces string
}

// Start prepares spec with launcher and starts it. It returns once the
// process runs (or failed to start, which is returned as error and
// leaves no run behind).
func (m *Manager) Start(ctx context.Context, spec Spec, launcher Launcher, opts StartOptions) (Info, error) {
	if launcher == nil {
		return Info{}, errors.New("run: no launcher")
	}
	l, err := launcher(ctx)
	if err != nil {
		return Info{}, err
	}
	if l == nil || l.Cmd == nil {
		return Info{}, errors.New("run: launcher returned no command")
	}
	m.mu.Lock()
	if m.runs == nil {
		m.runs = map[string]*proc{}
	}
	m.seq++
	id := "run-" + strconv.Itoa(m.seq)
	m.mu.Unlock()

	cmd := l.Cmd
	exists := m.Exists
	if exists == nil {
		exists = FileExists
	}
	dirs := append([]string{cmd.Dir}, opts.LinkDirs...)
	p := &proc{id: id, launcher: launcher, done: make(chan struct{}), launch: l,
		linker: Linker{Mappings: l.Mappings, Dirs: dirs, Exists: exists},
		info: Info{ID: id, Name: spec.Name, Config: spec.Config, Type: spec.Type, Display: l.Display, Where: l.Where,
			Dir: cmd.Dir, Project: opts.Project, State: StateRunning, ExitCode: -1, Started: m.now(), Replaces: opts.Replaces, Spec: spec}}
	if p.info.Name == "" {
		p.info.Name = spec.Display()
	}

	cmd.Stdin = nil
	// Wrappers (composer.bat, scripts, docker) run the real work in
	// child processes: stopping kills the whole tree.
	p.tree = proctree.New(cmd)
	// cmd.Wait owns copying the output (writers, not StdoutPipe), so
	// WaitDelay bounds the wait for pipes a leftover process keeps open.
	cmd.WaitDelay = 2 * time.Second
	outR, outW := io.Pipe()
	errR, errW := io.Pipe()
	cmd.Stdout, cmd.Stderr = outW, errW
	if err := cmd.Start(); err != nil {
		_ = outW.Close()
		_ = errW.Close()
		return Info{}, fmt.Errorf("run: starting %s: %w", l.Display, err)
	}
	p.tree.Attach()

	m.mu.Lock()
	m.runs[id] = p
	m.pruneLocked()
	m.mu.Unlock()
	if m.OnStart != nil {
		m.OnStart(p.snapshot(m.now()))
	}
	go m.wait(p, cmd, outR, errR, outW, errW)
	return p.snapshot(m.now()), nil
}

// pruneLocked forgets the oldest finished runs beyond MaxRuns.
func (m *Manager) pruneLocked() {
	max := m.MaxRuns
	if max <= 0 {
		max = DefaultMaxRuns
	}
	var finished []*proc
	for _, p := range m.runs {
		p.mu.Lock()
		done := !p.info.Finished.IsZero()
		p.mu.Unlock()
		if done {
			finished = append(finished, p)
		}
	}
	if len(finished) <= max {
		return
	}
	sort.Slice(finished, func(i, j int) bool { return finished[i].info.Started.Before(finished[j].info.Started) })
	for _, p := range finished[:len(finished)-max] {
		delete(m.runs, p.id)
	}
}

func (m *Manager) wait(p *proc, cmd *exec.Cmd, stdout, stderr io.Reader, outW, errW io.Closer) {
	var wg sync.WaitGroup
	wg.Add(2)
	go m.pump(p, StreamStdout, stdout, &wg)
	go m.pump(p, StreamStderr, stderr, &wg)
	flushDone := make(chan struct{})
	stopFlush := make(chan struct{})
	go func() {
		defer close(flushDone)
		iv := m.FlushInterval
		if iv <= 0 {
			iv = DefaultFlushInterval
		}
		t := time.NewTicker(iv)
		defer t.Stop()
		for {
			select {
			case <-t.C:
				m.flush(p)
			case <-stopFlush:
				return
			}
		}
	}()
	err := cmd.Wait()
	p.tree.Release()
	_ = outW.Close()
	_ = errW.Close()
	wg.Wait()
	close(stopFlush)
	<-flushDone

	code := -1
	if cmd.ProcessState != nil {
		code = cmd.ProcessState.ExitCode()
	}
	p.mu.Lock()
	stopped := p.stopping
	p.info.ExitCode = code
	p.info.Finished = m.now()
	p.info.State = StateExited
	if stopped {
		p.info.State = StateStopped
	}
	var ee *exec.ExitError
	if err != nil && !errors.As(err, &ee) {
		p.info.Error = err.Error()
	}
	msg := fmt.Sprintf("Process finished with exit code %d", code)
	if stopped {
		msg = "Process stopped"
	}
	p.appendLocked(m, StreamSystem, msg)
	p.mu.Unlock()
	m.flush(p)
	m.mu.Lock()
	m.pruneLocked()
	m.mu.Unlock()
	close(p.done)
	if m.OnExit != nil {
		m.OnExit(p.snapshot(m.now()))
	}
}

// pump reads lines of one stream. Long lines are split at maxLineBytes;
// carriage-return progress output keeps the last state; terminal escape
// sequences are removed.
func (m *Manager) pump(p *proc, stream string, rd io.Reader, wg *sync.WaitGroup) {
	defer wg.Done()
	br := bufio.NewReaderSize(rd, maxLineBytes)
	var part []byte
	for {
		chunk, isPrefix, err := br.ReadLine()
		if err != nil {
			if len(part) > 0 {
				m.addLine(p, stream, string(part))
			}
			// Keep draining so a writer never blocks on a full pipe.
			_, _ = io.Copy(io.Discard, rd)
			return
		}
		part = append(part, chunk...)
		if isPrefix && len(part) < maxLineBytes {
			continue
		}
		m.addLine(p, stream, string(part))
		part = part[:0]
	}
}

// addLine records a raw output line.
func (m *Manager) addLine(p *proc, stream, raw string) {
	if i := strings.LastIndexByte(strings.TrimRight(raw, "\r"), '\r'); i >= 0 {
		raw = raw[i+1:]
	}
	text := StripANSI(strings.TrimRight(raw, "\r"))
	p.mu.Lock()
	defer p.mu.Unlock()
	if stream == StreamStderr && p.launch.PIDMarker != "" && p.pid == "" {
		if pid, ok := strings.CutPrefix(text, p.launch.PIDMarker); ok {
			p.pid = strings.TrimSpace(pid)
			return
		}
	}
	p.appendLocked(m, stream, text)
}

// appendLocked adds a line (p.mu held).
func (p *proc) appendLocked(m *Manager, stream, text string) {
	p.info.Lines++
	ln := Line{Seq: p.info.Lines, Stream: stream, Text: text}
	if stream != StreamSystem {
		ln.Links = p.linker.Links(text)
	}
	p.lines = append(p.lines, ln)
	if max := m.maxLines(); len(p.lines) > max {
		p.lines = append(p.lines[:0:0], p.lines[len(p.lines)-max:]...)
		p.dropped = true
	}
	p.pending = append(p.pending, ln)
}

// flush publishes the pending lines of p.
func (m *Manager) flush(p *proc) {
	p.mu.Lock()
	batch := p.pending
	p.pending = nil
	p.mu.Unlock()
	if len(batch) > 0 && m.OnOutput != nil {
		m.OnOutput(Output{RunID: p.id, Lines: batch})
	}
}

func (p *proc) snapshot(now time.Time) Info {
	p.mu.Lock()
	defer p.mu.Unlock()
	i := p.info
	end := now
	if !i.Finished.IsZero() {
		end = i.Finished
	}
	i.DurationMs = end.Sub(i.Started).Milliseconds()
	i.Spec.Args = append([]string(nil), i.Spec.Args...)
	i.Spec.Env = append([]string(nil), i.Spec.Env...)
	return i
}

func (m *Manager) get(id string) (*proc, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	p, ok := m.runs[id]
	if !ok {
		return nil, fmt.Errorf("%w %q", ErrUnknownRun, id)
	}
	return p, nil
}

// Get returns the state of a run.
func (m *Manager) Get(id string) (Info, error) {
	p, err := m.get(id)
	if err != nil {
		return Info{}, err
	}
	return p.snapshot(m.now()), nil
}

// List returns all known runs, oldest first.
func (m *Manager) List() []Info {
	m.mu.Lock()
	ps := make([]*proc, 0, len(m.runs))
	for _, p := range m.runs {
		ps = append(ps, p)
	}
	m.mu.Unlock()
	now := m.now()
	out := make([]Info, 0, len(ps))
	for _, p := range ps {
		out = append(out, p.snapshot(now))
	}
	sort.Slice(out, func(i, j int) bool {
		if !out[i].Started.Equal(out[j].Started) {
			return out[i].Started.Before(out[j].Started)
		}
		return runNo(out[i].ID) < runNo(out[j].ID)
	})
	return out
}

func runNo(id string) int {
	n, _ := strconv.Atoi(strings.TrimPrefix(id, "run-"))
	return n
}

// Output returns the buffered lines with a sequence number above since,
// at most limit of the newest (limit <= 0: all buffered).
func (m *Manager) Output(id string, since, limit int) (Page, error) {
	p, err := m.get(id)
	if err != nil {
		return Page{}, err
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	pg := Page{RunID: id, State: p.info.State, Last: p.info.Lines, Lines: []Line{}}
	i := sort.Search(len(p.lines), func(i int) bool { return p.lines[i].Seq > since })
	lines := p.lines[i:]
	if limit > 0 && len(lines) > limit {
		lines = lines[len(lines)-limit:]
	}
	pg.Lines = append(pg.Lines, lines...)
	pg.Dropped = len(pg.Lines) > 0 && pg.Lines[0].Seq > since+1
	return pg, nil
}

// Stop stops a run: the process inside a container first (if the target
// needs it), then the local process tree. It returns immediately; Wait
// observes the end. Stopping a finished run is a no-op.
func (m *Manager) Stop(id string) error {
	p, err := m.get(id)
	if err != nil {
		return err
	}
	m.stop(p)
	return nil
}

func (m *Manager) stop(p *proc) {
	select {
	case <-p.done:
		return
	default:
	}
	p.stopOnce.Do(func() {
		p.mu.Lock()
		p.stopping = true
		pid, remote := p.pid, p.launch.StopRemote
		p.mu.Unlock()
		go func() {
			if remote != nil {
				ctx, cancel := context.WithTimeout(context.Background(), stopTimeout)
				_ = remote(ctx, pid)
				cancel()
				// The client usually ends with the remote process; give it
				// a moment before killing it.
				select {
				case <-p.done:
					return
				case <-time.After(time.Second):
				}
			}
			_ = p.tree.Kill()
		}()
	})
}

// StopAll stops the active runs for which match returns true (nil: all).
func (m *Manager) StopAll(match func(Info) bool) []string {
	var ids []string
	for _, i := range m.List() {
		if i.Active() && (match == nil || match(i)) {
			ids = append(ids, i.ID)
			_ = m.Stop(i.ID)
		}
	}
	return ids
}

// Wait blocks until the run finished or ctx ends.
func (m *Manager) Wait(ctx context.Context, id string) (Info, error) {
	p, err := m.get(id)
	if err != nil {
		return Info{}, err
	}
	select {
	case <-p.done: // finished runs win over an expired ctx
		return p.snapshot(m.now()), nil
	default:
	}
	select {
	case <-p.done:
		return p.snapshot(m.now()), nil
	case <-ctx.Done():
		return p.snapshot(m.now()), fmt.Errorf("run: waiting for %s: %w", id, ctx.Err())
	}
}

// Rerun starts the spec of run id again with the same launcher (which
// resolves the target anew). An active run is stopped first; the old run
// is forgotten and the new one reports it in Replaces.
func (m *Manager) Rerun(ctx context.Context, id string) (Info, error) {
	p, err := m.get(id)
	if err != nil {
		return Info{}, err
	}
	m.stop(p)
	select {
	case <-p.done:
	case <-ctx.Done():
		return Info{}, fmt.Errorf("run: stopping %s for rerun: %w", id, ctx.Err())
	}
	old := p.snapshot(m.now())
	info, err := m.Start(ctx, old.Spec, p.launcher, StartOptions{Project: old.Project, LinkDirs: p.linker.Dirs[1:], Replaces: id})
	if err != nil {
		return Info{}, err
	}
	m.mu.Lock()
	delete(m.runs, id)
	m.mu.Unlock()
	return info, nil
}

// Remove forgets a finished run (its tab was closed).
func (m *Manager) Remove(id string) error {
	p, err := m.get(id)
	if err != nil {
		return err
	}
	select {
	case <-p.done:
	default:
		return fmt.Errorf("%w: %s", ErrRunning, id)
	}
	m.mu.Lock()
	delete(m.runs, id)
	m.mu.Unlock()
	return nil
}

// Active returns the active runs.
func (m *Manager) Active() []Info {
	var out []Info
	for _, i := range m.List() {
		if i.Active() {
			out = append(out, i)
		}
	}
	return out
}
