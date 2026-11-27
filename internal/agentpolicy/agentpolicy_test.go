package agentpolicy

import (
	"encoding/json"
	"testing"

	"github.com/ape-dev-de/tiegel/internal/acp"
	"github.com/ape-dev-de/tiegel/internal/api"
)

func shell(cmd string) acp.ToolCall {
	raw, _ := json.Marshal(map[string]string{"command": cmd})
	return acp.ToolCall{Name: "bash", Kind: acp.KindExecute, RawInput: raw}
}

func TestCategory(t *testing.T) {
	cases := []struct {
		tc  acp.ToolCall
		cat string
	}{
		{shell("ls -la"), api.AgentCatShell},
		{shell("cd x && git commit -m y"), api.AgentCatGitCommit},
		{shell("sh -c 'git push origin'"), api.AgentCatGitPush},
		{acp.ToolCall{Name: "edit", Kind: acp.KindEdit}, api.AgentCatEdit},
		{acp.ToolCall{Name: "webfetch"}, api.AgentCatWeb},
		{acp.ToolCall{Name: "mcp_tool"}, api.AgentCatOther},
	}
	for _, c := range cases {
		if got, _ := Category(c.tc); got != c.cat {
			t.Errorf("%+v: %s, want %s", c.tc, got, c.cat)
		}
	}
	p := api.AgentPolicy{Shell: api.PolicyDeny, GitCommit: api.PolicyAsk, GitPush: api.PolicyAsk, Edit: api.PolicyAllow}
	if Decide(p, api.AgentCatGitCommit) != api.PolicyDeny || Decide(p, api.AgentCatEdit) != api.PolicyAllow {
		t.Fatal("decide")
	}
}

func TestReadOnlyAndPaths(t *testing.T) {
	if !IsReadOnly(acp.ToolCall{Name: "read"}) || !IsReadOnly(acp.ToolCall{Kind: acp.KindSearch}) || IsReadOnly(acp.ToolCall{Name: "edit"}) {
		t.Fatal("read only")
	}
	if IsReadOnly(acp.ToolCall{Kind: acp.KindRead, Content: []acp.ToolCallContent{{Type: "diff"}}}) {
		t.Fatal("diff is not read only")
	}
	tc := acp.ToolCall{RawInput: json.RawMessage(`{"filePath":"a.php"}`), Locations: []acp.Location{{Path: "b.php"}, {Path: "a.php"}},
		Content: []acp.ToolCallContent{{Type: "diff", Path: "a.php"}}}
	if got := EditPaths(tc); len(got) != 2 || got[0] != "a.php" || got[1] != "b.php" {
		t.Fatal(got)
	}
	opts := []acp.PermissionOption{{OptionID: "a", Kind: acp.AllowAlways}, {OptionID: "o", Kind: acp.AllowOnce}, {OptionID: "r", Kind: acp.RejectOnce}}
	if AllowOption(opts) != "o" || RejectOption(opts) != "r" || OptionKind(opts, "a") != acp.AllowAlways {
		t.Fatal("options")
	}
}
