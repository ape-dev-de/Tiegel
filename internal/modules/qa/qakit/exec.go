package qakit

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/ape-dev-de/tiegel/internal/api"
	"github.com/ape-dev-de/tiegel/internal/compose"
	"github.com/ape-dev-de/tiegel/internal/core/proctree"
	"github.com/ape-dev-de/tiegel/internal/interp"
	"github.com/ape-dev-de/tiegel/internal/run"
)

// result is a finished tool process.
type result struct {
	Stdout, Stderr []byte
	Code           int
	// Mappings translate container paths of the output to the host.
	Mappings compose.Mappings
	Display  string
}

// maxOutput bounds the captured stdout and stderr of a tool.
const maxOutput = 64 << 20

// errToolTimeout is the cause of the context of a tool run when the
// plugin's timeout setting ends it (as opposed to the caller's deadline).
var errToolTimeout = errors.New("tool timeout")

// execTool runs program with args in the project root, through the run
// infrastructure: in the project interpreter (useInterpreter) or on the
// host. stdin may be nil. The run ends at the timeout setting or when ctx
// ends; processes inside a container are stopped too.
func (p *Plugin) execTool(ctx context.Context, program string, args []string, stdin []byte) (result, error) {
	return p.execWith(ctx, program, func(run.Target) ([]string, error) { return args, nil }, stdin)
}

// execWith is execTool with the arguments built for the resolved target
// (a formatter places its copy depending on where the tool runs).
func (p *Plugin) execWith(ctx context.Context, program string, build func(t run.Target) ([]string, error), stdin []byte) (result, error) {
	app := p.getApp()
	if app == nil {
		return result{}, errors.New(p.def.ID + ": not started")
	}
	root := app.ProjectRoot()
	if root == "" {
		return result{}, errors.New("no project open")
	}
	ipt := run.InterpreterHost
	if p.useInterpreter() {
		ipt = run.InterpreterProject
	}
	timeout := p.timeout()
	ctx, cancel := context.WithTimeoutCause(ctx, timeout, errToolTimeout)
	defer cancel()
	t, err := app.RunTarget(ctx, ipt)
	if err != nil {
		return result{}, err
	}
	args, err := build(t)
	if err != nil {
		return result{}, err
	}
	pathEnv := p.pathEnv()
	tc, err := toolCommand(runtime.GOOS, hostSide(t), root, program, append(args, p.listSetting(KeyArgs)...), hostFS{
		Look: func(name string) (string, error) { return lookPath(name, pathEnv) },
	})
	if err != nil {
		return result{}, fmt.Errorf("%s: %w", p.def.Name, err)
	}
	spec := run.Spec{Name: p.def.Name, Program: tc.Program, PHP: tc.PHP, Args: tc.Args, Dir: root, Interpreter: ipt}
	l, err := run.Prepare(ctx, spec, t)
	if err != nil {
		return result{}, fmt.Errorf("%s: %w", p.def.Name, err)
	}
	return runLaunch(ctx, l, stdin, timeout)
}

// hostSide reports whether t runs processes directly on the host (no
// interpreter or a local php): Windows command line rules apply, and a
// temporary copy need not be visible to a container.
func hostSide(t run.Target) bool {
	switch t.Interp.(type) {
	case nil, *interp.Local:
		return true
	}
	return false
}

// runLaunch starts l, feeds stdin, collects the output and waits; at the
// end of ctx it kills the local process tree and the process inside the
// container (PID marker of the run infrastructure's stop helper).
func runLaunch(ctx context.Context, l *run.Launch, stdin []byte, timeout time.Duration) (result, error) {
	res := result{Mappings: l.Mappings, Display: l.Display}
	cmd := l.Cmd
	if stdin != nil {
		cmd.Stdin = bytes.NewReader(stdin)
	}
	var stdout limitBuffer
	stderr := &markerWriter{marker: strings.TrimSpace(l.PIDMarker)}
	cmd.Stdout, cmd.Stderr = &stdout, stderr
	tree := proctree.New(cmd)
	if err := cmd.Start(); err != nil {
		if errors.Is(err, exec.ErrNotFound) || errors.Is(err, os.ErrNotExist) {
			return res, fmt.Errorf("cannot start %s: %w", l.Display, err)
		}
		return res, fmt.Errorf("start %s: %w", l.Display, err)
	}
	tree.Attach()
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	var err error
	select {
	case err = <-done:
	case <-ctx.Done():
		_ = tree.Kill()
		if l.StopRemote != nil {
			sctx, scancel := context.WithTimeout(context.Background(), 10*time.Second)
			_ = l.StopRemote(sctx, stderr.PID())
			scancel()
		}
		select {
		case <-done:
		case <-time.After(5 * time.Second):
		}
		tree.Release()
		switch {
		case errors.Is(context.Cause(ctx), errToolTimeout):
			return res, fmt.Errorf("stopped after the timeout of %s", timeout)
		case errors.Is(ctx.Err(), context.DeadlineExceeded):
			// The caller's deadline, not the timeout setting.
			return res, fmt.Errorf("stopped: the caller stopped waiting: %w", ctx.Err())
		}
		return res, ctx.Err()
	}
	tree.Release()
	res.Stdout, res.Stderr = stdout.Bytes(), stderr.Bytes()
	var ee *exec.ExitError
	switch {
	case err == nil:
	case errors.As(err, &ee):
		res.Code = ee.ExitCode()
	default:
		return res, fmt.Errorf("%s: %w", l.Display, err)
	}
	return res, nil
}

// limitBuffer is a bytes.Buffer that drops output beyond maxOutput.
type limitBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *limitBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if room := maxOutput - b.buf.Len(); room > 0 {
		if len(p) > room {
			b.buf.Write(p[:room])
		} else {
			b.buf.Write(p)
		}
	}
	return len(p), nil
}

func (b *limitBuffer) Bytes() []byte {
	b.mu.Lock()
	defer b.mu.Unlock()
	return bytes.Clone(b.buf.Bytes())
}

// markerWriter collects stderr and removes the stop helper's PID line
// (the first line starting with marker), remembering the PID.
type markerWriter struct {
	marker string
	mu     sync.Mutex
	head   []byte // until the first newline, while the marker may come
	done   bool
	pid    string
	out    limitBuffer
}

func (w *markerWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.marker == "" || w.done {
		return w.out.Write(p)
	}
	w.head = append(w.head, p...)
	i := bytes.IndexByte(w.head, '\n')
	if i < 0 {
		if len(w.head) > 4096 {
			w.done = true
			_, _ = w.out.Write(w.head)
			w.head = nil
		}
		return len(p), nil
	}
	w.done = true
	line := strings.TrimSpace(string(w.head[:i]))
	if rest, ok := strings.CutPrefix(line, w.marker); ok {
		w.pid = strings.TrimSpace(rest)
		_, _ = w.out.Write(w.head[i+1:])
	} else {
		_, _ = w.out.Write(w.head)
	}
	w.head = nil
	return len(p), nil
}

// PID returns the PID from the marker line ("" if none arrived).
func (w *markerWriter) PID() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.pid
}

// Bytes returns stderr without the marker line.
func (w *markerWriter) Bytes() []byte {
	w.mu.Lock()
	defer w.mu.Unlock()
	if !w.done && len(w.head) > 0 {
		_, _ = w.out.Write(w.head)
		w.head, w.done = nil, true
	}
	return w.out.Bytes()
}

// format runs the formatter on the content of req.File and returns the
// result; the file itself is not written.
func (p *Plugin) format(ctx context.Context, req api.QAFormatRequest) ([]byte, error) {
	fs := p.def.Format
	if fs == nil {
		return nil, errors.New(p.def.Name + " is no formatter")
	}
	root := p.root()
	if root == "" || !within(root, req.File) {
		return nil, fmt.Errorf("%s is outside the project", req.File)
	}
	key := KeyPath
	if p.def.FormatProgram != "" {
		key = KeyFormatPath
	}
	r := p.resolve(key)
	if !r.Found {
		return nil, errors.New(r.Reason)
	}
	ok := fs.OK
	if ok == nil {
		ok = func(code int) bool { return code == 0 }
	}
	if fs.Stdin {
		rel, _ := filepath.Rel(root, req.File)
		res, err := p.execTool(ctx, r.Program, fs.Args(Call{File: filepath.ToSlash(rel), Config: p.config()}), req.Content)
		if err != nil {
			return nil, err
		}
		if fs.Unchanged != nil && fs.Unchanged(res.Code, res.Stdout) {
			return req.Content, nil
		}
		if !ok(res.Code) {
			return nil, toolError(p.def.Name, res)
		}
		if len(res.Stdout) == 0 && len(req.Content) > 0 {
			return nil, fmt.Errorf("%s printed no result", p.def.Name)
		}
		return res.Stdout, nil
	}
	// The tool fixes a copy in place: under the file's own name and
	// relative path, so that rules depending on the file name (PSR
	// autoloading) see the real one (D360).
	var cp *formatCopy
	defer func() {
		if cp != nil {
			cp.remove()
		}
	}()
	res, err := p.execWith(ctx, r.Program, func(t run.Target) ([]string, error) {
		c, err := newFormatCopy(root, req.File, req.Content, hostSide(t))
		if err != nil {
			return nil, err
		}
		cp = c
		return fs.Args(Call{File: c.arg, Config: p.config()}), nil
	}, nil)
	if err != nil {
		return nil, err
	}
	if !ok(res.Code) {
		return nil, toolError(p.def.Name, res)
	}
	out, err := os.ReadFile(cp.path)
	if err != nil {
		return nil, fmt.Errorf("read formatted copy: %w", err)
	}
	if bytes.Contains(out, []byte(cp.token)) && !bytes.Contains(req.Content, []byte(cp.token)) {
		return nil, fmt.Errorf("%s wrote the temporary location into the file; result discarded", p.def.Name)
	}
	return out, nil
}

// toolError describes a failed run with the tail of its output.
func toolError(name string, res result) error {
	msg := strings.TrimSpace(string(res.Stderr))
	if msg == "" {
		msg = strings.TrimSpace(string(res.Stdout))
	}
	if len(msg) > 2000 {
		msg = "…" + msg[len(msg)-2000:]
	}
	if msg == "" {
		return fmt.Errorf("%s failed with exit code %d", name, res.Code)
	}
	return fmt.Errorf("%s failed with exit code %d: %s", name, res.Code, msg)
}

// analyze runs the analyzer on req.Paths and maps the findings to host
// files.
func (p *Plugin) analyze(ctx context.Context, req api.QAAnalyzeRequest) (api.QAReport, error) {
	as := p.def.Analyze
	if as == nil {
		return api.QAReport{}, errors.New(p.def.Name + " is no analyzer")
	}
	root := p.root()
	if root == "" {
		return api.QAReport{}, errors.New("no project open")
	}
	r := p.resolve(KeyPath)
	if !r.Found {
		return api.QAReport{}, errors.New(r.Reason)
	}
	var paths []string
	for _, s := range req.Paths {
		rel, err := filepath.Rel(root, s)
		if err != nil || !within(root, s) {
			return api.QAReport{}, fmt.Errorf("%s is outside the project", s)
		}
		paths = append(paths, filepath.ToSlash(rel))
	}
	if len(paths) == 0 {
		paths = p.listSetting(KeyPaths)
		if len(paths) == 0 && as.DefaultPaths != nil {
			paths = as.DefaultPaths(root)
		}
	}
	res, err := p.execTool(ctx, r.Program, as.Args(Call{Paths: paths, Config: p.config()}), nil)
	if err != nil {
		return api.QAReport{}, err
	}
	ok := as.OK
	if ok == nil {
		ok = func(code int) bool { return code == 0 || code == 1 }
	}
	if !ok(res.Code) {
		return api.QAReport{}, toolError(p.def.Name, res)
	}
	findings, msgs, err := as.Parse(res.Stdout)
	if err != nil {
		return api.QAReport{}, fmt.Errorf("%s output: %w%s", p.def.Name, err, tail(res.Stderr))
	}
	sev := p.stringSetting(KeySeverity, SeverityAuto)
	return Report(root, res.Mappings, findings, msgs, sev), nil
}

func tail(b []byte) string {
	s := strings.TrimSpace(string(b))
	if s == "" {
		return ""
	}
	if len(s) > 1000 {
		s = "…" + s[len(s)-1000:]
	}
	return " (" + s + ")"
}

// Report groups findings by host file: relative files are taken from
// root, container paths are mapped back with m. sev other than
// SeverityAuto replaces the severities.
func Report(root string, m compose.Mappings, findings []Finding, msgs []string, sev string) api.QAReport {
	byFile := map[string]int{}
	rep := api.QAReport{Messages: msgs}
	for _, f := range findings {
		file := HostPath(root, m, f.File)
		if file == "" {
			rep.Messages = append(rep.Messages, fmt.Sprintf("%s:%d: %s", f.File, f.Line, f.Message))
			continue
		}
		s := f.Severity
		if sev != "" && sev != SeverityAuto {
			s = sev
		}
		if s == "" {
			s = SevWarning
		}
		d := api.QADiagnostic{Severity: s, Message: f.Message, Code: f.Code, Line: f.Line, Col: f.Col, EndLine: f.EndLine, EndCol: f.EndCol}
		i, ok := byFile[file]
		if !ok {
			i = len(rep.Files)
			byFile[file] = i
			rep.Files = append(rep.Files, api.QAFileDiagnostics{File: file})
		}
		rep.Files[i].Diagnostics = append(rep.Files[i].Diagnostics, d)
	}
	return rep
}

// HostPath maps a file name of tool output to a host path: container
// paths through m, relative names from root ("" if it cannot be placed).
func HostPath(root string, m compose.Mappings, name string) string {
	if name == "" {
		return ""
	}
	if strings.HasPrefix(name, "/") || filepath.IsAbs(name) {
		if h, ok := m.ToHost(name); ok {
			return filepath.Clean(h)
		}
		if filepath.IsAbs(name) {
			return filepath.Clean(name)
		}
		return ""
	}
	if root == "" {
		return ""
	}
	return filepath.Join(root, filepath.FromSlash(strings.TrimPrefix(name, "./")))
}
