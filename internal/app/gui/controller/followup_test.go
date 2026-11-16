package controller

import (
	"encoding/json"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/ape-dev-de/tiegel/internal/api"
	"github.com/ape-dev-de/tiegel/internal/gitcli"
)

// commitHunk is a hunk selection of git.commitSelection as sent.
type commitHunk struct {
	Hunk   int    `json:"hunk"`
	Lines  []int  `json:"lines"`
	Header string `json:"header"`
	Hash   string `json:"hash"`
}

type commitSel struct {
	Paths []string `json:"paths"`
	Hunks []struct {
		Path  string       `json:"path"`
		Hunks []commitHunk `json:"hunks"`
	} `json:"hunks"`
}

// Review 2026-11-16 #6 (GUI): hunk selections carry the header and hash
// of the diff they were made on, computed with the module's own parser.
func TestCommitSendsHunkHeaderAndHash(t *testing.T) {
	f := gitFake()
	f.answer("git.commitSelection", map[string]any{"hash": "abcdef1234"})
	c := started(t, f)
	g := c.Vcs
	g.LoadChanges()
	c.Settle(time.Second)
	files, err := gitcli.ParseDiff([]byte(rawDiff))
	if err != nil || len(files) != 1 || len(files[0].Hunks) != 2 {
		t.Fatalf("parse %v %+v", err, files)
	}
	want := files[0].Hunks
	g.Commit(GitCommitRequest{Message: "Fix", Files: []GitFileSelection{
		{Path: "src/Mailer.php", Hunks: []gitcli.HunkSelection{{Hunk: 1}, {Hunk: 0, Lines: []int{2}}}},
		{Path: "new.php", Untracked: true, Whole: true},
	}})
	c.Settle(time.Second)
	var p commitSel
	lastParams(t, f, "git.commitSelection", &p)
	if len(p.Hunks) != 1 || len(p.Hunks[0].Hunks) != 2 || !reflect.DeepEqual(p.Paths, []string{"new.php"}) {
		t.Fatalf("params %+v", p)
	}
	got := p.Hunks[0].Hunks
	if !reflect.DeepEqual(got[0], commitHunk{Hunk: 1, Header: want[1].Header(), Hash: want[1].Hash()}) ||
		got[1].Header != want[0].Header() || got[1].Hash != want[0].Hash() || got[1].Hash == "" {
		t.Fatalf("hunk identity %+v, want %q/%q and %q/%q", got, want[1].Header(), want[1].Hash(), want[0].Header(), want[0].Hash())
	}

	// Without a known diff (e.g. a synthesized untracked diff) nothing is
	// claimed.
	params := CommitParams(GitCommitRequest{Message: "x", Files: []GitFileSelection{{Path: "a.php", Hunks: []gitcli.HunkSelection{{Hunk: 0}}}}})
	raw, _ := json.Marshal(params)
	var np commitSel
	if err := json.Unmarshal(raw, &np); err != nil || np.Hunks[0].Hunks[0].Hash != "" || np.Hunks[0].Hunks[0].Header != "" {
		t.Fatalf("no diff: %s", raw)
	}
}

// A refused stale selection (-32004, reason staleSelection) reloads the
// changes and marks the problem; other conflicts do not.
func TestStaleSelectionReloadsChanges(t *testing.T) {
	f := gitFake()
	f.on("git.commitSelection", func(json.RawMessage) (any, error) {
		return nil, api.Errorf(api.CodeConflict, "src/Mailer.php: hunk 1 changed (hash mismatch); refresh the diff").
			WithData(map[string]any{"reason": "staleSelection", "path": "src/Mailer.php", "hunk": 1})
	})
	c := started(t, f)
	g := c.Vcs
	g.LoadChanges()
	c.Settle(time.Second)
	diffs := f.called("git.diff")
	g.Commit(GitCommitRequest{Message: "Fix", Files: []GitFileSelection{{Path: "src/Mailer.php", Hunks: []gitcli.HunkSelection{{Hunk: 1}}}}})
	c.Settle(time.Second)
	if pr := g.Problem; pr == nil || !pr.Stale || pr.Err == nil || g.Busy || g.Committed != 0 {
		t.Fatalf("problem %+v busy %v", g.Problem, g.Busy)
	}
	if f.called("git.diff") <= diffs {
		t.Fatal("the diff should be reloaded after a stale selection")
	}

	f.on("git.commitSelection", func(json.RawMessage) (any, error) {
		return nil, api.Errorf(api.CodeConflict, "git.commitSelection is not possible while merge is in progress")
	})
	g.Commit(GitCommitRequest{Message: "Fix", Files: []GitFileSelection{{Path: "x.php", Whole: true}}})
	c.Settle(time.Second)
	if pr := g.Problem; pr == nil || pr.Stale {
		t.Fatalf("merge conflict is no stale selection: %+v", pr)
	}
}

// Review 2026-11-16 #5 (GUI): recovery points are loaded when a project
// opens; they can be shown as a diff, restored or dismissed.
func TestRecoveryPoints(t *testing.T) {
	f := projectFake()
	file := filepath.Join(root, "src", "Mailer.php")
	pt := RecoveryPoint{Path: "src/Mailer.php", File: file, Revision: 7, Time: time.Unix(1700000000, 0).UTC(), Reason: "close"}
	f.answer("history.recovery", []RecoveryPoint{pt})
	f.answer("history.content", map[string]any{"text": "<?php // unsaved\n"})
	f.answer("files.read", map[string]any{"content": "<?php\n", "encoding": "utf-8"})
	f.answer("history.restore", map[string]any{"id": 8})
	f.answer("history.recoveryDismiss", []RecoveryPoint{})
	c := started(t, f)
	if len(c.Recovery) != 1 || c.Recovery[0] != pt {
		t.Fatalf("recovery %+v", c.Recovery)
	}

	c.ShowRecovery(pt)
	c.Settle(time.Second)
	if c.Tab(file) == nil {
		t.Fatal("show should open the file")
	}
	if rd := c.RecoveryShown; rd == nil || rd.Current != "<?php\n" || rd.Recovered != "<?php // unsaved\n" || rd.Point != pt {
		t.Fatalf("shown %+v", c.RecoveryShown)
	}
	var hp struct {
		Path string `json:"path"`
		ID   uint64 `json:"id"`
	}
	lastParams(t, f, "history.content", &hp)
	if hp.Path != file || hp.ID != 7 {
		t.Fatalf("history.content %+v", hp)
	}

	c.RestoreRecovery(pt)
	c.Settle(time.Second)
	lastParams(t, f, "history.restore", &hp)
	if hp.Path != file || hp.ID != 7 {
		t.Fatalf("history.restore %+v", hp)
	}
	var dp struct {
		Paths []string `json:"paths"`
	}
	lastParams(t, f, "history.recoveryDismiss", &dp)
	if !reflect.DeepEqual(dp.Paths, []string{file}) || len(c.Recovery) != 0 || c.RecoveryShown != nil {
		t.Fatalf("after restore: dismiss %+v recovery %+v", dp, c.Recovery)
	}

	c.setRecovery([]RecoveryPoint{pt})
	c.DismissRecovery()
	c.Settle(time.Second)
	dp.Paths = nil
	lastParams(t, f, "history.recoveryDismiss", &dp)
	if dp.Paths != nil || len(c.Recovery) != 0 {
		t.Fatalf("dismiss all: %+v %+v", dp, c.Recovery)
	}
}

// Without history.recovery (older backend) the start stays quiet.
func TestRecoveryUnavailableIsQuiet(t *testing.T) {
	c := started(t, projectFake())
	if c.Recovery != nil || c.Status.Notice != "" {
		t.Fatalf("recovery %+v notice %q", c.Recovery, c.Status.Notice)
	}
}

// Review 2026-11-16 #7 (GUI): the container-not-running hint reaches the
// status; only DetectPHPStarting (a user action) passes start: true.
func TestPHPContainerHintAndExplicitStart(t *testing.T) {
	f := projectFake()
	f.on("php.version", func(raw json.RawMessage) (any, error) {
		var p struct {
			Start bool `json:"start"`
		}
		_ = json.Unmarshal(raw, &p)
		if p.Start {
			return PHPVersion{Version: "8.3.12", Short: "8.3", Source: "interpreter"}, nil
		}
		return PHPVersion{Version: "8.2", Short: "8.2", Source: "require", Hint: &Hint{Severity: "info", Code: HintContainerNotRunning, Message: "container not running"}}, nil
	})
	c := started(t, f)
	if h := c.Status.PHP.Hint; h == nil || h.Code != HintContainerNotRunning || c.Status.PHP.Short != "8.2" {
		t.Fatalf("php status %+v", c.Status.PHP)
	}
	f.mu.Lock()
	for _, raw := range f.params["php.version"] {
		var p map[string]any
		_ = json.Unmarshal(raw, &p)
		if p["start"] != nil {
			t.Errorf("automatic php.version with start: %s", raw)
		}
	}
	f.mu.Unlock()

	c.DetectPHPStarting()
	c.Settle(time.Second)
	var p map[string]any
	lastParams(t, f, "php.version", &p)
	if p["start"] != true {
		t.Fatalf("explicit detection params %v", p)
	}
	if c.Status.PHP.Hint != nil || c.Status.PHP.Short != "8.3" || c.Status.Notice != "PHP 8.3 detected" {
		t.Fatalf("after start: %+v %q", c.Status.PHP, c.Status.Notice)
	}
}

// Revealing a file (tab activation) expands its parents in the tree; they
// are loaded level by level.
func TestLoadAncestors(t *testing.T) {
	f := projectFake()
	c := started(t, f)
	src := c.Tree.Node("src")
	if src == nil || src.Loaded {
		t.Fatalf("src %+v", src)
	}
	c.LoadAncestors("src/Mailer.php")
	c.Settle(time.Second)
	if !src.Loaded || c.Tree.Node("src/Mailer.php") == nil {
		t.Fatalf("src not loaded: %+v", src)
	}
	n := f.called("files.list")
	c.LoadAncestors("src/Mailer.php") // already loaded: no call
	c.LoadAncestors("deep/unknown/x.php")
	c.LoadAncestors("")
	c.Settle(time.Second)
	if f.called("files.list") != n {
		t.Fatal("unexpected files.list")
	}
}
