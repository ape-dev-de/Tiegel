package headless

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ape-dev-de/tiegel/internal/acp"
	"github.com/ape-dev-de/tiegel/internal/acp/acptest"
	agentmod "github.com/ape-dev-de/tiegel/internal/modules/agent"
)

// agentScript edits a.txt like opencode (permission with diff, direct
// write) and tries a git commit, which the default settings block.
func agentScript(root string) acptest.Script {
	return func(ctx context.Context, t *acptest.Turn) string {
		abs := filepath.Join(root, "a.txt")
		t.Text("Changing **a.txt**.")
		tc := acp.ToolCall{ToolCallID: "e", Name: "edit", Title: "edit a.txt", Kind: acp.KindEdit,
			Content: []acp.ToolCallContent{{Type: "diff", Path: abs, OldText: acptest.Str("old\n"), NewText: acptest.Str("new\n")}}}
		if t.Permission(ctx, tc) == "once" {
			_ = os.WriteFile(abs, []byte("new\n"), 0o644)
		}
		t.Tool(false, acp.ToolCall{ToolCallID: "e", Title: "edit a.txt", Kind: acp.KindEdit, Status: acp.StatusCompleted,
			Content: []acp.ToolCallContent{{Type: "diff", Path: abs, OldText: acptest.Str("old\n"), NewText: acptest.Str("new\n")}}})
		t.Permission(ctx, acp.ToolCall{ToolCallID: "c", Name: "shell", Kind: acp.KindExecute, RawInput: []byte(`{"command":"git commit -am x"}`)})
		t.Permission(ctx, acp.ToolCall{ToolCallID: "l", Name: "shell", Kind: acp.KindExecute, RawInput: []byte(`{"command":"ls"}`)})
		t.Text(" Done.")
		return ""
	}
}

func runAgentCLI(t *testing.T, cfg string, fake *acptest.Agent, args ...string) (int, string, string) {
	t.Helper()
	var out, errb bytes.Buffer
	env := testEnv(t, strings.NewReader(""), &out, &errb)
	env.Options.ConfigDir = cfg
	env.Modules.Agent = agentmod.Options{Start: fake.Start, LookPath: func(n string) (string, error) { return "/bin/" + n, nil }}
	code := Run(context.Background(), args, env)
	return code, out.String(), errb.String()
}

func TestAgentCommands(t *testing.T) {
	dir := project(t, map[string]string{"a.txt": "old\n"})
	cfg := filepath.Join(t.TempDir(), "cfg")
	fake := &acptest.Agent{Script: agentScript(dir)}
	code, out, errs := runAgentCLI(t, cfg, fake, "--headless", "--project", dir, "agent", "status")
	if code != 0 || !strings.Contains(out, "opencode acp (stopped)") || !strings.Contains(out, "Commits: blocked") {
		t.Fatalf("%d %s %s", code, out, errs)
	}
	code, out, errs = runAgentCLI(t, cfg, fake, "--headless", "--project", dir, "agent", "prompt", "please", "change")
	if code != 0 {
		t.Fatalf("%d %s %s", code, out, errs)
	}
	for _, want := range []string{"Changing **a.txt**.\n", "• edit a.txt  a.txt +1 -1  [completed]", "! Blocked: git commit (disabled in settings)",
		"! Blocked: shell commands (needs confirmation, nobody asked) – Run: ls", "1 changed file(s)"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
	if p := fake.Prompts(); len(p) != 1 || !strings.HasSuffix(p[0], "please change") {
		t.Fatal(p)
	}
	// A later run sees the persisted changes.
	code, out, _ = runAgentCLI(t, cfg, fake, "--headless", "--project", dir, "agent", "changes")
	if code != 0 || !strings.Contains(out, "modified a.txt  +1 -1") {
		t.Fatalf("%d %s", code, out)
	}
	code, out, _ = runAgentCLI(t, cfg, fake, "--headless", "--project", dir, "agent", "diff")
	if code != 0 || !strings.Contains(out, "-old\n+new") {
		t.Fatalf("%d %s", code, out)
	}
	code, out, _ = runAgentCLI(t, cfg, fake, "--headless", "--project", dir, "--json", "agent", "changes")
	var cs []map[string]any
	if code != 0 || json.Unmarshal([]byte(out), &cs) != nil || len(cs) != 1 {
		t.Fatalf("%d %s", code, out)
	}
	if code, _, _ = runAgentCLI(t, cfg, fake, "--headless", "--project", dir, "agent", "revert"); code != ExitUsage {
		t.Fatal(code)
	}
	code, out, _ = runAgentCLI(t, cfg, fake, "--headless", "--project", dir, "agent", "revert", "a.txt")
	if b, _ := os.ReadFile(filepath.Join(dir, "a.txt")); code != 0 || string(b) != "old\n" || !strings.Contains(out, "reverted a.txt") {
		t.Fatalf("%d %s %q", code, out, b)
	}
	// --yes allows what asks; JSON output carries the transcript.
	code, out, _ = runAgentCLI(t, cfg, fake, "--headless", "--project", dir, "--json", "agent", "prompt", "--yes", "again")
	// The transcript of the previous run is restored (D573); the fake
	// agent cannot resume its session, so a notice separates the history.
	_, again, found := strings.Cut(out, "Previous session ended")
	if code != 0 || !strings.Contains(out, `"transcript"`) || !found || strings.Contains(again, "Blocked: shell commands") {
		t.Fatalf("%d %s", code, out)
	}
	code, out, _ = runAgentCLI(t, cfg, fake, "--headless", "--project", dir, "agent", "revert", "--all")
	if code != 0 || !strings.Contains(out, "reverted a.txt") {
		t.Fatalf("%d %s", code, out)
	}
	code, out, _ = runAgentCLI(t, cfg, fake, "--headless", "--project", dir, "agent", "model")
	if code != 0 || !strings.Contains(out, "* fake/small") {
		t.Fatalf("%d %s", code, out)
	}
	code, out, _ = runAgentCLI(t, cfg, fake, "--headless", "--project", dir, "agent", "model", "fake/large")
	if code != 0 || !strings.Contains(out, "Model (model): fake/large") {
		t.Fatalf("%d %s", code, out)
	}
	code, out, _ = runAgentCLI(t, cfg, fake, "--headless", "--project", dir, "agent", "option", "mode", "plan")
	if code != 0 || !strings.Contains(out, "Session Mode (mode): plan") {
		t.Fatalf("%d %s", code, out)
	}
	for _, args := range [][]string{{"agent", "prompt"}, {"agent", "option", "x"}, {"agent", "bogus"}} {
		if code, _, _ := runAgentCLI(t, cfg, fake, append([]string{"--headless", "--project", dir}, args...)...); code != ExitUsage {
			t.Errorf("%v: %d", args, code)
		}
	}
	for _, sub := range []string{"start", "transcript", "new", "accept", "stop"} {
		if code, out, errs := runAgentCLI(t, cfg, fake, "--headless", "--project", dir, "agent", sub); code != 0 {
			t.Errorf("%s: %d %s %s", sub, code, out, errs)
		}
	}
	code, out, _ = runAgentCLI(t, cfg, fake, "--headless", "--project", dir, "agent", "changes")
	if code != 0 || !strings.Contains(out, "no changes") {
		t.Fatalf("%d %s", code, out)
	}
}
