package docker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/ape-dev-de/tiegel/internal/api"
	"github.com/ape-dev-de/tiegel/internal/compose"
	"github.com/ape-dev-de/tiegel/internal/core/settings"
	dk "github.com/ape-dev-de/tiegel/internal/docker"
)

// Services window backend (AP14.3): compose projects of the open project
// with services, containers, state and ports; actions; logs streamed as
// events; refreshing while a client watches (docker events, polling as
// fallback); shells in an external terminal; ports in the browser.
//
// Privacy (D230, D270): nothing here runs on its own. docker.services,
// actions, logs, shells and URLs are user actions; docker.watchStart is
// called by the GUI only while the Services window is visible and
// docker.watchStop when it is hidden.

// Event topics.
const (
	// TopicLog carries followed log lines (LogEvent).
	TopicLog = "docker.log"
	// TopicServicesChanged carries a new snapshot (ServicesResult) while
	// a watch is active.
	TopicServicesChanged = "docker.servicesChanged"
)

// Settings of the Services window.
const (
	// SettingRefresh is the polling interval in seconds when docker
	// events are not available (global).
	SettingRefresh = "docker.servicesRefreshSeconds"
	// SettingTerminal is the external terminal for "Open Shell" (global).
	SettingTerminal = "docker.terminal"
)

// Limits.
const (
	defaultTail   = 200
	maxLogLines   = 20000
	logFlushEvery = 100 * time.Millisecond
	logBatchMax   = 500
	debounce      = 250 * time.Millisecond
)

// ComposeProject is a compose project of the open project.
type ComposeProject struct {
	Name string `json:"name"`
	Dir  string `json:"dir"`
	// Files are the compose files, relative to the project root.
	Files    []string          `json:"files"`
	Services []compose.Service `json:"services"`
	// Error explains why the files could not be read or the containers
	// not be listed (engine down).
	Error string `json:"error,omitempty"`
}

// ServicesParams are the params of "docker.services".
type ServicesParams struct {
	// Standalone also lists the containers that do not belong to the
	// project's compose project (docker ps --all).
	Standalone bool `json:"standalone,omitempty"`
}

// ServicesResult is the result of "docker.services" and the payload of
// docker.servicesChanged.
type ServicesResult struct {
	// Context is the effective Docker context, Endpoint its endpoint.
	Context  string `json:"context"`
	Endpoint string `json:"endpoint,omitempty"`
	Remote   bool   `json:"remote,omitempty"`
	// Projects are the compose projects of the open project (empty
	// without compose files).
	Projects []ComposeProject `json:"projects"`
	// Standalone are the other containers (ServicesParams.Standalone).
	Standalone []dk.Container `json:"standalone,omitempty"`
	// Error explains a missing docker binary.
	Error string `json:"error,omitempty"`
	// Refresh is how a watch keeps the snapshot current: "events" or
	// "poll" (only in docker.servicesChanged).
	Refresh string `json:"refresh,omitempty"`
}

// ServiceActionParams are the params of "docker.serviceAction".
type ServiceActionParams struct {
	// Action is up, start, stop, restart or down.
	Action string `json:"action"`
	// Service or Services limit the action (all services when empty;
	// down always applies to the whole project).
	Service  string   `json:"service,omitempty"`
	Services []string `json:"services,omitempty"`
	// Container acts on one container by ID or name (start, stop,
	// restart) instead of compose services.
	Container string `json:"container,omitempty"`
	// Confirm must be true for down (removes containers and networks).
	Confirm bool `json:"confirm,omitempty"`
}

// ServiceActionResult is the result of "docker.serviceAction".
type ServiceActionResult struct {
	Action string `json:"action"`
	// Target names what was acted on ("project app", "service web", …).
	Target string `json:"target"`
	// Output is the progress compose printed.
	Output []string `json:"output"`
}

// LogsParams are the params of "docker.logs".
type LogsParams struct {
	// Service or Services select compose services (all when empty).
	Service  string   `json:"service,omitempty"`
	Services []string `json:"services,omitempty"`
	// Container shows the logs of one container (ID or name) instead.
	Container string `json:"container,omitempty"`
	// Tail is the number of lines from the end: 0 means 200, negative
	// means all.
	Tail int `json:"tail,omitempty"`
	// Follow streams new lines as docker.log events until docker.logsStop.
	Follow bool `json:"follow,omitempty"`
	// Filter keeps only lines containing the text (case-insensitive).
	Filter     string `json:"filter,omitempty"`
	Timestamps bool   `json:"timestamps,omitempty"`
}

// LogLine is one log line.
type LogLine struct {
	// Container is the compose prefix ("app-1"), empty for container logs.
	Container string `json:"container,omitempty"`
	Text      string `json:"text"`
	// Stream is "stderr" for lines docker wrote to standard error.
	Stream string `json:"stream,omitempty"`
}

// LogsResult is the result of "docker.logs".
type LogsResult struct {
	// ID identifies a followed stream (docker.log, docker.logsStop).
	ID string `json:"id,omitempty"`
	// Lines are the lines of a non-followed request.
	Lines     []LogLine `json:"lines"`
	Truncated bool      `json:"truncated,omitempty"`
}

// LogEvent is the payload of docker.log.
type LogEvent struct {
	ID    string    `json:"id"`
	Lines []LogLine `json:"lines,omitempty"`
	// Done marks the end of the stream; Error explains a failure.
	Done  bool   `json:"done,omitempty"`
	Error string `json:"error,omitempty"`
}

// IDParams carry a stream or watch ID.
type IDParams struct {
	ID string `json:"id"`
}

// StoppedResult reports whether something was stopped.
type StoppedResult struct {
	Stopped bool `json:"stopped"`
}

// WatchParams are the params of "docker.watchStart".
type WatchParams struct {
	Standalone bool `json:"standalone,omitempty"`
}

// WatchResult is the result of "docker.watchStart".
type WatchResult struct {
	ID string `json:"id"`
	// IntervalSeconds is the polling interval used without docker events.
	IntervalSeconds int `json:"intervalSeconds"`
}

// ShellParams are the params of "docker.shell".
type ShellParams struct {
	// Service of the compose project, or Container (ID or name).
	Service   string `json:"service,omitempty"`
	Container string `json:"container,omitempty"`
	// Shell inside the container; default sh.
	Shell string `json:"shell,omitempty"`
}

// LaunchResult is the result of "docker.shell" and "docker.openURL".
type LaunchResult struct {
	// Command is the docker command run in the terminal (docker.shell).
	Command []string `json:"command,omitempty"`
	// Launched is the program that was started.
	Launched []string `json:"launched"`
}

// URLParams are the params of "docker.openURL".
type URLParams struct {
	URL string `json:"url"`
}

// servicesState is the mutable state of the Services backend.
type servicesState struct {
	mu sync.Mutex
	// compose project cache, keyed by root and file stamps
	cacheKey string
	cacheVal *compose.Project
	cacheErr error

	seq    int
	logs   map[string]context.CancelFunc
	leases map[string]bool // watch ID → wants standalone containers
	watch  *watcher
}

type watcher struct {
	cancel context.CancelFunc
	done   chan struct{}
	kick   chan struct{}
}

func newServicesState() *servicesState {
	return &servicesState{logs: map[string]context.CancelFunc{}, leases: map[string]bool{}}
}

func (m *Module) registerServices(p *api.Provider) {
	api.Add(p, "docker.services", "Compose projects of the open project with services, containers, state and ports (contacts the engine)", m.services)
	api.Add(p, "docker.serviceAction", "Run up, start, stop, restart or down (confirm: true) for the compose project, a service or a container", m.serviceAction)
	api.Add(p, "docker.logs", "Logs of services or a container: tail, filter; follow streams docker.log events until docker.logsStop", m.logs)
	api.Add(p, "docker.logsStop", "Stop a followed log stream", m.logsStop)
	api.Add(p, "docker.watchStart", "Keep docker.servicesChanged coming while the Services window is visible (docker events, polling as fallback)", m.watchStart)
	api.Add(p, "docker.watchStop", "End a watch started by docker.watchStart", m.watchStop)
	api.Add(p, "docker.shell", "Open a shell inside a service or container in an external terminal", m.shell)
	api.Add(p, "docker.openURL", "Open an http(s) URL (a published port) in the default browser", m.openURL)
}

// serviceSpecs are the settings of the Services window.
func serviceSpecs() []settings.Spec {
	return []settings.Spec{
		{Key: SettingRefresh, Kind: settings.KindInt, Default: 5, Scope: settings.ScopeGlobal, Label: "Services refresh interval (seconds)",
			Help: "Polling interval of the Services window when docker events are not available; only while the window is visible.",
			Min:  settings.IntPtr(1), Max: settings.IntPtr(3600)},
		{Key: SettingTerminal, Kind: settings.KindString, Default: "", Scope: settings.ScopeGlobal, Label: "Terminal for container shells",
			Help: "macOS: application for `open -a` (default Terminal, e.g. iTerm). Linux/Windows: command the docker command is appended to (e.g. \"kitty\", \"alacritty -e\", \"wt\"); empty uses $TERMINAL, x-terminal-emulator, … or wt/cmd. The embedded terminal (M15.5) will replace this."},
	}
}

func (m *Module) refreshInterval() time.Duration {
	secs := 5
	if g := m.globalStore(); g != nil {
		secs = settings.Get(g, SettingRefresh, 5)
	}
	return time.Duration(max(1, secs)) * time.Second
}

// engineTimeout bounds compose ps and other short engine queries.
func (m *Module) engineTimeout() time.Duration { return 3 * m.timeout() }

// composeProject loads (cached by file modification times) the compose
// project at the project root; nil without project or compose files.
func (m *Module) composeProject(ctx context.Context) (*compose.Project, error) {
	root := m.app.ProjectRoot()
	if root == "" {
		return nil, nil
	}
	env := m.app.Options().ComposeEnv
	found, err := compose.FindFiles(root, env)
	if errors.Is(err, compose.ErrNoComposeFile) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var key strings.Builder
	key.WriteString(root)
	for _, f := range append(append([]string(nil), found.Paths...), filepath.Join(root, ".env")) {
		if st, err := os.Stat(f); err == nil {
			fmt.Fprintf(&key, "|%s:%d:%d", f, st.ModTime().UnixNano(), st.Size())
		}
	}
	sv := m.sv
	sv.mu.Lock()
	if sv.cacheKey == key.String() {
		p, err := sv.cacheVal, sv.cacheErr
		sv.mu.Unlock()
		return p, err
	}
	sv.mu.Unlock()
	p, err := compose.Load(ctx, root, compose.LoadOptions{Env: env, Files: found.Paths})
	if ctx.Err() == nil {
		sv.mu.Lock()
		sv.cacheKey, sv.cacheVal, sv.cacheErr = key.String(), p, err
		sv.mu.Unlock()
	}
	return p, err
}

// stack returns the compose stack of the open project.
func (m *Module) stack(ctx context.Context) (compose.Stack, error) {
	c, _, err := m.client(ctx, "")
	if err != nil {
		return compose.Stack{}, m.unavailable(err)
	}
	p, err := m.composeProject(ctx)
	if err != nil {
		return compose.Stack{}, api.Errorf(api.CodeFailed, "%w", err)
	}
	if p == nil {
		if m.app.ProjectRoot() == "" {
			return compose.Stack{}, api.Errorf(api.CodeNoProject, "no project open")
		}
		return compose.Stack{}, api.Errorf(api.CodeNotFound, "the project has no compose file")
	}
	return compose.NewStack(p, c), nil
}

// contextInfo resolves the effective context and its endpoint (reads only
// the local CLI configuration).
func (m *Module) contextInfo(ctx context.Context, c dk.Client) (name, endpoint string, remote bool) {
	name = c.Context
	list, err := c.Contexts(ctx)
	if err != nil {
		return name, "", false
	}
	info, ok := dk.FindContext(list, name)
	if name == "" {
		info, ok = dk.CurrentContext(list)
	}
	if !ok {
		return name, "", false
	}
	endpoint = info.Endpoint
	if host := m.getenv("DOCKER_HOST"); host != "" && c.Context == "" && info.Name == "default" {
		endpoint = host
	}
	return info.Name, endpoint, dk.IsRemote(endpoint)
}

// snapshot lists the compose projects (and standalone containers).
func (m *Module) snapshot(ctx context.Context, standalone bool) ServicesResult {
	res := ServicesResult{Projects: []ComposeProject{}}
	c, _, err := m.client(ctx, "")
	if err != nil {
		res.Error = err.Error()
		return res
	}
	res.Context, res.Endpoint, res.Remote = m.contextInfo(ctx, c)
	host := dk.LinkHost(res.Endpoint)
	root := m.app.ProjectRoot()
	p, perr := m.composeProject(ctx)
	ours := ""
	switch {
	case perr != nil:
		res.Projects = append(res.Projects, ComposeProject{Name: filepath.Base(root), Dir: root, Files: []string{}, Services: []compose.Service{}, Error: perr.Error()})
	case p != nil:
		ours = p.Name
		cp := ComposeProject{Name: p.Name, Dir: p.Dir, Files: make([]string, 0, len(p.Files))}
		for _, f := range p.Files {
			if r, err := filepath.Rel(root, f); err == nil && !strings.HasPrefix(r, "..") {
				f = filepath.ToSlash(r)
			}
			cp.Files = append(cp.Files, f)
		}
		qctx, cancel := context.WithTimeout(ctx, m.engineTimeout())
		states, err := compose.NewStack(p, c).PS(qctx)
		cancel()
		if err != nil {
			cp.Error = err.Error()
		}
		cp.Services = compose.MergeServices(p, states, host)
		res.Projects = append(res.Projects, cp)
	}
	if standalone {
		qctx, cancel := context.WithTimeout(ctx, m.engineTimeout())
		list, err := c.Containers(qctx, host)
		cancel()
		if err != nil && res.Error == "" {
			res.Error = err.Error()
		}
		for _, ct := range list {
			if ours == "" || ct.ComposeProject != ours {
				res.Standalone = append(res.Standalone, ct)
			}
		}
	}
	return res
}

func (m *Module) services(ctx context.Context, p ServicesParams) (ServicesResult, error) {
	return m.snapshot(ctx, p.Standalone), nil
}

func checkContainer(name string) error {
	if strings.TrimSpace(name) == "" || strings.HasPrefix(name, "-") || strings.ContainsAny(name, " \t\n") {
		return api.Errorf(api.CodeInvalidParams, "invalid container %q", name)
	}
	return nil
}

func (m *Module) serviceAction(ctx context.Context, p ServiceActionParams) (ServiceActionResult, error) {
	res := ServiceActionResult{Action: p.Action, Output: []string{}}
	services := append([]string(nil), p.Services...)
	if p.Service != "" {
		services = append(services, p.Service)
	}
	if p.Container != "" {
		if err := checkContainer(p.Container); err != nil {
			return res, err
		}
		switch p.Action {
		case compose.ActionStart, compose.ActionStop, compose.ActionRestart:
		default:
			return res, api.Errorf(api.CodeInvalidParams, "containers support start, stop and restart, not %q", p.Action)
		}
		c, _, err := m.client(ctx, "")
		if err != nil {
			return res, m.unavailable(err)
		}
		res.Target = "container " + p.Container
		out, err := c.Output(ctx, p.Action, p.Container)
		if s := strings.TrimSpace(string(out)); s != "" {
			res.Output = append(res.Output, strings.Split(s, "\n")...)
		}
		m.kickWatch()
		if err != nil {
			return res, api.Errorf(api.CodeFailed, "%w", err)
		}
		return res, nil
	}
	st, err := m.stack(ctx)
	if err != nil {
		return res, err
	}
	if _, err := st.ActionArgs(p.Action, services...); err != nil {
		return res, api.Errorf(api.CodeInvalidParams, "%w", err)
	}
	res.Target = "project " + st.Project
	if len(services) > 0 {
		res.Target = "service " + strings.Join(services, ", ")
	}
	if p.Action == compose.ActionDown && !p.Confirm {
		return res, api.Errorf(api.CodeInvalidParams, "down stops and removes the containers and networks of compose project %s; pass confirm: true", st.Project)
	}
	out, err := st.Action(ctx, p.Action, services...)
	res.Output = append(res.Output, out...)
	m.kickWatch()
	if err != nil {
		return res, api.Errorf(api.CodeFailed, "%w", err)
	}
	return res, nil
}

// logArgs returns the docker client and arguments for p.
func (m *Module) logArgs(ctx context.Context, p LogsParams) (dk.Client, []string, string, error) {
	tail := p.Tail
	if tail == 0 {
		tail = defaultTail
	}
	if p.Container != "" {
		if err := checkContainer(p.Container); err != nil {
			return dk.Client{}, nil, "", err
		}
		c, _, err := m.client(ctx, "")
		if err != nil {
			return c, nil, "", m.unavailable(err)
		}
		args := []string{"logs"}
		if tail >= 0 {
			args = append(args, "--tail", fmt.Sprint(tail))
		}
		if p.Follow {
			args = append(args, "--follow")
		}
		if p.Timestamps {
			args = append(args, "--timestamps")
		}
		return c, append(args, p.Container), "", nil
	}
	st, err := m.stack(ctx)
	if err != nil {
		return dk.Client{}, nil, "", err
	}
	services := append([]string(nil), p.Services...)
	if p.Service != "" {
		services = append(services, p.Service)
	}
	args, err := st.LogsArgs(compose.LogOptions{Services: services, Tail: tail, Follow: p.Follow, Timestamps: p.Timestamps})
	if err != nil {
		return dk.Client{}, nil, "", api.Errorf(api.CodeInvalidParams, "%w", err)
	}
	return st.Client, args, st.Dir, nil
}

// logLine converts an output line; ok is false when the filter drops it.
func logLine(l dk.Line, prefixed bool, filter string) (LogLine, bool) {
	ll := LogLine{Text: l.Text}
	if prefixed {
		ll.Container, ll.Text = compose.ParseLogLine(l.Text)
	}
	if l.Stream == "stderr" {
		ll.Stream = "stderr"
	}
	if filter != "" && !strings.Contains(strings.ToLower(l.Text), filter) {
		return ll, false
	}
	return ll, true
}

func (m *Module) logs(ctx context.Context, p LogsParams) (LogsResult, error) {
	c, args, dir, err := m.logArgs(ctx, p)
	if err != nil {
		return LogsResult{}, err
	}
	isCompose := p.Container == ""
	filter := strings.ToLower(strings.TrimSpace(p.Filter))
	if !p.Follow {
		res := LogsResult{Lines: []LogLine{}}
		cmd := c.Command(ctx, args...)
		cmd.Dir = dir
		err := dk.Stream(cmd, func(l dk.Line) {
			if ll, ok := logLine(l, isCompose, filter); ok {
				res.Lines = append(res.Lines, ll)
				if len(res.Lines) > maxLogLines {
					res.Lines, res.Truncated = res.Lines[1:], true
				}
			}
		})
		if err != nil {
			return res, api.Errorf(api.CodeFailed, "%w", err)
		}
		return res, nil
	}

	sv := m.sv
	sv.mu.Lock()
	sv.seq++
	id := fmt.Sprintf("logs-%d", sv.seq)
	// The stream outlives the request; only docker.logsStop (or a
	// project/context change) ends it.
	lctx, cancel := context.WithCancel(context.WithoutCancel(ctx))
	sv.logs[id] = cancel
	sv.mu.Unlock()

	cmd := c.Command(lctx, args...)
	cmd.Dir = dir
	go m.follow(lctx, cancel, id, cmd, isCompose, filter)
	return LogsResult{ID: id, Lines: []LogLine{}}, nil
}

// follow pumps a followed log stream into batched docker.log events.
func (m *Module) follow(ctx context.Context, cancel context.CancelFunc, id string, cmd *exec.Cmd, isCompose bool, filter string) {
	defer cancel()
	var (
		mu      sync.Mutex
		pending []LogLine
	)
	flush := func() {
		mu.Lock()
		lines := pending
		pending = nil
		mu.Unlock()
		if len(lines) > 0 {
			m.publish(TopicLog, LogEvent{ID: id, Lines: lines})
		}
	}
	stop := make(chan struct{})
	tickDone := make(chan struct{})
	go func() {
		defer close(tickDone)
		t := time.NewTicker(logFlushEvery)
		defer t.Stop()
		for {
			select {
			case <-stop:
				return
			case <-t.C:
				flush()
			}
		}
	}()
	err := dk.Stream(cmd, func(l dk.Line) {
		ll, ok := logLine(l, isCompose, filter)
		if !ok {
			return
		}
		mu.Lock()
		pending = append(pending, ll)
		full := len(pending) >= logBatchMax
		mu.Unlock()
		if full {
			flush()
		}
	})
	close(stop)
	<-tickDone
	flush()
	m.sv.mu.Lock()
	delete(m.sv.logs, id)
	m.sv.mu.Unlock()
	ev := LogEvent{ID: id, Done: true}
	if err != nil && ctx.Err() == nil {
		ev.Error = err.Error()
	}
	m.publish(TopicLog, ev)
}

func (m *Module) publish(topic string, payload any) {
	m.mu.Lock()
	app := m.app
	m.mu.Unlock()
	if app != nil {
		app.Bus().Publish(topic, payload)
	}
}

func (m *Module) logsStop(_ context.Context, p IDParams) (StoppedResult, error) {
	m.sv.mu.Lock()
	cancel, ok := m.sv.logs[p.ID]
	delete(m.sv.logs, p.ID)
	m.sv.mu.Unlock()
	if ok {
		cancel()
	}
	return StoppedResult{Stopped: ok}, nil
}

// stopLogs ends all followed streams.
func (m *Module) stopLogs() {
	m.sv.mu.Lock()
	cancels := make([]context.CancelFunc, 0, len(m.sv.logs))
	for id, c := range m.sv.logs {
		cancels = append(cancels, c)
		delete(m.sv.logs, id)
	}
	m.sv.mu.Unlock()
	for _, c := range cancels {
		c()
	}
}

func (m *Module) watchStart(_ context.Context, p WatchParams) (WatchResult, error) {
	m.sv.mu.Lock()
	m.sv.seq++
	id := fmt.Sprintf("watch-%d", m.sv.seq)
	m.sv.leases[id] = p.Standalone
	m.sv.mu.Unlock()
	m.restartWatch()
	return WatchResult{ID: id, IntervalSeconds: int(m.refreshInterval() / time.Second)}, nil
}

func (m *Module) watchStop(_ context.Context, p IDParams) (StoppedResult, error) {
	m.sv.mu.Lock()
	_, ok := m.sv.leases[p.ID]
	delete(m.sv.leases, p.ID)
	m.sv.mu.Unlock()
	if ok {
		m.restartWatch()
	}
	return StoppedResult{Stopped: ok}, nil
}

// restartWatch stops the running watcher and starts a new one when
// leases remain (after lease, project, context or setting changes).
func (m *Module) restartWatch() {
	sv := m.sv
	sv.mu.Lock()
	old := sv.watch
	sv.watch = nil
	standalone, active := false, len(sv.leases) > 0
	for _, s := range sv.leases {
		standalone = standalone || s
	}
	var w *watcher
	if active {
		ctx, cancel := context.WithCancel(context.Background())
		w = &watcher{cancel: cancel, done: make(chan struct{}), kick: make(chan struct{}, 1)}
		sv.watch = w
		go m.runWatch(ctx, w, standalone)
	}
	sv.mu.Unlock()
	if old != nil {
		old.cancel()
		<-old.done
	}
}

// stopWatch ends the watcher and drops all leases (module stop).
func (m *Module) stopWatch() {
	m.sv.mu.Lock()
	m.sv.leases = map[string]bool{}
	m.sv.mu.Unlock()
	m.restartWatch()
}

// kickWatch asks a running watcher to refresh now (after an action).
func (m *Module) kickWatch() {
	m.sv.mu.Lock()
	w := m.sv.watch
	m.sv.mu.Unlock()
	if w != nil {
		select {
		case w.kick <- struct{}{}:
		default:
		}
	}
}

// runWatch publishes docker.servicesChanged whenever the snapshot changes:
// after relevant docker events (debounced) and, when docker events fail,
// every docker.servicesRefreshSeconds.
func (m *Module) runWatch(ctx context.Context, w *watcher, standalone bool) {
	defer close(w.done)
	mode := "events"
	last := ""
	refresh := func() {
		res := m.snapshot(ctx, standalone)
		if ctx.Err() != nil {
			return
		}
		res.Refresh = mode
		b, _ := json.Marshal(res)
		if string(b) == last {
			return
		}
		last = string(b)
		m.publish(TopicServicesChanged, res)
	}
	evCh := make(chan struct{}, 1)
	evErr := make(chan error, 1)
	project := ""
	if !standalone {
		if p, err := m.composeProject(ctx); err == nil && p != nil {
			project = p.Name
		}
	}
	if c, _, err := m.client(ctx, ""); err == nil {
		go func() {
			evErr <- c.Events(ctx, project, func(e dk.Event) {
				if e.Relevant() {
					select {
					case evCh <- struct{}{}:
					default:
					}
				}
			})
		}()
	} else {
		mode = "poll"
	}
	refresh()
	ticker := time.NewTicker(m.refreshInterval())
	defer ticker.Stop()
	var wait <-chan time.Time
	for {
		select {
		case <-ctx.Done():
			return
		case <-evCh:
			if wait == nil {
				wait = time.After(debounce)
			}
		case <-wait:
			wait = nil
			refresh()
		case <-w.kick:
			refresh()
		case err := <-evErr:
			evErr = nil
			if ctx.Err() != nil {
				return
			}
			m.app.Logger().Info("docker events unavailable, polling", "err", err)
			mode = "poll"
			refresh()
		case <-ticker.C:
			if mode == "poll" {
				refresh()
			}
		}
	}
}

func (m *Module) goos() string {
	if m.opts.GOOS != "" {
		return m.opts.GOOS
	}
	return m.app.Options().GOOS
}

// launch starts an external program (terminal, browser) without waiting.
func (m *Module) launch(argv, env []string) error {
	if m.opts.Launch != nil {
		return m.opts.Launch(argv, env)
	}
	return startDetached(argv, env)
}

func (m *Module) shell(ctx context.Context, p ShellParams) (LaunchResult, error) {
	var (
		argv []string
		c    dk.Client
	)
	switch {
	case p.Container != "":
		if err := checkContainer(p.Container); err != nil {
			return LaunchResult{}, err
		}
		cl, _, err := m.client(ctx, "")
		if err != nil {
			return LaunchResult{}, m.unavailable(err)
		}
		c = cl
		sh := p.Shell
		if sh == "" {
			sh = "sh"
		}
		argv = append([]string{c.Bin()}, c.Args("exec", "-it", p.Container, sh)...)
	case p.Service != "":
		st, err := m.stack(ctx)
		if err != nil {
			return LaunchResult{}, err
		}
		c = st.Client
		if argv, err = st.ShellArgs(p.Service, p.Shell); err != nil {
			return LaunchResult{}, api.Errorf(api.CodeInvalidParams, "%w", err)
		}
	default:
		return LaunchResult{}, api.Errorf(api.CodeInvalidParams, "service or container is required")
	}
	term := ""
	if g := m.globalStore(); g != nil {
		term = settings.Get(g, SettingTerminal, "")
	}
	o := dk.TerminalOptions{GOOS: m.goos(), Terminal: term, Getenv: m.getenv, LookPath: m.opts.LookPath, TempDir: m.opts.TempDir, Env: c.Env}
	if o.LookPath == nil {
		o.LookPath = lookPath
	}
	launched, err := dk.TerminalCommand(o, argv)
	if err != nil {
		return LaunchResult{Command: argv}, api.Errorf(api.CodeUnavailable, "%w", err)
	}
	if err := m.launch(launched, c.Env); err != nil {
		return LaunchResult{Command: argv, Launched: launched}, api.Errorf(api.CodeFailed, "open terminal: %w", err)
	}
	return LaunchResult{Command: argv, Launched: launched}, nil
}

func (m *Module) openURL(_ context.Context, p URLParams) (LaunchResult, error) {
	argv, err := dk.BrowserCommand(m.goos(), p.URL)
	if err != nil {
		return LaunchResult{}, api.Errorf(api.CodeInvalidParams, "%w", err)
	}
	if err := m.launch(argv, nil); err != nil {
		return LaunchResult{Launched: argv}, api.Errorf(api.CodeFailed, "open browser: %w", err)
	}
	return LaunchResult{Launched: argv}, nil
}

// startDetached starts argv with env added to the process environment and
// does not wait for it.
func startDetached(argv, env []string) error {
	if len(argv) == 0 {
		return errors.New("docker: nothing to start")
	}
	cmd := exec.Command(argv[0], argv[1:]...)
	if len(env) > 0 {
		cmd.Env = dk.MergeEnv(os.Environ(), env)
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	go func() { _ = cmd.Wait() }()
	return nil
}

func lookPath(name string) (string, error) { return exec.LookPath(name) }
