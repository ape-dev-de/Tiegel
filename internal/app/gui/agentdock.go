package gui

import (
	"fmt"
	"net/url"
	"strings"

	"gioui.org/io/key"
	"gioui.org/layout"

	"github.com/ape-dev-de/tiegel/internal/api"
	"github.com/ape-dev-de/tiegel/internal/editor/keymap"
	"github.com/ape-dev-de/tiegel/internal/ui/atoms"
	"github.com/ape-dev-de/tiegel/internal/ui/fileicons"
	"github.com/ape-dev-de/tiegel/internal/ui/organisms/agentchat"
	"github.com/ape-dev-de/tiegel/internal/ui/tokens"
)

// AI agent sidebar (D540): the "Agent" tool window on the right shows
// the agent plugin's session (agent.*) with the agentchat organism; a
// header quick action opens it. The agent starts only when the user
// sends a message or picks a model, never with the window.

// Agent tool window and action IDs.
const (
	ToolAgent = "agent"

	ActAgentView keymap.Action = "view.agent"
	ActAgentOpen keymap.Action = "agent.open"
	agentOrder                 = 40
	// agentSettingsPage is the settings page of the agent plugin.
	agentSettingsPage = "plugin:agent"
)

type agentDock struct {
	v       *View
	chat    agentchat.Chat
	version uint64
	project string
	focus   bool
}

func installAgent(v *View) *agentDock {
	d := &agentDock{v: v}
	v.Registry.AddToolWindow(ToolWindow{
		ID: ToolAgent, Title: "Agent", Glyph: fileicons.ToolGlyph(fileicons.ToolAgent), Side: Right, Order: agentOrder,
		ActionID: ActAgentView,
		Badge: func() (string, atoms.BadgeKind) {
			if n := v.ctl.Agent.Status.Pending; n > 0 {
				return fmt.Sprint(n), atoms.BadgeWarning
			}
			if n := len(v.ctl.Agent.Changes); n > 0 {
				return fmt.Sprint(n), atoms.BadgeInfo
			}
			return "", 0
		},
		Layout: d.layout,
		OnOpen: func() {
			v.ctl.Agent.Load()
			d.focus = true
		},
	})
	v.Registry.AddAction(Action{ID: ActAgentOpen, Title: "AI Agent", Icon: string(fileicons.ToolAgent), Menu: "View", Section: "agent",
		Run: func(v *View) { v.ShowTool(ToolAgent) }})
	v.Registry.RegisterToolbarAction(ActAgentOpen)
	v.Registry.AddFrameHandler(d.frame)
	return d
}

// frame follows the project and shows the window when the agent asks.
func (d *agentDock) frame(v *View, _ layout.Context) {
	a := v.ctl.Agent
	if root := v.ctl.Project.Root; root != d.project {
		d.project = root
		a.ProjectChanged()
		if root != "" {
			a.Load()
		}
	}
	if a.Asked {
		a.Asked = false
		v.ShowTool(ToolAgent)
	}
}

// sync copies the controller state into the organism.
func (d *agentDock) sync() {
	a := d.v.ctl.Agent
	if a.Version == d.version && d.version != 0 {
		return
	}
	d.version = a.Version
	st := a.Status
	c := &d.chat
	c.Agent = strings.TrimSpace(st.Agent + " " + st.Version)
	if c.Agent == "" {
		c.Agent = "Agent"
	}
	c.Busy = st.State == api.AgentPrompting
	c.State = map[string]string{api.AgentStarting: "starting…", api.AgentReady: "ready", api.AgentPrompting: "working…", api.AgentFailed: "failed"}[st.State]
	c.Policy = st.Policy.Summary
	c.Models, c.Model = nil, st.Model
	for _, o := range st.Options {
		if o.ID == "model" || o.Category == "model" {
			for _, ch := range o.Choices {
				c.Models = append(c.Models, agentchat.Choice{Value: ch.Value, Name: ch.Name})
			}
		}
	}
	c.Notice = nil
	switch {
	case a.Disabled:
		c.Notice = &agentchat.NoticeBox{Title: "The agent plugin is disabled", Detail: "Enable it in Settings › Plugins."}
	case a.Loaded && !st.Available:
		n := &agentchat.NoticeBox{Error: true, Title: st.Command + " was not found", Detail: st.InstallHint}
		if st.Install != "" {
			n.Title = "opencode is not installed"
			n.Detail = "Install runs npm install -g opencode-ai – you see the command before it runs. Or install it as described on opencode.ai."
			n.Action = "Install…"
		}
		c.Notice = n
	case st.AuthRequired:
		c.Notice = &agentchat.NoticeBox{Error: true, Title: "The agent needs a login", Detail: st.AuthHint + "\nRun it in Tiegel's terminal, then send your message again."}
	case st.State == api.AgentFailed && st.Error != "":
		c.Notice = &agentchat.NoticeBox{Error: true, Title: "The agent stopped", Detail: st.Error}
	}
	c.Entries = c.Entries[:0]
	for _, e := range a.Entries {
		c.Entries = append(c.Entries, chatEntry(e))
	}
	c.Changes = c.Changes[:0]
	for _, ch := range a.Changes {
		c.Changes = append(c.Changes, agentchat.Change{Path: ch.Path, Kind: ch.Kind, Added: ch.Added, Removed: ch.Removed, Conflict: ch.Conflict})
	}
}

// chatEntry converts a transcript entry for the organism.
func chatEntry(e api.AgentEntry) agentchat.Entry {
	out := agentchat.Entry{Key: e.Index, Text: e.Text}
	switch e.Kind {
	case api.EntryUser:
		out.Kind = agentchat.User
		out.Context = chatContext(e.Context)
	case api.EntryAgent:
		out.Kind = agentchat.Agent
	case api.EntryThought:
		out.Kind = agentchat.Thought
	case api.EntryTool:
		out.Kind = agentchat.Tool
		t := e.Tool
		tc := &agentchat.ToolCall{Title: t.Title, Status: t.Status, Command: t.Command, Paths: t.Paths, Output: t.Output}
		for _, df := range t.Diffs {
			tc.Diffs = append(tc.Diffs, agentchat.DiffStat{Path: df.Path, Added: df.Added, Removed: df.Removed})
		}
		out.Tool = tc
	case api.EntryPlan:
		out.Kind = agentchat.Plan
		for _, p := range e.Plan {
			out.Plan = append(out.Plan, agentchat.PlanItem{Text: p.Content, Status: p.Status})
		}
	case api.EntryPermission:
		out.Kind = agentchat.Permission
		p := e.Permission
		r := &agentchat.Request{ID: p.RequestID, Summary: p.Summary, Answered: p.Answer != "", Allowed: p.Allowed}
		for _, o := range p.Options {
			r.Options = append(r.Options, agentchat.Option{ID: o.ID, Name: o.Name, Kind: o.Kind})
		}
		out.Perm = r
	default:
		out.Kind = agentchat.Notice
		switch e.Level {
		case api.NoticeBlocked:
			out.Level = agentchat.Blocked
		case api.NoticeError:
			out.Level = agentchat.Error
		}
	}
	return out
}

func (d *agentDock) layout(gtx layout.Context, th *tokens.Theme) layout.Dimensions {
	d.sync()
	a := d.v.ctl.Agent
	if d.focus {
		d.focus = false
		gtx.Execute(key.FocusCmd{Tag: d.chat.Input()})
	}
	for _, e := range d.chat.Update(gtx) {
		switch e := e.(type) {
		case agentchat.SendEvent:
			a.Prompt(e.Text)
		case agentchat.CancelEvent:
			a.Cancel()
		case agentchat.NewSessionEvent:
			a.NewSession()
		case agentchat.SettingsEvent:
			d.v.openSettings()
			d.v.settings.Selected = agentSettingsPage
		case agentchat.NoticeActionEvent:
			if a.Status.Install != "" {
				d.v.lspd.openInstall(a.Status.Install)
			}
		case agentchat.AnswerEvent:
			a.Answer(e.RequestID, e.OptionID)
		case agentchat.DiffEvent:
			a.OpenDiff(e.Path)
		case agentchat.OpenEvent:
			d.v.ctl.OpenFile(d.v.ctl.Abs(e.Path), nil)
		case agentchat.RevertEvent:
			a.Revert(e.Path)
		case agentchat.RevertAllEvent:
			a.Revert("")
		case agentchat.KeepAllEvent:
			a.KeepAll()
		case agentchat.ModelEvent:
			a.SetModel(e.Value)
		case agentchat.CopyEvent:
			d.v.CopyText(e.Text)
			d.v.ctl.Status.Notice = "Copied code block"
		case agentchat.LinkEvent:
			d.follow(e.Dest)
		case agentchat.OpenContextEvent:
			if e.File != "" {
				d.v.mdd.openAt(e.File, e.Anchor)
			}
		}
	}
	return d.chat.Layout(gtx, th)
}

// follow opens a link of an agent message: web links in the browser
// (only on click), project files in the editor.
func (d *agentDock) follow(dest string) {
	if u, err := url.Parse(dest); err == nil && u.Scheme != "" && u.Scheme != "file" {
		if s := strings.ToLower(u.Scheme); s == "http" || s == "https" {
			d.v.ctl.Terminals.OpenURL(dest)
		}
		return
	}
	d.v.ctl.OpenFile(d.v.ctl.Abs(strings.TrimPrefix(dest, "file://")), nil)
}

// chatContext converts the context of a user message (D571).
func chatContext(bs []api.AgentContextBlock) []agentchat.ContextBlock {
	var out []agentchat.ContextBlock
	for _, b := range bs {
		cb := agentchat.ContextBlock{Title: b.Title, Tokens: b.Tokens}
		for _, h := range b.Hits {
			ch := agentchat.ContextHit{Title: h.Title, Heading: h.Heading, Sources: h.Sources, Outdated: h.Outdated, File: h.File, Anchor: h.Anchor}
			if !h.GeneratedAt.IsZero() {
				ch.Generated = h.GeneratedAt.Local().Format("2006-01-02")
			}
			cb.Hits = append(cb.Hits, ch)
		}
		out = append(out, cb)
	}
	return out
}
