package controller

import (
	"context"
	"encoding/json"
	"strings"
	"time"
)

// Services tool window (AP14.3): compose projects of the open project with
// services, containers, state and ports; actions; logs; shells and ports.
// The window calls SetVisible: only while it is visible does the backend
// watch the engine (docker.watchStart/watchStop, D270). Everything else
// runs on a user action.

// DockerPort is a container port (docker.services).
type DockerPort struct {
	IP        string `json:"ip"`
	Published int    `json:"published"`
	Target    int    `json:"target"`
	Protocol  string `json:"protocol"`
	URL       string `json:"url"`
}

// DockerContainer is a container of a compose service.
type DockerContainer struct {
	ID       string       `json:"id"`
	Name     string       `json:"name"`
	State    string       `json:"state"`
	Status   string       `json:"status"`
	Health   string       `json:"health"`
	Image    string       `json:"image"`
	ExitCode int          `json:"exitCode"`
	Ports    []DockerPort `json:"ports"`
}

// DockerService is a compose service.
type DockerService struct {
	Name       string            `json:"name"`
	Image      string            `json:"image"`
	State      string            `json:"state"`
	Health     string            `json:"health"`
	Ports      []DockerPort      `json:"ports"`
	Containers []DockerContainer `json:"containers"`
	Defined    bool              `json:"defined"`
}

// DockerProject is a compose project of the open project.
type DockerProject struct {
	Name     string          `json:"name"`
	Dir      string          `json:"dir"`
	Files    []string        `json:"files"`
	Services []DockerService `json:"services"`
	Error    string          `json:"error"`
}

// DockerStandalone is a container outside the project's compose project.
type DockerStandalone struct {
	ID             string       `json:"id"`
	Name           string       `json:"name"`
	Image          string       `json:"image"`
	State          string       `json:"state"`
	Status         string       `json:"status"`
	Ports          []DockerPort `json:"ports"`
	ComposeProject string       `json:"composeProject"`
	ComposeService string       `json:"composeService"`
}

// DockerSnapshot is the result of docker.services (docker.servicesChanged).
type DockerSnapshot struct {
	Context    string             `json:"context"`
	Endpoint   string             `json:"endpoint"`
	Remote     bool               `json:"remote"`
	Projects   []DockerProject    `json:"projects"`
	Standalone []DockerStandalone `json:"standalone"`
	Error      string             `json:"error"`
	Refresh    string             `json:"refresh"`
}

// DockerContext is a Docker context (docker.contexts).
type DockerContext struct {
	Name     string `json:"name"`
	Provider string `json:"provider"`
	Endpoint string `json:"endpoint"`
	Remote   bool   `json:"remote"`
}

// DockerLogLine is a log line (docker.logs, docker.log).
type DockerLogLine struct {
	Container string `json:"container"`
	Text      string `json:"text"`
	Stream    string `json:"stream"`
}

// ServiceLogs is the log panel of the Services window.
type ServiceLogs struct {
	// Service or Container is shown; both empty means all services.
	Service, Container string
	// ID is the followed stream, empty when not following.
	ID string
	// Follow streams new lines; off shows the last lines once.
	Follow bool
	// Filter hides lines without the text (case-insensitive).
	Filter  string
	Lines   []DockerLogLine
	Loading bool
	Err     string
	// Open is true once a log was requested.
	Open bool
}

// Services is the state of the Services tool window.
type Services struct {
	c *Controller

	Snapshot DockerSnapshot
	Loaded   bool
	// Busy names a running action ("restart web"); Err the last error.
	Busy string
	Err  string
	// Output is the progress of the last action.
	Output []string
	// Contexts and Effective are the Docker contexts and the one in use.
	Contexts  []DockerContext
	Effective string
	// Standalone also lists containers outside the compose project.
	Standalone bool

	Logs ServiceLogs

	visible bool
	watchID string
	watchOp uint64 // invalidates late watchStart answers

	// Version changes with the snapshot, contexts, busy state;
	// LogVersion with the log lines.
	Version    uint64
	LogVersion uint64
}

// maxServiceLogLines bounds the log panel.
const maxServiceLogLines = 5000

func newServices(c *Controller) *Services { return &Services{c: c, Logs: ServiceLogs{Follow: true}} }

func (s *Services) changed() { s.Version++ }

// Visible reports whether the window is visible (SetVisible).
func (s *Services) Visible() bool { return s.visible }

// SetVisible starts the watch when the window becomes visible and stops
// it (and followed logs) when it is hidden.
func (s *Services) SetVisible(on bool) {
	if on == s.visible {
		return
	}
	s.visible = on
	if on {
		s.startWatch()
		s.LoadContexts()
		return
	}
	s.stopWatch()
	s.StopLogs()
}

func (s *Services) startWatch() {
	s.watchOp++
	op := s.watchOp
	standalone := s.Standalone
	s.c.Go(func(ctx context.Context) func() {
		var res struct {
			ID string `json:"id"`
		}
		err := s.c.call(ctx, "docker.watchStart", map[string]any{"standalone": standalone}, &res)
		return func() {
			if op != s.watchOp {
				// Hidden (or restarted) meanwhile: end the late watch.
				if err == nil {
					s.stopWatchID(res.ID)
				}
				return
			}
			if err != nil {
				s.Err = err.Error()
				s.changed()
				return
			}
			s.watchID = res.ID
		}
	})
}

func (s *Services) stopWatch() {
	s.watchOp++
	id := s.watchID
	s.watchID = ""
	s.stopWatchID(id)
}

func (s *Services) stopWatchID(id string) {
	if id == "" {
		return
	}
	s.c.Go(func(ctx context.Context) func() {
		_ = s.c.call(ctx, "docker.watchStop", map[string]any{"id": id}, nil)
		return nil
	})
}

// shutdown ends the watch and logs synchronously (window closes).
func (s *Services) shutdown() {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if s.watchID != "" {
		_ = s.c.call(ctx, "docker.watchStop", map[string]any{"id": s.watchID}, nil)
		s.watchID = ""
	}
	if s.Logs.ID != "" {
		_ = s.c.call(ctx, "docker.logsStop", map[string]any{"id": s.Logs.ID}, nil)
		s.Logs.ID = ""
	}
}

// SetStandalone shows or hides the other containers.
func (s *Services) SetStandalone(on bool) {
	if s.Standalone == on {
		return
	}
	s.Standalone = on
	s.changed()
	if s.visible {
		s.stopWatch()
		s.startWatch()
	}
}

// Refresh reloads the snapshot (toolbar button).
func (s *Services) Refresh() {
	standalone := s.Standalone
	s.c.Go(func(ctx context.Context) func() {
		var res DockerSnapshot
		err := s.c.call(ctx, "docker.services", map[string]any{"standalone": standalone}, &res)
		return func() {
			if err != nil {
				s.Err = err.Error()
				s.changed()
				return
			}
			s.apply(res)
		}
	})
}

func (s *Services) apply(res DockerSnapshot) {
	s.Snapshot, s.Loaded = res, true
	s.changed()
}

// DismissError clears the last error and action output.
func (s *Services) DismissError() {
	s.Err, s.Output = "", nil
	s.changed()
}

// LoadContexts reads the Docker contexts (local CLI configuration only).
func (s *Services) LoadContexts() {
	s.c.Go(func(ctx context.Context) func() {
		var res struct {
			Contexts  []DockerContext `json:"contexts"`
			Effective string          `json:"effective"`
		}
		err := s.c.call(ctx, "docker.contexts", nil, &res)
		return func() {
			if err != nil {
				return
			}
			s.Contexts, s.Effective = res.Contexts, res.Effective
			s.changed()
		}
	})
}

// SelectContext switches the project's Docker context.
func (s *Services) SelectContext(name string) {
	s.c.Go(func(ctx context.Context) func() {
		err := s.c.call(ctx, "docker.setContext", map[string]any{"name": name}, nil)
		return func() {
			if err != nil {
				s.Err = err.Error()
				s.changed()
				return
			}
			s.Effective = name
			s.changed()
			s.LoadContexts()
		}
	})
}

// Action runs up/start/stop/restart/down for the project (service and
// container empty), a service or a container. down needs confirm (the
// window asks first).
func (s *Services) Action(action, service, container string, confirm bool) {
	target := action
	switch {
	case container != "":
		target += " " + container
	case service != "":
		target += " " + service
	}
	s.Busy, s.Err, s.Output = target, "", nil
	s.changed()
	s.c.Go(func(ctx context.Context) func() {
		var res struct {
			Output []string `json:"output"`
		}
		err := s.c.call(ctx, "docker.serviceAction", map[string]any{
			"action": action, "service": service, "container": container, "confirm": confirm,
		}, &res)
		return func() {
			s.Busy, s.Output = "", res.Output
			if err != nil {
				s.Err = err.Error()
			}
			s.changed()
			if !s.visible || s.watchID == "" {
				s.Refresh()
			}
		}
	})
}

// ShowLogs shows the logs of a service, a container or (both empty) all
// services; with Follow on new lines stream in.
func (s *Services) ShowLogs(service, container string) {
	s.StopLogs()
	s.Logs.Service, s.Logs.Container = service, container
	s.Logs.Lines, s.Logs.Err, s.Logs.Loading, s.Logs.Open = nil, "", true, true
	s.LogVersion++
	follow := s.Logs.Follow
	s.c.Go(func(ctx context.Context) func() {
		var res struct {
			ID    string          `json:"id"`
			Lines []DockerLogLine `json:"lines"`
		}
		err := s.c.call(ctx, "docker.logs", map[string]any{"service": service, "container": container, "follow": follow}, &res)
		return func() {
			if s.Logs.Service != service || s.Logs.Container != container {
				if err == nil && res.ID != "" {
					s.stopLogID(res.ID)
				}
				return
			}
			s.Logs.Loading = false
			if err != nil {
				s.Logs.Err = err.Error()
			} else {
				s.Logs.ID = res.ID
				s.appendLogs(res.Lines)
			}
			s.LogVersion++
		}
	})
}

// SetFollow toggles following and reloads the log.
func (s *Services) SetFollow(on bool) {
	s.Logs.Follow = on
	if s.Logs.Open {
		s.ShowLogs(s.Logs.Service, s.Logs.Container)
	}
	s.LogVersion++
}

// SetLogFilter filters the shown lines.
func (s *Services) SetLogFilter(text string) {
	if s.Logs.Filter != text {
		s.Logs.Filter = text
		s.LogVersion++
	}
}

// VisibleLogs returns the lines that pass the filter.
func (s *Services) VisibleLogs() []DockerLogLine {
	f := strings.ToLower(strings.TrimSpace(s.Logs.Filter))
	if f == "" {
		return s.Logs.Lines
	}
	var out []DockerLogLine
	for _, l := range s.Logs.Lines {
		if strings.Contains(strings.ToLower(l.Container+" "+l.Text), f) {
			out = append(out, l)
		}
	}
	return out
}

// ClearLogs empties the panel (the stream keeps running).
func (s *Services) ClearLogs() {
	s.Logs.Lines = nil
	s.LogVersion++
}

// StopLogs ends a followed stream.
func (s *Services) StopLogs() {
	id := s.Logs.ID
	s.Logs.ID = ""
	s.stopLogID(id)
}

func (s *Services) stopLogID(id string) {
	if id == "" {
		return
	}
	s.c.Go(func(ctx context.Context) func() {
		_ = s.c.call(ctx, "docker.logsStop", map[string]any{"id": id}, nil)
		return nil
	})
}

func (s *Services) appendLogs(lines []DockerLogLine) {
	s.Logs.Lines = append(s.Logs.Lines, lines...)
	if n := len(s.Logs.Lines); n > maxServiceLogLines {
		s.Logs.Lines = append([]DockerLogLine(nil), s.Logs.Lines[n-maxServiceLogLines:]...)
	}
}

// OpenShell opens a shell in an external terminal (until the embedded
// terminal of M15.5 replaces it).
func (s *Services) OpenShell(service, container string) {
	s.c.Go(func(ctx context.Context) func() {
		var res struct {
			Command []string `json:"command"`
		}
		err := s.c.call(ctx, "docker.shell", map[string]any{"service": service, "container": container}, &res)
		return func() {
			if err != nil {
				s.Err = err.Error()
				s.changed()
				return
			}
			s.c.Status.Notice = "Opened a terminal: " + strings.Join(res.Command, " ")
		}
	})
}

// OpenURL opens a published port in the browser (on click only).
func (s *Services) OpenURL(url string) {
	s.c.Go(func(ctx context.Context) func() {
		err := s.c.call(ctx, "docker.openURL", map[string]any{"url": url}, nil)
		return func() { s.c.notice(err) }
	})
}

// handleEvent follows docker.* events.
func (s *Services) handleEvent(topic string, payload json.RawMessage) {
	switch topic {
	case "docker.servicesChanged":
		var res DockerSnapshot
		if s.visible && json.Unmarshal(payload, &res) == nil {
			s.apply(res)
		}
	case "docker.log":
		var ev struct {
			ID    string          `json:"id"`
			Lines []DockerLogLine `json:"lines"`
			Done  bool            `json:"done"`
			Error string          `json:"error"`
		}
		if json.Unmarshal(payload, &ev) != nil || ev.ID == "" || ev.ID != s.Logs.ID {
			return
		}
		s.appendLogs(ev.Lines)
		if ev.Done {
			s.Logs.ID = ""
			s.Logs.Err = ev.Error
		}
		s.LogVersion++
	case "docker.contextChanged":
		if s.visible {
			s.LoadContexts()
			s.Logs.ID = "" // the backend ended the stream
		}
	}
}
