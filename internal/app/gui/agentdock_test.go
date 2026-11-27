package gui

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ape-dev-de/tiegel/internal/acp"
	"github.com/ape-dev-de/tiegel/internal/acp/acptest"
	"github.com/ape-dev-de/tiegel/internal/api"
	"github.com/ape-dev-de/tiegel/internal/modules"
	agentmod "github.com/ape-dev-de/tiegel/internal/modules/agent"
	"github.com/ape-dev-de/tiegel/internal/ui/organisms/agentchat"
)

// newAgentEnv is newEnv with the agent plugin connected to fake (nil:
// the agent is not installed).
func newAgentEnv(t *testing.T, fake *acptest.Agent) *env {
	t.Helper()
	opts := agentmod.Options{LookPath: func(string) (string, error) { return "", errors.New("missing") }}
	if fake != nil {
		opts = agentmod.Options{Start: fake.Start, LookPath: func(n string) (string, error) { return "/bin/" + n, nil }}
	}
	envModules = modules.Options{Agent: opts}
	t.Cleanup(func() { envModules = modules.Options{} })
	return newEnv(t, true)
}

// guiScript streams an answer, edits src/Shop.php like opencode and asks
// before running the tests; a git commit is blocked by the settings.
func guiScript(root string) acptest.Script {
	return func(ctx context.Context, t *acptest.Turn) string {
		abs := filepath.Join(root, "src", "Shop.php")
		old, _ := os.ReadFile(abs)
		updated := strings.Replace(string(old), "greet", "welcome", 1)
		t.Thought("The function greet() is defined in src/Shop.php.")
		t.Plan(acp.PlanEntry{Content: "Rename greet()", Status: "in_progress"}, acp.PlanEntry{Content: "Run the tests", Status: "pending"})
		tc := acp.ToolCall{ToolCallID: "e1", Name: "edit", Title: "edit", Kind: acp.KindEdit, Locations: []acp.Location{{Path: abs}},
			Content: []acp.ToolCallContent{{Type: "diff", Path: abs, OldText: acptest.Str(string(old)), NewText: acptest.Str(updated)}}}
		t.Tool(false, acp.ToolCall{ToolCallID: "e1", Name: "edit", Title: "edit", Kind: acp.KindEdit, Status: acp.StatusPending})
		if t.Permission(ctx, tc) == "once" {
			_ = os.WriteFile(abs, []byte(updated), 0o644)
		}
		tc.Status = acp.StatusCompleted
		t.Tool(true, tc)
		t.Text("I renamed `greet()` to **welcome()** in `src/Shop.php`.")
		t.Permission(ctx, acp.ToolCall{ToolCallID: "c1", Name: "shell", Kind: acp.KindExecute, RawInput: json.RawMessage(`{"command":"git commit -am rename"}`)})
		run := acp.ToolCall{ToolCallID: "s1", Name: "shell", Title: "vendor/bin/phpunit", Kind: acp.KindExecute, RawInput: json.RawMessage(`{"command":"vendor/bin/phpunit"}`)}
		t.Tool(false, run)
		ans := t.Permission(ctx, run)
		run.Status = acp.StatusCompleted
		if ans != "once" {
			run.Status = acp.StatusFailed
		}
		t.Tool(true, run)
		t.Text("Tests ran.")
		return ""
	}
}

func TestAgentSidebar(t *testing.T) {
	fake := &acptest.Agent{Name: "OpenCode"}
	e := newAgentEnv(t, fake)
	fake.Script = guiScript(e.root)
	e.v.ShowTool(ToolAgent)
	e.settle()
	a := e.ctl.Agent
	e.waitFor("agent status", func() bool { return a.Loaded && a.Status.Available })
	if a.Status.State != api.AgentStopped {
		t.Fatalf("agent started without a user action: %+v", a.Status)
	}
	// The toolbar quick action exists.
	found := false
	for _, b := range e.v.Registry.Model().Toolbar(e.v.actionContext("toolbar"), nil, "darwin") {
		found = found || b.ID == string(ActAgentOpen)
	}
	if !found {
		t.Fatal("no toolbar button")
	}
	e.shot("agent-empty")

	a.Prompt("Rename greet() to welcome() and run the tests.")
	e.waitFor("permission card", func() bool {
		for _, en := range e.v.agtd.chat.Entries {
			if en.Kind == agentchat.Permission && !en.Perm.Answered {
				return true
			}
		}
		return false
	})
	e.settle()
	if len(e.v.agtd.chat.Changes) != 1 || e.v.agtd.chat.Changes[0].Path != "src/Shop.php" {
		t.Fatalf("changes %+v", e.v.agtd.chat.Changes)
	}
	blocked := false
	for _, en := range e.v.agtd.chat.Entries {
		blocked = blocked || en.Kind == agentchat.Notice && en.Level == agentchat.Blocked && strings.Contains(en.Text, "git commit")
	}
	if !blocked {
		t.Fatal("git commit not shown as blocked")
	}
	e.shot("agent-permission")

	var req string
	for _, en := range e.v.agtd.chat.Entries {
		if en.Perm != nil && !en.Perm.Answered {
			req = en.Perm.ID
		}
	}
	a.Answer(req, "once")
	e.waitFor("turn end", func() bool {
		return a.Status.State == api.AgentReady && len(a.Entries) > 0 && a.Entries[len(a.Entries)-1].Text == "Tests ran."
	})
	e.settle()
	e.shot("agent-done")

	a.OpenDiff("src/Shop.php")
	e.waitFor("diff tab", func() bool { return len(e.ctl.Work.Diffs()) == 1 })
	// The diff opens at its first change.
	e.waitFor("first change", func() bool {
		e.frame()
		for _, dv := range e.v.work.diffs {
			return dv.win.Count() > 0 && dv.win.Current() == 0
		}
		return false
	})
	a.Revert("src/Shop.php")
	e.waitFor("reverted", func() bool { return len(a.Changes) == 0 })
	if b, _ := os.ReadFile(filepath.Join(e.root, "src", "Shop.php")); string(b) != samplePHP {
		t.Fatalf("not reverted: %s", b)
	}
	a.NewSession()
	e.waitFor("new session", func() bool { return len(a.Entries) == 0 && a.Status.SessionID == "ses_2" })
	a.Stop()
	e.waitFor("stopped", func() bool { return a.Status.State == api.AgentStopped })
}

func TestAgentSidebarNotInstalled(t *testing.T) {
	e := newAgentEnv(t, nil)
	e.v.ShowTool(ToolAgent)
	e.waitFor("status", func() bool { return e.ctl.Agent.Loaded })
	e.settle()
	n := e.v.agtd.chat.Notice
	if n == nil || n.Action != "Install…" || !strings.Contains(n.Detail, "npm install -g opencode-ai") {
		t.Fatalf("%+v", n)
	}
	e.shot("agent-install")
}
