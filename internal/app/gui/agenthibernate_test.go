package gui

import (
	"testing"

	"github.com/ape-dev-de/tiegel/internal/acp/acptest"
	"github.com/ape-dev-de/tiegel/internal/api"
	"github.com/ape-dev-de/tiegel/internal/modules"
	agentmod "github.com/ape-dev-de/tiegel/internal/modules/agent"
	"github.com/ape-dev-de/tiegel/internal/ui/organisms/agentchat"
	"github.com/ape-dev-de/tiegel/internal/ui/organisms/toolbar"
	"github.com/ape-dev-de/tiegel/internal/workspace"
)

// D573: the agent sidebar of a hibernated project comes back with its
// conversation (not only the changed files).
func TestAgentTranscriptSurvivesHibernate(t *testing.T) {
	fake := &acptest.Agent{Name: "OpenCode"}
	e := newWsEnvWith(t, "background", func(o *api.Options) {
		o.Extensions = []func(*api.App) error{modules.Installer(modules.Options{Agent: agentmod.Options{
			Start: fake.Start, LookPath: func(n string) (string, error) { return "/bin/" + n, nil }}})}
	})
	e.openBeta()
	beta := e.view()
	beta.ShowTool(ToolAgent)
	a := beta.ctl.Agent
	e.waitFor("agent status", func() bool { return a.Loaded && a.Status.Available })
	a.Prompt("hello beta")
	e.waitFor("turn end", func() bool {
		return a.Status.State == api.AgentReady && len(a.Entries) == 2 && a.Entries[1].Kind == api.EntryAgent
	})

	beta.hdr.handle(toolbar.HibernateProject{ID: e.rootB})
	e.waitFor("beta hibernated", func() bool { return e.state(e.rootB) == workspace.StateHibernated })
	e.view().hdr.handle(toolbar.SwitchProject{ID: e.rootB})
	e.waitFor("beta resumed", func() bool { return e.cur().Project.Root == e.rootB && e.view() != beta })
	v := e.view()
	v.ShowTool(ToolAgent)
	e.waitFor("old conversation", func() bool {
		var user, agent bool
		for _, en := range v.agtd.chat.Entries {
			user = user || en.Kind == agentchat.User && en.Text == "hello beta"
			agent = agent || en.Kind == agentchat.Agent
		}
		return user && agent
	})
	if st := v.ctl.Agent.Status.State; st != api.AgentStopped {
		t.Fatalf("agent started on wake: %s", st)
	}
	if n := len(fake.Inits()); n != 1 {
		t.Fatalf("agent processes: %d", n)
	}
}
