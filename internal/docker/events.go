package docker

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

// Event is a container event of `docker events --format json`.
type Event struct {
	Type   string `json:"type"`
	Action string `json:"action"`
	ID     string `json:"id"`
	Name   string `json:"name,omitempty"`
	// Project and Service come from the compose labels.
	Project string `json:"project,omitempty"`
	Service string `json:"service,omitempty"`
}

type rawEvent struct {
	Type   string `json:"Type"`
	Action string `json:"Action"`
	Status string `json:"status"`
	ID     string `json:"id"`
	Actor  struct {
		ID         string            `json:"ID"`
		Attributes map[string]string `json:"Attributes"`
	} `json:"Actor"`
}

// ParseEvent parses one line of `docker events --format json`.
func ParseEvent(line []byte) (Event, error) {
	var r rawEvent
	if err := json.Unmarshal(line, &r); err != nil {
		return Event{}, fmt.Errorf("docker: parse event: %w", err)
	}
	e := Event{Type: r.Type, Action: r.Action, ID: r.Actor.ID}
	if e.Action == "" {
		e.Action = r.Status
	}
	if e.ID == "" {
		e.ID = r.ID
	}
	if a := r.Actor.Attributes; a != nil {
		e.Name, e.Project, e.Service = a["name"], a[LabelComposeProject], a[LabelComposeService]
	}
	return e, nil
}

// Relevant reports whether the event can change what a container list
// shows (state, health, existence). exec_* (every `docker compose exec`,
// e.g. a PHP probe), attach, resize, top and copy events cannot.
func (e Event) Relevant() bool {
	a := strings.ToLower(e.Action)
	for _, p := range []string{"exec_", "attach", "detach", "resize", "top", "copy", "archive-path", "extract-to-dir", "export", "commit"} {
		if strings.HasPrefix(a, p) {
			return false
		}
	}
	return a != ""
}

// EventsArgs returns the arguments of `docker events` for containers,
// limited to the compose project when project is not empty.
func EventsArgs(project string) []string {
	args := []string{"events", "--format", "json", "--filter", "type=container"}
	if project != "" {
		args = append(args, "--filter", "label="+LabelComposeProject+"="+project)
	}
	return args
}

// Events streams container events (see EventsArgs) to onEvent until ctx
// is cancelled or docker exits; unparsable lines are skipped. It contacts
// the engine and keeps the connection open: run it only while the user
// looks at the result (Services window).
func (c Client) Events(ctx context.Context, project string, onEvent func(Event)) error {
	cmd := c.Command(ctx, EventsArgs(project)...)
	err := Stream(cmd, func(l Line) {
		if l.Stream != "stdout" || !strings.HasPrefix(strings.TrimSpace(l.Text), "{") {
			return
		}
		if e, perr := ParseEvent([]byte(l.Text)); perr == nil && onEvent != nil {
			onEvent(e)
		}
	})
	if ctx.Err() != nil {
		return ctx.Err()
	}
	return err
}
