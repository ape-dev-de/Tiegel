package compose

import (
	"bytes"
	"context"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/ape-dev-de/tiegel/internal/docker"
)

// Stack drives a compose project as a whole for the Services window:
// container states, start/stop/up/down, logs and shells. Every method
// that runs docker contacts the engine; call them only for a user action
// or while the user looks at the result.
type Stack struct {
	// Project is the compose project name (-p).
	Project string
	// Dir is the project directory (--project-directory).
	Dir string
	// Files are the compose files (-f), in order.
	Files []string
	// Profiles are passed as --profile.
	Profiles []string
	// Client is the docker CLI (binary, context, environment).
	Client docker.Client
}

// NewStack returns the stack of p run with client.
func NewStack(p *Project, client docker.Client) Stack {
	return Stack{
		Project: p.Name, Dir: p.Dir,
		Files:    append([]string(nil), p.Files...),
		Profiles: append([]string(nil), p.Profiles...),
		Client:   client,
	}
}

// BaseArgs returns the `docker` arguments that select the project:
// compose --project-directory DIR -p NAME -f FILE... --profile P...
func (s Stack) BaseArgs() []string {
	args := []string{"compose"}
	if s.Dir != "" {
		args = append(args, "--project-directory", s.Dir)
	}
	if s.Project != "" {
		args = append(args, "-p", s.Project)
	}
	for _, f := range s.Files {
		args = append(args, "-f", f)
	}
	for _, p := range s.Profiles {
		args = append(args, "--profile", p)
	}
	return args
}

// args returns BaseArgs followed by more.
func (s Stack) args(more ...string) []string { return append(s.BaseArgs(), more...) }

// PS lists all containers of the project (`docker compose ps --all`).
func (s Stack) PS(ctx context.Context) ([]ContainerState, error) {
	cmd := s.Client.Command(ctx, s.args("ps", "--all", "--format", "json")...)
	cmd.Dir = s.Dir
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		if msg := strings.TrimSpace(stderr.String()); msg != "" {
			return nil, fmt.Errorf("compose: ps: %w: %s", err, firstLine(msg))
		}
		return nil, fmt.Errorf("compose: ps: %w", err)
	}
	return ParsePS(stdout.Bytes())
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return strings.TrimSpace(s[:i])
	}
	return s
}

// Actions of Stack.Action.
const (
	ActionUp      = "up"
	ActionDown    = "down"
	ActionStart   = "start"
	ActionStop    = "stop"
	ActionRestart = "restart"
)

// Actions lists the supported actions.
var Actions = []string{ActionUp, ActionStart, ActionStop, ActionRestart, ActionDown}

// nameRe is the charset of service and container names (and IDs). Names
// end up on terminal command lines (cmd.exe, wt, launcher scripts), so
// nothing a shell interprets passes, and no leading "-" docker would
// read as an option (D308).
var nameRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_./-]*$`)

// shellRe additionally allows an absolute shell path ("/bin/bash").
var shellRe = regexp.MustCompile(`^/?[A-Za-z0-9][A-Za-z0-9_./-]*$`)

// ValidName reports whether name is a safe service or container name.
func ValidName(name string) bool { return nameRe.MatchString(name) }

// CheckServices rejects service names outside the safe charset
// (ValidName), e.g. names docker would read as options.
func CheckServices(services []string) error {
	for _, s := range services {
		if !ValidName(s) {
			return fmt.Errorf("compose: invalid service name %q", s)
		}
	}
	return nil
}

// CheckShell rejects a shell (program in the container) outside the safe
// charset: a name or an absolute path.
func CheckShell(shell string) error {
	if !shellRe.MatchString(shell) {
		return fmt.Errorf("compose: invalid shell %q", shell)
	}
	return nil
}

// ActionArgs returns the docker arguments of action for services (all
// services when empty). up runs detached; down applies to the whole
// project and refuses services.
func (s Stack) ActionArgs(action string, services ...string) ([]string, error) {
	if err := CheckServices(services); err != nil {
		return nil, err
	}
	switch action {
	case ActionUp:
		return s.args(append([]string{"up", "--detach"}, services...)...), nil
	case ActionStart, ActionStop, ActionRestart:
		return s.args(append([]string{action}, services...)...), nil
	case ActionDown:
		if len(services) > 0 {
			return nil, fmt.Errorf("compose: down applies to the whole project, not to services")
		}
		return s.args("down"), nil
	}
	return nil, fmt.Errorf("compose: unknown action %q (%s)", action, strings.Join(Actions, ", "))
}

// Action runs action (see ActionArgs) and returns its output lines
// (compose reports progress on standard error).
func (s Stack) Action(ctx context.Context, action string, services ...string) ([]string, error) {
	args, err := s.ActionArgs(action, services...)
	if err != nil {
		return nil, err
	}
	cmd := s.Client.Command(ctx, args...)
	cmd.Dir = s.Dir
	var out []string
	err = docker.Stream(cmd, func(l docker.Line) {
		if strings.TrimSpace(l.Text) != "" && len(out) < 1000 {
			out = append(out, l.Text)
		}
	})
	return out, err
}

// LogOptions select the output of Stack.LogsArgs.
type LogOptions struct {
	// Services to show; empty means all.
	Services []string
	// Tail is the number of lines from the end; negative means all.
	Tail int
	// Follow keeps streaming new output.
	Follow bool
	// Timestamps prefixes every line with its time.
	Timestamps bool
}

// LogsArgs returns the docker arguments of `docker compose logs`.
func (s Stack) LogsArgs(o LogOptions) ([]string, error) {
	if err := CheckServices(o.Services); err != nil {
		return nil, err
	}
	a := s.args("logs", "--no-color")
	if o.Tail >= 0 {
		a = append(a, "--tail", strconv.Itoa(o.Tail))
	}
	if o.Follow {
		a = append(a, "--follow")
	}
	if o.Timestamps {
		a = append(a, "--timestamps")
	}
	return append(a, o.Services...), nil
}

// ShellArgs returns the complete docker command line (binary first) that
// opens shell (default "sh") inside service with a TTY.
func (s Stack) ShellArgs(service, shell string) ([]string, error) {
	if err := CheckServices([]string{service}); err != nil {
		return nil, err
	}
	if shell == "" {
		shell = "sh"
	}
	if err := CheckShell(shell); err != nil {
		return nil, err
	}
	return append([]string{s.Client.Bin()}, s.Client.Args(s.args("exec", service, shell)...)...), nil
}

// ParseLogLine splits a line of `docker compose logs` into the container
// prefix ("app-1  | text") and the text. Lines without a prefix return an
// empty container.
func ParseLogLine(line string) (container, text string) {
	i := strings.Index(line, " |")
	if i <= 0 {
		return "", line
	}
	prefix := strings.TrimSpace(line[:i])
	if prefix == "" || strings.ContainsAny(prefix, " \t") {
		return "", line
	}
	text = line[i+2:]
	text = strings.TrimPrefix(text, " ")
	return prefix, text
}

// Service is a compose service with its containers, for the Services
// window.
type Service struct {
	Name  string `json:"name"`
	Image string `json:"image,omitempty"`
	// State is StateRunning, StateStopped or StateMissing (no container).
	State string `json:"state"`
	// Health is the worst health of the running containers (unhealthy,
	// starting, healthy) or empty.
	Health string `json:"health,omitempty"`
	// Ports are the published and exposed ports of all containers.
	Ports      []docker.Port `json:"ports,omitempty"`
	Containers []Container   `json:"containers"`
	// Defined is false for containers of a service that is not in the
	// compose files (any more), e.g. started with another profile.
	Defined bool `json:"defined"`
	// Profiles of the service definition.
	Profiles []string `json:"profiles,omitempty"`
}

// Container is a container of a compose service.
type Container struct {
	ID       string        `json:"id,omitempty"`
	Name     string        `json:"name"`
	State    string        `json:"state"`
	Status   string        `json:"status,omitempty"`
	Health   string        `json:"health,omitempty"`
	Image    string        `json:"image,omitempty"`
	ExitCode int           `json:"exitCode,omitempty"`
	Ports    []docker.Port `json:"ports,omitempty"`
}

// ContainerPorts converts the publishers of a container; host replaces
// wildcard addresses in URLs (see docker.PortURL).
func ContainerPorts(pubs []Publisher, host string) []docker.Port {
	out := make([]docker.Port, 0, len(pubs))
	for _, p := range pubs {
		proto := strings.ToLower(p.Protocol)
		if proto == "" {
			proto = "tcp"
		}
		dp := docker.Port{Target: p.TargetPort, Protocol: proto}
		if p.PublishedPort > 0 {
			dp.IP, dp.Published = p.URL, p.PublishedPort
			dp.URL = docker.PortURL(p.URL, p.PublishedPort, proto, host)
		}
		out = append(out, dp)
	}
	return docker.SortPorts(out)
}

var healthRank = map[string]int{"": 0, "healthy": 1, "starting": 2, "unhealthy": 3}

// MergeServices combines the services defined in p (may be nil) with the
// containers reported by `docker compose ps`; services that only exist as
// containers come last with Defined false. host replaces wildcard
// addresses in port URLs.
func MergeServices(p *Project, states []ContainerState, host string) []Service {
	byName := map[string]*Service{}
	var order []string
	for _, n := range p.ServiceNames() {
		def, _ := p.Service(n)
		byName[n] = &Service{Name: n, Image: def.Image, State: StateMissing, Defined: true, Profiles: def.Profiles, Containers: []Container{}}
		order = append(order, n)
	}
	var extra []string
	for _, st := range states {
		svc, ok := byName[st.Service]
		if !ok {
			svc = &Service{Name: st.Service, Image: st.Image, State: StateMissing, Containers: []Container{}}
			byName[st.Service] = svc
			extra = append(extra, st.Service)
		}
		c := Container{ID: st.ID, Name: st.Name, State: st.State, Status: st.Status, Health: st.Health, Image: st.Image, ExitCode: st.ExitCode,
			Ports: ContainerPorts(st.Publishers, host)}
		svc.Containers = append(svc.Containers, c)
		running := strings.EqualFold(st.State, "running")
		switch {
		case running:
			svc.State = StateRunning
			if healthRank[st.Health] > healthRank[svc.Health] {
				svc.Health = st.Health
			}
		case svc.State != StateRunning:
			svc.State = StateStopped
		}
		svc.Ports = append(svc.Ports, c.Ports...)
		if svc.Image == "" {
			svc.Image = st.Image
		}
	}
	sort.Strings(extra)
	out := make([]Service, 0, len(order)+len(extra))
	for _, n := range append(order, extra...) {
		svc := byName[n]
		sort.SliceStable(svc.Containers, func(i, j int) bool { return svc.Containers[i].Name < svc.Containers[j].Name })
		svc.Ports = docker.SortPorts(svc.Ports)
		out = append(out, *svc)
	}
	return out
}
