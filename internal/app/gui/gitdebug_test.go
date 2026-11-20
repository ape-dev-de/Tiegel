package gui

import (
	"bufio"
	"context"
	"fmt"
	"image/png"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"gioui.org/io/key"
	"gioui.org/layout"

	"github.com/ape-dev-de/tiegel/internal/app/gui/controller"
	"github.com/ape-dev-de/tiegel/internal/dbgp"
	"github.com/ape-dev-de/tiegel/internal/ui/editorview"
	gitui "github.com/ape-dev-de/tiegel/internal/ui/organisms/git"
	"github.com/ape-dev-de/tiegel/internal/ui/organisms/toolbar"
	"github.com/ape-dev-de/tiegel/internal/ui/tokens"
	"github.com/ape-dev-de/tiegel/internal/ui/uitest"
)

// ---- helpers ------------------------------------------------------------------

func runGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return string(out)
}

// newGitEnv is an env whose project is a git repository with one commit.
func newGitEnv(t *testing.T) *env {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	return newEnvWith(t, true, nil, func(root string) {
		runGit(t, root, "init", "-q", "-b", "main")
		// Byte-exact working tree: ignore core.autocrlf=true from the
		// system config (Git for Windows); the app's git calls read it.
		runGit(t, root, "config", "core.autocrlf", "false")
		runGit(t, root, "config", "user.name", "Tiegel Test")
		runGit(t, root, "config", "user.email", "test@example.invalid")
		runGit(t, root, "config", "commit.gpgsign", "false")
		runGit(t, root, "add", ".")
		runGit(t, root, "commit", "-q", "-m", "Initial commit")
	})
}

func (e *env) key(name key.Name, mods key.Modifiers) {
	e.h.Router.Queue(key.Event{Name: name, Modifiers: mods, State: key.Press})
	e.frame()
	e.frame()
}

func (e *env) shot(name string) {
	dir := os.Getenv("TIEGEL_SHOTS")
	for _, mode := range []string{"dark", "light"} {
		e.ctl.Prefs.Theme = mode
		e.frame()
		img, err := uitest.Render(e.v.th, windowSize, func(gtx layout.Context, th *tokens.Theme) layout.Dimensions {
			return e.v.Frame(gtx)
		})
		if err != nil {
			e.t.Logf("headless rendering unavailable: %v", err)
			return
		}
		if n := uitest.CountDiffering(img, e.v.th.Color.Background); n < 20000 {
			e.t.Errorf("%s %s: only %d pixels drawn", name, mode, n)
		}
		if dir == "" {
			continue
		}
		f, err := os.Create(filepath.Join(dir, name+"-"+mode+".png"))
		if err != nil {
			e.t.Fatal(err)
		}
		_ = png.Encode(f, img)
		f.Close()
	}
	e.ctl.Prefs.Theme = "dark"
	e.frame()
}

func hasMarker(p editorview.MarkerProvider, line int, k editorview.MarkerKind) bool {
	if p == nil {
		return false
	}
	for _, m := range p.Markers(line) {
		if m.Kind == k {
			return true
		}
	}
	return false
}

// ---- git ------------------------------------------------------------------------

// TestGitEndToEnd: a change in the editor shows a gutter marker, the
// commit window commits it through git.commitSelection and git log
// contains the commit; the git windows go away with the module.
func TestGitEndToEnd(t *testing.T) {
	old := controller.GutterDelay
	controller.GutterDelay = 20 * time.Millisecond
	t.Cleanup(func() { controller.GutterDelay = old })
	e := newGitEnv(t)
	e.waitFor("git available", func() bool { return e.ctl.Vcs.Available })
	e.frame()
	if _, ok := e.v.Registry.ToolWindow(ToolCommit); !ok {
		t.Fatal("commit window not installed")
	}
	// The branch is shown in the header only, not in the footer.
	if e.v.hdr.bar.Branch != "main" {
		t.Fatalf("header branch %q", e.v.hdr.bar.Branch)
	}
	for _, it := range append(e.v.status.Left, e.v.status.Right...) {
		if strings.HasPrefix(it.Text, "main") {
			t.Fatalf("branch in the footer: %+v", it)
		}
	}

	e.ctl.OpenFile("src/Shop.php", nil)
	e.waitFor("tab", func() bool { return e.ctl.ActiveTab() != nil })
	file := filepath.Join(e.root, "src", "Shop.php")
	markers := func() editorview.MarkerProvider { return e.v.Registry.markers(file) }
	e.frame()
	e.settle()
	if hasMarker(markers(), 1, editorview.MarkerGitAdded) {
		t.Fatal("no changes yet")
	}
	// Edit through the API (like any client); the buffer is shared.
	if err := e.ctl.Backend().Call(context.Background(), "doc.insert", map[string]any{"path": file, "at": map[string]int{"line": 2}, "text": "// added\n"}, nil); err != nil {
		t.Fatal(err)
	}
	e.waitFor("added marker", func() bool { return hasMarker(markers(), 1, editorview.MarkerGitAdded) })

	// Clicking the marker opens the change popup.
	if !e.v.gitd.gutterClick(file, editorview.GutterClickEvent{Line: 1, Area: editorview.GutterChanges}) {
		t.Fatal("click on change not handled")
	}
	e.waitFor("change popup", func() bool { return e.v.popup != nil })
	if !strings.Contains(e.v.popup.Title, "added") {
		t.Fatalf("popup %q", e.v.popup.Title)
	}
	e.key(key.NameEscape, 0)
	if e.v.popup != nil || e.ctl.Vcs.Block != nil {
		t.Fatal("escape closes the popup")
	}

	// Cmd+K opens the commit window with the change.
	e.key("K", key.ModCommand)
	if !e.v.ToolShown(ToolCommit) {
		t.Fatal("Cmd+K should open the commit window")
	}
	e.waitFor("changes", func() bool { return len(e.v.gitd.panel.Selection()) == 1 })
	sel := e.v.gitd.panel.Selection()
	if sel[0].Path != "src/Shop.php" || !sel[0].Whole {
		t.Fatalf("selection %+v", sel)
	}
	e.shot("commit")
	uitest.Smoke(t, "commit-window", windowSize, func(gtx layout.Context, th *tokens.Theme) layout.Dimensions {
		e.ctl.Prefs.Theme = th.Mode.String()
		return e.v.Frame(gtx)
	})

	// Commit what the panel selected.
	e.v.gitd.panel.SetMessage("Add a comment")
	e.v.gitd.handleCommitRequest(gitui.CommitRequested{Message: e.v.gitd.panel.Message(), Files: e.v.gitd.panel.Selection()})
	e.waitFor("commit", func() bool { return e.ctl.Vcs.Committed == 1 })
	if log := runGit(t, e.root, "log", "--format=%s"); !strings.Contains(log, "Add a comment") {
		t.Fatalf("git log %q", log)
	}
	e.waitFor("panel reset", func() bool { return e.v.gitd.panel.Message() == "" })
	e.waitFor("markers cleared", func() bool { return !hasMarker(markers(), 1, editorview.MarkerGitAdded) })

	// Cmd+9 shows the log with both commits.
	e.key("9", key.ModCommand)
	if !e.v.ToolShown(ToolGit) {
		t.Fatal("Cmd+9 should open the git window")
	}
	e.waitFor("log", func() bool { return e.v.gitd.log.Len() == 2 })
	e.ctl.Vcs.SelectCommit(e.ctl.Vcs.Log[0].Hash)
	e.waitFor("commit diff", func() bool { return len(e.ctl.Vcs.CommitFiles) == 1 })
	e.shot("git-log")
	e.v.gitd.tab = 1
	e.waitFor("branches", func() bool { return len(e.ctl.Vcs.Branches) == 1 })
	e.frame()

	// Rollback at the caret (Cmd+Alt+Z) is undoable in the editor.
	if err := e.ctl.Backend().Call(context.Background(), "doc.insert", map[string]any{"path": file, "at": map[string]int{"line": 1}, "text": "// tmp\n"}, nil); err != nil {
		t.Fatal(err)
	}
	e.waitFor("marker line 1", func() bool { return hasMarker(markers(), 0, editorview.MarkerGitAdded) })
	s := e.v.editors[sessionKey{e.ctl.ActiveGroup(), file}]
	s.doc.Lock()
	s.ed.SetCaret(0)
	s.doc.Unlock()
	e.key("Z", key.ModCommand|key.ModAlt)
	e.waitFor("rollback", func() bool { return !hasMarker(markers(), 0, editorview.MarkerGitAdded) })

	// Disabling the module removes the git UI live.
	e.ctl.SetModuleEnabled("git", false)
	e.waitFor("git removed", func() bool {
		_, ok := e.v.Registry.ToolWindow(ToolCommit)
		return !ok && e.v.hdr.bar.Branch == ""
	})
	if _, ok := e.v.Registry.Action(ActCommit); ok {
		t.Fatal("commit action should be removed")
	}
	e.ctl.SetModuleEnabled("git", true)
	e.waitFor("git back", func() bool {
		_, ok := e.v.Registry.ToolWindow(ToolCommit)
		return ok
	})
}

// TestGitBranchesAndConflicts drives the branch popup, a delete with
// confirmation and the merge window of a real conflict.
func TestGitBranchesAndConflicts(t *testing.T) {
	e := newGitEnv(t)
	root := e.root
	runGit(t, root, "checkout", "-q", "-b", "other")
	if err := os.WriteFile(filepath.Join(root, "index.php"), []byte("<?php\necho 2;\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, root, "commit", "-q", "-am", "other")
	runGit(t, root, "checkout", "-q", "main")
	if err := os.WriteFile(filepath.Join(root, "index.php"), []byte("<?php\necho 3;\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, root, "commit", "-q", "-am", "main")
	runGit(t, root, "branch", "gone")
	e.ctl.Vcs.LoadBranches()
	e.waitFor("git", func() bool { return e.ctl.Vcs.Available && len(e.ctl.Vcs.Branches) == 3 })

	// Branch popup of the header.
	e.v.RunAction(ActBranches)
	e.frame()
	if e.v.hdr.bar.Opened() != toolbar.PopupBranch {
		t.Fatal("branch popup not opened")
	}
	e.v.hdr.bar.Close()

	// Delete asks first.
	e.v.Confirm("Delete branch gone?", "", "Delete", func() { e.ctl.Vcs.DeleteBranch("gone", false) })
	e.frame()
	if e.v.popup == nil {
		t.Fatal("confirmation expected")
	}
	e.v.popup.Buttons[0].OnClick()
	e.v.popup = nil
	e.waitFor("branch deleted", func() bool { return len(e.ctl.Vcs.Branches) == 2 })

	// Merge from the header's branch submenu (U11): confirmation, then
	// the conflict opens the merge window and the Git window.
	e.frame()
	e.v.hdr.handle(toolbar.MergeBranch{Name: "other"})
	e.frame()
	if e.v.popup == nil || !strings.Contains(e.v.popup.Title, "Merge “other” into “main”") {
		t.Fatalf("merge confirmation expected: %+v %q", e.v.popup, e.ctl.Status.Notice)
	}
	e.v.popup.Buttons[0].OnClick()
	e.v.popup = nil
	e.waitFor("conflict", func() bool { return len(e.ctl.Vcs.Conflicts) == 1 && e.ctl.Vcs.InProgress() == "merge" })
	if !strings.Contains(e.ctl.Status.Notice, "onflict") {
		t.Fatalf("hint %q", e.ctl.Status.Notice)
	}
	e.waitFor("merge window", func() bool { return e.v.ToolShown(toolMerge + "index.php") })
	// A second merge is refused while this one runs.
	e.v.hdr.handle(toolbar.RebaseOnto{Name: "other"})
	if e.v.popup != nil || !strings.Contains(e.ctl.Status.Notice, "in progress") {
		t.Fatalf("rebase during merge: %q", e.ctl.Status.Notice)
	}
	mv := e.v.gitd.merges["index.php"]
	if mv == nil || mv.Conflicts() != 1 {
		t.Fatal("merge view")
	}
	e.ctl.Vcs.ApplyMerge("index.php", []byte("<?php\necho 23;\n"))
	e.waitFor("resolved", func() bool {
		_, open := e.v.Registry.ToolWindow(toolMerge + "index.php")
		return len(e.ctl.Vcs.Conflicts) == 0 && !open
	})
	e.v.RunAction(ActGitContinue)
	e.waitFor("merge concluded", func() bool {
		return e.ctl.Vcs.InProgress() == "" && strings.Contains(e.ctl.Status.Notice, "Merge done")
	})
	if got := runGit(t, root, "rev-list", "--count", "--merges", "HEAD"); strings.TrimSpace(got) != "1" {
		t.Fatalf("merge commits %q", got)
	}
	data, _ := os.ReadFile(filepath.Join(root, "index.php"))
	if string(data) != "<?php\necho 23;\n" {
		t.Fatalf("resolved content %q", data)
	}
}

// ---- debug ------------------------------------------------------------------------

// fakeXdebug is a tiny DBGp engine (like Xdebug) running script; it stops
// at enabled breakpoints and steps line by line.
type fakeXdebug struct {
	t      *testing.T
	conn   net.Conn
	script string
	mu     sync.Mutex
	bps    map[string]int // id → line
	line   int
	next   int
}

func dialXdebug(t *testing.T, addr, script string) *fakeXdebug {
	t.Helper()
	conn, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	f := &fakeXdebug{t: t, conn: conn, script: script, bps: map[string]int{}}
	f.write(fmt.Sprintf(`<init xmlns="urn:debugger_protocol_v1" fileuri="%s" language="PHP" protocol_version="1.0" appid="1" idekey="TIEGEL"><engine version="3.3.1"><![CDATA[Xdebug]]></engine></init>`, script))
	go f.serve()
	return f
}

func (f *fakeXdebug) write(x string) {
	msg := `<?xml version="1.0" encoding="iso-8859-1"?>` + "\n" + x
	_, _ = fmt.Fprintf(f.conn, "%d\x00%s\x00", len(msg), msg)
}

func (f *fakeXdebug) serve() {
	r := bufio.NewReader(f.conn)
	for {
		line, err := r.ReadString(0)
		if err != nil {
			return
		}
		fields := strings.Fields(strings.TrimSuffix(line, "\x00"))
		if len(fields) == 0 {
			continue
		}
		args := map[string]string{}
		for i := 1; i+1 < len(fields); i += 2 {
			args[fields[i]] = strings.Trim(fields[i+1], `"`)
		}
		if !f.respond(fields[0], args) {
			_ = f.conn.Close()
			return
		}
	}
}

func (f *fakeXdebug) resp(cmd string, args map[string]string, attrs, body string) {
	f.write(fmt.Sprintf(`<response xmlns="urn:debugger_protocol_v1" xmlns:xdebug="https://xdebug.org/dbgp/xdebug" command="%s" transaction_id="%s"%s>%s</response>`, cmd, args["-i"], attrs, body))
}

func (f *fakeXdebug) breakAt(cmd string, args map[string]string, line int) {
	f.line = line
	f.resp(cmd, args, ` status="break" reason="ok"`, fmt.Sprintf(`<xdebug:message filename="%s" lineno="%d"></xdebug:message>`, f.script, line))
}

func (f *fakeXdebug) respond(cmd string, args map[string]string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	switch cmd {
	case "feature_set":
		f.resp(cmd, args, ` success="1"`, "")
	case "breakpoint_set":
		f.next++
		id := strconv.Itoa(f.next)
		n, _ := strconv.Atoi(args["-n"])
		if args["-s"] != "disabled" {
			f.bps[id] = n
		}
		f.resp(cmd, args, fmt.Sprintf(` id="%s" state="enabled"`, id), "")
	case "breakpoint_remove":
		delete(f.bps, args["-d"])
		f.resp(cmd, args, "", "")
	case "run":
		next := 0
		for _, l := range f.bps {
			if l > f.line && (next == 0 || l < next) {
				next = l
			}
		}
		if next == 0 {
			f.resp(cmd, args, ` status="stopping" reason="ok"`, "")
			break
		}
		f.breakAt(cmd, args, next)
	case "step_over", "step_into":
		f.breakAt(cmd, args, f.line+1)
	case "step_out":
		f.breakAt(cmd, args, f.line+2)
	case "stack_get":
		f.resp(cmd, args, "", fmt.Sprintf(`<stack where="{main}" level="0" type="file" filename="%s" lineno="%d"></stack>`, f.script, f.line))
	case "context_names":
		f.resp(cmd, args, "", `<context name="Locals" id="0"></context>`)
	case "context_get":
		f.resp(cmd, args, ` context="0"`, `<property name="$count" fullname="$count" type="int"><![CDATA[3]]></property>`+
			`<property name="$list" fullname="$list" type="array" children="1" numchildren="2" page="0" pagesize="32"></property>`)
	case "property_get":
		f.resp(cmd, args, "", `<property name="$list" fullname="$list" type="array" children="1" numchildren="2" page="0" pagesize="32"><property name="0" fullname="$list[0]" type="int"><![CDATA[1]]></property><property name="1" fullname="$list[1]" type="int"><![CDATA[2]]></property></property>`)
	case "eval":
		f.resp(cmd, args, "", `<property type="int"><![CDATA[4]]></property>`)
	case "stop":
		f.resp(cmd, args, ` status="stopped" reason="ok"`, "")
		return false
	case "detach":
		f.resp(cmd, args, ` status="stopping" reason="ok"`, "")
		return false
	default:
		f.resp(cmd, args, "", "")
	}
	return true
}

const debugScript = `<?php
$count = 3;
$list = [1, 2];
echo $count;
echo count($list);
`

// TestDebugEndToEnd: breakpoint per gutter click (without session) →
// listener → simulated Xdebug connects → the GUI runs to the breakpoint →
// execution point marker, stack and variables → F8 steps → F9 ends.
func TestDebugEndToEnd(t *testing.T) {
	e := newEnvWith(t, true, map[string]string{"debug.php": debugScript}, nil)
	d := e.ctl.Debug
	e.waitFor("debug available", func() bool { return d.Available })
	e.frame()
	if _, ok := e.v.Registry.ToolWindow(ToolDebug); !ok {
		t.Fatal("debug window not installed")
	}
	d.ListenAddr = "127.0.0.1:0"
	file := filepath.Join(e.root, "debug.php")
	e.ctl.OpenFile(file, nil)
	e.waitFor("tab", func() bool { return e.ctl.ActiveTab() != nil })
	markers := func() editorview.MarkerProvider { return e.v.Registry.markers(file) }

	// Click into the breakpoint column of line 3 (0-based 2): persistent
	// project breakpoint, no session needed.
	if !e.v.dbgd.gutterClick(file, editorview.GutterClickEvent{Line: 2, Area: editorview.GutterIcons, Clicks: 1}) {
		t.Fatal("gutter click not handled")
	}
	e.waitFor("breakpoint", func() bool { return hasMarker(markers(), 2, editorview.MarkerBreakpoint) })
	// Not paused: placeholders and the breakpoint list.
	e.v.ShowTool(ToolDebug)
	e.frame()
	e.shot("debug-idle")
	// Docked at the side the panel is narrow (D496: stacked, no
	// truncated section titles).
	e.ctl.Tools.MoveTo(ToolDebug, controller.SideRight, controller.GroupPrimary)
	e.v.ShowTool(ToolDebug)
	e.frame()
	e.shot("debug-idle-side")
	e.ctl.Tools.MoveTo(ToolDebug, controller.SideBottom, controller.GroupPrimary)
	e.v.ShowTool(ToolDebug)
	e.frame()
	// Right click edits the condition (chooser); empty keeps it plain.
	e.v.dbgd.gutterClick(file, editorview.GutterClickEvent{Line: 2, Area: editorview.GutterIcons, Secondary: true})
	e.frame()
	if e.v.chooseMode != chooseCustom || !e.v.chooser.NoList {
		t.Fatal("condition editor expected")
	}
	e.v.closeOverlay()
	// Cmd+F8 toggles at the caret (line 1) – on and off again.
	e.v.focusEditor = true
	e.frame()
	e.key("F8", key.ModCommand)
	e.waitFor("caret breakpoint", func() bool { _, ok := d.BreakpointAt(file, 1); return ok })
	e.key("F8", key.ModCommand)
	e.waitFor("caret breakpoint removed", func() bool { _, ok := d.BreakpointAt(file, 1); return !ok })

	// The header toggle starts the listener; the footer shows it only
	// while it runs.
	if len(e.v.dbgd.status()) != 0 || e.v.hdr.bar.NoListen {
		t.Fatal("listener: footer item while off or no header toggle")
	}
	e.v.hdr.handle(toolbar.ToggleListen{On: true})
	e.waitFor("listening", func() bool { return d.Listening && d.Addr != "" })

	engine := dialXdebug(t, d.Addr, dbgp.PathToURI(file))
	e.waitFor("break at breakpoint", func() bool {
		return d.Exec != nil && d.Exec.Line == 3 && len(d.Frames) == 1 && len(d.Variables) == 2
	})
	_ = engine
	e.frame()
	if !hasMarker(markers(), 2, editorview.MarkerExecutionPoint) {
		t.Fatal("execution point marker missing")
	}
	if !e.v.ToolShown(ToolDebug) {
		t.Fatal("debug window should come to the front on a break")
	}
	if e.v.dbgd.panel.Toolbar.State.String() != "paused" {
		t.Fatalf("toolbar state %v", e.v.dbgd.panel.Toolbar.State)
	}
	d.Expand("$list", 0)
	e.waitFor("children", func() bool {
		v := controller.FindVariable(d.Variables, "$list")
		return v != nil && len(v.Children) == 2
	})
	e.frame()
	e.shot("debug")
	e.ctl.Tools.MoveTo(ToolDebug, controller.SideRight, controller.GroupPrimary)
	e.v.ShowTool(ToolDebug)
	e.frame()
	e.shot("debug-side")
	e.ctl.Tools.MoveTo(ToolDebug, controller.SideBottom, controller.GroupPrimary)
	e.v.ShowTool(ToolDebug)
	e.frame()

	// F8 steps over.
	e.key("F8", 0)
	e.waitFor("step", func() bool { return d.Exec != nil && d.Exec.Line == 4 })
	e.frame()
	if !hasMarker(markers(), 3, editorview.MarkerExecutionPoint) || hasMarker(markers(), 2, editorview.MarkerExecutionPoint) {
		t.Fatal("execution point should move to line 4")
	}
	// F9 resumes: no further breakpoint → the script ends.
	e.key("F9", 0)
	e.waitFor("session ended", func() bool { return len(d.Sessions) == 0 && d.Exec == nil })
	e.frame()
	if hasMarker(markers(), 3, editorview.MarkerExecutionPoint) {
		t.Fatal("no execution point after the end")
	}
	d.ToggleListen()
	e.waitFor("stopped", func() bool { return !d.Listening })
}

// TestGitDebugSmokeBothThemes renders the workbench with the commit and
// the debug window in both themes.
func TestGitDebugSmokeBothThemes(t *testing.T) {
	e := newGitEnv(t)
	e.waitFor("git", func() bool { return e.ctl.Vcs.Available && e.ctl.Debug.Available })
	e.frame()
	for _, id := range []string{ToolCommit, ToolGit, ToolDebug} {
		e.v.ShowTool(id)
		e.settle()
		uitest.Smoke(t, id, windowSize, func(gtx layout.Context, th *tokens.Theme) layout.Dimensions {
			e.ctl.Prefs.Theme = th.Mode.String()
			return e.v.Frame(gtx)
		})
	}
	e.ctl.Debug.Hints = []controller.Hint{{Severity: "warning", Message: "xdebug.mode does not contain debug"}}
	e.frame()
}
