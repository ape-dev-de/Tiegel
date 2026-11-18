package run

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/ape-dev-de/tiegel/internal/compose"
	"github.com/ape-dev-de/tiegel/internal/docker"
	"github.com/ape-dev-de/tiegel/internal/interp"
)

// Interpreter choices of Spec.Interpreter besides interpreter IDs.
const (
	// InterpreterProject is the project's selected interpreter.
	InterpreterProject = "project"
	// InterpreterHost runs the program directly on the host ("php" from
	// PATH for PHP specs).
	InterpreterHost = "host"
)

// Spec describes a process to run.
type Spec struct {
	// Name is the display name (tab title).
	Name string `json:"name"`
	// Config and Type name the run configuration the spec was built from
	// (empty for ad-hoc runs).
	Config string `json:"config,omitempty"`
	Type   string `json:"type,omitempty"`
	// PHP runs the interpreter's php binary with Args; otherwise Program.
	PHP     bool     `json:"php,omitempty"`
	Program string   `json:"program,omitempty"`
	Args    []string `json:"args,omitempty"`
	// Dir is the host working directory (empty: the project root).
	Dir string `json:"dir,omitempty"`
	// Env are extra KEY=VALUE entries.
	Env []string `json:"env,omitempty"`
	// Interpreter is InterpreterProject, InterpreterHost or an
	// interpreter ID (empty: InterpreterHost for programs,
	// InterpreterProject for PHP).
	Interpreter string `json:"interpreter,omitempty"`
	// Debug marks a run under the debugger: the kernel makes the
	// debugger listen before every start and rerun and sets the Xdebug
	// trigger (DebugEnv, DebugPHPArgs).
	Debug bool `json:"debug,omitempty"`
}

// DebugSession is the value of XDEBUG_SESSION of debugged runs.
const DebugSession = "tiegel"

// DebugEnv returns the environment entries that make Xdebug connect.
func DebugEnv() []string {
	return []string{"XDEBUG_TRIGGER=1", "XDEBUG_SESSION=" + DebugSession}
}

// DebugPHPArgs returns php options that enable step debugging for one run
// (prepended to the arguments of PHP specs); port is the listener port
// (0: keep the interpreter's xdebug.client_port).
func DebugPHPArgs(port int) []string {
	out := []string{"-dxdebug.mode=debug", "-dxdebug.start_with_request=trigger"}
	if port > 0 {
		out = append(out, fmt.Sprintf("-dxdebug.client_port=%d", port))
	}
	return out
}

// Validate checks that the spec names something to run.
func (s Spec) Validate() error {
	if !s.PHP && strings.TrimSpace(s.Program) == "" {
		return errors.New("run: spec needs a program (or php)")
	}
	for _, kv := range s.Env {
		if k, _, ok := strings.Cut(kv, "="); !ok || strings.TrimSpace(k) == "" {
			return fmt.Errorf("run: environment entry %q is not KEY=VALUE", kv)
		}
	}
	return nil
}

// EffectiveInterpreter returns Interpreter with the defaults applied.
func (s Spec) EffectiveInterpreter() string {
	if s.Interpreter != "" {
		return s.Interpreter
	}
	if s.PHP {
		return InterpreterProject
	}
	return InterpreterHost
}

// Display renders the spec as command text (before interpreter wrapping).
func (s Spec) Display() string {
	argv := append([]string{s.Program}, s.Args...)
	if s.PHP {
		argv[0] = "php"
	}
	return QuoteWords(argv)
}

// Target is where a spec runs: on the host or in an interpreter.
type Target struct {
	// Interp is the interpreter (nil: the host).
	Interp interp.Interpreter
	// Mappings translate host paths into the container and back.
	Mappings compose.Mappings
	// ProjectDir is the project root (default working directory).
	ProjectDir string
	// PathEnv is the PATH for host processes ("" keeps the process PATH),
	// e.g. including the login shell PATH on macOS.
	PathEnv string
	// Plain disables the stop helper of `docker compose exec` (a shell
	// wrapper that reports the PID inside the container), so that the
	// command line is exactly the program (installation plans).
	Plain bool
}

// Launch is a prepared, not yet started process.
type Launch struct {
	Cmd *exec.Cmd
	// Display is the command as shell text.
	Display string
	// Where describes the target ("host", an interpreter label).
	Where string
	// Dir is the host working directory of the program: relative paths
	// in the output are resolved against it (empty: Cmd.Dir). It differs
	// from Cmd.Dir when docker runs in the compose directory while the
	// program works in a mapped project directory.
	Dir string
	// Mappings map container paths of the output back to the host.
	Mappings compose.Mappings
	// PIDMarker is the prefix of a stderr line announcing the PID of the
	// process inside a container; the line is removed from the output.
	PIDMarker string
	// StopRemote ends the process inside the container (pid from the
	// marker line, "" if none arrived); nil when killing the local
	// process tree is enough.
	StopRemote func(ctx context.Context, pid string) error
}

// Launcher prepares a launch. Manager calls it on Start and again on
// every Rerun (so the interpreter state is checked anew).
type Launcher func(ctx context.Context) (*Launch, error)

// Execer is implemented by interpreters that build arbitrary commands
// themselves (tests, plugins); it takes precedence over the built-in
// interpreter kinds.
type Execer interface {
	ExecCommand(ctx context.Context, x Exec) (*Launch, error)
}

// Exec is the resolved request passed to an Execer.
type Exec struct {
	Spec Spec
	// Program is the program to run (the interpreter's php for PHP
	// specs), Args its arguments, Dir the host working directory.
	Program string
	Args    []string
	Dir     string
	Env     []string
}

// pidMarker prefixes the PID line of the compose exec stop helper.
const pidMarker = "__TIEGEL_RUN_PID__ "

// wrapScript is the stop helper (`sh -c wrapScript sh program args…`)
// for processes inside a container or WSL: it prints its PID and execs
// the program, which keeps that PID. The program leads its own process
// group so that stopScript reaches its children too: `docker exec`
// already starts a group leader, otherwise setsid makes one (setsid
// would fork in a group leader, hence the look at /proc).
const wrapScript = `echo "` + pidMarker + `$$" >&2; pg=; read -r _ _ _ _ pg _ 2>/dev/null </proc/$$/stat; ` +
	`if [ "$pg" != "$$" ] && command -v setsid >/dev/null 2>&1; then exec setsid "$@"; fi; exec "$@"`

// stopScript (`sh -c stopScript sh PID`) ends the process PID and its
// process group: TERM, up to 5 s grace, then KILL. It uses the shell's
// kill builtin (Debian images have no kill program) and no "--" (dash
// rejects it).
const stopScript = `s() { kill -"$1" -"$2" 2>/dev/null || kill -"$1" "$2"; }; ` +
	`a() { kill -0 -"$1" 2>/dev/null || kill -0 "$1" 2>/dev/null; }; ` +
	`s TERM "$1" || exit 1; i=0; while a "$1" && [ "$i" -lt 25 ]; do sleep 0.2; i=$((i+1)); done; ` +
	`if a "$1"; then s KILL "$1"; fi; exit 0`

// Prepare builds the command of spec on target t.
func Prepare(ctx context.Context, spec Spec, t Target) (*Launch, error) {
	if err := spec.Validate(); err != nil {
		return nil, err
	}
	dir := spec.Dir
	if dir == "" {
		dir = t.ProjectDir
	}
	if dir != "" && !filepath.IsAbs(dir) && t.ProjectDir != "" {
		dir = filepath.Join(t.ProjectDir, dir)
	}
	// Commands outlive the request that prepared them: only the Manager
	// ends them (process tree kill), never the request context.
	bg := context.WithoutCancel(ctx)
	x := Exec{Spec: spec, Program: spec.Program, Args: append([]string(nil), spec.Args...), Dir: dir, Env: append([]string(nil), spec.Env...)}
	var (
		l   *Launch
		err error
	)
	switch i := t.Interp.(type) {
	case nil:
		if spec.PHP {
			x.Program = "php"
		}
		l, err = hostLaunch(ctx, x, t, "")
		if l != nil {
			l.Where = InterpreterHost
		}
	case Execer:
		l, err = i.ExecCommand(bg, x)
	case *interp.Local:
		if spec.PHP {
			x.Program = i.Path
		}
		l, err = hostLaunch(ctx, x, t, filepath.Dir(i.Path))
		if l != nil {
			l.Where = i.Label()
		}
	case *compose.Interpreter:
		l, err = composeLaunch(ctx, bg, i, x, t)
	case *docker.ImageInterpreter:
		l, err = imageLaunch(bg, i, x, t)
	case *interp.WSL:
		l, err = wslLaunch(i, x, t)
	default:
		if !spec.PHP {
			return nil, fmt.Errorf("run: interpreter %s can only run php, not %s", t.Interp.ID(), spec.Program)
		}
		cmd, cerr := t.Interp.Command(bg, x.Args...)
		if cerr != nil {
			return nil, fmt.Errorf("run: %s: %w", t.Interp.ID(), cerr)
		}
		cmd.Dir = dir
		cmd.Env = mergeEnv(cmdEnv(cmd), x.Env)
		l = &Launch{Cmd: cmd, Display: QuoteWords(cmd.Args), Where: t.Interp.Label(), Mappings: t.Mappings}
	}
	if err != nil {
		return nil, err
	}
	if l.Dir == "" {
		l.Dir = dir
	}
	return l, nil
}

// cmdEnv returns the environment cmd starts with.
func cmdEnv(cmd *exec.Cmd) []string {
	if cmd.Env != nil {
		return cmd.Env
	}
	return os.Environ()
}

// hostLaunch runs x on the host. extraPath is prepended to PATH (the
// directory of a local php, so that composer and scripts use it).
func hostLaunch(_ context.Context, x Exec, t Target, extraPath string) (*Launch, error) {
	env := os.Environ()
	pathEnv := t.PathEnv
	if pathEnv == "" {
		pathEnv = os.Getenv("PATH")
	}
	if extraPath != "" && extraPath != "." {
		pathEnv = extraPath + string(os.PathListSeparator) + pathEnv
	}
	env = mergeEnv(env, []string{"PATH=" + pathEnv})
	env = mergeEnv(env, x.Env)
	prog := x.Program
	if !strings.ContainsAny(prog, `/\`) {
		found, err := lookPath(prog, pathEnv)
		if err != nil {
			return nil, err
		}
		prog = found
	}
	cmd := exec.Command(prog, x.Args...)
	cmd.Dir = x.Dir
	cmd.Env = env
	return &Launch{Cmd: cmd, Display: QuoteWords(append([]string{x.Program}, x.Args...)), Mappings: t.Mappings}, nil
}

// lookPath finds name in the directories of pathEnv (PATHEXT on Windows).
func lookPath(name, pathEnv string) (string, error) {
	exts := []string{""}
	if runtime.GOOS == "windows" {
		exts = []string{".exe", ".bat", ".cmd", ".com", ""}
		if pe := os.Getenv("PATHEXT"); pe != "" {
			exts = append(strings.Split(strings.ToLower(pe), ";"), "")
		}
	}
	for _, d := range filepath.SplitList(pathEnv) {
		if d == "" {
			continue
		}
		for _, e := range exts {
			p := filepath.Join(d, name+e)
			if st, err := os.Stat(p); err == nil && !st.IsDir() && (runtime.GOOS == "windows" || st.Mode()&0o111 != 0) {
				return p, nil
			}
		}
	}
	return "", fmt.Errorf("run: %s not found in PATH: %w", name, exec.ErrNotFound)
}

// mergeEnv returns base with the entries of over replacing entries of the
// same key (case-insensitive on Windows).
func mergeEnv(base, over []string) []string {
	if len(over) == 0 {
		return base
	}
	return docker.MergeEnv(base, over)
}

// remoteArgs maps host paths in args to the container: absolute host
// paths covered by a mapping are translated, everything else is kept.
func remoteArgs(args []string, m compose.Mappings) []string {
	out := make([]string, len(args))
	for i, a := range args {
		out[i] = a
		if filepath.IsAbs(a) {
			if r, ok := m.ToRemote(a); ok {
				out[i] = r
			}
		}
	}
	return out
}

// remoteDir maps the host working directory into the container ("" when
// no mapping covers it: the container's default applies).
func remoteDir(dir string, m compose.Mappings) string {
	if dir == "" {
		return ""
	}
	if r, ok := m.ToRemote(dir); ok {
		return r
	}
	return ""
}

// envArgs turns KEY=VALUE entries into "-e KEY=VALUE" options.
func envArgs(env []string) []string {
	var out []string
	for _, kv := range env {
		out = append(out, "-e", kv)
	}
	return out
}

// containerName returns a fresh name for a container started by a run.
func containerName() string {
	var b [6]byte
	_, _ = rand.Read(b[:])
	return "tiegel-run-" + hex.EncodeToString(b[:])
}

// composeLaunch runs x in a compose service: `exec` into the running
// container (with a stop helper that reports the PID inside it) or a
// one-off `run --rm` container with a known name.
func composeLaunch(ctx, bg context.Context, c *compose.Interpreter, x Exec, t Target) (*Launch, error) {
	m := t.Mappings
	if len(m) == 0 {
		m = c.Mappings
	}
	useExec := true
	switch c.Mode {
	case compose.RunAlways:
		useExec = false
	case compose.ExecAuto:
		running, err := c.Running(ctx)
		if err != nil {
			return nil, fmt.Errorf("run: compose service %s: %w", c.Service, err)
		}
		useExec = running
	}
	prog := x.Program
	if x.Spec.PHP {
		prog = c.PHP
		if prog == "" {
			prog = "php"
		}
	}
	args := remoteArgs(x.Args, m)
	opts := envArgs(x.Env)
	if w := remoteDir(x.Dir, m); w != "" {
		opts = append(opts, "-w", w)
	}
	a := c.BaseArgs()
	l := &Launch{Where: c.Label(), Mappings: m}
	client := c.Client()
	if useExec {
		a = append(a, "exec", "-T")
		a = append(a, opts...)
		a = append(a, c.Service)
		if t.Plain {
			a = append(a, prog)
		} else {
			// The helper prints its PID and execs the program, so the
			// PID is the program's: Stop can end it inside the container
			// (killing the docker client does not).
			a = append(a, "sh", "-c", wrapScript, "sh", prog)
			l.PIDMarker = pidMarker
			base := c.BaseArgs()
			service := c.Service
			l.StopRemote = func(ctx context.Context, pid string) error {
				if pid == "" {
					return nil
				}
				kill := append(append([]string(nil), base...), "exec", "-T", service, "sh", "-c", stopScript, "sh", pid)
				_, err := client.Output(ctx, kill...)
				return err
			}
		}
	} else {
		name := containerName()
		a = append(a, "run", "--rm", "-T", "--name", name)
		if !c.RunDeps {
			a = append(a, "--no-deps")
		}
		a = append(a, opts...)
		a = append(a, c.Service, prog)
		l.StopRemote = func(ctx context.Context, _ string) error {
			_, err := client.Output(ctx, "kill", name)
			return err
		}
	}
	a = append(a, args...)
	cmd := client.Command(bg, a...)
	cmd.Dir = c.Dir
	l.Cmd = cmd
	l.Display = QuoteWords(append([]string{client.Bin()}, client.Args(a...)...))
	return l, nil
}

// imageLaunch runs x in a one-off container of an image interpreter
// (`docker run --rm` with the project volume and a known name).
func imageLaunch(ctx context.Context, d *docker.ImageInterpreter, x Exec, t Target) (*Launch, error) {
	m := t.Mappings
	if len(m) == 0 {
		for _, mp := range d.Mappings() {
			m = m.Set(mp.Host, mp.Remote)
		}
	}
	prog := x.Program
	if x.Spec.PHP {
		prog = d.PHP
		if prog == "" {
			prog = "php"
		}
	}
	name := containerName()
	a := []string{"run", "--rm", "-i", "--name", name}
	if d.NoPull {
		a = append(a, "--pull", "never")
	}
	if d.HostDir != "" {
		a = append(a, "-v", d.HostDir+":"+d.RemoteDir())
		w := remoteDir(x.Dir, m)
		if w == "" {
			w = d.RemoteDir()
		}
		a = append(a, "-w", w)
	}
	a = append(a, envArgs(x.Env)...)
	a = append(a, "--entrypoint", prog, d.Image)
	a = append(a, remoteArgs(x.Args, m)...)
	cmd := d.Client.Command(ctx, a...)
	if d.HostDir != "" {
		cmd.Dir = d.HostDir
	}
	client := d.Client
	return &Launch{
		Cmd: cmd, Display: QuoteWords(append([]string{client.Bin()}, client.Args(a...)...)), Where: d.Label(), Mappings: m,
		StopRemote: func(ctx context.Context, _ string) error {
			_, err := client.Output(ctx, "kill", name)
			return err
		},
	}, nil
}

// wslLaunch runs x inside a WSL distribution (`wsl.exe -d D --cd DIR --
// env K=V program args`). Windows paths of the project map to /mnt/….
func wslLaunch(w *interp.WSL, x Exec, t Target) (*Launch, error) {
	if w.Distro == "" {
		return nil, errors.New("run: WSL interpreter without distribution")
	}
	m := t.Mappings
	if len(m) == 0 && t.ProjectDir != "" {
		if r, ok := interp.WindowsToWSL(t.ProjectDir, w.Distro); ok {
			m = m.Set(t.ProjectDir, path.Clean(r))
		}
	}
	prog := x.Program
	if x.Spec.PHP {
		prog = w.Path
		if prog == "" {
			prog = "php"
		}
	}
	a := []string{"-d", w.Distro}
	if wd := remoteDir(x.Dir, m); wd != "" {
		a = append(a, "--cd", wd)
	}
	a = append(a, "--")
	if len(x.Env) > 0 {
		a = append(append(a, "env"), x.Env...)
	}
	if !t.Plain {
		// Killing wsl.exe does not end the Linux process: the stop
		// helper reports its PID for StopRemote (as for compose exec).
		a = append(a, "sh", "-c", wrapScript, "sh")
	}
	a = append(a, prog)
	a = append(a, remoteArgs(x.Args, m)...)
	exe := w.Program
	if exe == "" {
		exe = interp.DefaultWSLProgram
	}
	cmd := exec.Command(exe, a...)
	cmd.Dir = x.Dir
	l := &Launch{Cmd: cmd, Display: QuoteWords(append([]string{exe}, a...)), Where: w.Label(), Mappings: m}
	if !t.Plain {
		l.PIDMarker = pidMarker
		distro := w.Distro
		l.StopRemote = func(ctx context.Context, pid string) error {
			if pid == "" {
				return nil
			}
			out, err := exec.CommandContext(ctx, exe, "-d", distro, "--", "sh", "-c", stopScript, "sh", pid).CombinedOutput()
			if err != nil {
				if msg := strings.TrimSpace(string(out)); msg != "" {
					return fmt.Errorf("run: stopping %s in WSL: %w: %s", pid, err, firstLine(msg))
				}
				return fmt.Errorf("run: stopping %s in WSL: %w", pid, err)
			}
			return nil
		}
	}
	return l, nil
}

// firstLine returns the first line of s.
func firstLine(s string) string {
	line, _, _ := strings.Cut(s, "\n")
	return strings.TrimSpace(line)
}
