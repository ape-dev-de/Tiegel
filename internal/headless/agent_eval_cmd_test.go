package headless

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ape-dev-de/tiegel/internal/acp"
	"github.com/ape-dev-de/tiegel/internal/acp/acptest"
	"github.com/ape-dev-de/tiegel/internal/api"
)

// evalAgent reads a file, greps and answers with a citation.
func evalAgent(_ context.Context, t *acptest.Turn) string {
	t.Tool(false, acp.ToolCall{ToolCallID: "r", Name: "read", Title: "read a.php", Kind: acp.KindRead, Status: acp.StatusCompleted})
	t.Tool(false, acp.ToolCall{ToolCallID: "g", Name: "grep", Title: "grep x", Kind: acp.KindSearch, Status: acp.StatusCompleted})
	t.Text("It is in a.php:1.")
	return ""
}

func TestAgentEvalCommand(t *testing.T) {
	dir := project(t, map[string]string{"a.php": "<?php\n"})
	cfg := filepath.Join(t.TempDir(), "cfg")
	fake := &acptest.Agent{Script: evalAgent}
	qs := filepath.Join(t.TempDir(), "questions.txt")
	if err := os.WriteFile(qs, []byte("# comment\nWhere is x?\n\nWhat does a.php do?\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Without --yes: the plan, no agent turn.
	code, out, errs := runAgentCLI(t, cfg, fake, "--headless", "--project", dir, "agent", "eval", "--modes", "off,rag", "--runs", "2", "--file", qs)
	if code != 0 || !strings.Contains(out, "nothing ran") || !strings.Contains(out, "8 agent turns") || !strings.Contains(out, "billed") ||
		!strings.Contains(out, "2. What does a.php do?") || !strings.Contains(out, "Run it with --yes.") || len(fake.Prompts()) != 0 {
		t.Fatalf("%d %s %s", code, out, errs)
	}
	code, _, errs = runAgentCLI(t, cfg, fake, "--headless", "--project", dir, "agent", "eval", "--modes", "magic", "q")
	if code == 0 || !strings.Contains(errs, "unknown mode") {
		t.Fatalf("%d %s", code, errs)
	}
	if code, _, _ = runAgentCLI(t, cfg, fake, "--headless", "--project", dir, "agent", "eval"); code != ExitUsage {
		t.Fatal(code)
	}
	// With --yes: the table, progress on stderr, the answers on disk.
	code, out, errs = runAgentCLI(t, cfg, fake, "--headless", "--project", dir, "agent", "eval", "--modes", "off,rag", "--yes", "Where", "is", "x?")
	if code != 0 || len(fake.Prompts()) != 2 {
		t.Fatalf("%d %s %s", code, out, errs)
	}
	for _, want := range []string{"MODE", "TOOL CALLS", "CITES", "off ", "rag ", "100%", "read 1", "grep 1", "Answers: "} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
	t.Log(out)
	if !strings.Contains(errs, "[1/2] off") || !strings.Contains(errs, "[2/2] rag:") {
		t.Fatalf("progress: %s", errs)
	}
	i := strings.Index(out, "Answers: ")
	adir := strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(out[i+len("Answers: "):]), "(<mode>-<n>.md)"))
	if !strings.HasPrefix(adir, cfg) || !strings.Contains(adir, string(filepath.Separator)+"eval"+string(filepath.Separator)) {
		t.Fatal(adir)
	}
	for _, f := range []string{"off-1.md", "rag-1.md"} {
		b, err := os.ReadFile(filepath.Join(adir, f))
		if err != nil || !strings.Contains(string(b), "It is in a.php:1.") {
			t.Fatalf("%s: %v %s", f, err, b)
		}
	}
	// JSON.
	code, out, errs = runAgentCLI(t, cfg, fake, "--headless", "--project", dir, "--json", "agent", "eval", "--modes", "mcp", "--yes", "q")
	var res api.AgentEvalResult
	if code != 0 || json.Unmarshal([]byte(out), &res) != nil || !res.Ran || len(res.Runs) != 1 || res.Runs[0].Tools["read"] != 1 || !res.Runs[0].Cites {
		t.Fatalf("%d %s %s", code, out, errs)
	}
}
