package knowledge

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ape-dev-de/tiegel/internal/acp"
	kn "github.com/ape-dev-de/tiegel/internal/knowledge"
)

// TestRealOpencodeStartsMCPServer checks with a real opencode (no prompt,
// no tokens) that an MCP server passed in session/new is started.
// TIEGEL_REAL_OPENCODE=1 enables it.
func TestRealOpencodeStartsMCPServer(t *testing.T) {
	if os.Getenv("TIEGEL_REAL_OPENCODE") != "1" {
		t.Skip("set TIEGEL_REAL_OPENCODE=1 to run against the installed opencode")
	}
	oc, err := exec.LookPath("opencode")
	if err != nil {
		home, _ := os.UserHomeDir()
		oc = filepath.Join(home, ".opencode", "bin", "opencode")
		if _, err := os.Stat(oc); err != nil {
			t.Skip("opencode not installed")
		}
	}
	dir := t.TempDir()
	log := filepath.Join(dir, "mcp.log")
	// A minimal MCP server in sh: logs that it was started and answers
	// initialize and tools/list.
	script := filepath.Join(dir, "mcp.sh")
	body := "#!/bin/sh\necho started \"$@\" >> " + log + "\n" +
		"while IFS= read -r line; do\n" +
		"  echo \"$line\" >> " + log + "\n" +
		"  id=$(echo \"$line\" | sed -n 's/.*\"id\":\\([0-9]*\\).*/\\1/p')\n" +
		"  case \"$line\" in\n" +
		"    *'\"initialize\"'*) echo '{\"jsonrpc\":\"2.0\",\"id\":'$id',\"result\":{\"protocolVersion\":\"2025-06-18\",\"capabilities\":{\"tools\":{}},\"serverInfo\":{\"name\":\"t\",\"version\":\"1\"}}}' ;;\n" +
		"    *'\"tools/list\"'*) echo '{\"jsonrpc\":\"2.0\",\"id\":'$id',\"result\":{\"tools\":[{\"name\":\"knowledge_search\",\"inputSchema\":{\"type\":\"object\"}}]}}' ;;\n" +
		"    *'\"id\"'*) echo '{\"jsonrpc\":\"2.0\",\"id\":'$id',\"result\":{}}' ;;\n" +
		"  esac\n" +
		"done\n"
	if err := os.WriteFile(script, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	a := &acpAgent{root: dir, sinks: map[string]*strings.Builder{}}
	proc, err := acp.Start(ctx, acp.Command{Path: oc, Args: []string{"acp"}, Dir: dir, Env: append(os.Environ(), "OPENCODE_CONFIG_CONTENT="+readOnlyConfig)}, acpHandler{a})
	if err != nil {
		t.Fatal(err)
	}
	defer proc.Close()
	init, err := proc.Initialize(ctx, acp.InitializeRequest{ProtocolVersion: acp.ProtocolVersion})
	if err != nil {
		t.Fatal(err, proc.Stderr())
	}
	t.Logf("agent %+v caps %+v", init.AgentInfo, init.AgentCapabilities)
	res, err := proc.NewSessionWith(ctx, dir, []acp.MCPServerStdio{{Name: "tiegel-knowledge", Command: script, Args: []string{"--headless", "mcp", "knowledge"}}})
	if err != nil {
		t.Fatal(err, proc.Stderr())
	}
	t.Logf("session %s", res.SessionID)
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		if b, _ := os.ReadFile(log); strings.Contains(string(b), "tools/list") {
			t.Logf("MCP server log:\n%s", b)
			return
		}
		time.Sleep(200 * time.Millisecond)
	}
	b, _ := os.ReadFile(log)
	t.Fatalf("opencode did not list the tools of the MCP server; log: %q stderr: %s", b, proc.Stderr())
}

// TestRealOllamaEmbedding embeds with the configured default model of a
// running Ollama (TIEGEL_REAL_OLLAMA=1; the model must be pulled).
func TestRealOllamaEmbedding(t *testing.T) {
	if os.Getenv("TIEGEL_REAL_OLLAMA") != "1" {
		t.Skip("set TIEGEL_REAL_OLLAMA=1 to embed with a local Ollama")
	}
	model := os.Getenv("TIEGEL_OLLAMA_MODEL")
	if model == "" {
		model = kn.DefaultModel
	}
	e, err := kn.NewEmbedder(kn.EmbedConfig{Model: model})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	ok, err := e.HasModel(ctx)
	if err != nil {
		t.Skip("ollama not reachable:", err)
	}
	if !ok {
		t.Skipf("model %s not pulled (ollama pull %s)", model, model)
	}
	doc, q := kn.Prefixes(e.Model())
	vs, err := e.Embed(ctx, []string{doc("Orders") + "Orders are stored in the orders table.", doc("Mail") + "Mails are sent with SMTP.", q + "where are orders saved"})
	if err != nil {
		t.Fatal(err)
	}
	a, b, qv := kn.Normalize(vs[0]), kn.Normalize(vs[1]), kn.Normalize(vs[2])
	cos := func(x, y []float32) (s float64) {
		for i := range x {
			s += float64(x[i]) * float64(y[i])
		}
		return s
	}
	t.Logf("dim %d, cos(orders)=%.3f cos(mail)=%.3f", len(vs[0]), cos(qv, a), cos(qv, b))
	if cos(qv, a) <= cos(qv, b) {
		t.Fatal("the orders text should be closer to the query")
	}
}
