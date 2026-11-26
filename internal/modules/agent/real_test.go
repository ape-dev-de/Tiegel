package agent

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ape-dev-de/tiegel/internal/api"
	"github.com/ape-dev-de/tiegel/internal/core/module"
	"github.com/ape-dev-de/tiegel/internal/modules/modtest"
)

// TestRealOpencode drives the installed opencode. It sends a prompt to a
// language model (network, costs), so it runs only with
// TIEGEL_REAL_OPENCODE=1; TIEGEL_REAL_OPENCODE_MODEL picks the model.
func TestRealOpencode(t *testing.T) {
	if os.Getenv("TIEGEL_REAL_OPENCODE") != "1" {
		t.Skip("set TIEGEL_REAL_OPENCODE=1 to talk to a real opencode (uses an LLM)")
	}
	if _, err := exec.LookPath("opencode"); err != nil {
		t.Skip("opencode not installed")
	}
	m := New(Options{})
	app := modtest.NewApp(t, []module.Module{m}, nil)
	root := modtest.Project(t, app, map[string]string{"foo.txt": "line a\nline b\n"})
	if model := os.Getenv("TIEGEL_REAL_OPENCODE_MODEL"); model != "" {
		raw, _ := json.Marshal(model)
		modtest.Call(t, app, "settings.set", api.SettingsSetParams{Key: SettingModel, Scope: "global", Value: raw}, nil)
	}
	defer modtest.Call(t, app, "agent.stop", nil, nil)
	var res api.AgentTurnResult
	modtest.Call(t, app, "agent.prompt", api.AgentPromptParams{Wait: true, Ask: api.PolicyDeny,
		Text: "Use your tools, no explanations: 1) in foo.txt replace 'line a' with 'line A' using the edit tool. 2) run the shell command: git commit -am test"}, &res)
	var cs []api.AgentChange
	modtest.Call(t, app, "agent.changes", nil, &cs)
	if len(cs) != 1 || cs[0].Path != "foo.txt" {
		t.Fatalf("changes %+v (turn %+v)", cs, res)
	}
	var es []api.AgentEntry
	modtest.Call(t, app, "agent.transcript", nil, &es)
	for _, e := range es {
		if e.Kind == api.EntryTool && e.Tool.Command != "" && strings.Contains(e.Tool.Command, "git commit") && e.Tool.Status == "completed" {
			t.Fatalf("git commit ran: %+v", e.Tool)
		}
	}
	var rr api.AgentRevertResult
	modtest.Call(t, app, "agent.revert", api.AgentRevertParams{All: true}, &rr)
	if b, _ := os.ReadFile(filepath.Join(root, "foo.txt")); string(b) != "line a\nline b\n" {
		t.Fatalf("revert: %q", b)
	}
}
