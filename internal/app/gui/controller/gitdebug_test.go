package controller

import (
	"encoding/json"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/ape-dev-de/tiegel/internal/api"
	"github.com/ape-dev-de/tiegel/internal/gitcli"
)

func lastParams(t *testing.T, f *fakeBackend, method string, v any) {
	t.Helper()
	f.mu.Lock()
	ps := f.params[method]
	f.mu.Unlock()
	if len(ps) == 0 {
		t.Fatalf("%s not called (calls %v)", method, f.calls)
	}
	if err := json.Unmarshal(ps[len(ps)-1], v); err != nil {
		t.Fatal(err)
	}
}

func waitFor(t *testing.T, c *Controller, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timeout waiting for %s", what)
		}
		c.Settle(50 * time.Millisecond)
	}
}

const rawDiff = `diff --git a/src/Mailer.php b/src/Mailer.php
index 1111111..2222222 100644
--- a/src/Mailer.php
+++ b/src/Mailer.php
@@ -1,3 +1,4 @@
 <?php
-$a = 1;
+$a = 2;
+$b = 3;
 echo $a;
@@ -10,2 +11,2 @@ function x()
-old
+new
 end
`

func gitFake() *fakeBackend {
	f := projectFake()
	f.answer("git.status", map[string]any{"branch": "main", "upstream": "origin/main", "ahead": 2, "behind": 1, "files": []map[string]any{
		{"path": "src/Mailer.php", "kind": "ordinary", "index": ".", "worktree": "M"},
		{"path": "new.php", "kind": "untracked", "index": "?", "worktree": "?"},
	}})
	f.answer("git.diff", map[string]any{"raw": rawDiff})
	f.answer("git.recentMessages", []string{"Last message", "Older"})
	f.answer("files.read", map[string]any{"content": "<?php\necho 1;\n", "encoding": "utf-8"})
	return f
}

func TestGitStatusAndCommitWindow(t *testing.T) {
	f := gitFake()
	c := started(t, f)
	g := c.Vcs
	if !g.Available || g.Info.Branch != "main" || g.Info.Ahead != 2 || g.Info.Behind != 1 || g.Info.Upstream != "origin/main" {
		t.Fatalf("git info %+v available %v", g.Info, g.Available)
	}
	g.LoadChanges()
	c.Settle(time.Second)
	if f.called("autosave.trigger") == 0 {
		t.Fatal("documents should be saved before the diff is computed")
	}
	if len(g.Changes) != 2 || g.Changes[0].Diff == nil || len(g.Changes[0].Diff.Hunks) != 2 || g.Changes[1].Diff != nil {
		t.Fatalf("changes %+v", g.Changes)
	}
	if g.Changes[1].Status.Kind != gitcli.KindUntracked || g.Changes[0].Status.Worktree != 'M' {
		t.Fatalf("status conversion %+v", g.Changes)
	}
	var dp map[string]any
	lastParams(t, f, "git.diff", &dp)
	if dp["base"] != "HEAD" || dp["raw"] != true {
		t.Fatalf("diff params %v", dp)
	}
	if !reflect.DeepEqual(g.History, []string{"Last message", "Older"}) {
		t.Fatalf("history %v", g.History)
	}
	g.LoadUntrackedDiff("new.php")
	c.Settle(time.Second)
	if d := g.Diffs["new.php"]; d == nil || !d.IsNew || len(d.Hunks) != 1 || len(d.Hunks[0].Lines) != 2 || d.Hunks[0].Lines[1].Text != "echo 1;" {
		t.Fatalf("untracked diff %+v", g.Diffs["new.php"])
	}
	g.LoadAmendMessage()
	c.Settle(time.Second)
	if g.AmendMessage != "Last message" || g.AmendVersion == 0 {
		t.Fatalf("amend %q", g.AmendMessage)
	}
}

func TestCommitWithHunkSelection(t *testing.T) {
	f := gitFake()
	f.answer("git.commitSelection", map[string]any{"hash": "abcdef1234", "message": "Fix", "pushed": false})
	c := started(t, f)
	g := c.Vcs
	g.Commit(GitCommitRequest{Message: "Fix", Files: []GitFileSelection{
		{Path: "src/Mailer.php", Hunks: []gitcli.HunkSelection{{Hunk: 0, Lines: []int{1, 2}}, {Hunk: 1}}},
		{Path: "new.php", Untracked: true, Whole: true},
		{Path: "b.php", OrigPath: "a.php", Whole: true},
	}})
	if !g.Busy {
		t.Fatal("busy while committing")
	}
	c.Settle(time.Second)
	var p struct {
		Message string   `json:"message"`
		Paths   []string `json:"paths"`
		Hunks   []struct {
			Path  string `json:"path"`
			Hunks []struct {
				Hunk  int   `json:"hunk"`
				Lines []int `json:"lines"`
			} `json:"hunks"`
		} `json:"hunks"`
		Amend bool `json:"amend"`
		Push  bool `json:"push"`
	}
	lastParams(t, f, "git.commitSelection", &p)
	if p.Message != "Fix" || !reflect.DeepEqual(p.Paths, []string{"new.php", "b.php", "a.php"}) || len(p.Hunks) != 1 ||
		p.Hunks[0].Path != "src/Mailer.php" || len(p.Hunks[0].Hunks) != 2 || !reflect.DeepEqual(p.Hunks[0].Hunks[0].Lines, []int{1, 2}) ||
		p.Hunks[0].Hunks[1].Lines != nil || p.Amend || p.Push {
		t.Fatalf("commit params %+v", p)
	}
	if g.Busy || g.Committed != 1 || g.LastCommit != "abcdef1234" || g.Problem != nil || !strings.Contains(c.Status.Notice, "abcdef1") {
		t.Fatalf("after commit: busy %v committed %d problem %+v notice %q", g.Busy, g.Committed, g.Problem, c.Status.Notice)
	}
	if f.called("git.log") == 0 || f.called("git.branches") == 0 {
		t.Fatal("log and branches should reload after a commit")
	}

	// Amend without files rewords the last commit.
	params := CommitParams(GitCommitRequest{Message: "Reword", Amend: true})
	if params["amend"] != true || params["allowEmpty"] != true || params["paths"] != nil {
		t.Fatalf("amend params %v", params)
	}

	// Commit and push: the push fails, the commit exists.
	f.answer("git.commitSelection", map[string]any{"hash": "1234567", "pushError": map[string]any{"kind": "rejected", "message": "non-fast-forward", "stderr": "! [rejected]"}})
	g.Commit(GitCommitRequest{Message: "More", Push: true, Files: []GitFileSelection{{Path: "x.php", Whole: true}}})
	c.Settle(time.Second)
	lastParams(t, f, "git.commitSelection", &p)
	if !p.Push {
		t.Fatal("push flag")
	}
	if pr := g.Problem; pr == nil || !pr.Push || !pr.Committed || pr.PushKind != gitcli.PushRejected || pr.Stderr != "! [rejected]" {
		t.Fatalf("push problem %+v", g.Problem)
	}

	// A failing commit keeps the form (no Committed bump).
	f.on("git.commitSelection", func(json.RawMessage) (any, error) {
		return nil, api.Errorf(api.CodeFailed, "commit.before: blocked")
	})
	before := g.Committed
	g.Commit(GitCommitRequest{Message: "x", Files: []GitFileSelection{{Path: "x.php", Whole: true}}})
	c.Settle(time.Second)
	if g.Committed != before || g.Problem == nil || g.Problem.Err == nil {
		t.Fatalf("failed commit: %+v", g.Problem)
	}
}

func TestPushErrorFromErrorData(t *testing.T) {
	f := gitFake()
	f.on("git.push", func(json.RawMessage) (any, error) {
		return nil, api.Errorf(api.CodeFailed, "push failed (auth): denied").WithData(map[string]string{"kind": "auth", "message": "denied", "stderr": "fatal: auth"})
	})
	c := started(t, f)
	c.Vcs.Push()
	c.Settle(time.Second)
	pr := c.Vcs.Problem
	if pr == nil || !pr.Push || pr.Committed || pr.PushKind != gitcli.PushAuthFailed || pr.Message != "denied" {
		t.Fatalf("problem %+v", pr)
	}
	if !strings.Contains(c.Status.Notice, "Push failed") {
		t.Fatalf("notice %q", c.Status.Notice)
	}
	f.answer("git.push", map[string]string{"remote": "origin", "branch": "main", "remoteBranch": "main"})
	c.Vcs.Push()
	c.Settle(time.Second)
	if c.Vcs.Problem != nil || c.Status.Notice != "Pushed main to origin/main" {
		t.Fatalf("push ok: %+v %q", c.Vcs.Problem, c.Status.Notice)
	}
}

func TestGutterMarkersUpdate(t *testing.T) {
	old := GutterDelay
	GutterDelay = 10 * time.Millisecond
	t.Cleanup(func() { GutterDelay = old })
	f := gitFake()
	ranges := []GutterRange{{Kind: "modified", Start: 2, End: 3, BaseStart: 2, BaseEnd: 3}}
	f.on("git.gutter", func(json.RawMessage) (any, error) {
		f.mu.Lock()
		defer f.mu.Unlock()
		return map[string]any{"ranges": ranges}, nil
	})
	c := started(t, f)
	file := filepath.Join(root, "src/Mailer.php")
	c.OpenFile("src/Mailer.php", nil)
	c.Settle(time.Second)
	g := c.Vcs
	if g.Gutter(file) != nil {
		t.Fatal("first call only starts loading")
	}
	c.Settle(time.Second)
	if got := g.Gutter(file); len(got) != 1 || got[0].Kind != "modified" {
		t.Fatalf("gutter %+v", got)
	}
	if r, ok := g.RangeAt(file, 2); !ok || r.Start != 2 {
		t.Fatal("range at line 2")
	}
	if _, ok := g.RangeAt(file, 3); ok {
		t.Fatal("end is exclusive")
	}
	n := f.called("git.gutter")
	// An edit reloads after the debounce delay; several edits coalesce.
	f.mu.Lock()
	ranges = append(ranges, GutterRange{Kind: "added", Start: 5, End: 7})
	f.mu.Unlock()
	for i := 0; i < 5; i++ {
		f.emit(api.TopicDocEdited, map[string]any{"path": "src/Mailer.php", "file": file})
	}
	waitFor(t, c, "gutter after edit", func() bool { return len(g.Gutter(file)) == 2 })
	if got := f.called("git.gutter") - n; got > 2 {
		t.Fatalf("edits not debounced: %d reloads", got)
	}
	// git.changed (e.g. a commit) reloads as well.
	f.mu.Lock()
	ranges = nil
	f.mu.Unlock()
	f.emit("git.changed", map[string]any{"op": "commit"})
	waitFor(t, c, "gutter after commit", func() bool { return len(g.Gutter(file)) == 0 })

	// Deletion ranges belong to their start line.
	del := GutterRange{Kind: "deleted", Start: 4, End: 4}
	if !del.Contains(4) || del.Contains(5) {
		t.Fatal("deletion contains")
	}

	// Block popup data and rollback.
	f.mu.Lock()
	ranges = []GutterRange{{Kind: "modified", Start: 2, End: 3, BaseStart: 2, BaseEnd: 3}}
	f.mu.Unlock()
	f.emit("git.changed", map[string]any{"op": "x"})
	waitFor(t, c, "gutter", func() bool { return len(g.Gutter(file)) == 1 })
	f.answer("git.show", map[string]any{"content": "<?php\n$this->old();\n"})
	f.answer("doc.text", map[string]any{"text": "<?php\n$this->send();\n"})
	f.answer("git.rollback", DocState{})
	g.ShowBlock(file, 2)
	c.Settle(time.Second)
	if b := g.Block; b == nil || len(b.Diff.Hunks) != 1 || len(b.Diff.Hunks[0].Lines) != 2 ||
		b.Diff.Hunks[0].Lines[0].Text != "$this->old();" || b.Diff.Hunks[0].Lines[1].Kind != gitcli.LineAdded {
		t.Fatalf("block %+v", g.Block)
	}
	g.Rollback(file, 2)
	c.Settle(time.Second)
	var rp struct {
		Path string `json:"path"`
		Line int    `json:"line"`
	}
	lastParams(t, f, "git.rollback", &rp)
	if rp.Path != file || rp.Line != 2 || g.Block != nil {
		t.Fatalf("rollback %+v block %v", rp, g.Block)
	}
	// Disabled module: no markers.
	f.on("git.status", func(json.RawMessage) (any, error) {
		return nil, api.Errorf(api.CodeModuleDisabled, "module git is disabled")
	})
	f.emit("modules.changed", map[string]any{})
	waitFor(t, c, "git unavailable", func() bool { return !g.Available })
	if g.Gutter(file) != nil {
		t.Fatal("no gutter without git")
	}
}

func TestLogBranchesMerge(t *testing.T) {
	f := gitFake()
	page := func(skip, n int) []map[string]any {
		var out []map[string]any
		for i := 0; i < n; i++ {
			out = append(out, map[string]any{"hash": strings.Repeat("a", 39) + string(rune('0'+(skip+i)%10)), "parents": []string{"p"}, "subject": "c"})
		}
		return out
	}
	f.on("git.log", func(raw json.RawMessage) (any, error) {
		var p struct{ Skip int }
		_ = json.Unmarshal(raw, &p)
		if p.Skip == 0 {
			return page(0, LogPage), nil
		}
		return page(p.Skip, 3), nil
	})
	f.answer("git.branches", []map[string]any{{"name": "main", "current": true}, {"name": "origin/feature", "remote": true}})
	f.answer("git.checkout", map[string]any{})
	f.answer("git.createBranch", []any{})
	f.answer("git.deleteBranch", []any{})
	c := started(t, f)
	g := c.Vcs
	g.LoadLog(true)
	c.Settle(time.Second)
	if len(g.Log) != LogPage || !g.LogMore {
		t.Fatalf("log %d more %v", len(g.Log), g.LogMore)
	}
	v := g.LogVersion
	g.LoadLog(false)
	c.Settle(time.Second)
	if len(g.Log) != LogPage+3 || g.LogMore || g.LogVersion != v {
		t.Fatalf("paged log %d more %v", len(g.Log), g.LogMore)
	}
	g.SelectCommit(g.Log[0].Hash)
	c.Settle(time.Second)
	var dp map[string]any
	lastParams(t, f, "git.diff", &dp)
	if dp["base"] != "p" || dp["target"] != g.Log[0].Hash || len(g.CommitFiles) != 1 {
		t.Fatalf("commit diff %v files %d", dp, len(g.CommitFiles))
	}
	g.LoadBranches()
	c.Settle(time.Second)
	if len(g.Branches) != 2 || g.Branches[1].Ref != "refs/remotes/origin/feature" {
		t.Fatalf("branches %+v", g.Branches)
	}
	g.Checkout("origin/feature", true)
	c.Settle(time.Second)
	var cb map[string]any
	lastParams(t, f, "git.createBranch", &cb)
	if cb["name"] != "feature" || cb["start"] != "origin/feature" || cb["checkout"] != true {
		t.Fatalf("tracking branch %v", cb)
	}
	g.DeleteBranch("old", false)
	c.Settle(time.Second)
	lastParams(t, f, "git.deleteBranch", &cb)
	if cb["name"] != "old" {
		t.Fatalf("delete %v", cb)
	}

	// Conflicts → merge data → resolve.
	f.answer("git.status", map[string]any{"branch": "main", "files": []map[string]any{{"path": "a.php", "kind": "unmerged", "conflict": true}}})
	f.emit("git.changed", map[string]any{"op": "merge"})
	waitFor(t, c, "conflicts", func() bool { return len(g.Conflicts) == 1 })
	if !strings.Contains(c.Status.Notice, "Merge conflicts") || !g.IsConflict("a.php") {
		t.Fatalf("conflict hint %q", c.Status.Notice)
	}
	f.answer("git.mergeData", map[string]any{"path": "a.php", "blocks": []map[string]any{
		{"kind": "unchanged", "base": "a\n", "ours": "a\n", "theirs": "a\n", "baseStart": 1, "oursStart": 1, "theirsStart": 1},
		{"kind": "conflict", "base": "b\n", "ours": "B1\n", "theirs": "B2\n", "baseStart": 2, "oursStart": 2, "theirsStart": 2},
	}})
	f.answer("git.resolve", map[string]any{"path": "a.php"})
	g.OpenMerge("a.php")
	c.Settle(time.Second)
	m := g.Merges["a.php"]
	if m == nil || m.Result.Conflicts() != 1 || m.Result.Blocks[1].Ours[0] != "B1\n" || m.Result.Blocks[1].OursStart != 1 {
		t.Fatalf("merge %+v", m)
	}
	g.ApplyMerge("a.php", []byte("a\nB1\n"))
	c.Settle(time.Second)
	var rp map[string]any
	lastParams(t, f, "git.resolve", &rp)
	if rp["content"] != "a\nB1\n" || g.Merges["a.php"] != nil {
		t.Fatalf("resolve %v", rp)
	}
}

func debugFake() *fakeBackend {
	f := gitFake()
	var bps []Breakpoint
	f.on("debug.breakpoints.list", func(json.RawMessage) (any, error) {
		return map[string]any{"breakpoints": bps}, nil
	})
	f.on("debug.breakpoints.set", func(raw json.RawMessage) (any, error) {
		var p struct {
			File      string
			Line      int
			Condition string
			Enabled   *bool
		}
		_ = json.Unmarshal(raw, &p)
		rel, _ := filepath.Rel(root, p.File)
		rel = filepath.ToSlash(rel)
		bps = append(bps, Breakpoint{ID: rel + ":" + string(rune('0'+p.Line)), Path: rel, Line: p.Line, Condition: p.Condition, Enabled: p.Enabled == nil || *p.Enabled})
		return map[string]any{"breakpoints": bps}, nil
	})
	f.on("debug.breakpoints.remove", func(json.RawMessage) (any, error) {
		bps = nil
		return map[string]any{"breakpoints": bps}, nil
	})
	f.answer("debug.watches", []DebugWatch{{Expr: "$count", Value: &DebugVariable{Type: "int", Value: "3"}}})
	f.answer("debug.watches.set", []string{"$count"})
	f.answer("debug.listen", map[string]any{"listening": true, "addr": "127.0.0.1:9003"})
	f.answer("debug.stop", map[string]any{})
	f.answer("runtime.diagnose", Diagnosis{Hints: []Hint{{Severity: "warning", Code: "mode", Message: "xdebug.mode lacks debug"}}})
	for _, m := range []string{"debug.run", "debug.stepOver", "debug.stepInto", "debug.stepOut", "debug.stopSession"} {
		f.answer(m, map[string]any{"status": "running"})
	}
	f.answer("debug.stack", []map[string]any{{"Level": 0, "Where": "{main}", "File": filepath.Join(root, "index.php"), "Line": 3}, {"Level": 1, "Where": "x", "File": filepath.Join(root, "src/Mailer.php"), "Line": 2}})
	f.on("debug.variables", func(raw json.RawMessage) (any, error) {
		var p struct {
			FullName string `json:"fullname"`
			Page     int    `json:"page"`
			Depth    int    `json:"depth"`
		}
		_ = json.Unmarshal(raw, &p)
		switch {
		case p.FullName == "$list" && p.Page == 0:
			return map[string]any{"variables": []map[string]any{{"name": "0", "fullname": "$list[0]", "type": "int", "value": "1"}}, "parent": map[string]any{"numChildren": 2, "pageSize": 1}, "hasMore": true}, nil
		case p.FullName == "$list":
			return map[string]any{"variables": []map[string]any{{"name": "1", "fullname": "$list[1]", "type": "int", "value": "2"}}}, nil
		}
		return map[string]any{"variables": []map[string]any{
			{"name": "$count", "fullname": "$count", "type": "int", "value": fmtInt(3 + p.Depth)},
			{"name": "$list", "fullname": "$list", "type": "array", "hasChildren": true, "numChildren": 2},
		}}, nil
	})
	return f
}

func fmtInt(n int) string { return string(rune('0' + n)) }

// TestDebugEventSequence: session → break → stack/variables loaded →
// step → running → end.
func TestDebugEventSequence(t *testing.T) {
	f := debugFake()
	c := started(t, f)
	d := c.Debug
	if !d.Available || len(d.Watches) != 1 {
		t.Fatalf("debug available %v watches %+v", d.Available, d.Watches)
	}
	d.Listen()
	c.Settle(time.Second)
	if !d.Listening || d.Addr != "127.0.0.1:9003" || len(d.Hints) != 1 {
		t.Fatalf("listening %v %q hints %+v", d.Listening, d.Addr, d.Hints)
	}
	// Hints only once.
	d.DismissHints()
	f.emit("debug.stopped", map[string]any{})
	c.Settle(200 * time.Millisecond)
	d.Listen()
	c.Settle(time.Second)
	if len(d.Hints) != 0 || f.called("runtime.diagnose") != 1 {
		t.Fatal("hints should be checked only on the first start")
	}

	f.emit(api.TopicDebugSessionStarted, map[string]any{"id": "s1", "file": "file:///app/index.php", "status": "starting"})
	c.Settle(time.Second)
	if len(d.Sessions) != 1 || d.Current != "s1" || f.called("debug.run") != 1 {
		t.Fatalf("session %+v run %d", d.Sessions, f.called("debug.run"))
	}
	f.emit("debug.status", map[string]any{"session": "s1", "status": "running"})
	f.emit("debug.status", map[string]any{"session": "s1", "status": "break"})
	f.emit(api.TopicDebugBreak, api.DebugBreakEvent{Session: "s1", File: filepath.Join(root, "index.php"), Path: "index.php", Line: 3})
	c.Settle(time.Second)
	if !d.Paused() || d.Exec == nil || d.Exec.Line != 3 || d.Exec.File != filepath.Join(root, "index.php") || d.BreakVersion != 1 {
		t.Fatalf("exec %+v state %q", d.Exec, d.State())
	}
	if len(d.Frames) != 2 || len(d.Variables) != 2 || d.Variables[0].Value != "3" || d.Watches[0].Value == nil {
		t.Fatalf("stop data frames %d vars %+v watches %+v", len(d.Frames), d.Variables, d.Watches)
	}
	if tab := c.ActiveTab(); tab == nil || tab.File != filepath.Join(root, "index.php") || c.Reveal == nil || c.Reveal.Range.Start.Line != 3 || c.Reveal.Range.Start.Offset != -1 {
		t.Fatalf("break should open index.php at line 3: %+v", c.Reveal)
	}
	// Lazy children with paging.
	d.Expand("$list", 0)
	c.Settle(time.Second)
	list := FindVariable(d.Variables, "$list")
	if len(list.Children) != 1 || list.PageSize != 1 {
		t.Fatalf("children %+v", list)
	}
	d.Expand("$list", 1)
	c.Settle(time.Second)
	if len(list.Children) != 2 || list.Page != 1 {
		t.Fatalf("page 2 %+v", list)
	}
	// Frame selection loads that frame's variables.
	d.SelectFrame(1)
	c.Settle(time.Second)
	if d.SelectedFrame != 1 || d.Variables[0].Value != "4" {
		t.Fatalf("frame vars %+v", d.Variables[0])
	}
	// Watches set and evaluated.
	d.SetWatches([]string{"$count"})
	c.Settle(time.Second)
	var wp map[string]any
	lastParams(t, f, "debug.watches.set", &wp)
	if !reflect.DeepEqual(wp["expressions"], []any{"$count"}) {
		t.Fatalf("watches %v", wp)
	}
	// Step → running clears the execution point.
	d.Step("stepOver")
	c.Settle(time.Second)
	var sp map[string]string
	lastParams(t, f, "debug.stepOver", &sp)
	if sp["session"] != "s1" {
		t.Fatalf("step params %v", sp)
	}
	f.emit("debug.status", map[string]any{"session": "s1", "status": "running"})
	c.Settle(200 * time.Millisecond)
	if d.Exec != nil || d.Frames != nil || d.Paused() {
		t.Fatal("running clears the stop state")
	}
	d.Step("stepInto") // ignored while running
	c.Settle(100 * time.Millisecond)
	if f.called("debug.stepInto") != 0 {
		t.Fatal("stepping needs a paused session")
	}
	// The script ends: stopping → stop the session; then it ends.
	f.emit("debug.status", map[string]any{"session": "s1", "status": "stopping"})
	c.Settle(time.Second)
	if f.called("debug.stopSession") != 1 {
		t.Fatal("stopping should stop the session")
	}
	f.emit(api.TopicDebugSessionEnded, map[string]any{"session": "s1"})
	c.Settle(200 * time.Millisecond)
	if len(d.Sessions) != 0 || d.Current != "" {
		t.Fatalf("sessions %+v", d.Sessions)
	}
	d.ToggleListen()
	c.Settle(time.Second)
	if d.Listening || f.called("debug.stop") != 1 {
		t.Fatal("toggle listen should stop")
	}
}

func TestBreakpointsToggleAndEvents(t *testing.T) {
	f := debugFake()
	c := started(t, f)
	d := c.Debug
	file := filepath.Join(root, "index.php")
	d.ToggleBreakpoint(file, 2)
	c.Settle(time.Second)
	b, ok := d.BreakpointAt(file, 2)
	if !ok || !b.Enabled || b.File != file {
		t.Fatalf("breakpoint %+v (%v)", d.Breakpoints, ok)
	}
	var p map[string]any
	lastParams(t, f, "debug.breakpoints.set", &p)
	if p["file"] != file || p["line"] != float64(2) {
		t.Fatalf("set params %v", p)
	}
	d.ToggleBreakpoint(file, 2)
	c.Settle(time.Second)
	if _, ok := d.BreakpointAt(file, 2); ok || f.called("debug.breakpoints.remove") != 1 {
		t.Fatal("second toggle removes")
	}
	d.SetCondition(file, 5, " $x > 1 ")
	c.Settle(time.Second)
	lastParams(t, f, "debug.breakpoints.set", &p)
	if p["condition"] != "$x > 1" {
		t.Fatalf("condition %v", p)
	}
	// Changes from other clients arrive as event.
	f.emit(api.TopicDebugBreakpoints, []Breakpoint{{ID: "src/Mailer.php:7", Path: "src/Mailer.php", Line: 7, Enabled: false}})
	c.Settle(200 * time.Millisecond)
	if b, ok := d.BreakpointAt(filepath.Join(root, "src/Mailer.php"), 7); !ok || b.Enabled {
		t.Fatalf("event breakpoints %+v", d.Breakpoints)
	}
	f.answer("debug.breakpoints.enable", map[string]any{"breakpoints": []Breakpoint{}})
	d.EnableBreakpoint("src/Mailer.php:7", true)
	c.Settle(time.Second)
	lastParams(t, f, "debug.breakpoints.enable", &p)
	if p["id"] != "src/Mailer.php:7" || p["enabled"] != true {
		t.Fatalf("enable %v", p)
	}
	// Module disabled → unavailable.
	f.on("debug.breakpoints.list", func(json.RawMessage) (any, error) {
		return nil, api.Errorf(api.CodeModuleDisabled, "disabled")
	})
	f.emit("modules.changed", map[string]any{})
	waitFor(t, c, "debug unavailable", func() bool { return !d.Available })
	d.ToggleBreakpoint(file, 3)
	c.Settle(100 * time.Millisecond)
	if len(d.Breakpoints) != 0 {
		t.Fatal("no breakpoints without module")
	}
}
