package agent

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ape-dev-de/tiegel/internal/acp"
	"github.com/ape-dev-de/tiegel/internal/acp/acptest"
	"github.com/ape-dev-de/tiegel/internal/api"
	"github.com/ape-dev-de/tiegel/internal/core/bus"
	"github.com/ape-dev-de/tiegel/internal/core/module"
	"github.com/ape-dev-de/tiegel/internal/modules/modtest"
)

// fixture is an App with the agent module connected to a fake agent.
type fixture struct {
	t    *testing.T
	app  *api.App
	root string
	fake *acptest.Agent
	mod  *Module
	rec  *modtest.Recorder

	mu   sync.Mutex
	cmds []acp.Command
}

func newFixture(t *testing.T, fake *acptest.Agent, files map[string]string) *fixture {
	t.Helper()
	f := &fixture{t: t, fake: fake}
	f.mod = New(Options{
		LookPath: func(name string) (string, error) { return "/fake/bin/" + name, nil },
		Start: func(ctx context.Context, c acp.Command, h acp.Handler) (*acp.Process, error) {
			f.mu.Lock()
			f.cmds = append(f.cmds, c)
			f.mu.Unlock()
			return fake.Start(ctx, c, h)
		},
	})
	f.app = modtest.NewApp(t, []module.Module{f.mod}, nil)
	f.rec = modtest.Record(t, f.app, "agent.*")
	if files == nil {
		files = map[string]string{"a.txt": "line a\nline b\n"}
	}
	f.root = modtest.Project(t, f.app, files)
	return f
}

func (f *fixture) call(method string, params, out any) {
	f.t.Helper()
	modtest.Call(f.t, f.app, method, params, out)
}

func (f *fixture) set(key string, v any) {
	f.t.Helper()
	raw, _ := json.Marshal(v)
	f.call("settings.set", api.SettingsSetParams{Key: key, Scope: "global", Value: raw}, nil)
}

func (f *fixture) prompt(text, ask string) api.AgentTurnResult {
	f.t.Helper()
	var r api.AgentTurnResult
	f.call("agent.prompt", api.AgentPromptParams{Text: text, Wait: true, Ask: ask}, &r)
	return r
}

func (f *fixture) transcript() []api.AgentEntry {
	var es []api.AgentEntry
	f.call("agent.transcript", nil, &es)
	return es
}

func (f *fixture) changes() []api.AgentChange {
	var cs []api.AgentChange
	f.call("agent.changes", nil, &cs)
	return cs
}

func (f *fixture) read(rel string) (string, bool) {
	b, err := os.ReadFile(filepath.Join(f.root, rel))
	return string(b), err == nil
}

// editScript behaves like opencode: it asks for the edit permission with
// a diff, then writes the file itself and reports the completed call.
func editScript(path, newText string) acptest.Script {
	return editWith(path, func(string) string { return newText })
}

// editWith is editScript computing the new text from the file content
// after the permission was given.
func editWith(path string, change func(old string) string) acptest.Script {
	return func(ctx context.Context, t *acptest.Turn) string {
		newText := change("")
		abs := path
		old, err := os.ReadFile(abs)
		var oldp *string
		if err == nil {
			oldp = acptest.Str(string(old))
		}
		tc := acp.ToolCall{ToolCallID: "e1", Name: "edit", Title: "edit", Kind: acp.KindEdit, Status: acp.StatusPending,
			Locations: []acp.Location{{Path: abs}}, RawInput: json.RawMessage(`{"path":"` + abs + `"}`),
			Content: []acp.ToolCallContent{{Type: "diff", Path: abs, OldText: oldp, NewText: acptest.Str(newText)}}}
		t.Tool(false, acp.ToolCall{ToolCallID: "e1", Name: "edit", Title: "edit", Kind: acp.KindEdit, Status: acp.StatusPending})
		opt := t.Permission(ctx, tc)
		if opt != "once" && opt != "always" {
			t.Tool(true, acp.ToolCall{ToolCallID: "e1", Status: acp.StatusFailed})
			t.Text("edit rejected")
			return ""
		}
		if cur, err := os.ReadFile(abs); err == nil {
			newText = change(string(cur))
		}
		_ = os.WriteFile(abs, []byte(newText), 0o644)
		t.Tool(true, acp.ToolCall{ToolCallID: "e1", Status: acp.StatusCompleted, Content: []acp.ToolCallContent{
			{Type: "content", Content: &acp.ContentBlock{Type: "text", Text: "Edited"}},
			{Type: "diff", Path: abs, OldText: oldp, NewText: acptest.Str(newText)}}})
		t.Text("done")
		return ""
	}
}

func TestStatusInstallAndPolicy(t *testing.T) {
	m := New(Options{LookPath: func(string) (string, error) { return "", errors.New("missing") }})
	app := modtest.NewApp(t, []module.Module{m}, nil)
	modtest.Project(t, app, map[string]string{"x": "y"})
	var st api.AgentStatus
	modtest.Call(t, app, "agent.status", nil, &st)
	if st.Available || st.Install != ID || st.Command != DefaultCommand || st.State != api.AgentStopped {
		t.Fatalf("%+v", st)
	}
	if st.Policy.Summary != "Edits: allowed · Shell: ask · Commits: blocked · Push: blocked · Web: ask" {
		t.Fatal(st.Policy.Summary)
	}
	// Not found: the start fails with a hint, nothing runs.
	if err := modtest.WantCode(t, app, "agent.start", nil, api.CodeNotFound); !strings.Contains(err.Error(), "install") {
		t.Fatal(err)
	}
	var plan struct {
		Display string `json:"display"`
		Tool    string `json:"tool"`
	}
	if err := modtest.TryCall(app, "install.plan", map[string]string{"plugin": ID}, &plan); err != nil || plan.Tool != "opencode" {
		t.Fatalf("plan %v %+v", err, plan)
	}
	raw, _ := json.Marshal("my-agent --acp")
	modtest.Call(t, app, "settings.set", api.SettingsSetParams{Key: SettingCommand, Scope: "global", Value: raw}, nil)
	modtest.Call(t, app, "agent.status", nil, &st)
	if st.Install != "" || !strings.Contains(st.InstallHint, "my-agent") {
		t.Fatalf("%+v", st)
	}
	if s, ti := m.SettingsPage(); s != "AI" || ti != "Agent" {
		t.Fatal(s, ti)
	}
}

func TestPromptStreamsAndRules(t *testing.T) {
	fake := &acptest.Agent{Script: func(ctx context.Context, t *acptest.Turn) string {
		t.Thought("hmm")
		t.Text("Hello ")
		t.Text("**world**")
		t.Plan(acp.PlanEntry{Content: "first", Status: "completed"}, acp.PlanEntry{Content: "second", Status: "pending"})
		t.Update(acp.UpdateCommands, map[string]any{"availableCommands": []acp.SlashCommand{{Name: "init"}}})
		return ""
	}}
	f := newFixture(t, fake, nil)
	f.set(SettingRules, "Never touch vendor/.")
	r := f.prompt("hi there", "")
	if r.StopReason != acp.StopEndTurn {
		t.Fatalf("%+v", r)
	}
	f.prompt("again", "")
	ps := fake.Prompts()
	if len(ps) != 2 || !strings.Contains(ps[0], "Never touch vendor/.") || !strings.Contains(ps[0], "git commits") || !strings.HasSuffix(ps[0], "hi there") || ps[1] != "again" {
		t.Fatalf("prompts %q", ps)
	}
	es := f.transcript()
	kinds := []string{}
	for _, e := range es {
		kinds = append(kinds, e.Kind)
	}
	if strings.Join(kinds, ",") != "user,thought,agent,plan,user,thought,agent,plan" {
		t.Fatal(kinds)
	}
	if es[0].Text != "hi there" || es[2].Text != "Hello **world**" || len(es[3].Plan) != 2 || es[1].Text != "hmm" {
		t.Fatalf("%+v", es[:4])
	}
	var st api.AgentStatus
	f.call("agent.status", nil, &st)
	if st.State != api.AgentReady || st.Agent != "Fake Agent" || st.Model != "fake/small" || len(st.Options) != 2 || len(st.Commands) != 1 {
		t.Fatalf("%+v", st)
	}
	// Streaming events carry deltas.
	var deltas []string
	for _, ev := range f.rec.Events(api.TopicAgentUpdate) {
		if u := ev.Payload.(api.AgentUpdateEvent); u.Delta != "" {
			deltas = append(deltas, u.Delta)
		}
	}
	if !strings.Contains(strings.Join(deltas, "|"), "Hello |**world**") {
		t.Fatal(deltas)
	}
	f.mu.Lock()
	cmd := f.cmds[0]
	f.mu.Unlock()
	if cmd.Path != "/fake/bin/opencode" || strings.Join(cmd.Args, " ") != "acp" || cmd.Dir != f.root {
		t.Fatalf("%+v", cmd)
	}
	cfg := ""
	for _, e := range cmd.Env {
		if v, ok := strings.CutPrefix(e, "OPENCODE_CONFIG_CONTENT="); ok {
			cfg = v
		}
	}
	if !strings.Contains(cfg, `"git commit*":"deny"`) {
		t.Fatal(cfg)
	}
	// A new session clears the transcript and sends the rules again.
	f.call("agent.newSession", nil, &st)
	if len(f.transcript()) != 0 || st.SessionID != "ses_2" {
		t.Fatalf("%+v", st)
	}
	f.prompt("third", "")
	if ps := fake.Prompts(); !strings.Contains(ps[2], "Never touch vendor/.") {
		t.Fatal(ps[2])
	}
	modtest.WantCode(t, f.app, "agent.prompt", api.AgentPromptParams{Text: "  "}, api.CodeInvalidParams)
	modtest.WantCode(t, f.app, "agent.prompt", api.AgentPromptParams{Text: "x", Ask: "maybe"}, api.CodeInvalidParams)
	f.call("agent.stop", nil, &st)
	if st.State != api.AgentStopped {
		t.Fatal(st.State)
	}
}

func TestEditTrackedAndReverted(t *testing.T) {
	fake := &acptest.Agent{}
	f := newFixture(t, fake, map[string]string{"a.txt": "line a\nline b\n", "keep.txt": "k\n"})
	abs := filepath.Join(f.root, "a.txt")
	fake.Script = editScript(abs, "line A\nline b\n")
	// The file is open with unsaved edits: they are saved before the
	// agent edits, so the agent works on what the user sees.
	f.call("doc.open", api.DocOpenParams{Path: "a.txt"}, nil)
	f.call("doc.insert", api.DocInsertParams{Path: "a.txt", At: api.Pos{Line: 3, Col: 1}, Text: "user\n"}, nil)
	fake.Script = editWith(abs, func(old string) string { return strings.Replace(old, "line a", "line A", 1) })
	f.prompt("change it", "")
	if got, _ := f.read("a.txt"); got != "line A\nline b\nuser\n" {
		t.Fatalf("disk %q", got)
	}
	// The open document follows the agent's write.
	var txt api.DocText
	f.call("doc.text", api.DocTextParams{Path: "a.txt"}, &txt)
	if txt.Text != "line A\nline b\nuser\n" {
		t.Fatalf("doc %q", txt.Text)
	}
	cs := f.changes()
	if len(cs) != 1 || cs[0].Path != "a.txt" || cs[0].Kind != api.ChangeModified || cs[0].Added != 1 || cs[0].Removed != 1 || cs[0].Conflict {
		t.Fatalf("%+v", cs)
	}
	var d api.AgentDiff
	f.call("agent.diff", api.AgentPathParams{Path: "a.txt"}, &d)
	if d.Original != "line a\nline b\nuser\n" || !strings.Contains(d.Unified, "-line a\n+line A") {
		t.Fatalf("%+v", d)
	}
	// The tool entry shows the diff.
	var tool *api.AgentToolCall
	for _, e := range f.transcript() {
		if e.Kind == api.EntryTool {
			tool = e.Tool
		}
	}
	if tool == nil || tool.Status != acp.StatusCompleted || len(tool.Diffs) != 1 || tool.Diffs[0].Added == 0 || tool.Output != "Edited" {
		t.Fatalf("%+v", tool)
	}
	var hist []struct {
		Label string `json:"label"`
	}
	f.call("history.list", api.PathParams{Path: "a.txt"}, &hist)
	if len(hist) < 2 || !strings.HasPrefix(hist[0].Label, "agent:") {
		t.Fatalf("history %+v", hist)
	}
	var rr api.AgentRevertResult
	f.call("agent.revert", api.AgentRevertParams{Path: "a.txt"}, &rr)
	if len(rr.Reverted) != 1 || len(rr.Conflicts) != 0 {
		t.Fatalf("%+v", rr)
	}
	if got, _ := f.read("a.txt"); got != "line a\nline b\nuser\n" {
		t.Fatalf("reverted %q", got)
	}
	f.call("doc.text", api.DocTextParams{Path: "a.txt"}, &txt)
	if txt.Text != "line a\nline b\nuser\n" {
		t.Fatalf("doc after revert %q", txt.Text)
	}
	if len(f.changes()) != 0 {
		t.Fatal("change still listed")
	}
	modtest.WantCode(t, f.app, "agent.revert", api.AgentRevertParams{Path: "a.txt"}, api.CodeNotFound)
	modtest.WantCode(t, f.app, "agent.diff", api.AgentPathParams{}, api.CodeInvalidParams)
	f.rec.Wait(t, api.TopicAgentChanges, func(bus.Event) bool { return true })
}

func TestCreatedFileAndConflict(t *testing.T) {
	fake := &acptest.Agent{}
	f := newFixture(t, fake, nil)
	created := filepath.Join(f.root, "new.txt")
	modified := filepath.Join(f.root, "a.txt")
	fake.Script = func(ctx context.Context, t *acptest.Turn) string {
		editScript(created, "hello\n")(ctx, t)
		return editScript(modified, "changed\n")(ctx, t)
	}
	f.prompt("go", "")
	cs := f.changes()
	if len(cs) != 2 || cs[0].Kind != api.ChangeCreated || cs[1].Kind != api.ChangeModified {
		t.Fatalf("%+v", cs)
	}
	// The user edits the file afterwards: revert refuses without force.
	if err := os.WriteFile(modified, []byte("user edit\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	var rr api.AgentRevertResult
	f.call("agent.revert", api.AgentRevertParams{All: true}, &rr)
	if len(rr.Reverted) != 1 || rr.Reverted[0] != "new.txt" || len(rr.Conflicts) != 1 || rr.Conflicts[0].Path != "a.txt" {
		t.Fatalf("%+v", rr)
	}
	if _, ok := f.read("new.txt"); ok {
		t.Fatal("created file not deleted")
	}
	if cs := f.changes(); len(cs) != 1 || !cs[0].Conflict {
		t.Fatalf("%+v", cs)
	}
	f.call("agent.revert", api.AgentRevertParams{Path: "a.txt", Force: true}, &rr)
	if got, _ := f.read("a.txt"); got != "line a\nline b\n" || len(rr.Reverted) != 1 {
		t.Fatalf("%q %+v", got, rr)
	}
	// A file the agent deleted is restored.
	fake.Script = func(ctx context.Context, t *acptest.Turn) string {
		tc := acp.ToolCall{ToolCallID: "d1", Name: "delete", Kind: acp.KindDelete, Locations: []acp.Location{{Path: modified}}}
		if t.Permission(ctx, tc) == "once" {
			_ = os.Remove(modified)
		}
		t.Tool(true, acp.ToolCall{ToolCallID: "d1", Status: acp.StatusCompleted})
		return ""
	}
	f.prompt("delete", "")
	if cs := f.changes(); len(cs) != 1 || cs[0].Kind != api.ChangeDeleted {
		t.Fatalf("%+v", cs)
	}
	f.call("agent.revert", api.AgentRevertParams{Path: "a.txt"}, &rr)
	if got, ok := f.read("a.txt"); !ok || got != "line a\nline b\n" {
		t.Fatalf("restore %q %v", got, ok)
	}
	// Accept clears the list.
	f.prompt("again", "")
	fake.Script = editScript(modified, "x\n")
	f.prompt("edit", "")
	// The changes survive a restart (a new module instance).
	m2 := New(Options{})
	m2.app.Store(f.app)
	if cs := m2.changeList(); len(cs) != 1 || cs[0].Path != "a.txt" {
		t.Fatalf("persisted %+v", cs)
	}
	var cl []api.AgentChange
	f.call("agent.acceptChanges", nil, &cl)
	if cs := (&Module{changes: newTracker()}).changeList(); len(cs) != 0 {
		t.Fatal(cs)
	}
	if len(f.changes()) != 0 {
		t.Fatal("accept")
	}
}

func TestPermissions(t *testing.T) {
	fake := &acptest.Agent{}
	f := newFixture(t, fake, nil)
	var mu sync.Mutex
	answers := map[string]string{}
	fake.Script = func(ctx context.Context, t *acptest.Turn) string {
		for _, cmd := range []string{"git commit -m x", "npm test", "npm run lint", "git push"} {
			b, _ := json.Marshal(map[string]string{"command": cmd})
			a := t.Permission(ctx, acp.ToolCall{ToolCallID: cmd, Name: "shell", Kind: acp.KindExecute, Title: cmd, RawInput: b})
			mu.Lock()
			answers[cmd] = a
			mu.Unlock()
		}
		return ""
	}
	// Headless without --yes: "ask" becomes deny; commits are blocked.
	f.prompt("run", api.PolicyDeny)
	mu.Lock()
	if answers["git commit -m x"] != "reject" || answers["npm test"] != "reject" || answers["git push"] != "reject" {
		t.Fatal(answers)
	}
	mu.Unlock()
	var blocked []string
	for _, e := range f.transcript() {
		if e.Kind == api.EntryNotice && e.Level == api.NoticeBlocked {
			blocked = append(blocked, e.Text)
		}
	}
	if len(blocked) != 4 || !strings.HasPrefix(blocked[0], "Blocked: git commit (disabled in settings)") || !strings.Contains(blocked[1], "nobody asked") {
		t.Fatal(blocked)
	}
	// --yes allows what asks, never what is blocked.
	f.prompt("run", api.PolicyAllow)
	mu.Lock()
	if answers["git commit -m x"] != "reject" || answers["npm test"] != "once" {
		t.Fatal(answers)
	}
	mu.Unlock()
	// Interactive: the requests wait for agent.permission.
	f.set(SettingCommits, true)
	f.set(SettingPush, true)
	modtest.Eventually(t, "restart notice", func() bool {
		for _, e := range f.transcript() {
			if strings.HasPrefix(e.Text, "Permission settings changed") {
				return true
			}
		}
		return false
	})
	var r api.AgentTurnResult
	f.call("agent.prompt", api.AgentPromptParams{Text: "run"}, &r)
	if !r.Started {
		t.Fatal(r)
	}
	answer := func(cmd string, p api.AgentPermissionParams) {
		ev := f.rec.Wait(t, api.TopicAgentPermission, func(e bus.Event) bool {
			return e.Payload.(api.AgentPermission).Tool.Command == cmd
		})
		perm := ev.Payload.(api.AgentPermission)
		p.RequestID = perm.RequestID
		f.call("agent.permission", p, nil)
	}
	answer("git commit -m x", api.AgentPermissionParams{Allow: true})
	answer("npm test", api.AgentPermissionParams{OptionID: "always"})
	// "npm run lint": remembered by "Always" for npm, no question.
	answer("git push", api.AgentPermissionParams{Deny: true})
	modtest.Eventually(t, "turn end", func() bool {
		var st api.AgentStatus
		f.call("agent.status", nil, &st)
		return st.State == api.AgentReady
	})
	mu.Lock()
	if answers["git commit -m x"] != "once" || answers["npm test"] != "once" || answers["npm run lint"] != "once" || answers["git push"] != "reject" {
		t.Fatal(answers)
	}
	mu.Unlock()
	modtest.WantCode(t, f.app, "agent.permission", api.AgentPermissionParams{RequestID: "nope", Allow: true}, api.CodeNotFound)
	var perms int
	for _, e := range f.transcript() {
		if e.Kind == api.EntryPermission && e.Permission.Answer != "" {
			perms++
		}
	}
	if perms != 3 {
		t.Fatal(perms)
	}
}

func TestPermissionValidationAndCancel(t *testing.T) {
	fake := &acptest.Agent{}
	f := newFixture(t, fake, nil)
	fake.Script = func(ctx context.Context, t *acptest.Turn) string {
		t.Permission(ctx, acp.ToolCall{ToolCallID: "w", Name: "webfetch", Kind: acp.KindFetch, RawInput: []byte(`{"url":"https://x"}`)})
		<-ctx.Done()
		return acp.StopCancelled
	}
	var r api.AgentTurnResult
	f.call("agent.prompt", api.AgentPromptParams{Text: "fetch"}, &r)
	ev := f.rec.Wait(t, api.TopicAgentPermission, func(bus.Event) bool { return true })
	perm := ev.Payload.(api.AgentPermission)
	if perm.Category != api.AgentCatWeb || perm.Summary != "Web: https://x" {
		t.Fatalf("%+v", perm)
	}
	modtest.WantCode(t, f.app, "agent.permission", api.AgentPermissionParams{RequestID: perm.RequestID, OptionID: "bogus"}, api.CodeInvalidParams)
	modtest.WantCode(t, f.app, "agent.permission", api.AgentPermissionParams{RequestID: perm.RequestID}, api.CodeInvalidParams)
	modtest.WantCode(t, f.app, "agent.prompt", api.AgentPromptParams{Text: "busy"}, api.CodeConflict)
	var st api.AgentStatus
	f.call("agent.status", nil, &st)
	if st.Pending != 1 || st.State != api.AgentPrompting {
		t.Fatalf("%+v", st)
	}
	f.call("agent.cancel", nil, &st)
	f.rec.Wait(t, api.TopicAgentTurnEnded, func(e bus.Event) bool {
		return e.Payload.(api.AgentTurnResult).StopReason == acp.StopCancelled
	})
	modtest.Eventually(t, "ready", func() bool {
		f.call("agent.status", nil, &st)
		return st.State == api.AgentReady && st.Pending == 0
	})
}

func TestModelRememberedAndAuth(t *testing.T) {
	fake := &acptest.Agent{}
	f := newFixture(t, fake, nil)
	var st api.AgentStatus
	f.call("agent.setModel", api.AgentModelParams{Model: "fake/large"}, &st)
	if st.Model != "fake/large" || fake.Model() != "fake/large" {
		t.Fatalf("%+v", st)
	}
	var v api.SettingValue
	f.call("settings.get", map[string]string{"key": SettingModel}, &v)
	if string(v.Value) != `"fake/large"` {
		t.Fatalf("%s", v.Value)
	}
	modtest.WantCode(t, f.app, "agent.setModel", api.AgentModelParams{}, api.CodeInvalidParams)
	modtest.WantCode(t, f.app, "agent.setOption", api.AgentOptionParams{}, api.CodeInvalidParams)
	modtest.WantCode(t, f.app, "agent.setModel", api.AgentModelParams{Model: "nope"}, api.CodeFailed)
	f.call("agent.setOption", api.AgentOptionParams{ID: "mode", Value: "plan"}, &st)
	// A new agent process picks the remembered model.
	f.call("agent.stop", nil, nil)
	fake2 := &acptest.Agent{}
	f.mod.opts.Start = fake2.Start
	f.call("agent.start", nil, &st)
	if fake2.Model() != "fake/large" || st.Model != "fake/large" {
		t.Fatalf("%+v %s", st, fake2.Model())
	}

	auth := &acptest.Agent{AuthRequired: true}
	g := newFixture(t, auth, nil)
	modtest.WantCode(t, g.app, "agent.start", nil, api.CodeFailed)
	g.call("agent.status", nil, &st)
	if !st.AuthRequired || !strings.Contains(st.AuthHint, "fake auth login") || st.State != api.AgentFailed {
		t.Fatalf("%+v", st)
	}
}

func TestClientFileAccess(t *testing.T) {
	fake := &acptest.Agent{}
	f := newFixture(t, fake, map[string]string{"a.txt": "one\ntwo\nthree\n"})
	abs := filepath.Join(f.root, "a.txt")
	f.call("doc.open", api.DocOpenParams{Path: "a.txt"}, nil)
	f.call("doc.insert", api.DocInsertParams{Path: "a.txt", At: api.Pos{Line: 1, Col: 1}, Text: "zero\n"}, nil)
	got := make(chan string, 3)
	fake.Script = func(ctx context.Context, t *acptest.Turn) string {
		s, _ := t.ReadFile(ctx, abs) // unsaved buffer text
		got <- s
		var r acp.ReadTextFileResponse
		line, limit := 2, 1
		_ = acptestCall(ctx, t, abs, &line, &limit, &r)
		got <- r.Content
		if _, err := t.ReadFile(ctx, filepath.Join(f.root, "missing")); err == nil {
			got <- "missing read"
		} else {
			got <- "ok"
		}
		_ = t.WriteFile(ctx, abs, "agent text\n")
		_ = t.WriteFile(ctx, filepath.Join(f.root, "made.txt"), "made\n")
		return ""
	}
	f.prompt("files", "")
	if s := <-got; s != "zero\none\ntwo\nthree\n" {
		t.Fatalf("read %q", s)
	}
	if s := <-got; s != "one\n" {
		t.Fatalf("range %q", s)
	}
	if s := <-got; s != "ok" {
		t.Fatal(s)
	}
	var txt api.DocText
	f.call("doc.text", api.DocTextParams{Path: "a.txt"}, &txt)
	disk, _ := f.read("a.txt")
	if txt.Text != "agent text\n" || disk != "agent text\n" {
		t.Fatalf("doc %q disk %q", txt.Text, disk)
	}
	cs := f.changes()
	if len(cs) != 2 || cs[1].Kind != api.ChangeCreated {
		t.Fatalf("%+v", cs)
	}
	var rr api.AgentRevertResult
	f.call("agent.revert", api.AgentRevertParams{All: true}, &rr)
	f.call("doc.text", api.DocTextParams{Path: "a.txt"}, &txt)
	// The original is what the user saw: the unsaved buffer text.
	if txt.Text != "zero\none\ntwo\nthree\n" || len(rr.Reverted) != 2 {
		t.Fatalf("%q %+v", txt.Text, rr)
	}
}

// acptestCall reads a line range (the fake's ReadFile has no range).
func acptestCall(ctx context.Context, t *acptest.Turn, path string, line, limit *int, out *acp.ReadTextFileResponse) error {
	h := t.Conn()
	return h.Call(ctx, acp.MethodReadTextFile, acp.ReadTextFileRequest{SessionID: t.SessionID, Path: path, Line: line, Limit: limit}, out)
}

func TestAgentExitReported(t *testing.T) {
	fake := &acptest.Agent{}
	f := newFixture(t, fake, nil)
	var st api.AgentStatus
	f.call("agent.start", nil, &st)
	f.mod.mu.Lock()
	proc := f.mod.proc
	f.mod.mu.Unlock()
	// Simulate a crash: close the connection from the outside.
	go func() { _ = proc.Close() }()
	modtest.Eventually(t, "failed state", func() bool {
		f.call("agent.status", nil, &st)
		return st.State == api.AgentFailed
	})
	if !strings.Contains(st.Error, "exited") {
		t.Fatal(st.Error)
	}
	// The next prompt starts a new agent.
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if err := modtest.TryCall(f.app, "agent.prompt", api.AgentPromptParams{Text: "hi", Wait: true}, nil); err == nil {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("restart failed")
}
