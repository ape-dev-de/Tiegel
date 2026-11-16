package git

import (
	"errors"
	"fmt"
	"image"
	"reflect"
	"testing"

	"gioui.org/io/key"

	"github.com/ape-dev-de/tiegel/internal/core/hooks"
	"github.com/ape-dev-de/tiegel/internal/gitcli"
	"github.com/ape-dev-de/tiegel/internal/ui/atoms"
	"github.com/ape-dev-de/tiegel/internal/ui/uitest"
)

const (
	mailer     = "src/Mail/Mailer.php"
	controller = "src/Http/Controller/UserController.php"
)

func newSamplePanel(t testing.TB) *CommitPanel {
	p := NewCommitPanel()
	p.SetChanges(sampleChanges(t))
	return p
}

func fileByPath(p *CommitPanel, path string) *fileSel {
	for _, f := range p.files {
		if f.change.Status.Path == path {
			return f
		}
	}
	return nil
}

func TestSelectionModelHierarchy(t *testing.T) {
	f := newFileSel(Change{Diff: diffFor(t, controller)}, groupChanges, true)
	if f.state() != atoms.Checked || f.hunkState(0) != atoms.Checked || f.hunkState(1) != atoms.Checked {
		t.Fatal("new tracked file must be fully selected")
	}
	f.setHunk(1, false)
	if f.state() != atoms.Indeterminate || f.hunkState(1) != atoms.Unchecked {
		t.Fatalf("after unchecking hunk: file %v hunk %v", f.state(), f.hunkState(1))
	}
	// Uncheck one line of hunk 0 (line 3 is "-        private UserRepository").
	if f.change.Diff.Hunks[0].Lines[3].Kind != gitcli.LineDeleted {
		t.Fatalf("fixture changed: %+v", f.change.Diff.Hunks[0].Lines[3])
	}
	f.setLine(0, 3, false)
	if f.hunkState(0) != atoms.Indeterminate {
		t.Fatalf("hunk 0 state %v", f.hunkState(0))
	}
	sel, ok := f.selection()
	if !ok || sel.Whole || len(sel.Hunks) != 1 || sel.Hunks[0].Hunk != 0 {
		t.Fatalf("selection %+v", sel)
	}
	var want []int
	for l, dl := range f.change.Diff.Hunks[0].Lines {
		if dl.Kind != gitcli.LineContext && l != 3 {
			want = append(want, l)
		}
	}
	if !reflect.DeepEqual(sel.Hunks[0].Lines, want) {
		t.Errorf("lines %v, want %v", sel.Hunks[0].Lines, want)
	}
	// The selection must produce a valid patch with the gitcli builder.
	if _, err := f.change.Diff.Patch(sel.Hunks); err != nil {
		t.Errorf("patch: %v", err)
	}
	// Context lines cannot be toggled.
	f.setLine(0, 0, false)
	if f.lines[0][0] {
		t.Error("context line selected")
	}
	f.setAll(true)
	if sel, _ := f.selection(); !sel.Whole || sel.Hunks != nil {
		t.Errorf("all selected must be Whole: %+v", sel)
	}
	f.setAll(false)
	if _, ok := f.selection(); ok || f.state() != atoms.Unchecked {
		t.Error("nothing selected")
	}
}

func TestSelectionModelWholeOnly(t *testing.T) {
	bin := newFileSel(Change{Status: gitcli.FileStatus{Path: "public/logo.png"}, Diff: &gitcli.FileDiff{NewPath: "public/logo.png", IsBinary: true}}, groupChanges, true)
	if bin.state() != atoms.Checked {
		t.Fatal("binary file selected as a whole")
	}
	if sel, ok := bin.selection(); !ok || !sel.Whole {
		t.Fatalf("binary selection %+v", sel)
	}
	conflict := newFileSel(Change{Status: gitcli.FileStatus{Kind: gitcli.KindUnmerged, Path: "x"}}, groupChanges, true)
	conflict.setAll(true)
	if conflict.state() != atoms.Unchecked {
		t.Error("conflicts must not be selectable")
	}
	un := newFileSel(Change{Status: gitcli.FileStatus{Kind: gitcli.KindUntracked, Path: "n.md"}}, groupUnversioned, false)
	if un.state() != atoms.Unchecked {
		t.Error("untracked starts unselected")
	}
	un.setAll(true)
	if sel, _ := un.selection(); !sel.Untracked || !sel.Whole {
		t.Errorf("untracked selection %+v", sel)
	}
	if g := groupState([]*fileSel{bin, conflict, un}); g != atoms.Checked {
		t.Errorf("group state %v (conflicts are ignored)", g)
	}
	bin.setAll(false)
	if g := groupState([]*fileSel{bin, un}); g != atoms.Indeterminate {
		t.Errorf("group state %v", g)
	}
	if g := groupState([]*fileSel{conflict}); g != atoms.Unchecked {
		t.Errorf("only conflicts: %v", g)
	}
}

func TestCommitPanelSmoke(t *testing.T) {
	p := newSamplePanel(t)
	uitest.Smoke(t, "commit panel", area, p.Layout)
	// Expanded files and hunks with a selected file.
	for _, f := range p.files {
		f.expanded = true
		for h := range f.hunkExpanded {
			f.hunkExpanded[h] = true
		}
	}
	p.dirty = true
	p.selected = 3
	p.diff.SetDiff(p.files[3].change.Diff)
	p.SetProblem(ProblemFromError(&gitcli.PushError{Kind: gitcli.PushRejected, Message: "rejected"}))
	p.SetHistory([]string{"Fix rounding", "Add invoices\n\nLong body"})
	p.history.Open = true
	uitest.Smoke(t, "commit panel expanded", area, p.Layout)
	uitest.Smoke(t, "commit panel small", image.Pt(320, 240), p.Layout)
	empty := NewCommitPanel()
	uitest.Smoke(t, "commit panel empty", area, empty.Layout)
}

func TestCommitPanelKeepsSelectionOnRefresh(t *testing.T) {
	p := newSamplePanel(t)
	f := fileByPath(p, controller)
	f.setHunk(0, false)
	f.expanded = true
	un := fileByPath(p, "docs/invoices.md")
	un.setAll(true)
	p.SetChanges(sampleChanges(t)) // same content, new pointers
	f = fileByPath(p, controller)
	if f.hunkState(0) != atoms.Unchecked || f.hunkState(1) != atoms.Checked || !f.expanded {
		t.Errorf("partial selection lost: %v %v %v", f.hunkState(0), f.hunkState(1), f.expanded)
	}
	if fileByPath(p, "docs/invoices.md").state() != atoms.Checked {
		t.Error("whole-file selection lost")
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
	frame(h, p)
	// Rows: toolbar 32 + divider 1, then 24dp rows. Row 0 is the
	// "Changes" group, row 1 the first file (config/services.yaml).
	groupBox := image.Pt(8+20+7, 33+12)
	fileBox := image.Pt(8+16+20+7, 33+24+12)

	first := p.files[p.groups[groupChanges][0]]
	h.Click(fileBox)
	frame(h, p)
	if first.state() != atoms.Unchecked {
		t.Fatalf("file click: state %v", first.state())
	}
	if groupState(p.groupFiles(groupChanges)) != atoms.Indeterminate {
		t.Fatal("group must become indeterminate")
	}
	// Clicking the indeterminate group checks every file.
	h.Click(groupBox)
	frame(h, p)
	for _, i := range p.groups[groupChanges] {
		if f := p.files[i]; !f.disabled && f.state() != atoms.Checked {
			t.Fatalf("%s not checked after group click", f.change.Status.Path)
		}
	}
	h.Click(groupBox)
	frame(h, p)
	if p.hasSelection() {
		t.Fatal("group uncheck left a selection")
	}

	// Expand the first file and toggle its hunk: the file follows.
	first.setAll(true)
	h.Click(image.Pt(8+16+10, 33+24+12)) // arrow
	frame(h, p)
	if !first.expanded {
		t.Fatal("arrow did not expand the file")
	}
	frame(h, p)
	hunkBox := image.Pt(8+16+16+20+7, 33+48+12)
	h.Click(hunkBox)
	frame(h, p)
	if first.hunkState(0) != atoms.Unchecked || first.state() != atoms.Unchecked {
		t.Fatalf("hunk click: hunk %v file %v", first.hunkState(0), first.state())
	}
}

func (p *CommitPanel) groupFiles(g int) []*fileSel {
	var out []*fileSel
	for _, i := range p.groups[g] {
		out = append(out, p.files[i])
	}
	return out
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
	frame(h, p)
	h.Router.Queue(key.Event{Name: key.NameReturn, Modifiers: key.ModShortcut, State: key.Press})
	ev := frame(h, p)
	var req *CommitRequested
	for _, e := range ev {
		if c, ok := e.(CommitRequested); ok {
			req = &c
		}
	}
	if req == nil {
		t.Fatalf("no CommitRequested in %v", ev)
	}
	if req.Message != "Send welcome mail\n\nDetails." || req.Amend || req.Push {
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

func TestAmendRequestsMessage(t *testing.T) {
	h := uitest.NewHarness(area)
	p := newSamplePanel(t)
	frame(h, p)
	// The Amend checkbox is the first control of the message area at the
	// bottom left: 8dp inset, 28dp row, 4 + 96 + 8 + 28 + 8 below.
	amendBox := image.Pt(8+7, area.Y-8-28-8-96-4-28+14)
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
}

func TestDiffRequestedForUntracked(t *testing.T) {
	h := uitest.NewHarness(area)
	p := newSamplePanel(t)
	frame(h, p)
	// Rows: group Changes (1) + 8 files, group Unversioned, then files.
	n := 1 + len(p.groups[groupChanges]) + 1
	h.Click(image.Pt(400, 33+24*n+12))
	ev := frame(h, p)
	var got *DiffRequested
	for _, e := range ev {
		if d, ok := e.(DiffRequested); ok {
			got = &d
		}
	}
	if got == nil || !got.Untracked {
		t.Fatalf("events %v", ev)
	}
	sel := p.files[p.selected]
	if sel.change.Status.Path != got.Path {
		t.Fatalf("selected %s, requested %s", sel.change.Status.Path, got.Path)
	}
	d := &gitcli.FileDiff{NewPath: got.Path, IsNew: true, Hunks: []gitcli.Hunk{{NewStart: 1, NewLines: 1,
		Lines: []gitcli.DiffLine{{Kind: gitcli.LineAdded, Text: "x", NewLine: 1}}}}}
	p.SetDiff(got.Path, d)
	if p.diff.Diff() != d {
		t.Error("preview not updated")
	}
	// No second request for the same file.
	h.Click(image.Pt(400, 33+24*n+12))
	for _, e := range frame(h, p) {
		if _, ok := e.(DiffRequested); ok {
			t.Error("duplicate DiffRequested")
		}
	}
}

func TestProblemFromError(t *testing.T) {
	if ProblemFromError(nil) != nil {
		t.Fatal("nil")
	}
	for k := gitcli.PushFailed; k <= gitcli.PushRepoNotFound; k++ {
		p := ProblemFromError(fmt.Errorf("wrap: %w", &gitcli.PushError{Kind: k, Message: "m", Stderr: "remote: denied"}))
		if p.Title == "" || p.Detail == "" || p.Warning {
			t.Errorf("push kind %d: %+v", k, p)
		}
		if PushErrorTitle(k) == "" {
			t.Errorf("no title for %d", k)
		}
	}
	ab := &hooks.AbortError{HookError: hooks.HookError{Hook: "commit.before", Owner: "phpstan", Err: errors.New("3 errors")}}
	if p := ProblemFromError(ab); p.Title != "Commit blocked by phpstan (commit.before)." || p.Detail != "3 errors" {
		t.Errorf("guard: %+v", p)
	}
	if p := ProblemFromError(gitcli.ErrEmptyMessage); !p.Warning {
		t.Error("empty message is a warning")
	}
	for _, err := range []error{gitcli.ErrEmptySelection, gitcli.ErrNoRemote, gitcli.ErrDetachedHead, errors.New("boom")} {
		if p := ProblemFromError(err); p.Title == "" {
			t.Errorf("%v: empty title", err)
		}
	}
}

func TestStatusOf(t *testing.T) {
	cases := []struct {
		st   gitcli.FileStatus
		want string
	}{
		{gitcli.FileStatus{Kind: gitcli.KindUntracked}, "untracked"},
		{gitcli.FileStatus{Kind: gitcli.KindIgnored}, "ignored"},
		{gitcli.FileStatus{Kind: gitcli.KindUnmerged}, "conflict"},
		{gitcli.FileStatus{Index: 'A', Worktree: '.'}, "added"},
		{gitcli.FileStatus{Index: '.', Worktree: 'D'}, "deleted"},
		{gitcli.FileStatus{Index: '.', Worktree: 'M'}, "modified"},
		{gitcli.FileStatus{Kind: gitcli.KindRenamed, Index: 'R'}, "modified"},
	}
	for _, c := range cases {
		if got := statusOf(c.st).String(); got != c.want {
			t.Errorf("%+v: %s, want %s", c.st, got, c.want)
		}
	}
}
