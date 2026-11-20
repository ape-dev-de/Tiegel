package git

import (
	"image"
	"strings"
	"testing"

	"gioui.org/io/key"
	"gioui.org/layout"

	"github.com/ape-dev-de/tiegel/internal/gitcli"
	"github.com/ape-dev-de/tiegel/internal/ui/atoms"
	"github.com/ape-dev-de/tiegel/internal/ui/tokens"
	"github.com/ape-dev-de/tiegel/internal/ui/uitest"
)

// Geometry of the test harness (1dp = 1px): rows are 24dp, a row's
// content starts 8dp in plus 16dp per tree level with a 20dp arrow
// before the 14dp checkbox.
const rowH = 24

func rowY(p *CommitPanel, i int) int { return p.headH + 2 + i*rowH + rowH/2 }

func boxX(depth int) int { return 8 + 16*depth + 20 + 7 }

func arrowX(depth int) int { return 8 + 16*depth + 10 }

// rowNames renders the rows as "depth:name" for readable assertions.
func rowNames(p *CommitPanel) []string {
	var out []string
	for _, r := range p.rows {
		name := ""
		switch r.kind {
		case rowGroup, rowDir:
			name = r.node.Name
		case rowFile:
			name = r.node.Name
		case rowHunk:
			name = "@@"
		default:
			name = "line"
		}
		out = append(out, string(rune('0'+r.depth))+":"+name)
	}
	return out
}

func eventsOf[T Event](ev []Event) []T {
	var out []T
	for _, e := range ev {
		if t, ok := e.(T); ok {
			out = append(out, t)
		}
	}
	return out
}

func TestCommitPanelSmoke(t *testing.T) {
	p := newSamplePanel(t)
	p.SetBranch("feature/invoices")
	uitest.Smoke(t, "commit panel", area, p.Layout)
	// Expanded files and hunks, options, problem and history.
	for _, f := range p.files {
		f.expanded = true
		for h := range f.hunkExpanded {
			f.hunkExpanded[h] = true
		}
	}
	p.dirty = true
	p.selected = 3
	p.showOpts = true
	p.SetMessage(strings.Repeat("A long commit message line\n", 40))
	p.SetProblem(ProblemFromError(&gitcli.PushError{Kind: gitcli.PushRejected, Message: "rejected"}))
	p.SetHistory([]string{"Fix rounding", "Add invoices\n\nLong body"})
	p.history.Open = true
	p.view.Open = true
	p.FileGlyph = func(string) *atoms.Glyph { return atoms.GlyphFolder }
	uitest.Smoke(t, "commit panel expanded", area, p.Layout)
	uitest.Smoke(t, "commit panel small", image.Pt(320, 240), p.Layout)
	for _, g := range gitcli.ChangeGroupings {
		p.SetGrouping(g)
		uitest.Smoke(t, "grouping "+string(g), area, p.Layout)
	}
	p.stashTab = true
	uitest.Smoke(t, "stash placeholder", area, p.Layout)
	p.Stash = func(gtx layout.Context, th *tokens.Theme) layout.Dimensions {
		return layout.Dimensions{Size: gtx.Constraints.Min}
	}
	uitest.Smoke(t, "stash host", area, p.Layout)
	empty := NewCommitPanel()
	uitest.Smoke(t, "commit panel empty", area, empty.Layout)
}

func TestCommitPanelTree(t *testing.T) {
	p := newSamplePanel(t)
	p.SetBranch("feature/invoices")
	p.buildRows()
	want := []string{"0:Changes", "1:config", "2:services.yaml", "1:public", "2:logo.png", "1:src", "2:Billing", "3:PriceCalculator.php",
		"2:Domain", "3:Invoice.php", "2:Http/Controller", "3:UserController.php", "2:Mail", "3:Mailer.php", "2:Support", "3:Str.php",
		"1:tests/Legacy", "2:LegacyMailerTest.php", "0:Unversioned Files", "1:docs", "2:invoices.md", "1:src/Domain", "2:InvoiceNumber.php"}
	if got := rowNames(p); strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("rows\n%v\nwant\n%v", got, want)
	}
	if p.tree.Changes.Badge != "feature/invoices" || p.tree.Changes.Count != 8 || p.tree.Unversioned.Count != 2 {
		t.Fatalf("groups %+v", p.tree.Changes)
	}
	if o := p.Order(); len(o) != 10 || o[0] != "config/services.yaml" || o[9] != "src/Domain/InvoiceNumber.php" {
		t.Fatalf("order %v", o)
	}
	// Selection follows the tree order.
	sel := p.Selection()
	if len(sel) != 7 || sel[0].Path != "config/services.yaml" {
		t.Fatalf("selection order %+v", sel)
	}
	// Collapse all keeps the groups, expand all restores everything.
	h := uitest.NewHarness(area)
	frame(h, p)
	p.toolbar(layout.Context{}, toolCollapse)
	p.buildRows()
	if got := rowNames(p); strings.Join(got, ",") != "0:Changes,1:config,1:public,1:src,1:tests/Legacy,0:Unversioned Files,1:docs,1:src/Domain" {
		t.Fatalf("collapsed %v", got)
	}
	p.toolbar(layout.Context{}, toolExpand)
	p.buildRows()
	if len(p.rows) != len(want) {
		t.Fatal("expand all")
	}
	// Module and flat grouping; the detail shows the rest of the path.
	p.SetGrouping(gitcli.GroupModule)
	p.buildRows()
	if got := rowNames(p); got[1] != "1:config" || got[5] != "1:src" || p.Grouping() != gitcli.GroupModule {
		t.Fatalf("module rows %v", got)
	}
	if d := p.fileDetail(p.tree.Find("src/Support/Str.php")); d != "Support  ← src/Util/Str.php" {
		t.Errorf("module detail %q", d)
	}
	p.SetGrouping("")
	if p.Grouping() != gitcli.GroupDirectory {
		t.Fatal("default grouping")
	}
	p.SetGrouping(gitcli.GroupFlat)
	p.buildRows()
	if got := rowNames(p); len(got) != 12 || got[1] != "1:services.yaml" || got[9] != "0:Unversioned Files" {
		t.Fatalf("flat rows %v", got)
	}
	if d := p.fileDetail(p.tree.Find("config/services.yaml")); d != "config" {
		t.Errorf("flat detail %q", d)
	}
	if !p.Select("docs/invoices.md") || p.SelectedPath() != "docs/invoices.md" || p.Select("nope") {
		t.Fatal("Select")
	}
}

func TestCommitPanelKeepsSelectionOnRefresh(t *testing.T) {
	p := newSamplePanel(t)
	f := fileByPath(p, controller)
	f.setHunk(0, false)
	f.expanded = true
	un := fileByPath(p, "docs/invoices.md")
	un.setAll(true)
	p.Select(mailer)
	p.SetChanges(sampleChanges(t)) // same content, new pointers
	f = fileByPath(p, controller)
	if f.hunkState(0) != atoms.Unchecked || f.hunkState(1) != atoms.Checked || !f.expanded {
		t.Errorf("partial selection lost: %v %v %v", f.hunkState(0), f.hunkState(1), f.expanded)
	}
	if fileByPath(p, "docs/invoices.md").state() != atoms.Checked || p.SelectedPath() != mailer {
		t.Error("whole-file selection or selected file lost")
	}
	// A changed diff resets partial selections to "all".
	ch := sampleChanges(t)
	for i := range ch {
		if ch[i].Status.Path == controller {
			d := *ch[i].Diff
			d.Hunks = d.Hunks[:1]
			ch[i].Diff = &d
		}
	}
	p.SetChanges(ch)
	if fileByPath(p, controller).state() != atoms.Checked {
		t.Error("changed diff must reset to selected")
	}
}

func TestCommitPanelCheckboxClicks(t *testing.T) {
	h := uitest.NewHarness(area)
	p := newSamplePanel(t)
	p.SetGrouping(gitcli.GroupFlat)
	frame(h, p)
	frame(h, p)
	first := p.files[p.groups[groupChanges][0]]
	h.Click(image.Pt(boxX(1), rowY(p, 1)))
	frame(h, p)
	if first.state() != atoms.Unchecked {
		t.Fatalf("file click: state %v", first.state())
	}
	if groupState(p.groupFiles(groupChanges)) != atoms.Indeterminate {
		t.Fatal("group must become indeterminate")
	}
	// Clicking the indeterminate group checks every file.
	h.Click(image.Pt(boxX(0), rowY(p, 0)))
	frame(h, p)
	for _, i := range p.groups[groupChanges] {
		if f := p.files[i]; !f.disabled && f.state() != atoms.Checked {
			t.Fatalf("%s not checked after group click", f.change.Status.Path)
		}
	}
	h.Click(image.Pt(boxX(0), rowY(p, 0)))
	frame(h, p)
	for _, i := range p.groups[groupChanges] {
		if p.files[i].state() != atoms.Unchecked {
			t.Fatal("group uncheck left a selection")
		}
	}

	// Expand the first file and toggle its hunk: the file follows.
	first.setAll(true)
	h.Click(image.Pt(arrowX(1), rowY(p, 1)))
	frame(h, p)
	if !first.expanded {
		t.Fatal("arrow did not expand the file")
	}
	frame(h, p)
	h.Click(image.Pt(boxX(2), rowY(p, 2)))
	frame(h, p)
	if first.hunkState(0) != atoms.Unchecked || first.state() != atoms.Unchecked {
		t.Fatalf("hunk click: hunk %v file %v", first.hunkState(0), first.state())
	}
	// The hunk's arrow shows its lines; a line checkbox selects one line.
	h.Click(image.Pt(arrowX(2), rowY(p, 2)))
	frame(h, p)
	frame(h, p)
	if !first.hunkExpanded[0] || p.rows[3].kind != rowLine {
		t.Fatalf("hunk lines not shown: %v", rowNames(p)[:5])
	}

	// A directory checkbox toggles the files below it (conflicts excluded).
	first.expanded = false
	p.SetGrouping(gitcli.GroupDirectory)
	frame(h, p)
	frame(h, p)
	src := p.rows[5]
	if src.node.Name != "src" {
		t.Fatalf("row 5 is %s", src.node.Name)
	}
	h.Click(image.Pt(boxX(1), rowY(p, 5)))
	frame(h, p)
	for _, i := range p.under[src.node] {
		if f := p.files[i]; !f.disabled && f.state() != atoms.Checked {
			t.Fatalf("%s not toggled", f.change.Status.Path)
		}
	}
	// Clicking a group row (not its checkbox) collapses it.
	h.Click(image.Pt(400, rowY(p, 0)))
	frame(h, p)
	frame(h, p)
	if !p.closed["g0"] || p.rows[1].kind != rowGroup {
		t.Fatalf("group not collapsed: %v", rowNames(p))
	}
	// Double click on a directory collapses it as well.
	p.closed = map[string]bool{}
	p.dirty = true
	frame(h, p)
	h.DoubleClick(image.Pt(400, rowY(p, 1)))
	frame(h, p)
	if !p.closed["0:config"] {
		t.Fatal("directory double click")
	}
}

func (p *CommitPanel) groupFiles(g int) []*fileSel {
	var out []*fileSel
	for _, i := range p.groups[g] {
		out = append(out, p.files[i])
	}
	return out
}

func TestCommitPanelOpenDiff(t *testing.T) {
	h := uitest.NewHarness(area)
	p := newSamplePanel(t)
	p.SetGrouping(gitcli.GroupFlat)
	frame(h, p)
	frame(h, p)
	// Double click on a file opens its diff tab.
	h.DoubleClick(image.Pt(400, rowY(p, 6)))
	ev := frame(h, p)
	if d := eventsOf[DiffOpenRequested](ev); len(d) != 1 || d[0].Path != mailer || d[0].Untracked {
		t.Fatalf("double click: %v", ev)
	}
	// The tree has the focus now: Down selects the next file, Enter opens
	// it, Space toggles it, F4 opens the file itself.
	for _, k := range []key.Name{key.NameDownArrow, key.NameReturn, key.NameSpace, key.NameF4} {
		h.Router.Queue(key.Event{Name: k, State: key.Press})
	}
	ev = frame(h, p)
	if p.SelectedPath() != "src/Support/Str.php" {
		t.Fatalf("down: selected %s", p.SelectedPath())
	}
	if d := eventsOf[DiffOpenRequested](ev); len(d) != 1 || d[0].Path != "src/Support/Str.php" {
		t.Fatalf("enter: %v", ev)
	}
	if fileByPath(p, "src/Support/Str.php").state() != atoms.Unchecked {
		t.Fatal("space did not toggle")
	}
	if o := eventsOf[FileOpenRequested](ev); len(o) != 1 || o[0].Path != "src/Support/Str.php" {
		t.Fatalf("F4: %v", ev)
	}
	// Up from the first file stays; up without selection goes to the end.
	p.selected = -1
	h.Router.Queue(key.Event{Name: key.NameUpArrow, State: key.Press})
	ev = frame(h, p)
	if p.SelectedPath() != "src/Domain/InvoiceNumber.php" {
		t.Fatalf("up: %s", p.SelectedPath())
	}
	// The last file is untracked without a diff: selecting it asks for it.
	dr := eventsOf[DiffRequested](ev)
	if len(dr) != 1 || !dr[0].Untracked {
		t.Fatalf("DiffRequested %v", ev)
	}
	d := &gitcli.FileDiff{NewPath: dr[0].Path, IsNew: true, Hunks: []gitcli.Hunk{{NewStart: 1, NewLines: 1,
		Lines: []gitcli.DiffLine{{Kind: gitcli.LineAdded, Text: "x", NewLine: 1}}}}}
	p.SetDiff(d.NewPath, d)
	if fileByPath(p, d.NewPath).change.Diff != d {
		t.Fatal("SetDiff")
	}
	// Show Diff in the toolbar (5th button) opens the selected file.
	h.Click(image.Pt(4+4*28+14, 37+2+14))
	if d := eventsOf[DiffOpenRequested](frame(h, p)); len(d) != 1 || !d[0].Untracked {
		t.Fatal("toolbar Show Diff")
	}
}

func TestCommitPanelToolbarAndTabs(t *testing.T) {
	h := uitest.NewHarness(area)
	p := newSamplePanel(t)
	frame(h, p)
	y := 37 + 2 + 14
	var got []Event
	for i := range 4 {
		h.Click(image.Pt(4+i*28+14, y))
		got = append(got, frame(h, p)...)
	}
	if len(eventsOf[RefreshRequested](got)) != 1 || len(eventsOf[UpdateRequested](got)) != 1 || len(eventsOf[StashRequested](got)) != 1 {
		t.Fatalf("toolbar events %v", got)
	}
	rb := eventsOf[RollbackRequested](got)
	if len(rb) != 1 || len(rb[0].Paths) != 7 {
		t.Fatalf("rollback %v", rb)
	}
	// Without checked files Rollback takes the selected file.
	for _, f := range p.files {
		f.setAll(false)
	}
	p.Select(mailer)
	if r := p.rollbackPaths(); len(r) != 1 || r[0] != mailer {
		t.Fatalf("rollback selected %v", r)
	}
	// The Stash tab: probe the tab row for the second button.
	var shown []StashTabShown
	for x := 60; x < 300 && len(shown) == 0; x += 10 {
		h.Click(image.Pt(x, 18))
		shown = eventsOf[StashTabShown](frame(h, p))
	}
	if len(shown) != 1 || !shown[0].Shown || !p.StashShown() {
		t.Fatalf("stash tab %v", shown)
	}
	h.Click(image.Pt(30, 18))
	if s := eventsOf[StashTabShown](frame(h, p)); len(s) != 1 || s[0].Shown {
		t.Fatalf("commit tab %v", s)
	}
}

func TestCommitButtonState(t *testing.T) {
	h := uitest.NewHarness(area)
	p := newSamplePanel(t)
	frame(h, p)
	if !p.commit.Disabled || !p.push.Disabled || p.CanCommit() {
		t.Fatal("empty message must disable commit")
	}
	p.SetMessage("Add invoices")
	frame(h, p)
	if p.commit.Disabled || p.push.Disabled {
		t.Fatal("message + selection must enable commit")
	}
	for _, f := range p.files {
		f.setAll(false)
	}
	frame(h, p)
	if !p.commit.Disabled {
		t.Fatal("empty selection must disable commit")
	}
	// Amend allows an empty selection (reword).
	p.amend.State = atoms.Checked
	frame(h, p)
	if p.commit.Disabled {
		t.Fatal("amend with message must enable commit")
	}
	p.SetBusy(true)
	frame(h, p)
	if !p.commit.Disabled || !p.Busy() {
		t.Fatal("busy must disable commit")
	}
	p.SetMessage("   \n")
	p.SetBusy(false)
	frame(h, p)
	if !p.commit.Disabled {
		t.Fatal("blank message must disable commit")
	}
}

func TestCommitShortcutEmitsSelection(t *testing.T) {
	h := uitest.NewHarness(area)
	p := newSamplePanel(t)
	f := fileByPath(p, controller)
	f.setHunk(1, false)
	fileByPath(p, "src/Domain/InvoiceNumber.php").setAll(true)
	p.SetMessage("Send welcome mail\n\nDetails.\n\n")
	p.signOff.State = atoms.Checked
	frame(h, p)
	h.Router.Queue(key.Event{Name: key.NameReturn, Modifiers: key.ModShortcut, State: key.Press})
	ev := frame(h, p)
	reqs := eventsOf[CommitRequested](ev)
	if len(reqs) != 1 {
		t.Fatalf("no CommitRequested in %v", ev)
	}
	req := reqs[0]
	if req.Message != "Send welcome mail\n\nDetails." || req.Amend || req.Push || !req.SignOff || req.NoVerify {
		t.Errorf("request %+v", req)
	}
	got := map[string]FileSelection{}
	for _, fs := range req.Files {
		got[fs.Path] = fs
	}
	if fs := got[controller]; fs.Whole || len(fs.Hunks) != 1 || fs.Hunks[0].Hunk != 0 || fs.Hunks[0].Lines != nil {
		t.Errorf("controller selection %+v", fs)
	}
	if fs := got[mailer]; !fs.Whole {
		t.Errorf("mailer selection %+v", fs)
	}
	if fs := got["src/Support/Str.php"]; fs.OrigPath != "src/Util/Str.php" {
		t.Errorf("rename selection %+v", fs)
	}
	if fs, ok := got["src/Domain/InvoiceNumber.php"]; !ok || !fs.Untracked || !fs.Whole {
		t.Errorf("untracked selection %+v", fs)
	}
	if _, ok := got["docs/invoices.md"]; ok {
		t.Error("unselected untracked file committed")
	}
	if _, ok := got["src/Billing/PriceCalculator.php"]; ok {
		t.Error("conflict committed")
	}
	// Without a message the shortcut does nothing.
	p.SetMessage("")
	h.Router.Queue(key.Event{Name: key.NameReturn, Modifiers: key.ModShortcut, State: key.Press})
	if ev := frame(h, p); len(ev) != 0 {
		t.Errorf("events without message: %v", ev)
	}
}

func TestCommitOptionsAndPush(t *testing.T) {
	h := uitest.NewHarness(area)
	p := newSamplePanel(t)
	p.SetMessage("Ship it")
	frame(h, p)
	// The gear is the last control of the button row (bottom right).
	gear := image.Pt(area.X-12-14, area.Y-12-14)
	h.Click(gear)
	frame(h, p)
	if !p.showOpts {
		t.Fatal("gear did not show the options")
	}
	p.noVerify.State = atoms.Checked
	// "Commit and Push…" is right of "Commit": probe the button row.
	var reqs []CommitRequested
	for x := 140; x < 400 && len(reqs) == 0; x += 12 {
		h.Click(image.Pt(x, area.Y-12-14))
		reqs = eventsOf[CommitRequested](frame(h, p))
	}
	if len(reqs) != 1 || !reqs[0].Push || !reqs[0].NoVerify {
		t.Fatalf("commit and push %+v", reqs)
	}
	h.Click(gear)
	frame(h, p)
	if p.showOpts {
		t.Fatal("gear did not hide the options")
	}
}

func TestAmendRequestsMessage(t *testing.T) {
	h := uitest.NewHarness(area)
	p := newSamplePanel(t)
	p.SetHistory([]string{"Previous commit\n\nbody"})
	if p.lastMsg != "Previous commit" {
		t.Fatalf("last commit %q", p.lastMsg)
	}
	p.SetLastCommit("Other")
	if p.lastMsg != "Other" {
		t.Fatal("SetLastCommit")
	}
	frame(h, p)
	// Bottom up: 12 inset, 28 buttons, 8 gap, 72 editor, 4 gap, 28 row.
	amendBox := image.Pt(12+7, area.Y-12-28-8-72-4-14)
	h.Click(amendBox)
	ev := frame(h, p)
	if !p.Amend() || len(ev) != 1 || ev[0] != (AmendMessageRequested{}) {
		t.Fatalf("amend click: amend=%v events=%v", p.Amend(), ev)
	}
	p.SetAmendMessage("Previous commit")
	if p.Message() != "Previous commit" {
		t.Fatalf("amend message %q", p.Message())
	}
	// Unchecking removes the untouched amend message again.
	h.Click(amendBox)
	frame(h, p)
	if p.Amend() || p.Message() != "" {
		t.Fatalf("uncheck: amend=%v message=%q", p.Amend(), p.Message())
	}
	// A second check reuses the known message without asking again.
	h.Click(amendBox)
	if ev := frame(h, p); len(ev) != 0 || p.Message() != "Previous commit" {
		t.Fatalf("recheck: %v %q", ev, p.Message())
	}
	p.SetProblem(&Problem{Title: "x"})
	p.Reset()
	if p.Message() != "" || p.Amend() || p.Problem() != nil {
		t.Error("reset")
	}
	fresh := NewCommitPanel()
	fresh.SetAmendMessage("From amend\nbody")
	if fresh.lastMsg != "From amend" {
		t.Errorf("last from amend %q", fresh.lastMsg)
	}
}

func TestDefaultFileGlyph(t *testing.T) {
	for p, want := range map[string]*atoms.Glyph{"a/B.PHP": atoms.GlyphFileCode, "README.md": atoms.GlyphFileText, "logo.png": atoms.GlyphFile} {
		if got := DefaultFileGlyph(p); got != want {
			t.Errorf("%s: %s", p, got.Name())
		}
	}
}
