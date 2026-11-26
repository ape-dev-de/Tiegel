package agent

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/ape-dev-de/tiegel/internal/acp"
	"github.com/ape-dev-de/tiegel/internal/api"
)

func shellCall(cmd string) acp.ToolCall {
	b, _ := json.Marshal(map[string]string{"command": cmd})
	return acp.ToolCall{ToolCallID: "x", Name: "shell", Kind: acp.KindExecute, RawInput: b}
}

func TestShellCategory(t *testing.T) {
	cases := []struct{ cmd, cat, verb string }{
		{"ls -la", api.AgentCatShell, ""},
		{"git status", api.AgentCatShell, ""},
		{"git log --grep commit", api.AgentCatShell, ""},
		{"git commit -m x", api.AgentCatGitCommit, "git commit"},
		{"cd /p && git commit -am test 2>&1", api.AgentCatGitCommit, "git commit"},
		{"git -C sub -c user.name=x commit --amend", api.AgentCatGitCommit, "git commit"},
		{"GIT_AUTHOR_NAME=x git commit", api.AgentCatGitCommit, "git commit"},
		{"sudo /usr/bin/git reset --hard", api.AgentCatGitCommit, "git reset"},
		{"git add . ; git push origin main", api.AgentCatGitPush, "git push"},
		{"sh -c 'git push'", api.AgentCatGitPush, "git push"},
		{`bash -lc "make && git commit -m \"x\""`, api.AgentCatGitCommit, "git commit"},
		{"git --no-pager stash", api.AgentCatGitCommit, "git stash"},
		{"echo $(git rev-parse HEAD)", api.AgentCatShell, ""},
	}
	for _, c := range cases {
		cat, verb := category(shellCall(c.cmd))
		if cat != c.cat || verb != c.verb {
			t.Errorf("%q: got %s/%q, want %s/%q", c.cmd, cat, verb, c.cat, c.verb)
		}
	}
}

func TestCategoryKinds(t *testing.T) {
	cases := []struct {
		tc  acp.ToolCall
		cat string
	}{
		{acp.ToolCall{Kind: acp.KindEdit}, api.AgentCatEdit},
		{acp.ToolCall{Name: "write"}, api.AgentCatEdit},
		{acp.ToolCall{Kind: acp.KindOther, Content: []acp.ToolCallContent{{Type: "diff", Path: "/a"}}}, api.AgentCatEdit},
		{acp.ToolCall{Name: "webfetch"}, api.AgentCatWeb},
		{acp.ToolCall{Kind: acp.KindFetch}, api.AgentCatWeb},
		{acp.ToolCall{Name: "external_directory", Kind: acp.KindOther}, api.AgentCatOther},
		{acp.ToolCall{Name: "bash"}, api.AgentCatShell},
		{acp.ToolCall{Kind: acp.KindOther, RawInput: []byte(`{"command":"git push"}`)}, api.AgentCatGitPush},
	}
	for i, c := range cases {
		if got, _ := category(c.tc); got != c.cat {
			t.Errorf("%d: %s, want %s", i, got, c.cat)
		}
	}
	if inputField([]byte(`not json`), "x") != "" || inputField(nil, "x") != "" {
		t.Fatal("inputField of garbage")
	}
}

func TestDecideAndSummary(t *testing.T) {
	p := api.AgentPolicy{Edit: api.PolicyAllow, Shell: api.PolicyAsk, GitCommit: api.PolicyDeny, GitPush: api.PolicyAsk, Web: api.PolicyDeny, Other: api.PolicyAsk}
	for cat, want := range map[string]string{api.AgentCatEdit: "allow", api.AgentCatShell: "ask", api.AgentCatGitCommit: "deny",
		api.AgentCatGitPush: "ask", api.AgentCatWeb: "deny", api.AgentCatOther: "ask"} {
		if got := decide(p, cat); got != want {
			t.Errorf("%s: %s", cat, got)
		}
	}
	s := summarize(p).Summary
	if s != "Edits: allowed · Shell: ask · Commits: blocked · Push: ask · Web: blocked" {
		t.Fatal(s)
	}
	p.Shell = api.PolicyDeny
	if decide(p, api.AgentCatGitPush) != api.PolicyDeny || decide(p, api.AgentCatGitCommit) != api.PolicyDeny {
		t.Fatal("denied shell must block git")
	}
	pre := rulesPreamble(api.AgentPolicy{Shell: api.PolicyDeny, GitCommit: api.PolicyDeny, GitPush: api.PolicyDeny, Web: api.PolicyDeny, Rules: "Never touch vendor/."})
	for _, want := range []string{"git commits", "git push", "shell commands", "web access", "Never touch vendor/."} {
		if !strings.Contains(pre, want) {
			t.Errorf("preamble lacks %q:\n%s", want, pre)
		}
	}
	if strings.Contains(rulesPreamble(api.AgentPolicy{Shell: api.PolicyAsk, GitCommit: api.PolicyAsk, GitPush: api.PolicyAsk}), "disabled") {
		t.Fatal("nothing disabled")
	}
	for _, c := range []string{api.AgentCatEdit, api.AgentCatShell, api.AgentCatGitCommit, api.AgentCatGitPush, api.AgentCatWeb, api.AgentCatOther} {
		if label(c) == "" {
			t.Fatal(c)
		}
	}
}

func TestOpencodeConfig(t *testing.T) {
	var cfg struct {
		Permission struct {
			Edit     string            `json:"edit"`
			Bash     map[string]string `json:"bash"`
			Webfetch string            `json:"webfetch"`
		} `json:"permission"`
	}
	raw := opencodeConfig(api.AgentPolicy{Edit: api.PolicyAllow, Shell: api.PolicyAllow, GitCommit: api.PolicyDeny, GitPush: api.PolicyDeny, Web: api.PolicyAllow})
	if err := json.Unmarshal([]byte(raw), &cfg); err != nil {
		t.Fatal(err)
	}
	p := cfg.Permission
	if p.Edit != "ask" || p.Bash["*"] != "ask" || p.Bash["git commit*"] != "deny" || p.Bash["git push*"] != "deny" || p.Webfetch != "ask" {
		t.Fatalf("%s", raw)
	}
	raw = opencodeConfig(api.AgentPolicy{Shell: api.PolicyDeny, GitCommit: api.PolicyAsk, GitPush: api.PolicyAsk, Web: api.PolicyDeny})
	_ = json.Unmarshal([]byte(raw), &cfg)
	if cfg.Permission.Bash["*"] != "deny" || cfg.Permission.Webfetch != "deny" {
		t.Fatalf("%s", raw)
	}
}

func TestOptionsAndKeys(t *testing.T) {
	opts := []acp.PermissionOption{{OptionID: "a", Kind: acp.AllowAlways}, {OptionID: "o", Kind: acp.AllowOnce}, {OptionID: "r", Kind: acp.RejectAlways}}
	if allowOption(opts) != "o" || rejectOption(opts) != "r" || optionKind(opts, "a") != acp.AllowAlways || optionKind(opts, "z") != "" {
		t.Fatal("options")
	}
	if alwaysKey(api.AgentCatShell, shellCall("npm test")) != "shell:npm" || alwaysKey(api.AgentCatGitCommit, shellCall("git commit")) != "" ||
		alwaysKey(api.AgentCatEdit, acp.ToolCall{}) != "edit" || alwaysKey(api.AgentCatOther, acp.ToolCall{Name: "mcp"}) != "other:mcp" ||
		alwaysKey(api.AgentCatShell, acp.ToolCall{}) != "shell" {
		t.Fatal("always keys")
	}
	tc := acp.ToolCall{Content: []acp.ToolCallContent{{Type: "diff", Path: "/a"}}, Locations: []acp.Location{{Path: "/a"}, {Path: "/b"}}, RawInput: []byte(`{"filePath":"/c"}`)}
	if got := editPaths(tc); !slices.Equal(got, []string{"/a", "/b", "/c"}) {
		t.Fatal(got)
	}
	if shorten("abcdef", 3) != "abc …" || shorten("ab", 3) != "ab" {
		t.Fatal("shorten")
	}
	for _, c := range []struct{ cat, verb, want string }{
		{api.AgentCatShell, "", "Run a command"}, {api.AgentCatGitCommit, "git commit", "Run: git commit"},
		{api.AgentCatEdit, "", "Change files"}, {api.AgentCatWeb, "", "Access the web"}, {api.AgentCatOther, "", "Use x"},
	} {
		if got := permSummary(c.cat, c.verb, api.AgentToolCall{Title: "x"}); got != c.want {
			t.Errorf("%s: %q", c.cat, got)
		}
	}
	if got := permSummary(api.AgentCatWeb, "", api.AgentToolCall{Input: `{"url":"https://e.org"}`}); got != "Web: https://e.org" {
		t.Fatal(got)
	}
	if got := permSummary(api.AgentCatOther, "", api.AgentToolCall{Name: "ext", Paths: []string{"/etc"}}); got != "Use ext (/etc)" {
		t.Fatal(got)
	}
}

func TestTrackerAndUnified(t *testing.T) {
	tr := newTracker()
	tr.before("/a", []byte("one\ntwo\n"), true)
	tr.before("/a", []byte("ignored"), true)
	tr.before("/new", nil, false)
	if len(tr.changed()) != 0 {
		t.Fatal("nothing completed yet")
	}
	tr.after("/a", []byte("one\nTWO\nthree\n"), true)
	tr.after("/new", []byte("x"), true)
	tr.after("/untracked", nil, false)
	ch := tr.changed()
	if len(ch) != 2 || ch[0].kind() != api.ChangeModified || ch[1].kind() != api.ChangeCreated {
		t.Fatalf("%+v", ch)
	}
	if !ch[0].conflict([]byte("other"), true) || ch[0].conflict([]byte("one\nTWO\nthree\n"), true) {
		t.Fatal("conflict")
	}
	if a, r := diffStat([]byte("one\ntwo\n"), []byte("one\nTWO\nthree\n")); a != 2 || r != 1 {
		t.Fatal(a, r)
	}
	u := unified("a.txt", []byte("one\ntwo\n"), []byte("one\nTWO\nthree\n"), true, true)
	want := "--- a/a.txt\n+++ b/a.txt\n@@ -1,2 +1,3 @@\n one\n-two\n+TWO\n+three\n"
	if u != want {
		t.Fatalf("unified:\n%s", u)
	}
	if u := unified("n", nil, []byte("x"), false, true); !strings.Contains(u, "--- /dev/null") || !strings.Contains(u, "\\ No newline") || !strings.Contains(u, "@@ -0,0 +1 @@") {
		t.Fatalf("created:\n%s", u)
	}
	if u := unified("d", []byte("x\n"), nil, true, false); !strings.Contains(u, "+++ /dev/null") {
		t.Fatal(u)
	}
	if unified("s", []byte("same"), []byte("same"), true, true) != "" {
		t.Fatal("no diff")
	}
	// Two far apart changes give two hunks.
	var a, b []string
	for i := 0; i < 30; i++ {
		a = append(a, "l")
		b = append(b, "l")
	}
	b[2], b[25] = "X", "Y"
	if u := unified("f", []byte(strings.Join(a, "\n")+"\n"), []byte(strings.Join(b, "\n")+"\n"), true, true); strings.Count(u, "@@ -") != 2 {
		t.Fatalf("hunks:\n%s", u)
	}
	// Back to the original: dropped.
	tr.after("/a", []byte("one\ntwo\n"), true)
	if tr.tracking("/a") {
		t.Fatal("unchanged file still tracked")
	}
	tr.before("/d", []byte("x"), true)
	tr.after("/d", nil, false)
	if tr.files["/d"].kind() != api.ChangeDeleted {
		t.Fatal("deleted")
	}
	tr.clear()
	if len(tr.changed()) != 0 {
		t.Fatal("clear")
	}
}
