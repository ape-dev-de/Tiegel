package headless

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ape-dev-de/tiegel/internal/acp"
	"github.com/ape-dev-de/tiegel/internal/acp/acptest"
	"github.com/ape-dev-de/tiegel/internal/api"
	kn "github.com/ape-dev-de/tiegel/internal/knowledge"
	"github.com/ape-dev-de/tiegel/internal/knowledge/knowledgetest"
	knmod "github.com/ape-dev-de/tiegel/internal/modules/knowledge"
)

// knowledgeFake answers the pipeline's prompts; the script adds a
// thought and a read tool call to every describe prompt.
var knowledgeFake = &knowledgetest.Fake{Service: kn.Ref{File: "src/Service/OrderService.php", Symbol: `App\Service\OrderService::save`}}

func knowledgeScript(ctx context.Context, t *acptest.Turn) string {
	if _, step, _ := knowledgetest.Level(t.Prompt); step == kn.StepDescribe {
		t.Thought("Checking the routes first.")
		t.Tool(false, acp.ToolCall{ToolCallID: "r1", Title: "read src/Controller/OrderController.php", Kind: acp.KindRead, Status: "completed"})
	}
	t.Text(knowledgeFake.Answer(ctx, t.Prompt))
	return ""
}

func runKnowledgeCLI(t *testing.T, cfg string, stdin io.Reader, args ...string) (int, string, string) {
	t.Helper()
	var out, errb bytes.Buffer
	env := testEnv(t, stdin, &out, &errb)
	env.Options.ConfigDir = cfg
	fake := &acptest.Agent{Script: knowledgeScript}
	env.Modules.Knowledge = knmod.Options{Start: fake.Start, LookPath: func(n string) (string, error) { return "/bin/" + n, nil },
		Executable: func() (string, error) { return "/usr/local/bin/tiegel", nil }}
	code := Run(context.Background(), args, env)
	return code, out.String(), errb.String()
}

func TestKnowledgeCommands(t *testing.T) {
	dir := project(t, map[string]string{
		"composer.json":                      `{"require":{"symfony/framework-bundle":"^7"},"autoload":{"psr-4":{"App\\":"src/"}}}`,
		"src/Controller/OrderController.php": "<?php\nnamespace App\\Controller;\nclass OrderController {\n  #[Route('/orders', methods: ['POST'])]\n  public function create() {}\n}\n",
		"src/Service/OrderService.php":       "<?php\nnamespace App\\Service;\nclass OrderService {\n  public function save() {}\n}\n",
	})
	cfg := filepath.Join(t.TempDir(), "cfg")
	// The embedding server is unreachable: keyword search only.
	code, out, errs := runKnowledgeCLI(t, cfg, nil, "--headless", "--project", dir, "settings", "set", "knowledge.embedEndpoint", "http://127.0.0.1:1")
	if code != 0 {
		t.Fatal(code, out, errs)
	}
	run := func(args ...string) (int, string, string) {
		return runKnowledgeCLI(t, cfg, nil, append([]string{"--headless", "--project", dir, "knowledge"}, args...)...)
	}
	code, out, errs = run("status")
	if code != 0 || !strings.Contains(out, "0 built, 3 to build") || !strings.Contains(out, "Overview:") || !strings.Contains(out, "Questions:") || !strings.Contains(out, "(local)") {
		t.Fatalf("%d %s %s", code, out, errs)
	}
	code, out, _ = run("plan")
	if code != 0 || !strings.Contains(squash(out), "overview overview Overview") || !strings.Contains(squash(out), "Processes 1 of 1 2") || !strings.Contains(out, "billed by your agent's provider") || !strings.Contains(out, "upper bound") {
		t.Fatalf("%d %s", code, out)
	}
	code, out, _ = run("plan", "--level", "entrypoints")
	if code != 0 || strings.Contains(out, "overview") || !strings.Contains(out, "entrypoints-http") {
		t.Fatalf("%d %s", code, out)
	}
	if code, _, _ = run("plan", "--level", "nope"); code == 0 {
		t.Fatal("unknown level")
	}
	// build without --yes runs nothing.
	code, out, _ = run("build")
	if code != 0 || !strings.Contains(out, "nothing was run: add --yes") {
		t.Fatalf("%d %s", code, out)
	}
	code, out, errs = run("build", "--yes", "--verbose")
	if code != 0 || !strings.Contains(out, "5 pages built, 0 failed") || !strings.Contains(errs, "[1/1] overview overview: write done (confidence 0.85)") ||
		!strings.Contains(errs, "describe running (step 1 of 3)") || !strings.Contains(errs, "  overview thought: Checking the routes first.") ||
		!strings.Contains(errs, "  overview tool read src/Controller/OrderController.php [completed]") || !strings.Contains(out, "embedding:") {
		t.Fatalf("%d %s %s", code, out, errs)
	}
	// The step lines come once each, without counter repeats.
	if n := strings.Count(errs, "overview overview: describe running"); n != 1 {
		t.Fatal(n, errs)
	}
	code, out, _ = run("transcript", "overview")
	if code != 0 || !strings.Contains(out, "» describe (step 1 of 3)") || !strings.Contains(out, "thought: Checking the routes first.") || !strings.Contains(out, "log: ") {
		t.Fatalf("%d %s", code, out)
	}
	if code, _, _ = run("transcript"); code != ExitUsage {
		t.Fatal(code)
	}
	code, out, _ = run("plan")
	if code != 0 || !strings.Contains(out, "nothing to build") {
		t.Fatalf("%d %s", code, out)
	}
	code, out, _ = run("list")
	if code != 0 || !strings.Contains(squash(out), "overview overview Overview") || !strings.Contains(squash(out), "processes process-app-controller-ordercontroller OrderController (HTTP)") || !strings.Contains(out, "0.85") {
		t.Fatalf("%d %q", code, out)
	}
	code, out, _ = run("show", "overview")
	if code != 0 || !strings.HasPrefix(out, "---\nid: overview") || !strings.Contains(out, "```mermaid") {
		t.Fatalf("%d %s", code, out)
	}
	code, out, _ = run("read", "index.md")
	if code != 0 || !strings.Contains(out, "(processes/app-controller-ordercontroller.md)") {
		t.Fatalf("%d %s", code, out)
	}
	code, out, _ = run("questions", "--answered")
	if code != 0 || !strings.Contains(out, "Which table keeps the order lines?") {
		t.Fatalf("%d %s", code, out)
	}
	code, out, _ = run("search", "orders", "POST", "-k", "2")
	if code != 0 || !strings.Contains(out, "1. ") || !strings.Contains(out, ".md#") {
		t.Fatalf("%d %s", code, out)
	}
	code, out, _ = run("search", "orders", "--json")
	var res api.KnowledgeSearchResult
	if code != 0 || json.Unmarshal([]byte(out), &res) != nil || res.Mode != "keyword" || len(res.Hits) == 0 {
		t.Fatalf("%d %s", code, out)
	}
	code, out, _ = run("embed")
	if code != 1 || !strings.Contains(out, "embedding failed") {
		t.Fatalf("%d %s", code, out)
	}
	code, out, _ = run("pull")
	if code != 0 || !strings.Contains(out, "server not reachable") || !strings.Contains(out, "ollama pull "+kn.DefaultModel) || !strings.Contains(out, "--yes") {
		t.Fatalf("%d %s", code, out)
	}
	code, out, _ = run("mcp-config")
	if code != 0 || !strings.Contains(out, `"/usr/local/bin/tiegel"`) || !strings.Contains(out, "mcpServers") {
		t.Fatalf("%d %s", code, out)
	}
	code, out, _ = run("cancel")
	if code != 0 || !strings.Contains(out, "no build") {
		t.Fatalf("%d %s", code, out)
	}
	for _, bad := range [][]string{{"nope"}, {"search"}, {"read"}} {
		if code, _, _ := run(bad...); code != ExitUsage {
			t.Fatal(bad, code)
		}
	}

	// The MCP server answers a client over stdio.
	lines := strings.Join([]string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18"}}`,
		`{"jsonrpc":"2.0","method":"notifications/initialized"}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/list"}`,
		`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"knowledge_search","arguments":{"query":"orders"}}}`,
		`{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"knowledge_list","arguments":{}}}`,
		`{"jsonrpc":"2.0","id":5,"method":"tools/call","params":{"name":"knowledge_read","arguments":{"path":"overview.md"}}}`,
		`{"jsonrpc":"2.0","id":6,"method":"tools/call","params":{"name":"knowledge_read","arguments":{"path":"../../secret.md"}}}`,
	}, "\n") + "\n"
	done := make(chan struct{})
	var mcpOut string
	go func() {
		defer close(done)
		code, mcpOut, errs = runKnowledgeCLI(t, cfg, strings.NewReader(lines), "--headless", "mcp", "knowledge", "--project", dir)
	}()
	select {
	case <-done:
	case <-time.After(30 * time.Second):
		t.Fatal("mcp server did not end")
	}
	if code != 0 {
		t.Fatal(code, errs)
	}
	var resps []map[string]any
	sc := bufio.NewScanner(strings.NewReader(mcpOut))
	sc.Buffer(nil, 1<<20)
	for sc.Scan() {
		var m map[string]any
		if err := json.Unmarshal(sc.Bytes(), &m); err != nil {
			t.Fatalf("stdout must only carry MCP messages: %q", sc.Text())
		}
		resps = append(resps, m)
	}
	if len(resps) != 6 {
		t.Fatalf("%d responses: %s", len(resps), mcpOut)
	}
	text := func(i int) (string, bool) {
		r := resps[i]["result"].(map[string]any)
		return r["content"].([]any)[0].(map[string]any)["text"].(string), r["isError"] == true
	}
	if tools := resps[1]["result"].(map[string]any)["tools"].([]any); len(tools) != 3 {
		t.Fatal(tools)
	}
	if s, isErr := text(2); isErr || !strings.Contains(s, "page: ") {
		t.Fatal(s)
	}
	if s, _ := text(3); !strings.Contains(s, "## Overview") || !strings.Contains(s, "## Processes") || !strings.Contains(s, "processes/app-controller-ordercontroller.md") {
		t.Fatal(s)
	}
	if s, _ := text(4); !strings.Contains(s, "# Overview") {
		t.Fatal(s)
	}
	if s, isErr := text(5); !isErr || !strings.Contains(s, "does not exist") {
		t.Fatal(s, isErr)
	}
	if code, _, _ := runKnowledgeCLI(t, cfg, nil, "--headless", "mcp", "other", "--project", dir); code != ExitUsage {
		t.Fatal(code)
	}
}

// squash collapses runs of white space (tab-aligned tables).
func squash(s string) string { return strings.Join(strings.Fields(s), " ") }
