package compose

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"

	"github.com/ape-dev-de/tiegel/internal/docker"
	"github.com/ape-dev-de/tiegel/internal/interp"
)

// ExecMode selects how PHP is started inside the service.
type ExecMode int

const (
	// ExecAuto uses `exec` when the service is running and `run --rm`
	// otherwise.
	ExecAuto ExecMode = iota
	// ExecAlways always uses `docker compose exec`.
	ExecAlways
	// RunAlways always uses `docker compose run --rm`.
	RunAlways
)

// Interpreter runs php inside a compose service. It implements
// interp.Interpreter.
type Interpreter struct {
	// Project is the compose project name (-p).
	Project string
	// Dir is the project directory (--project-directory).
	Dir string
	// Files are the compose files (-f), in order.
	Files []string
	// Profiles are passed as --profile.
	Profiles []string
	// Service is the service that runs PHP.
	Service string
	// Mode selects exec vs. run; default ExecAuto.
	Mode ExecMode
	// RunDeps starts linked services for `run`; by default --no-deps is
	// passed so a quick `php -v` does not boot the whole stack.
	RunDeps bool
	// Docker is the docker binary; empty means "docker" from PATH.
	Docker string
	// Context is the Docker context (passed as --context before
	// "compose"); empty means docker's current context.
	Context string
	// Env holds extra environment entries for docker (e.g. the PATH that
	// contains the compose plugin and credential helpers).
	Env []string
	// PHP is the php binary inside the container; empty means "php".
	PHP string
	// Mappings translate between host and container paths. Initialised
	// from the service's bind mounts and freely editable.
	Mappings Mappings
	// PHPVersion is a known version (e.g. from a cached probe); it is only
	// set before the interpreter is shared and never written by methods.
	// Version results go to an internal, synchronised cache (see
	// CachedVersion).
	PHPVersion string

	detected *interp.VersionCache
}

var _ interp.Interpreter = (*Interpreter)(nil)

// NewInterpreter returns an interpreter for service in p, with path mappings
// derived from the service's volumes.
func NewInterpreter(p *Project, service string) *Interpreter {
	c := &Interpreter{
		Project:  p.Name,
		Dir:      p.Dir,
		Files:    append([]string(nil), p.Files...),
		Profiles: append([]string(nil), p.Profiles...),
		Service:  service,
		detected: interp.NewVersionCache(),
	}
	if svc, ok := p.Service(service); ok {
		c.Mappings = ServiceMappings(p.Dir, svc)
	}
	return c
}

// ExecOnly returns a copy of c that only uses `docker compose exec`: it
// never starts a container (`run` could create one and make Docker pull an
// image). Automatic probes that the user did not trigger (version
// detection at project open, hooks) must use it; on a stopped service
// they fail instead.
func (c *Interpreter) ExecOnly() *Interpreter {
	cp := *c
	cp.Mode = ExecAlways
	cp.Files = append([]string(nil), c.Files...)
	cp.Profiles = append([]string(nil), c.Profiles...)
	cp.Env = append([]string(nil), c.Env...)
	return &cp
}

// ID implements interp.Interpreter.
func (c *Interpreter) ID() string { return "compose:" + c.Project + ":" + c.Service }

// Label implements interp.Interpreter.
func (c *Interpreter) Label() string {
	if v := c.CachedVersion(); v != "" {
		return fmt.Sprintf("PHP %s (compose %s/%s)", v, c.Project, c.Service)
	}
	return fmt.Sprintf("PHP (compose %s/%s)", c.Project, c.Service)
}

// Kind implements interp.Interpreter.
func (c *Interpreter) Kind() interp.Kind { return interp.KindCompose }

// Version implements interp.Interpreter.
func (c *Interpreter) Version(ctx context.Context) (string, error) {
	v, err := interp.QueryVersion(ctx, c)
	if err != nil {
		return "", err
	}
	c.detected.Set(v)
	return v, nil
}

// CachedVersion returns the version of the last successful Version call
// (shared with copies from ExecOnly), else PHPVersion. Safe for
// concurrent use.
func (c *Interpreter) CachedVersion() string {
	if v := c.detected.Get(); v != "" {
		return v
	}
	return c.PHPVersion
}

// Modules implements interp.Interpreter.
func (c *Interpreter) Modules(ctx context.Context) ([]string, error) {
	return interp.QueryModules(ctx, c)
}

// Ini implements interp.Interpreter.
func (c *Interpreter) Ini(ctx context.Context) (interp.IniValues, error) {
	return interp.QueryIni(ctx, c)
}

// Client returns the docker CLI of c (binary, context, environment).
func (c *Interpreter) Client() docker.Client {
	return docker.Client{Binary: c.Docker, Context: c.Context, Env: c.Env}
}

// DockerArgs returns the complete docker arguments for php with args:
// the global options (--context) followed by ExecArgs.
func (c *Interpreter) DockerArgs(useExec bool, args ...string) []string {
	return c.Client().Args(c.ExecArgs(useExec, args...)...)
}

// BaseArgs returns the `docker` arguments that select the project:
// compose --project-directory DIR -p NAME -f FILE... --profile P...
func (c *Interpreter) BaseArgs() []string {
	args := []string{"compose"}
	if c.Dir != "" {
		args = append(args, "--project-directory", c.Dir)
	}
	if c.Project != "" {
		args = append(args, "-p", c.Project)
	}
	for _, f := range c.Files {
		args = append(args, "-f", f)
	}
	for _, p := range c.Profiles {
		args = append(args, "--profile", p)
	}
	return args
}

// ExecArgs returns the docker arguments to run php with args, via exec when
// useExec is true and via run --rm otherwise.
func (c *Interpreter) ExecArgs(useExec bool, args ...string) []string {
	php := c.PHP
	if php == "" {
		php = "php"
	}
	a := c.BaseArgs()
	if useExec {
		a = append(a, "exec", "-T", c.Service, php)
	} else {
		a = append(a, "run", "--rm", "-T")
		if !c.RunDeps {
			a = append(a, "--no-deps")
		}
		a = append(a, c.Service, php)
	}
	return append(a, args...)
}

// Command implements interp.Interpreter. In ExecAuto mode it queries the
// service status first, so it may block briefly.
func (c *Interpreter) Command(ctx context.Context, args ...string) (*exec.Cmd, error) {
	useExec := true
	switch c.Mode {
	case RunAlways:
		useExec = false
	case ExecAuto:
		running, err := c.Running(ctx)
		if err != nil {
			return nil, err
		}
		useExec = running
	}
	cmd := c.Client().Command(ctx, c.ExecArgs(useExec, args...)...)
	cmd.Dir = c.Dir
	return cmd, nil
}

// ContainerState is one entry of `docker compose ps --format json`.
type ContainerState struct {
	Name    string `json:"Name"`
	Service string `json:"Service"`
	State   string `json:"State"`
	Health  string `json:"Health"`
	Status  string `json:"Status"`
}

// Status lists the containers of the project (all services).
func (c *Interpreter) Status(ctx context.Context) ([]ContainerState, error) {
	args := append(c.BaseArgs(), "ps", "--all", "--format", "json")
	cmd := c.Client().Command(ctx, args...)
	cmd.Dir = c.Dir
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("compose: ps: %w: %s", err, strings.TrimSpace(stderr.String()))
	}
	return ParsePS(stdout.Bytes())
}

// Running reports whether a container of the service is in state "running".
func (c *Interpreter) Running(ctx context.Context) (bool, error) {
	states, err := c.Status(ctx)
	if err != nil {
		return false, err
	}
	for _, s := range states {
		if s.Service == c.Service && strings.EqualFold(s.State, "running") {
			return true, nil
		}
	}
	return false, nil
}

// Service states reported by ServiceState.
const (
	StateRunning = "running" // a container of the service runs
	StateStopped = "stopped" // containers exist, none runs
	StateMissing = "missing" // no container was created yet
)

// ServiceState returns the state of the interpreter's service
// (StateRunning, StateStopped or StateMissing) and its containers.
// It asks the engine (`docker compose ps`), so call it only for a user
// action or an execution.
func (c *Interpreter) ServiceState(ctx context.Context) (string, []ContainerState, error) {
	states, err := c.Status(ctx)
	if err != nil {
		return "", nil, err
	}
	var mine []ContainerState
	state := StateMissing
	for _, s := range states {
		if s.Service != c.Service {
			continue
		}
		mine = append(mine, s)
		if strings.EqualFold(s.State, "running") {
			state = StateRunning
		} else if state != StateRunning {
			state = StateStopped
		}
	}
	return state, mine, nil
}

// ParsePS parses `docker compose ps --format json`. Compose v2.21+ prints
// one JSON object per line, older versions a single JSON array; both are
// accepted.
func ParsePS(data []byte) ([]ContainerState, error) {
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 {
		return nil, nil
	}
	if trimmed[0] == '[' {
		var list []ContainerState
		if err := json.Unmarshal(trimmed, &list); err != nil {
			return nil, fmt.Errorf("compose: parse ps output: %w", err)
		}
		return list, nil
	}
	var list []ContainerState
	sc := bufio.NewScanner(bytes.NewReader(trimmed))
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for sc.Scan() {
		line := bytes.TrimSpace(sc.Bytes())
		if len(line) == 0 {
			continue
		}
		var s ContainerState
		if err := json.Unmarshal(line, &s); err != nil {
			return nil, fmt.Errorf("compose: parse ps output: %w", err)
		}
		list = append(list, s)
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("compose: read ps output: %w", err)
	}
	return list, nil
}
