package gui

import (
	"fmt"
	"net/url"
	"strings"
	"time"

	"gioui.org/io/key"
	"gioui.org/layout"

	"github.com/ape-dev-de/tiegel/internal/api"
	"github.com/ape-dev-de/tiegel/internal/editor/keymap"
	"github.com/ape-dev-de/tiegel/internal/ui/atoms"
	"github.com/ape-dev-de/tiegel/internal/ui/fileicons"
	"github.com/ape-dev-de/tiegel/internal/ui/molecules"
	"github.com/ape-dev-de/tiegel/internal/ui/organisms/agentchat"
	"github.com/ape-dev-de/tiegel/internal/ui/organisms/planview"
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

	// Plan mode (D567): the plan view or a role's transcript replace
	// the conversation while the checkbox is on and a plan exists.
	plan        planview.View
	roleChat    agentchat.Chat
	back        atoms.Button
	planVersion uint64
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
			v.ctl.Plan.Load()
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
		v.ctl.Plan.ProjectChanged()
		if root != "" {
			a.Load()
			v.ctl.Plan.Load()
		}
	}
	if a.Asked {
		a.Asked = false
		v.ShowTool(ToolAgent)
	}
}

// sync copies the controller state into the organism.
func (d *agentDock) sync() {
	d.syncPlan()
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
	if d.focus {
		d.focus = false
		gtx.Execute(key.FocusCmd{Tag: d.chat.Input()})
	}
	for _, e := range d.chat.Update(gtx) {
		d.layoutEvent(e)
	}
	d.planEvents(gtx)
	return d.chat.Layout(gtx, th)
}

// layoutEvent handles an event of the chat organism.
func (d *agentDock) layoutEvent(e agentchat.Event) {
	a := d.v.ctl.Agent
	{
		switch e := e.(type) {
		case agentchat.SendEvent:
			if e.Plan {
				d.v.ctl.Plan.Start(e.Text)
				break
			}
			a.Prompt(e.Text)
		case agentchat.PlanModeEvent:
			d.v.ctl.Plan.Mode = e.On
			d.v.ctl.Plan.Version++
			if e.On {
				d.v.ctl.Plan.Load()
			}
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
		}
	}
}

// syncPlan copies the plan state into the plan view.
func (d *agentDock) syncPlan() {
	p := d.v.ctl.Plan
	d.chat.PlanAvailable, d.chat.PlanMode = p.Available, p.Mode
	d.chat.Body = nil
	if p.Mode && p.Status.ID != "" {
		d.chat.Body = d.planBody
	}
	if p.Version == d.planVersion && d.planVersion != 0 {
		return
	}
	d.planVersion = p.Version
	st, v := p.Status, &d.plan
	v.Title, v.State, v.Phase, v.Running, v.Iteration, v.Error = st.Title, st.State, st.Phase, st.Running, st.Iteration, st.Error
	v.Notes = st.Notes
	v.Roles = v.Roles[:0]
	for _, r := range st.Roles {
		v.Roles = append(v.Roles, planview.Role{Name: r.Role, Title: roleTitle(r.Role), State: r.State, Task: r.Task, Elapsed: elapsed(r)})
	}
	v.Summary = p.Summary
	v.Gates = v.Gates[:0]
	for _, g := range st.Gates {
		state, text := "passed", g.Summary
		switch {
		case g.Skipped != "":
			state, text = "skipped", g.Skipped
		case !g.Passed:
			state = "failed"
		}
		v.Gates = append(v.Gates, planview.Gate{Name: g.Name, State: state, Text: text})
	}
	v.Disputes = v.Disputes[:0]
	for _, it := range st.Disputes {
		dp := planview.Dispute{ID: it.ID, Claim: it.Claim, Evidence: it.Evidence}
		if it.Suggestion != "" {
			dp.Positions = append(dp.Positions, planview.Position{Role: it.From, Text: it.Suggestion})
		}
		for _, r := range it.Responses {
			dp.Positions = append(dp.Positions, planview.Position{Role: r.Role, Text: strings.TrimSpace(r.Verdict + ": " + r.Text)})
		}
		v.Disputes = append(v.Disputes, dp)
	}
	v.Changes = v.Changes[:0]
	for _, c := range p.Changes {
		v.Changes = append(v.Changes, planview.Change{Path: c.Path, Added: c.Added, Removed: c.Removed})
	}
	v.Workspace, v.Worktree = "", false
	if w := st.Workspace; w != nil {
		v.Worktree = w.Mode == "worktree"
		v.Workspace = "Agents work in place in the project (changes are tracked and can be reverted)."
		if v.Worktree {
			v.Workspace = "Worktree " + w.Dir + " on branch " + w.Branch
		}
		if w.Note != "" {
			v.Workspace += " – " + w.Note
		}
	}
	d.roleChat.Entries = d.roleChat.Entries[:0]
	for _, e := range p.Transcript {
		d.roleChat.Entries = append(d.roleChat.Entries, chatEntry(e))
	}
	d.roleChat.Empty = "No transcript yet for this role."
}

// planBody is the plan view or a role's transcript.
func (d *agentDock) planBody(gtx layout.Context) layout.Dimensions {
	th := d.v.Theme()
	p := d.v.ctl.Plan
	if p.Role == "" {
		return d.plan.Layout(gtx, th)
	}
	if d.back.Text == "" {
		d.back = atoms.Button{Text: "Plan", Kind: atoms.Ghost, Glyph: atoms.GlyphArrowLeft}
	}
	return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return molecules.SectionHeader{Title: roleTitle(p.Role), Trailing: func(gtx layout.Context) layout.Dimensions { return d.back.Layout(gtx, th) }}.Layout(gtx, th)
		}),
		layout.Flexed(1, func(gtx layout.Context) layout.Dimensions { return d.roleChat.LayoutTranscript(gtx, th) }),
	)
}

// planEvents turns the plan view's events into plan.* calls.
func (d *agentDock) planEvents(gtx layout.Context) {
	p := d.v.ctl.Plan
	if d.back.Clicked(gtx) {
		p.ShowRole("")
	}
	for _, e := range d.roleChat.Update(gtx) {
		switch e := e.(type) {
		case agentchat.LinkEvent:
			d.follow(e.Dest)
		case agentchat.CopyEvent:
			d.v.CopyText(e.Text)
		}
	}
	for _, e := range d.plan.Update(gtx) {
		switch e := e.(type) {
		case planview.AcceptEvent:
			p.Accept()
		case planview.ChangesEvent:
			p.RequestChanges(e.Comment)
		case planview.DiscardEvent:
			p.Discard()
		case planview.DocumentEvent:
			p.OpenDocument()
		case planview.RoleEvent:
			p.ShowRole(e.Role)
		case planview.ResolveEvent:
			p.Resolve(e.Item, e.Decision, e.Comment)
		case planview.ApplyEvent:
			p.Apply()
		case planview.KeepEvent:
			p.Keep()
		case planview.CancelEvent:
			p.Cancel()
		case planview.ResumeEvent:
			p.Resume()
		case planview.DiffEvent:
			p.OpenDiff(e.Path)
		case planview.CloseEvent:
			p.Mode = false
			p.Version++
		}
	}
}

// roleTitle is the label of a plan role.
func roleTitle(r string) string {
	switch r {
	case api.RolePlannerArch:
		return "Planner · architecture"
	case api.RolePlannerCode:
		return "Planner · code"
	case api.RolePlannerTests:
		return "Planner · tests"
	case api.RoleValidator:
		return "Validator"
	case api.RoleQA:
		return "QA"
	case "qa-review":
		return "QA · plan review"
	case api.RoleDev:
		return "DEV"
	case api.RoleCritic:
		return "Critic"
	case api.RoleMediator:
		return "Mediator"
	case api.RoleSummarizer:
		return "Summary"
	}
	return r
}

// elapsed formats the time of a role's running or last turn.
func elapsed(r api.PlanRole) string {
	ms := r.ElapsedMs
	if r.State == "running" && !r.Started.IsZero() {
		ms = time.Since(r.Started).Milliseconds()
	}
	if ms <= 0 {
		return ""
	}
	s := ms / 1000
	if s == 0 {
		return "<1s"
	}
	if s < 60 {
		return fmt.Sprintf("%ds", s)
	}
	return fmt.Sprintf("%dm%02ds", s/60, s%60)
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
