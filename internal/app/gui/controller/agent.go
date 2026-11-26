package controller

import (
	"context"
	"encoding/json"

	"github.com/ape-dev-de/tiegel/internal/api"
)

// Agent state of the window (D540): status, transcript, changed files
// of the AI agent plugin (agent.*), kept up to date by its events.
type Agent struct {
	c *Controller
	// Status is the agent plugin's status (Loaded tells whether it was
	// fetched); Disabled is set while the plugin does not run.
	Status   api.AgentStatus
	Loaded   bool
	Disabled bool
	Entries  []api.AgentEntry
	Changes  []api.AgentChange
	// Version changes with every update (the view rebuilds its data).
	Version uint64
	// Asked is set when a permission request arrived (the view shows
	// the tool window); the view clears it.
	Asked bool

	project string
}

func newAgent(c *Controller) *Agent { return &Agent{c: c} }

// Load fetches status, transcript and changes.
func (a *Agent) Load() {
	if a.c.Project.Root == "" {
		return
	}
	a.c.Go(func(ctx context.Context) func() {
		var st api.AgentStatus
		err := a.c.call(ctx, "agent.status", nil, &st)
		var es []api.AgentEntry
		var cs []api.AgentChange
		if err == nil {
			_ = a.c.call(ctx, "agent.transcript", nil, &es)
			_ = a.c.call(ctx, "agent.changes", nil, &cs)
		}
		return func() {
			a.Loaded = true
			a.Disabled = disabledErr(err)
			if err == nil {
				a.Status, a.Entries, a.Changes = st, es, cs
			}
			a.Version++
		}
	})
}

// ProjectChanged forgets the state of the previous project.
func (a *Agent) ProjectChanged() {
	if a.c.Project.Root == a.project {
		return
	}
	a.project = a.c.Project.Root
	a.Status, a.Entries, a.Changes, a.Loaded = api.AgentStatus{}, nil, nil, false
	a.Version++
}

// callStatus runs an agent method that returns the status.
func (a *Agent) callStatus(method string, params any) {
	a.c.Go(func(ctx context.Context) func() {
		var st api.AgentStatus
		err := a.c.call(ctx, method, params, &st)
		return func() {
			if err != nil {
				a.c.Status.Notice = "Agent: " + err.Error()
			}
			if st.State != "" {
				a.Status = st
			}
			a.Version++
		}
	})
}

// Prompt sends a message (starts the agent if needed).
func (a *Agent) Prompt(text string) {
	a.c.Go(func(ctx context.Context) func() {
		var r api.AgentTurnResult
		err := a.c.call(ctx, "agent.prompt", api.AgentPromptParams{Text: text}, &r)
		return func() {
			if err != nil {
				a.c.Status.Notice = "Agent: " + err.Error()
				a.Load()
			}
		}
	})
}

// Start starts the agent without a message.
func (a *Agent) Start() { a.callStatus("agent.start", nil) }

// Cancel cancels the running turn.
func (a *Agent) Cancel() { a.callStatus("agent.cancel", nil) }

// NewSession starts a new session.
func (a *Agent) NewSession() { a.callStatus("agent.newSession", nil) }

// Stop ends the agent process.
func (a *Agent) Stop() { a.callStatus("agent.stop", nil) }

// SetModel chooses the model.
func (a *Agent) SetModel(model string) {
	a.callStatus("agent.setModel", api.AgentModelParams{Model: model})
}

// Answer answers a permission request.
func (a *Agent) Answer(requestID, optionID string) {
	a.callStatus("agent.permission", api.AgentPermissionParams{RequestID: requestID, OptionID: optionID})
}

// Revert reverts one file (path) or all (path "").
func (a *Agent) Revert(path string) {
	a.c.Go(func(ctx context.Context) func() {
		var r api.AgentRevertResult
		err := a.c.call(ctx, "agent.revert", api.AgentRevertParams{Path: path, All: path == ""}, &r)
		return func() {
			switch {
			case err != nil:
				a.c.Status.Notice = "Revert: " + err.Error()
			case len(r.Conflicts) > 0:
				a.c.Status.Notice = "Not reverted: " + r.Conflicts[0].Path + " " + r.Conflicts[0].Reason
			}
		}
	})
}

// KeepAll accepts all changes.
func (a *Agent) KeepAll() {
	a.c.Go(func(ctx context.Context) func() {
		err := a.c.call(ctx, "agent.acceptChanges", nil, nil)
		return func() {
			if err != nil {
				a.c.Status.Notice = "Agent: " + err.Error()
			}
		}
	})
}

// OpenDiff shows a changed file before the agent's first change and now
// in the diff window.
func (a *Agent) OpenDiff(path string) {
	a.c.Go(func(ctx context.Context) func() {
		var d api.AgentDiff
		err := a.c.call(ctx, "agent.diff", api.AgentPathParams{Path: path}, &d)
		return func() {
			if err != nil {
				a.c.Status.Notice = "Diff: " + err.Error()
				return
			}
			a.c.Work.OpenDiff(DiffSpec{Path: d.Path, Title: d.Path + " (agent changes)",
				Texts: &DiffTexts{Left: d.Original, Right: d.Current, LeftLabel: "Before the agent", RightLabel: "Now", Key: "agent:" + d.Path}})
		}
	})
}

// handleEvent applies an agent.* event.
func (a *Agent) handleEvent(topic string, payload json.RawMessage) {
	switch topic {
	case api.TopicAgentStatus:
		var st api.AgentStatus
		if json.Unmarshal(payload, &st) == nil {
			a.Status, a.Loaded = st, true
		}
	case api.TopicAgentUpdate:
		var u api.AgentUpdateEvent
		if json.Unmarshal(payload, &u) != nil {
			return
		}
		i := u.Entry.Index
		switch {
		case i < 0:
			a.Entries = nil // new session
		case i < len(a.Entries):
			a.Entries[i] = u.Entry
		case i == len(a.Entries):
			a.Entries = append(a.Entries, u.Entry)
		default:
			a.Load() // missed updates
		}
	case api.TopicAgentPermission:
		a.Asked = true
	case api.TopicAgentChanges:
		var cs []api.AgentChange
		if json.Unmarshal(payload, &cs) == nil {
			a.Changes = cs
		}
	}
	a.Version++
}
