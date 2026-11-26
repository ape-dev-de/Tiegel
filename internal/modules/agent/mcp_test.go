package agent

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/ape-dev-de/tiegel/internal/acp"
	"github.com/ape-dev-de/tiegel/internal/acp/acptest"
	"github.com/ape-dev-de/tiegel/internal/api"
)

// MCP servers other modules offer (the project knowledge) are passed in
// session/new (D563).
func TestMCPServersFromHook(t *testing.T) {
	f := newFixture(t, &acptest.Agent{}, nil)
	api.HookAgentMCPServers.On(f.app.Hooks(), 0, func(_ context.Context, l []api.AgentMCPServer) ([]api.AgentMCPServer, error) {
		return append(l, api.AgentMCPServer{Name: "kb", Command: "/bin/tiegel", Args: []string{"--headless", "mcp", "knowledge"}, Env: []string{"A=1", "broken"}}), nil
	})
	var st api.AgentStatus
	f.call("agent.start", nil, &st)
	news := f.fake.NewSessions()
	if len(news) != 1 || len(news[0].MCPServers) != 1 {
		t.Fatalf("%+v", news)
	}
	b, _ := json.Marshal(news[0].MCPServers[0])
	var srv acp.MCPServerStdio
	_ = json.Unmarshal(b, &srv)
	if srv.Name != "kb" || srv.Command != "/bin/tiegel" || len(srv.Args) != 3 || len(srv.Env) != 1 || srv.Env[0].Name != "A" || srv.Env[0].Value != "1" {
		t.Fatalf("%+v", srv)
	}
}
