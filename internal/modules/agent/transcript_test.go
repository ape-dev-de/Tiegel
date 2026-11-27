package agent

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/ape-dev-de/tiegel/internal/acp"
	"github.com/ape-dev-de/tiegel/internal/acp/acptest"
	"github.com/ape-dev-de/tiegel/internal/api"
	"github.com/ape-dev-de/tiegel/internal/core/bus"
	"github.com/ape-dev-de/tiegel/internal/core/module"
	"github.com/ape-dev-de/tiegel/internal/modules/modtest"
)

// reopen simulates a hibernation and wake: the App (with the module and
// the agent process) closes, a new App with a new module opens the same
// project with the same configuration. fake replaces the agent (nil keeps
// it, like opencode keeping its sessions on disk).
func reopen(t *testing.T, f *fixture, fake *acptest.Agent) *fixture {
	t.Helper()
	cfg := f.app.ConfigDir()
	if err := f.app.Close(); err != nil {
		t.Fatal(err)
	}
	if fake == nil {
		fake = f.fake
	}
	g := &fixture{t: t, fake: fake, root: f.root}
	g.mod = New(Options{
		LookPath: func(name string) (string, error) { return "/fake/bin/" + name, nil },
		Start: func(ctx context.Context, c acp.Command, h acp.Handler) (*acp.Process, error) {
			g.mu.Lock()
			g.cmds = append(g.cmds, c)
			g.mu.Unlock()
			return fake.Start(ctx, c, h)
		},
	})
	g.app = modtest.NewApp(t, []module.Module{g.mod}, func(o *api.Options) { o.ConfigDir = cfg })
	g.rec = modtest.Record(t, g.app, "agent.*")
	if _, err := g.app.OpenProject(context.Background(), f.root); err != nil {
		t.Fatal(err)
	}
	return g
}

// echoLast answers with the last prompt block (not the rules).
func echoLast(_ context.Context, t *acptest.Turn) string {
	t.Text("You said: " + t.Blocks[len(t.Blocks)-1].Text)
	return ""
}

func (f *fixture) transcriptFile() string {
	return filepath.Join(f.app.ProjectSettings().Dir(), transcriptFile)
}

func (f *fixture) started() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.cmds)
}

func (f *fixture) busy() []string {
	l, err := api.HookProjectBusy.Apply(context.Background(), f.app.Hooks(), nil)
	if err != nil {
		f.t.Fatal(err)
	}
	return l
}

// texts lists kind:text of the message and notice entries.
func texts(es []api.AgentEntry) []string {
	var out []string
	for _, e := range es {
		switch e.Kind {
		case api.EntryUser, api.EntryAgent, api.EntryNotice:
			out = append(out, e.Kind+":"+e.Text)
		}
	}
	return out
}

func TestBusyWhileTurnRuns(t *testing.T) {
	release := make(chan struct{})
	fake := &acptest.Agent{Script: echoLast}
	f := newFixture(t, fake, nil)
	if l := f.busy(); len(l) != 0 {
		t.Fatalf("busy before start: %v", l)
	}
	fake.Script = func(ctx context.Context, tr *acptest.Turn) string {
		tr.Text("working")
		select {
		case <-release:
		case <-ctx.Done():
			return acp.StopCancelled
		}
		return ""
	}
	f.call("agent.prompt", api.AgentPromptParams{Text: "work"}, nil)
	if l := f.busy(); !slices.Contains(l, "agent turn") {
		t.Fatalf("running turn not busy: %v", l)
	}
	close(release)
	f.rec.Wait(t, api.TopicAgentTurnEnded, func(bus.Event) bool { return true })
	if l := f.busy(); len(l) != 0 {
		t.Fatalf("idle session busy: %v", l)
	}

	// A permission request waiting for the user also blocks.
	fake.Script = func(ctx context.Context, tr *acptest.Turn) string {
		tr.Permission(ctx, acp.ToolCall{ToolCallID: "w", Name: "webfetch", Kind: acp.KindFetch, RawInput: []byte(`{"url":"https://x"}`)})
		return ""
	}
	f.call("agent.prompt", api.AgentPromptParams{Text: "fetch"}, nil)
	ev := f.rec.Wait(t, api.TopicAgentPermission, func(bus.Event) bool { return true })
	if l := f.busy(); !slices.Contains(l, "agent turn") {
		t.Fatalf("pending permission not busy: %v", l)
	}
	f.call("agent.permission", api.AgentPermissionParams{RequestID: ev.Payload.(api.AgentPermission).RequestID, Deny: true}, nil)
	modtest.Eventually(t, "idle", func() bool { return len(f.busy()) == 0 })
}

func TestTranscriptRestoredWithoutLoadSession(t *testing.T) {
	fake := &acptest.Agent{Script: echoLast}
	f := newFixture(t, fake, nil)
	f.prompt("hello", "")
	var st api.AgentStatus
	f.call("agent.setOption", api.AgentOptionParams{ID: "mode", Value: "plan"}, &st)
	f.prompt("second", "")
	stored := readTranscript(f.transcriptFile())
	if stored.SessionID != "ses_1" || stored.Model != "fake/small" || stored.Mode != "plan" || len(stored.Entries) != 4 {
		t.Fatalf("stored %+v", stored)
	}

	// Wake with an agent that lost the session (new process, no
	// session/load): the history shows at once, without starting it.
	g := reopen(t, f, &acptest.Agent{Script: echoLast})
	want := []string{"user:hello", "agent:You said: hello", "user:second", "agent:You said: second"}
	if got := texts(g.transcript()); !slices.Equal(got, want) {
		t.Fatalf("restored %q", got)
	}
	g.call("agent.status", nil, &st)
	if st.State != api.AgentStopped || g.started() != 0 {
		t.Fatalf("agent started on wake: %+v %d", st, g.started())
	}
	g.prompt("again", "")
	got := texts(g.transcript())
	want = append(want, "notice:"+NoticePreviousSession, "user:again", "agent:You said: again")
	if !slices.Equal(got, want) {
		t.Fatalf("after new session %q", got)
	}
	if n := len(g.fake.LoadSessions()); n != 0 {
		t.Fatalf("session/load without capability: %d", n)
	}
	// The new session gets the rules again and the stored mode.
	if p := g.fake.Prompts(); len(p) != 1 || !strings.Contains(p[0], "Tiegel") {
		t.Fatalf("prompts %q", p)
	}
	g.call("agent.status", nil, &st)
	for _, o := range st.Options {
		if o.ID == "mode" && o.Value != "plan" {
			t.Fatalf("mode not restored: %+v", o)
		}
	}
	if s := readTranscript(g.transcriptFile()); s.SessionID != "ses_1" || len(s.Entries) != 7 {
		t.Fatalf("stored after new session %+v", s)
	}
}

func TestTranscriptResumedWithLoadSession(t *testing.T) {
	fake := &acptest.Agent{LoadSession: true, Script: echoLast}
	f := newFixture(t, fake, nil)
	f.prompt("hello", "")
	g := reopen(t, f, nil)
	if got := texts(g.transcript()); len(got) != 2 {
		t.Fatalf("restored %q", got)
	}
	g.prompt("again", "")
	loads := fake.LoadSessions()
	if len(loads) != 1 || loads[0].SessionID != "ses_1" || loads[0].Cwd != f.root {
		t.Fatalf("loads %+v", loads)
	}
	if n := len(fake.NewSessions()); n != 1 {
		t.Fatalf("new session despite session/load: %d", n)
	}
	// The replay is not added again; no notice; the rules are not repeated.
	want := []string{"user:hello", "agent:You said: hello", "user:again", "agent:You said: again"}
	if got := texts(g.transcript()); !slices.Equal(got, want) {
		t.Fatalf("after load %q", got)
	}
	if p := fake.Prompts(); len(p) != 2 || strings.Contains(p[1], "Context from the IDE") {
		t.Fatalf("prompts %q", p)
	}
	var st api.AgentStatus
	g.call("agent.status", nil, &st)
	if st.SessionID != "ses_1" {
		t.Fatalf("%+v", st)
	}
}

func TestTranscriptLoadFailsFallsBack(t *testing.T) {
	f := newFixture(t, &acptest.Agent{LoadSession: true, Script: echoLast}, nil)
	f.prompt("hello", "")
	// The agent lost its sessions (e.g. its data was deleted).
	g := reopen(t, f, &acptest.Agent{LoadSession: true, Script: echoLast})
	g.prompt("again", "")
	if n := len(g.fake.LoadSessions()); n != 1 {
		t.Fatalf("loads %d", n)
	}
	got := texts(g.transcript())
	if len(got) != 5 || got[2] != "notice:"+NoticePreviousSession {
		t.Fatalf("%q", got)
	}
}

func TestNewSessionClearsTranscript(t *testing.T) {
	f := newFixture(t, &acptest.Agent{Script: echoLast}, nil)
	f.prompt("hello", "")
	if _, err := os.Stat(f.transcriptFile()); err != nil {
		t.Fatal(err)
	}
	f.call("agent.newSession", nil, nil)
	if _, err := os.Stat(f.transcriptFile()); !os.IsNotExist(err) {
		t.Fatalf("transcript kept: %v", err)
	}
	g := reopen(t, f, nil)
	if es := g.transcript(); len(es) != 0 {
		t.Fatalf("%+v", es)
	}
	g.prompt("fresh", "")
	if got := texts(g.transcript()); len(got) != 2 {
		t.Fatalf("notice without history: %q", got)
	}
}

func TestTranscriptFollowsProjectSwitch(t *testing.T) {
	f := newFixture(t, &acptest.Agent{Script: echoLast}, nil)
	f.prompt("hello", "")
	other := t.TempDir()
	if _, err := f.app.OpenProject(context.Background(), other); err != nil {
		t.Fatal(err)
	}
	var st api.AgentStatus
	modtest.Eventually(t, "other project", func() bool {
		f.call("agent.status", nil, &st)
		return len(f.transcript()) == 0 && st.State == api.AgentStopped
	})
	if _, err := f.app.OpenProject(context.Background(), f.root); err != nil {
		t.Fatal(err)
	}
	modtest.Eventually(t, "restored", func() bool { return len(texts(f.transcript())) == 2 })
}

func TestTranscriptLimits(t *testing.T) {
	m := New(Options{})
	for i := range maxStoredEntries + 20 {
		m.addEntry(api.AgentEntry{Kind: api.EntryUser, Text: strings.Repeat("x", i%3)})
	}
	m.entries[0].Permission = &api.AgentPermission{RequestID: "p1"}
	st := m.snapshotLocked()
	if len(st.Entries) != maxStoredEntries || st.Entries[0].Index != 20 {
		t.Fatalf("%d %d", len(st.Entries), st.Entries[0].Index)
	}
	path := filepath.Join(t.TempDir(), "s", transcriptFile)
	big := storedTranscript{SessionID: "s"}
	for i := range 10 {
		big.Entries = append(big.Entries, api.AgentEntry{Kind: api.EntryAgent, Text: strings.Repeat(string(rune('a'+i)), 400<<10)})
	}
	if err := storeTranscript(path, big); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil || info.Size() > maxStoredBytes {
		t.Fatalf("%v %v", info, err)
	}
	back := readTranscript(path)
	if n := len(back.Entries); n == 0 || n == 10 || back.Entries[n-1].Text[0] != 'j' {
		t.Fatalf("kept %d", n)
	}
	// Restored: indexes renumbered, open requests answered.
	m.restoreLocked(storedTranscript{Entries: []api.AgentEntry{{Index: 7, Kind: api.EntryPermission, Permission: &api.AgentPermission{RequestID: "p1"}}}})
	if e := m.entries[0]; e.Index != 0 || e.Permission.Answer != acp.OutcomeCancelled || !m.tr.restored {
		t.Fatalf("%+v", e)
	}
	if err := storeTranscript(path, storedTranscript{}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("empty transcript kept")
	}
	if st := readTranscript(filepath.Join(t.TempDir(), "missing")); len(st.Entries) != 0 {
		t.Fatal(st)
	}
	_ = os.WriteFile(path, []byte("{"), 0o600)
	if st := readTranscript(path); len(st.Entries) != 0 {
		t.Fatal(st)
	}
}
