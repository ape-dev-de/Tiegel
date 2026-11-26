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

	"github.com/ape-dev-de/tiegel/internal/acp/acptest"
	"github.com/ape-dev-de/tiegel/internal/api"
	kn "github.com/ape-dev-de/tiegel/internal/knowledge"
	knmod "github.com/ape-dev-de/tiegel/internal/modules/knowledge"
)

// knowledgeScript answers the three pipeline prompts.
func knowledgeScript(_ context.Context, t *acptest.Turn) string {
	p := t.Prompt
	switch {
	case strings.Contains(p, "You document the data flows"):
		d := kn.Description{Summary: "Stores orders.", Flows: []kn.Flow{{Name: "Place order", Trigger: "POST /orders",
			Steps: []kn.Step{{File: "src/Order.php", Symbol: "Order::place", What: "saves the order"}}}}}
		b, _ := json.Marshal(d)
		t.Text("```json\n" + string(b) + "\n```")
	case strings.Contains(p, "You review a description"):
		i := strings.Index(p, "```json\n")
		j := strings.Index(p[i+8:], "\n```")
		var d kn.Description
		_ = json.Unmarshal([]byte(p[i+8:i+8+j]), &d)
		v, _ := json.Marshal(kn.Validation{Confidence: 0.9, Description: &d})
		t.Text("```json\n" + string(v) + "\n```")
	default:
		t.Text("## Overview\nStores orders in the database.\n\n## Data flows\n### Place order\n1. [src/Order.php:3](src/Order.php#L3) – saves the order\n")
	}
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
		"composer.json": `{"autoload":{"psr-4":{"App\\":"src/"}}}`,
		"src/Order.php": "<?php\nclass Order {\n  public function place() {}\n}\n",
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
	if code != 0 || !strings.Contains(out, "0 built, 1 to build") || !strings.Contains(out, "Embeddings:") || !strings.Contains(out, "(local)") {
		t.Fatalf("%d %s %s", code, out, errs)
	}
	code, out, _ = run("plan")
	if code != 0 || !strings.Contains(squash(out), "src App 1 new") || !strings.Contains(out, "billed by your agent's provider") {
		t.Fatalf("%d %s", code, out)
	}
	// build without --yes runs nothing.
	code, out, _ = run("build")
	if code != 0 || !strings.Contains(out, "nothing was run: add --yes") {
		t.Fatalf("%d %s", code, out)
	}
	code, out, errs = run("build", "--yes")
	if code != 0 || !strings.Contains(out, "1 units built, 0 failed") || !strings.Contains(errs, "[1/1] src: write done (confidence 0.90)") || !strings.Contains(out, "embedding:") {
		t.Fatalf("%d %s %s", code, out, errs)
	}
	code, out, _ = run("plan")
	if code != 0 || !strings.Contains(out, "nothing to build") {
		t.Fatalf("%d %s", code, out)
	}
	code, out, _ = run("list")
	if code != 0 || !strings.Contains(squash(out), "src App 1 0.90 current") {
		t.Fatalf("%d %q", code, out)
	}
	code, out, _ = run("show", "src")
	if code != 0 || !strings.HasPrefix(out, "---\nunit: src") || !strings.Contains(out, "## Data flows") {
		t.Fatalf("%d %s", code, out)
	}
	code, out, _ = run("read", "index.md")
	if code != 0 || !strings.Contains(out, "(units/src.md)") {
		t.Fatalf("%d %s", code, out)
	}
	code, out, _ = run("search", "orders", "database", "-k", "2")
	if code != 0 || !strings.Contains(out, "1. ") || !strings.Contains(out, "units/src.md#") {
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
		`{"jsonrpc":"2.0","id":5,"method":"tools/call","params":{"name":"knowledge_read","arguments":{"path":"units/src.md"}}}`,
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
	if s, isErr := text(2); isErr || !strings.Contains(s, "page: units/src.md") {
		t.Fatal(s)
	}
	if s, _ := text(3); !strings.Contains(s, "units/src.md  App") {
		t.Fatal(s)
	}
	if s, _ := text(4); !strings.Contains(s, "## Overview") {
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
