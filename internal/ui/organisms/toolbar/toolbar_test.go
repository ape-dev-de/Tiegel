package toolbar

import (
	"fmt"
	"image"
	"reflect"
	"sort"
	"testing"
	"time"

	"gioui.org/f32"
	"gioui.org/io/key"
	"gioui.org/io/pointer"
	"gioui.org/layout"

	"github.com/ape-dev-de/tiegel/internal/actions"
	"github.com/ape-dev-de/tiegel/internal/gitcli"
	"github.com/ape-dev-de/tiegel/internal/ui/atoms"
	"github.com/ape-dev-de/tiegel/internal/ui/tokens"
	"github.com/ape-dev-de/tiegel/internal/ui/uitest"
)

const winH = 600

func sample(long bool) *Bar {
	b := New()
	name, branch := "shop", "main"
	if long {
		name = "a-really-long-project-name-that-never-fits-into-the-header-toolbar"
		branch = "feature/JIRA-12345-an-extremely-long-branch-name-for-truncation-tests"
	}
	b.Projects = []Project{
		{ID: "p1", Name: name, Path: "/work/" + name, State: ProjectActive},
		{ID: "p2", Name: "api", Path: "/work/api", State: ProjectBackground},
		{ID: "p3", Name: "legacy blog", Path: "/work/blog", State: ProjectHibernated},
	}
	b.Current = "p1"
	b.Recent = []RecentProject{{Name: "api", Path: "/work/api"}, {Name: "tools", Path: "/work/tools"}}
	b.Branch = branch
	b.Branches = []gitcli.Branch{
		{Name: branch, Current: true, Ahead: 1},
		{Name: "develop", Behind: 2},
		{Name: "feature/login", UpstreamGone: true},
		{Name: "origin/main", Remote: true},
		{Name: "origin/feature/login", Remote: true},
	}
	b.RecentBranches = []string{"develop", "missing"}
	b.Configs = []RunConfig{{ID: "c1", Name: "PHPUnit (all tests) with a very long configuration name"}, {ID: "c2", Name: "serve", Detail: "PHP Script", Glyph: atoms.GlyphComputer}}
	b.Config = "c1"
	b.Quick = []QuickAction{
		{ID: "claude.open", Glyph: atoms.GlyphRun, Tooltip: "Claude Code", Badge: "2"},
		{Separator: true},
		{ID: "opencode.open", Glyph: atoms.GlyphMore, Tooltip: "opencode", Active: true},
	}
	return b
}

type rig struct {
	t   *testing.T
	b   *Bar
	h   *uitest.Harness
	evs []Event
}

func newRig(t *testing.T, b *Bar, w int) *rig {
	r := &rig{t: t, b: b, h: uitest.NewHarness(image.Pt(w, winH))}
	r.frame()
	return r
}

func (r *rig) widget(gtx layout.Context, th *tokens.Theme) layout.Dimensions {
	r.evs = append(r.evs, r.b.Update(gtx)...)
	return r.b.Layout(gtx, th)
}

func (r *rig) frame() { r.h.Frame(r.widget) }

func (r *rig) click(p image.Point) {
	r.h.Click(p)
	r.frame()
	r.frame()
}

func (r *rig) key(names ...key.Name) {
	for _, n := range names {
		r.h.Router.Queue(key.Event{Name: n, State: key.Press})
		r.frame()
	}
}

func (r *rig) seg(name string) image.Rectangle {
	rc, ok := r.b.Rect(name)
	if !ok {
		r.t.Fatalf("segment %q not drawn", name)
	}
	return rc
}

func center(rc image.Rectangle) image.Point { return rc.Min.Add(rc.Max).Div(2) }

// take returns and clears the collected events.
func (r *rig) take() []Event {
	out := r.evs
	r.evs = nil
	return out
}

func (r *rig) expect(want ...Event) {
	r.t.Helper()
	got := r.take()
	if !reflect.DeepEqual(got, want) && !(len(got) == 0 && len(want) == 0) {
		r.t.Fatalf("events %#v, want %#v", got, want)
	}
}

// rowPoint returns the center of popup row i (top level, no scrolling).
func (r *rig) rowPoint(p *popup, i int) image.Point {
	panel, _ := r.b.PopupRect()
	th := r.h.Theme
	y := panel.Min.Y + int(th.Space.XS)
	if p.search != nil {
		y += int(th.Size.ControlH) + 2*int(th.Space.S)
	}
	for k := 0; k < i; k++ {
		y += rowH(th, p.items[k])
	}
	return image.Pt(panel.Min.X+40, y+rowH(th, p.items[i])/2)
}

// subPoint returns the center of submenu row j.
func (r *rig) subPoint(p *popup, j int) image.Point {
	_, sub := r.b.PopupRect()
	th := r.h.Theme
	y := sub.Min.Y + int(th.Space.XS)
	items := p.items[p.subOf].sub
	for k := 0; k < j; k++ {
		y += rowH(th, items[k])
	}
	return image.Pt(sub.Min.X+40, y+rowH(th, items[j])/2)
}

func rowH(th *tokens.Theme, it item) int {
	if it.kind == kindSeparator {
		return 2*int(th.Space.XS) + int(th.Size.BorderWidth)
	}
	return int(th.Size.RowH)
}

func labels(items []item) []string {
	var out []string
	for _, it := range items {
		switch it.kind {
		case kindHeader:
			out = append(out, "#"+it.label)
		case kindSeparator:
			out = append(out, "-")
		default:
			out = append(out, it.label)
		}
	}
	return out
}

func TestSmokeAndNoOverlap(t *testing.T) {
	for _, long := range []bool{false, true} {
		for _, w := range []int{800, 1200, 1800} {
			name := fmt.Sprintf("long=%v/w=%d", long, w)
			b := sample(long)
			b.Running = true
			d := uitest.Smoke(t, name, image.Pt(w, 40), b.Layout)
			if d.Size != image.Pt(w, 40) {
				t.Fatalf("%s: size %v", name, d.Size)
			}
			r := newRig(t, b, w)
			var rs []image.Rectangle
			for _, s := range []string{SegProject, SegBranch, SegConfig, SegRun, SegDebug, SegStop, SegListen, SegQuick(0), SegQuick(2), SegSearch, SegSettings} {
				rs = append(rs, r.seg(s))
			}
			sort.Slice(rs, func(i, j int) bool { return rs[i].Min.X < rs[j].Min.X })
			for i, rc := range rs {
				if rc.Min.X < 0 || rc.Max.X > w || rc.Min.Y < 0 || rc.Max.Y > 40 {
					t.Fatalf("%s: %v outside the bar", name, rc)
				}
				if i > 0 && rc.Min.X < rs[i-1].Max.X {
					t.Fatalf("%s: %v overlaps %v", name, rc, rs[i-1])
				}
			}
		}
	}
	// Long names are truncated more on narrow windows.
	narrow, wide := newRig(t, sample(true), 800), newRig(t, sample(true), 1800)
	for _, s := range []string{SegProject, SegBranch, SegConfig} {
		if n, w := narrow.seg(s).Dx(), wide.seg(s).Dx(); n >= w {
			t.Errorf("%s: narrow %d >= wide %d", s, n, w)
		}
	}
	// Without repository, listen toggle or projects.
	b := New()
	b.NoListen = true
	r := newRig(t, b, 800)
	if _, ok := b.Rect(SegBranch); ok {
		t.Fatal("branch switcher without branch")
	}
	if _, ok := b.Rect(SegListen); ok {
		t.Fatal("listen toggle hidden")
	}
	r.click(center(r.seg(SegRun)))
	r.expect() // disabled without configuration
	uitest.Smoke(t, "empty", image.Pt(400, 40), New().Layout)
	var zero Bar
	uitest.Smoke(t, "zero", image.Pt(1000, 40), zero.Layout)
}

func TestButtons(t *testing.T) {
	b := sample(false)
	b.Running = true
	r := newRig(t, b, 1400)
	for _, c := range []struct {
		seg  string
		want Event
	}{
		{SegRun, Run{Config: "c1"}}, {SegDebug, Debug{Config: "c1"}}, {SegStop, Stop{}},
		{SegListen, ToggleListen{On: true}}, {SegSearch, SearchEverywhere{}}, {SegSettings, OpenSettings{}},
		{SegQuick(0), QuickActionClicked{ID: "claude.open"}}, {SegQuick(2), QuickActionClicked{ID: "opencode.open"}},
	} {
		r.click(center(r.seg(c.seg)))
		r.expect(c.want)
	}
	b.Listening = true
	r.frame()
	r.click(center(r.seg(SegListen)))
	r.expect(ToggleListen{On: false})
	// Hovering shows a tooltip after the delay.
	p := center(r.seg(SegQuick(0)))
	r.h.Router.Queue(pointer.Event{Kind: pointer.Move, Source: pointer.Mouse, Position: f32.Pt(float32(p.X), float32(p.Y)), Time: 100 * time.Second})
	for i := 0; i < 40; i++ {
		r.frame()
	}
	if !b.quickTips[0].Visible() || b.quickTips[0].Text != "Claude Code (2)" {
		t.Fatalf("tooltip %q visible=%v", b.quickTips[0].Text, b.quickTips[0].Visible())
	}
	// The quick strip follows the data.
	b.Quick = b.Quick[:1]
	r.frame()
	if _, ok := b.Rect(SegQuick(2)); ok {
		t.Fatal("removed quick action still drawn")
	}
}

func TestProjectPopup(t *testing.T) {
	b := sample(false)
	r := newRig(t, b, 1200)
	r.click(center(r.seg(SegProject)))
	r.expect(PopupOpened{Popup: PopupProject})
	if b.Opened() != PopupProject {
		t.Fatal("not open")
	}
	want := []string{"#Open Projects", "shop", "api", "legacy blog", "#Recent Projects", "tools", "-", "Open…", "Hibernate shop", "Resume legacy blog"}
	if got := labels(b.projectPop.items); !reflect.DeepEqual(got, want) {
		t.Fatalf("items %q", got)
	}
	panel, _ := b.PopupRect()
	if panel.Empty() || panel.Min.Y < 40-int(r.h.Theme.Space.S) {
		t.Fatalf("panel %v", panel)
	}
	r.click(r.rowPoint(&b.projectPop, 2))
	r.expect(SwitchProject{ID: "p2"})
	if b.Opened() != PopupNone {
		t.Fatal("still open after choosing")
	}
	// Recent project and actions by mouse.
	for _, c := range []struct {
		row  int
		want Event
	}{{5, OpenRecentProject{Path: "/work/tools"}}, {7, OpenProject{}}, {8, HibernateProject{ID: "p1"}}, {9, ResumeProject{ID: "p3"}}} {
		r.click(center(r.seg(SegProject)))
		r.take()
		r.click(r.rowPoint(&b.projectPop, c.row))
		r.expect(c.want)
	}
	// Keyboard: the first selectable row is preselected; arrows move,
	// Enter chooses; Up from the top wraps to the end.
	r.click(center(r.seg(SegProject)))
	r.take()
	r.key(key.NameDownArrow, key.NameDownArrow, key.NameUpArrow)
	if b.projectPop.sel != 2 {
		t.Fatalf("sel %d", b.projectPop.sel)
	}
	r.key(key.NameReturn)
	r.expect(SwitchProject{ID: "p2"})
	r.click(center(r.seg(SegProject)))
	r.take()
	r.key(key.NameUpArrow, key.NameEnter)
	r.expect(ResumeProject{ID: "p3"})
	// Escape closes; a click outside closes.
	r.click(center(r.seg(SegProject)))
	r.take()
	r.key(key.NameEscape)
	if b.Opened() != PopupNone {
		t.Fatal("Escape did not close")
	}
	r.click(center(r.seg(SegProject)))
	r.take()
	r.click(image.Pt(1100, 500))
	if b.Opened() != PopupNone {
		t.Fatal("outside click did not close")
	}
	r.expect()
	// A hibernated current project offers no "Hibernate".
	b.Projects[0].State = ProjectHibernated
	if got := labels(b.projectItems(DefaultIcons())); got[len(got)-3] != "Open…" {
		t.Fatalf("items %q", got)
	}
}

func TestBranchPopup(t *testing.T) {
	b := sample(false)
	r := newRig(t, b, 1200)
	r.click(center(r.seg(SegBranch)))
	r.expect(PopupOpened{Popup: PopupBranch})
	want := []string{"New Branch…", "Checkout Tag or Revision…", "-", "#Recent", "develop", "-", "#Local", "main", "develop", "feature/login", "-", "#Remote", "origin/main", "origin/feature/login"}
	if got := labels(b.branchPop.items); !reflect.DeepEqual(got, want) {
		t.Fatalf("items %q", got)
	}
	// Mouse: a branch row opens its submenu; a submenu row chooses.
	r.click(r.rowPoint(&b.branchPop, 8)) // local develop
	if b.branchPop.subOf != 8 {
		t.Fatalf("submenu of %d", b.branchPop.subOf)
	}
	_, sub := b.PopupRect()
	panel, _ := b.PopupRect()
	if sub.Empty() || sub.Min.X < panel.Max.X-int(r.h.Theme.Space.S) {
		t.Fatalf("submenu %v next to %v", sub, panel)
	}
	r.click(r.subPoint(&b.branchPop, 0))
	r.expect(CheckoutBranch{Name: "develop"})
	if b.Opened() != PopupNone {
		t.Fatal("still open")
	}
	// The actions at the top.
	r.click(center(r.seg(SegBranch)))
	r.take()
	r.click(r.rowPoint(&b.branchPop, 1))
	r.expect(CheckoutRevision{})

	// Keyboard with search: typing filters, Enter opens the submenu of
	// the selected branch, arrows move in it, Enter chooses.
	r.click(center(r.seg(SegBranch)))
	r.take()
	r.frame()
	r.h.Router.Queue(key.EditEvent{Text: "login"})
	r.frame()
	r.frame()
	want = []string{"#Local", "feature/login", "-", "#Remote", "origin/feature/login"}
	if got := labels(b.branchPop.items); !reflect.DeepEqual(got, want) {
		t.Fatalf("filtered %q", got)
	}
	r.key(key.NameDownArrow) // → origin/feature/login
	r.key(key.NameReturn)
	if b.branchPop.subOf != 4 {
		t.Fatalf("submenu of %d (sel %d)", b.branchPop.subOf, b.branchPop.sel)
	}
	r.key(key.NameDownArrow, key.NameDownArrow, key.NameDownArrow) // Checkout → New from → Merge → Rebase
	r.key(key.NameReturn)
	r.expect(RebaseOnto{Name: "origin/feature/login", Remote: true})

	// Escape closes the submenu first, then the popup.
	r.click(center(r.seg(SegBranch)))
	r.take()
	r.frame()
	r.key(key.NameDownArrow, key.NameDownArrow, key.NameDownArrow) // → main (current)
	r.key(key.NameReturn)
	if b.branchPop.subOf < 0 {
		t.Fatal("no submenu")
	}
	// The current branch cannot be checked out, merged, rebased or
	// deleted: the first selectable entry is "New Branch from".
	r.key(key.NameReturn)
	r.expect(NewBranch{From: "main"})
	r.click(center(r.seg(SegBranch)))
	r.take()
	r.frame()
	r.key(key.NameDownArrow, key.NameDownArrow, key.NameDownArrow, key.NameDownArrow, key.NameReturn)
	r.key(key.NameEscape)
	if b.Opened() != PopupBranch || b.branchPop.subOf >= 0 {
		t.Fatal("first Escape should close the submenu only")
	}
	r.key(key.NameEscape)
	if b.Opened() != PopupNone {
		t.Fatal("second Escape should close the popup")
	}
	// Nothing found.
	b.Show(PopupBranch)
	b.branchPop.search.SetText("zzz")
	r.frame()
	b.branchPop.queryChanged = true
	r.frame()
	if len(b.branchPop.items) != 0 {
		t.Fatalf("items %q", labels(b.branchPop.items))
	}
	// Removing the repository closes the popup and hides the switcher.
	b.Branch = ""
	r.frame()
	if b.Opened() != PopupNone {
		t.Fatal("branch popup open without repository")
	}
}

func TestRunPopup(t *testing.T) {
	b := sample(false)
	r := newRig(t, b, 1000)
	r.click(center(r.seg(SegConfig)))
	r.expect(PopupOpened{Popup: PopupRun})
	if got := labels(b.runPop.items); !reflect.DeepEqual(got, []string{b.Configs[0].Name, "serve", "-", "Edit Configurations…"}) {
		t.Fatalf("items %q", got)
	}
	panel, _ := b.PopupRect()
	if panel.Max.X > 1000 {
		t.Fatalf("popup %v leaves the window", panel)
	}
	r.click(r.rowPoint(&b.runPop, 1))
	r.expect(SelectRunConfig{ID: "c2"})
	r.click(center(r.seg(SegConfig)))
	r.take()
	r.key(key.NameUpArrow, key.NameSpace)
	r.expect(EditRunConfigs{})
	// Opening another popup closes the first; clicking the trigger again
	// closes it.
	b.Show(PopupRun)
	b.Show(PopupProject)
	if b.Opened() != PopupProject || b.runPop.open {
		t.Fatal("Show should switch popups")
	}
	r.take()
	b.Close()
	r.frame()
	r.click(center(r.seg(SegConfig)))
	r.click(center(r.seg(SegConfig)))
	if b.Opened() != PopupNone {
		t.Fatal("second trigger click should close")
	}
}

func TestSubmenuKeys(t *testing.T) {
	p := &popup{subOf: -1}
	sub := []item{{label: "a", ev: Stop{}}, {label: "b", disabled: true}, {label: "c", ev: Run{}}}
	p.show()
	p.setItems([]item{header("x"), {label: "one", sub: sub}, {label: "two", ev: OpenSettings{}}})
	if p.sel != 1 {
		t.Fatalf("sel %d", p.sel)
	}
	if evs := p.activate(1); evs != nil || p.subOf != 1 || p.subSel != 0 {
		t.Fatalf("activate sub: %v %d %d", evs, p.subOf, p.subSel)
	}
	p.move(1)
	if p.subSel != 2 {
		t.Fatalf("disabled entry not skipped: %d", p.subSel)
	}
	if evs := p.enter(); len(evs) != 1 || evs[0] != (Run{}) || p.open {
		t.Fatalf("enter %v", evs)
	}
	if p.activateSub(0) != nil || p.activate(0) != nil || p.activate(9) != nil {
		t.Fatal("invalid activation reported events")
	}
	if nextSel([]item{header("x")}, -1, 1) != -1 {
		t.Fatal("no selectable item")
	}
}

func TestHelpers(t *testing.T) {
	for in, want := range map[string]string{"tiegel": "TI", "my-shop": "MS", "legacy blog app": "LB", "": "?", "x": "X", "--": "?", "über_api": "ÜA"} {
		if got := Initials(in); got != want {
			t.Errorf("Initials(%q) = %q, want %q", in, got, want)
		}
	}
	for _, c := range []struct {
		want  []int
		avail int
		out   []int
	}{
		{[]int{100, 50, 80}, 1000, []int{100, 50, 80}},
		{[]int{100, 50, 80}, 150, []int{50, 50, 50}},
		{[]int{300, 20, 100}, 220, []int{100, 20, 100}},
		{[]int{100, 0, 100}, 100, []int{50, 0, 50}},
	} {
		if got := share(c.want, c.avail); !reflect.DeepEqual(got, c.out) {
			t.Errorf("share(%v, %d) = %v, want %v", c.want, c.avail, got, c.out)
		}
	}
	if ProjectBackground.String() != "background" || ProjectHibernated.String() != "hibernated" || ProjectActive.String() != "active" {
		t.Fatal("state names")
	}
	if PopupBranch.String() != "branch" || PopupNone.String() != "none" {
		t.Fatal("popup names")
	}
	qa := QuickActions([]actions.ToolbarButton{{ID: "a", Icon: "run", Tooltip: "A", Active: true, Badge: "1"}, {Separator: true}, {ID: "b", Icon: "nope"}})
	if len(qa) != 3 || qa[0].Glyph != atoms.GlyphRun || !qa[0].Active || qa[0].Badge != "1" || !qa[1].Separator || qa[2].Glyph != atoms.GlyphMore {
		t.Fatalf("quick actions %+v", qa)
	}
	// Custom icons replace defaults, missing ones fall back.
	b := &Bar{Icons: Icons{Run: atoms.GlyphDebug}}
	if ic := b.icons(); ic.Run != atoms.GlyphDebug || ic.Stop != atoms.GlyphStop {
		t.Fatal("icons")
	}
	if b.CurrentProject() != nil {
		t.Fatal("current project without projects")
	}
	b.Projects = []Project{{ID: "x", State: ProjectBackground}}
	if b.CurrentProject().ID != "x" {
		t.Fatal("fallback to first project")
	}
	if b.PopupMaxHeight != 0 || b.Opened() != PopupNone {
		t.Fatal("zero bar")
	}
	if p, s := b.PopupRect(); !p.Empty() || !s.Empty() {
		t.Fatal("popup rect when closed")
	}
}
