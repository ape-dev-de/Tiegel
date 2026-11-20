// Package toolbar provides the header toolbar organism of the main window
// (AP18.3): a single row, left to right
//
//   - project switcher: two-letter avatar, project name, chevron; its
//     inline popup lists open projects with their state (active,
//     background, hibernated), recent projects and the actions "Open…",
//     "Hibernate <project>", "Resume <project>", "Close <project>" and
//     submenus to hibernate or close the other open projects;
//   - branch switcher: branch glyph, branch name, chevron; its inline
//     popup has a search field, the actions "New Branch…" and "Checkout
//     Tag or Revision…", the sections Recent, Local and Remote and a
//     submenu per branch (Checkout, New branch from, Merge into current,
//     Rebase onto, Delete);
//   - right: run configuration dropdown, Run, Debug, Stop (only while
//     running), the Xdebug listen toggle, the plugin quick-action strip
//     (QuickAction, contributed through actions.Registry.AddToolbar),
//     search everywhere and settings.
//
// Texts never wrap: project, branch and configuration names share the
// free width and are truncated with an ellipsis.
//
// The bar is data in, events out (D71): the host sets the exported
// fields every frame and handles the Events returned by Update. It holds
// no project, git or run logic.
package toolbar

import (
	"fmt"
	"hash/maphash"
	"image"
	"image/color"
	"strings"

	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/unit"
	"gioui.org/widget"

	"github.com/ape-dev-de/tiegel/internal/gitcli"
	"github.com/ape-dev-de/tiegel/internal/ui/atoms"
	"github.com/ape-dev-de/tiegel/internal/ui/internal/draw"
	"github.com/ape-dev-de/tiegel/internal/ui/molecules"
	"github.com/ape-dev-de/tiegel/internal/ui/organisms/git"
	"github.com/ape-dev-de/tiegel/internal/ui/tokens"
)

// The widths of the popups and the caps of the truncated names are
// tokens (tokens.ShellSizes); PopupMaxHeight 0 uses ShellSizes.PopupMaxH.

// Segment names of Rect (for hosts that anchor overlays and for tests).
const (
	SegProject  = "project"
	SegBranch   = "branch"
	SegConfig   = "config"
	SegRun      = "run"
	SegDebug    = "debug"
	SegStop     = "stop"
	SegListen   = "listen"
	SegSearch   = "search"
	SegSettings = "settings"
)

// SegQuick returns the segment name of quick action i.
func SegQuick(i int) string { return fmt.Sprintf("quick:%d", i) }

// Bar is the header toolbar organism. Set the exported fields every frame
// (they are plain data) and call Update before Layout.
type Bar struct {
	// Projects are the open projects; Current is the ID of the project
	// shown (empty: the first ProjectActive one).
	Projects []Project
	Current  string
	Recent   []RecentProject

	// Branch is the checked out branch (or e.g. "HEAD (a1b2c3d)" when
	// detached); empty hides the branch switcher (no repository).
	Branch string
	// Branches are local and remote branches; RecentBranches are names of
	// recently checked out local branches, most recent first.
	Branches       []gitcli.Branch
	RecentBranches []string

	// Configs are the run configurations; Config is the selected ID.
	Configs []RunConfig
	Config  string
	// Running shows the Stop button.
	Running bool
	// Listening is the Xdebug listen state; NoListen hides the toggle
	// (Xdebug module disabled).
	Listening bool
	NoListen  bool

	// Quick are the plugin quick-action buttons.
	Quick []QuickAction

	// Icons replaces the default glyphs (zero fields use DefaultIcons).
	Icons Icons
	// PopupMaxHeight bounds the popups (0: tokens.ShellSizes.PopupMaxH).
	PopupMaxHeight unit.Dp

	projectBtn, branchBtn, configBtn widget.Clickable
	projectTip, branchTip, configTip atoms.Tooltip

	run, debug, stop, listen, search, settings atoms.IconButton
	tips                                       [6]atoms.Tooltip
	quick                                      []atoms.IconButton
	quickTips                                  []atoms.Tooltip

	projectPop, branchPop, runPop popup
	inited                        bool
	// builtIcons are the resolved icons the popup items were built with; a
	// change rebuilds them.
	builtIcons Icons
	hash       maphash.Hash

	q     []Event
	rects map[string]image.Rectangle
}

// New returns an empty bar.
func New() *Bar {
	b := &Bar{}
	b.init()
	return b
}

func (b *Bar) init() {
	if b.inited {
		return
	}
	b.inited = true
	b.branchPop.search = molecules.NewSearchField("Search branches and actions")
	b.projectPop.subOf, b.branchPop.subOf, b.runPop.subOf = -1, -1, -1
	b.branchPop.expand = func(it item) []item { return b.branchSub(b.icons(), it.branch) }
}

// icons returns Icons with defaults filled in.
func (b *Bar) icons() Icons {
	d, ic := DefaultIcons(), b.Icons
	fill := func(dst **atoms.Glyph, def *atoms.Glyph) {
		if *dst == nil {
			*dst = def
		}
	}
	fill(&ic.Chevron, d.Chevron)
	fill(&ic.Branch, d.Branch)
	fill(&ic.Run, d.Run)
	fill(&ic.Debug, d.Debug)
	fill(&ic.Stop, d.Stop)
	fill(&ic.Listen, d.Listen)
	fill(&ic.Search, d.Search)
	fill(&ic.Settings, d.Settings)
	fill(&ic.Folder, d.Folder)
	fill(&ic.Add, d.Add)
	fill(&ic.Tag, d.Tag)
	fill(&ic.Open, d.Open)
	fill(&ic.Hibernate, d.Hibernate)
	fill(&ic.Resume, d.Resume)
	fill(&ic.Close, d.Close)
	fill(&ic.Check, d.Check)
	return ic
}

// CurrentProject returns the project shown in the switcher, or nil.
func (b *Bar) CurrentProject() *Project {
	for i := range b.Projects {
		if b.Projects[i].ID == b.Current && b.Current != "" {
			return &b.Projects[i]
		}
	}
	for i := range b.Projects {
		if b.Projects[i].State == ProjectActive {
			return &b.Projects[i]
		}
	}
	if len(b.Projects) > 0 {
		return &b.Projects[0]
	}
	return nil
}

// selectedConfig returns the selected run configuration, or nil.
func (b *Bar) selectedConfig() *RunConfig {
	for i := range b.Configs {
		if b.Configs[i].ID == b.Config {
			return &b.Configs[i]
		}
	}
	return nil
}

func (b *Bar) pop(p Popup) *popup {
	switch p {
	case PopupProject:
		return &b.projectPop
	case PopupBranch:
		return &b.branchPop
	case PopupRun:
		return &b.runPop
	}
	return nil
}

// Opened returns the open popup (PopupNone if none).
func (b *Bar) Opened() Popup {
	for _, p := range []Popup{PopupProject, PopupBranch, PopupRun} {
		if b.pop(p).open {
			return p
		}
	}
	return PopupNone
}

// Show opens popup p (closing any other), e.g. from a keyboard shortcut.
// It reports PopupOpened like a click.
func (b *Bar) Show(p Popup) {
	b.init()
	b.Close()
	if pp := b.pop(p); pp != nil {
		pp.show()
		b.q = append(b.q, PopupOpened{Popup: p})
	}
}

// Close closes any open popup.
func (b *Bar) Close() {
	b.projectPop.close()
	b.branchPop.close()
	b.runPop.close()
}

func (b *Bar) toggle(p Popup) {
	if b.pop(p).open {
		b.Close()
		return
	}
	b.Show(p)
}

// Rect returns the bounds of a segment (Seg* names) in the last frame,
// in the bar's coordinates; ok is false when it was not drawn.
func (b *Bar) Rect(seg string) (r image.Rectangle, ok bool) {
	r, ok = b.rects[seg]
	return r, ok
}

// PopupRect returns the bounds of the open popup's panel and of its open
// submenu (empty if none) in the last frame, in the bar's coordinates.
func (b *Bar) PopupRect() (panel, sub image.Rectangle) {
	seg := map[Popup]string{PopupProject: SegProject, PopupBranch: SegBranch, PopupRun: SegConfig}
	p := b.Opened()
	if p == PopupNone {
		return image.Rectangle{}, image.Rectangle{}
	}
	o := b.rects[seg[p]].Min
	pp := b.pop(p)
	panel = pp.rect.Add(o)
	if !pp.subRect.Empty() {
		sub = pp.subRect.Add(o)
	}
	return panel, sub
}

// Update processes input and returns the events since the last call.
func (b *Bar) Update(gtx layout.Context) []Event {
	b.update(gtx)
	out := b.q
	b.q = nil
	return out
}

func (b *Bar) update(gtx layout.Context) {
	b.init()
	if b.projectBtn.Clicked(gtx) {
		b.toggle(PopupProject)
	}
	if b.branchBtn.Clicked(gtx) && b.Branch != "" {
		b.toggle(PopupBranch)
	}
	if b.configBtn.Clicked(gtx) {
		b.toggle(PopupRun)
	}
	if b.Branch == "" {
		b.branchPop.close()
	}
	ic := b.icons()
	// Only the open popup has items; they are rebuilt when its data or
	// the search query changed (U3).
	for _, pp := range []*popup{&b.projectPop, &b.branchPop, &b.runPop} {
		if !pp.open && pp.built {
			pp.items, pp.built = nil, false
		}
	}
	if p := b.Opened(); p != PopupNone {
		pp := b.pop(p)
		b.refresh(p, ic)
		b.q = append(b.q, pp.process(gtx)...)
		if pp.queryChanged {
			pp.queryChanged = false
			pp.sel, pp.built = -1, false
			b.refresh(p, ic)
			pp.list.ScrollTo(0)
		}
	}
	b.syncButtons(ic)
	if b.run.Clicked(gtx) {
		b.q = append(b.q, Run{Config: b.Config})
	}
	if b.debug.Clicked(gtx) {
		b.q = append(b.q, Debug{Config: b.Config})
	}
	if b.stop.Clicked(gtx) && b.Running {
		b.q = append(b.q, Stop{})
	}
	if b.listen.Clicked(gtx) && !b.NoListen {
		b.q = append(b.q, ToggleListen{On: !b.Listening})
	}
	if b.search.Clicked(gtx) {
		b.q = append(b.q, SearchEverywhere{})
	}
	if b.settings.Clicked(gtx) {
		b.q = append(b.q, OpenSettings{})
	}
	for i := range b.Quick {
		if !b.Quick[i].Separator && b.quick[i].Clicked(gtx) {
			b.q = append(b.q, QuickActionClicked{ID: b.Quick[i].ID})
		}
	}
	if len(b.q) > 0 {
		gtx.Execute(op.InvalidateCmd{})
	}
}

// refresh rebuilds the items of popup p when it is open and its data
// changed since the last build (the search query is tracked through
// popup.queryChanged).
func (b *Bar) refresh(p Popup, ic Icons) {
	pp := b.pop(p)
	if !pp.open {
		return
	}
	if ic != b.builtIcons {
		b.builtIcons = ic
		b.projectPop.built, b.branchPop.built, b.runPop.built = false, false, false
	}
	key := b.dataKey(p)
	if pp.built && pp.key == key {
		return
	}
	switch p {
	case PopupProject:
		pp.setItems(b.projectItems(ic))
	case PopupBranch:
		pp.setItems(b.branchItems(ic))
	case PopupRun:
		pp.setItems(b.runItems(ic))
	}
	pp.key, pp.built = key, true
}

// dataKey hashes the data popup p is built from. It does not allocate,
// so checking it every frame is cheap even for thousands of branches.
func (b *Bar) dataKey(p Popup) uint64 {
	h := &b.hash
	h.Reset()
	str := func(s string) {
		h.WriteString(s)
		h.WriteByte(0)
	}
	num := func(v int) {
		var buf [8]byte
		for i := range buf {
			buf[i] = byte(v >> (8 * i))
		}
		h.Write(buf[:])
	}
	flag := func(v bool) {
		if v {
			h.WriteByte(1)
		} else {
			h.WriteByte(0)
		}
	}
	switch p {
	case PopupProject:
		str(b.Current)
		for _, pr := range b.Projects {
			str(pr.ID)
			str(pr.Name)
			str(pr.Path)
			num(int(pr.State))
		}
		h.WriteByte(1)
		for _, r := range b.Recent {
			str(r.Name)
			str(r.Path)
		}
	case PopupBranch:
		str(b.Branch)
		for _, br := range b.Branches {
			str(br.Name)
			flag(br.Remote)
			flag(br.Current)
			flag(br.UpstreamGone)
			num(br.Ahead)
			num(br.Behind)
		}
		h.WriteByte(1)
		for _, n := range b.RecentBranches {
			str(n)
		}
	case PopupRun:
		str(b.Config)
		for _, c := range b.Configs {
			str(c.ID)
			str(c.Name)
			str(c.Detail)
			maphash.WriteComparable(h, c.Glyph)
		}
	}
	return h.Sum64()
}

// syncButtons copies the data into the button states; it runs in Update
// and Layout, since the host may replace Quick in between.
func (b *Bar) syncButtons(ic Icons) {
	if len(b.quick) != len(b.Quick) {
		b.quick = make([]atoms.IconButton, len(b.Quick))
		b.quickTips = make([]atoms.Tooltip, len(b.Quick))
	}
	for i, qa := range b.Quick {
		btn := &b.quick[i]
		btn.Glyph, btn.Active, btn.Disabled, btn.Description = qa.Glyph, qa.Active, qa.Disabled, qa.Tooltip
		tip := qa.Tooltip
		if qa.Badge != "" {
			tip += " (" + qa.Badge + ")"
		}
		b.quickTips[i].Text = tip
	}
	noConfig := b.selectedConfig() == nil
	b.run.Glyph, b.run.Disabled, b.run.Description = ic.Run, noConfig, "Run"
	b.debug.Glyph, b.debug.Disabled, b.debug.Description = ic.Debug, noConfig, "Debug"
	b.stop.Glyph, b.stop.Description = ic.Stop, "Stop"
	b.listen.Glyph, b.listen.Active = ic.Listen, b.Listening
	b.listen.Description = "Start Listening for PHP Debug Connections"
	if b.Listening {
		b.listen.Description = "Stop Listening for PHP Debug Connections"
	}
	b.search.Glyph, b.search.Description = ic.Search, "Search Everywhere"
	b.settings.Glyph, b.settings.Description = ic.Settings, "Settings"
	name := "Run"
	if c := b.selectedConfig(); c != nil {
		name = "Run '" + c.Name + "'"
	}
	b.tips[0].Text = name
	b.tips[1].Text = strings.Replace(name, "Run", "Debug", 1)
	b.tips[2].Text = "Stop"
	b.tips[3].Text = b.listen.Description
	b.tips[4].Text = "Search Everywhere"
	b.tips[5].Text = "Settings"
}

// projectItems builds the project popup.
func (b *Bar) projectItems(ic Icons) []item {
	var items []item
	cur := b.CurrentProject()
	open := map[string]bool{}
	if len(b.Projects) > 0 {
		items = append(items, header("Open Projects"))
	}
	for _, p := range b.Projects {
		open[p.Path] = true
		items = append(items, item{
			label: p.Name, detail: p.Path, avatar: p.Name, state: p.State, hasState: true,
			bold: cur != nil && p.ID == cur.ID, ev: SwitchProject{ID: p.ID},
		})
	}
	first := true
	for _, r := range b.Recent {
		if open[r.Path] {
			continue
		}
		if first {
			items = append(items, header("Recent Projects"))
			first = false
		}
		items = append(items, item{label: r.Name, detail: r.Path, glyph: ic.Folder, ev: OpenRecentProject{Path: r.Path}})
	}
	if len(items) > 0 {
		items = append(items, separator())
	}
	items = append(items, item{label: "Open…", glyph: ic.Open, ev: OpenProject{}})
	if cur != nil && cur.State != ProjectHibernated {
		items = append(items, item{label: "Hibernate " + cur.Name, glyph: ic.Hibernate, ev: HibernateProject{ID: cur.ID}})
	}
	var hibernate, closeOther []item
	for _, p := range b.Projects {
		if cur != nil && p.ID == cur.ID {
			continue
		}
		closeOther = append(closeOther, item{label: p.Name, detail: p.Path, avatar: p.Name, ev: CloseProject{ID: p.ID}})
		if p.State != ProjectHibernated {
			hibernate = append(hibernate, item{label: p.Name, detail: p.Path, avatar: p.Name, ev: HibernateProject{ID: p.ID}})
		}
	}
	if len(hibernate) > 0 {
		items = append(items, item{label: "Hibernate Other Project", glyph: ic.Hibernate, sub: hibernate})
	}
	for _, p := range b.Projects {
		if p.State == ProjectHibernated {
			items = append(items, item{label: "Resume " + p.Name, glyph: ic.Resume, ev: ResumeProject{ID: p.ID}})
		}
	}
	if cur != nil {
		items = append(items, item{label: "Close " + cur.Name, glyph: ic.Close, ev: CloseProject{ID: cur.ID}})
	}
	if len(closeOther) > 0 {
		items = append(items, item{label: "Close Other Project", glyph: ic.Close, sub: closeOther})
	}
	return items
}

// branchItems builds the branch popup for the current search query.
func (b *Bar) branchItems(ic Icons) []item {
	q := b.branchPop.query()
	match := func(s string) bool { return q == "" || containsFold(s, q) }
	var items []item
	for _, a := range []item{
		{label: "New Branch…", glyph: ic.Add, ev: NewBranch{}},
		{label: "Checkout Tag or Revision…", glyph: ic.Tag, ev: CheckoutRevision{}},
	} {
		if match(a.label) {
			items = append(items, a)
		}
	}
	section := func(title string, brs []gitcli.Branch) {
		head := len(items)
		for _, br := range brs {
			if match(br.Name) {
				items = append(items, b.branchItem(ic, br))
			}
		}
		if len(items) > head {
			hdr := []item{header(title)}
			if head > 0 {
				hdr = []item{separator(), header(title)}
			}
			items = append(items[:head], append(hdr, items[head:]...)...)
		}
	}
	byName := map[string]gitcli.Branch{}
	var local, remote []gitcli.Branch
	for _, br := range b.Branches {
		if br.Remote {
			remote = append(remote, br)
		} else {
			local = append(local, br)
			byName[br.Name] = br
		}
	}
	var recent []gitcli.Branch
	for _, n := range b.RecentBranches {
		if br, ok := byName[n]; ok {
			recent = append(recent, br)
		}
	}
	section("Recent", recent)
	section("Local", local)
	section("Remote", remote)
	return items
}

// branchItem is a branch row; its submenu is built when it opens
// (branchSub).
func (b *Bar) branchItem(ic Icons, br gitcli.Branch) item {
	g := git.BranchGlyph(br)
	if !br.Remote {
		g = ic.Branch
	}
	var badges []atoms.Badge
	if br.Current || br.Ahead > 0 || br.Behind > 0 || br.UpstreamGone {
		badges = git.BranchBadges(br)
	}
	return item{label: br.Name, glyph: g, accent: br.Current, bold: br.Current, badges: badges, lazy: true, branch: br}
}

// branchSub is the submenu of a branch row.
func (b *Bar) branchSub(ic Icons, br gitcli.Branch) []item {
	cur := b.Branch
	n := br.Name
	sub := []item{
		{label: "Checkout", glyph: ic.Check, disabled: br.Current, ev: CheckoutBranch{Name: n, Remote: br.Remote}},
		{label: "New Branch from '" + n + "'…", glyph: ic.Add, ev: NewBranch{From: n}},
		separator(),
		{label: "Merge '" + n + "' into '" + cur + "'", disabled: br.Current, ev: MergeBranch{Name: n, Remote: br.Remote}},
		{label: "Rebase '" + cur + "' onto '" + n + "'", disabled: br.Current, ev: RebaseOnto{Name: n, Remote: br.Remote}},
		separator(),
		{label: "Delete", glyph: atoms.GlyphDelete, disabled: br.Current, ev: DeleteBranch{Name: n, Remote: br.Remote}},
	}
	return sub
}

// containsFold reports whether s contains sub, ignoring case, without
// allocating for ASCII text.
func containsFold(s, sub string) bool {
	if sub == "" {
		return true
	}
	if !isASCII(s) || !isASCII(sub) {
		return strings.Contains(strings.ToLower(s), strings.ToLower(sub))
	}
	n := len(sub)
	for i := 0; i+n <= len(s); i++ {
		if strings.EqualFold(s[i:i+n], sub) {
			return true
		}
	}
	return false
}

func isASCII(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] >= 0x80 {
			return false
		}
	}
	return true
}

// runItems builds the run configuration popup.
func (b *Bar) runItems(ic Icons) []item {
	var items []item
	for _, c := range b.Configs {
		g := c.Glyph
		if g == nil {
			g = ic.Run
		}
		items = append(items, item{label: c.Name, detail: c.Detail, glyph: g, accent: c.ID == b.Config, bold: c.ID == b.Config, ev: SelectRunConfig{ID: c.ID}})
	}
	if len(items) > 0 {
		items = append(items, separator())
	}
	return append(items, item{label: "Edit Configurations…", glyph: atoms.GlyphEdit, ev: EditRunConfigs{}})
}

// Height returns the bar height in pixels: the tab height plus a small
// margin (40dp with the default tokens).
func Height(gtx layout.Context, th *tokens.Theme) int {
	return gtx.Dp(th.Size.TabH) + gtx.Dp(th.Space.S)
}

// seg is one placed widget of the row.
type seg struct {
	name string
	w    layout.Widget
	gap  int // space before the widget
}

// Layout draws the bar across the full width and, when open, its popup.
func (b *Bar) Layout(gtx layout.Context, th *tokens.Theme) layout.Dimensions {
	b.init()
	if b.Branch == "" {
		b.branchPop.close()
	}
	ic := b.icons()
	// Input is handled in Update; here only data the host changed since
	// is picked up (a no-op unless the open popup's data changed).
	if p := b.Opened(); p != PopupNone {
		b.refresh(p, ic)
	}
	b.syncButtons(ic)
	sh := th.Shell()
	w := draw.Width(gtx, gtx.Dp(sh.FallbackW))
	h := Height(gtx, th)
	draw.Rect(gtx.Ops, image.Rect(0, 0, w, h), th.Color.SurfaceAlt)
	draw.Rect(gtx.Ops, image.Rect(0, h-gtx.Dp(th.Size.BorderWidth), w, h), th.Color.Border)
	if b.rects == nil {
		b.rects = map[string]image.Rectangle{}
	}
	clear(b.rects)
	cg := gtx
	cg.Constraints = layout.Constraints{Max: image.Pt(w, h)}

	padX, gap, group := gtx.Dp(th.Space.S), gtx.Dp(th.Space.XS), gtx.Dp(th.Space.S)
	cur := b.CurrentProject()
	projName := "No Project"
	if cur != nil {
		projName = cur.Name
	}
	cfgName := "Add Configuration…"
	if c := b.selectedConfig(); c != nil {
		cfgName = c.Name
	}
	projLabel := atoms.Strong(projName)
	branchLabel := atoms.Text(b.Branch)
	cfgLabel := atoms.Text(cfgName)
	if b.selectedConfig() == nil {
		cfgLabel = atoms.Muted(cfgName)
	}

	// Natural widths of the truncatable names, then the share each gets.
	natural := func(l atoms.Label) int {
		mg := cg
		mg.Constraints = layout.Constraints{Max: image.Pt(draw.Unbounded, h)}
		return draw.Measure(mg, l.SingleLine().Fn(th)).Size.X
	}
	want := []int{min(natural(projLabel), gtx.Dp(sh.ProjectNameCap)), 0, min(natural(cfgLabel), gtx.Dp(sh.ConfigNameCap))}
	if b.Branch != "" {
		want[1] = min(natural(branchLabel), gtx.Dp(sh.BranchNameCap))
	}
	segsFor := func(lw []int) (left, right []seg) {
		left = append(left, seg{SegProject, b.trigger(th, &b.projectBtn, &b.projectTip, b.projectPop.open, projName,
			func(gtx layout.Context) layout.Dimensions {
				return avatar(gtx, th, projName, gtx.Dp(th.Size.RowH)-2*gtx.Dp(th.Space.XXS))
			}, projLabel, lw[0], ic), 0})
		if b.Branch != "" {
			left = append(left, seg{SegBranch, b.trigger(th, &b.branchBtn, &b.branchTip, b.branchPop.open, b.Branch,
				atoms.Icon{Glyph: ic.Branch, Color: th.Color.TextMuted}.Fn(th), branchLabel, lw[1], ic), gap})
		}
		cfgGlyph := ic.Run
		if c := b.selectedConfig(); c != nil && c.Glyph != nil {
			cfgGlyph = c.Glyph
		}
		right = append(right, seg{SegConfig, b.trigger(th, &b.configBtn, &b.configTip, b.runPop.open, cfgName,
			atoms.Icon{Glyph: cfgGlyph, Color: th.Color.TextMuted}.Fn(th), cfgLabel, lw[2], ic), 0})
		btn := func(name string, ib *atoms.IconButton, tip *atoms.Tooltip, col color.NRGBA, g int) seg {
			return seg{name, func(gtx layout.Context) layout.Dimensions {
				ib.Color = col
				return tip.Layout(gtx, th, func(gtx layout.Context) layout.Dimensions { return ib.Layout(gtx, th) })
			}, g}
		}
		right = append(right,
			btn(SegRun, &b.run, &b.tips[0], th.Color.Success, gap),
			btn(SegDebug, &b.debug, &b.tips[1], color.NRGBA{}, 0))
		if b.Running {
			right = append(right, btn(SegStop, &b.stop, &b.tips[2], th.Color.Danger, 0))
		}
		if !b.NoListen {
			right = append(right, btn(SegListen, &b.listen, &b.tips[3], color.NRGBA{}, 0))
		}
		g := group
		for i := range b.Quick {
			qa := b.Quick[i]
			if qa.Separator {
				g = group
				continue
			}
			i := i
			right = append(right, seg{SegQuick(i), func(gtx layout.Context) layout.Dimensions {
				return b.quickTips[i].Layout(gtx, th, func(gtx layout.Context) layout.Dimensions {
					d := b.quick[i].Layout(gtx, th)
					if qa.Badge != "" {
						r := gtx.Dp(th.Space.XS) - gtx.Dp(th.Space.XXS)/2
						cx, cy := d.Size.X-r-gtx.Dp(th.Space.XS), r+gtx.Dp(th.Space.XS)
						draw.Circle(gtx.Ops, cx, cy, r+gtx.Dp(th.Size.BorderWidth), th.Color.SurfaceAlt)
						draw.Circle(gtx.Ops, cx, cy, r, th.Color.Accent)
					}
					return d
				})
			}, g})
			g = 0
		}
		right = append(right, btn(SegSearch, &b.search, &b.tips[4], color.NRGBA{}, group), btn(SegSettings, &b.settings, &b.tips[5], color.NRGBA{}, 0))
		return left, right
	}
	width := func(segs []seg) int {
		x := 0
		for _, s := range segs {
			x += s.gap + draw.Measure(cg, s.w).Size.X
		}
		return x
	}
	l0, r0 := segsFor([]int{0, 0, 0})
	fixed := 2*padX + width(l0) + width(r0) + group
	alloc := share(want, max(0, w-fixed))
	left, right := segsFor(alloc)
	b.place(cg, h, padX, left)
	b.place(cg, h, w-padX-width(right), right)

	// Popups, anchored below their trigger.
	maxH := gtx.Dp(b.PopupMaxHeight)
	if maxH <= 0 {
		maxH = gtx.Dp(sh.PopupMaxH)
	}
	for _, p := range []struct {
		pp    *popup
		seg   string
		width unit.Dp
	}{{&b.projectPop, SegProject, sh.ProjectPopupW}, {&b.branchPop, SegBranch, sh.BranchPopupW}, {&b.runPop, SegConfig, sh.RunPopupW}} {
		r, ok := b.rects[p.seg]
		if !p.pp.open || !ok {
			continue
		}
		t := op.Offset(r.Min).Push(gtx.Ops)
		pw := min(gtx.Dp(p.width), w-2*padX)
		p.pp.layout(gtx, th, image.Pt(r.Dx(), h-r.Min.Y), pw, w-padX-r.Min.X, maxH)
		t.Pop()
	}
	return layout.Dimensions{Size: image.Pt(w, h)}
}

// place lays out segs from x, vertically centered, and records their
// rectangles.
func (b *Bar) place(gtx layout.Context, h, x int, segs []seg) {
	for _, s := range segs {
		x += s.gap
		m := op.Record(gtx.Ops)
		d := s.w(gtx)
		call := m.Stop()
		y := (h - d.Size.Y) / 2
		t := op.Offset(image.Pt(x, y)).Push(gtx.Ops)
		call.Add(gtx.Ops)
		t.Pop()
		b.rects[s.name] = image.Rectangle{Min: image.Pt(x, y), Max: image.Pt(x+d.Size.X, y+d.Size.Y)}
		x += d.Size.X
	}
}

// share distributes avail among the wanted widths: everyone gets what it
// wants if possible, otherwise the narrow ones keep theirs and the wide
// ones share the rest equally (water filling).
func share(want []int, avail int) []int {
	out := make([]int, len(want))
	idx := make([]int, 0, len(want))
	total := 0
	for i, v := range want {
		total += v
		if v > 0 {
			idx = append(idx, i)
		}
	}
	if total <= avail {
		copy(out, want)
		return out
	}
	// Ascending by wanted width.
	for i := 1; i < len(idx); i++ {
		for j := i; j > 0 && want[idx[j]] < want[idx[j-1]]; j-- {
			idx[j], idx[j-1] = idx[j-1], idx[j]
		}
	}
	rest := avail
	for k, i := range idx {
		fair := rest / (len(idx) - k)
		out[i] = min(want[i], fair)
		rest -= out[i]
	}
	return out
}

// trigger draws a popup trigger: leading marker, truncated label (exactly
// labelW wide at most), chevron; hover and open states tint it. The
// tooltip shows the full text.
func (b *Bar) trigger(th *tokens.Theme, click *widget.Clickable, tip *atoms.Tooltip, open bool, full string,
	lead layout.Widget, label atoms.Label, labelW int, ic Icons) layout.Widget {
	return func(gtx layout.Context) layout.Dimensions {
		tip.Text = full
		return tip.Layout(gtx, th, func(gtx layout.Context) layout.Dimensions {
			return click.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				hgt := gtx.Dp(th.Size.ControlH)
				padX := gtx.Dp(th.Space.S)
				cg := gtx
				cg.Constraints = layout.Constraints{Max: image.Pt(draw.Unbounded, hgt)}
				m := op.Record(gtx.Ops)
				d := layout.Flex{Alignment: layout.Middle}.Layout(cg,
					layout.Rigid(lead),
					layout.Rigid(layout.Spacer{Width: th.Space.S}.Layout),
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						gtx.Constraints = layout.Constraints{Max: image.Pt(labelW, hgt)}
						if labelW <= 0 {
							return layout.Dimensions{}
						}
						return label.SingleLine().Layout(gtx, th)
					}),
					layout.Rigid(layout.Spacer{Width: th.Space.XS}.Layout),
					layout.Rigid(atoms.Icon{Glyph: ic.Chevron, Size: th.Size.IconSmall, Color: th.Color.TextMuted}.Fn(th)),
				)
				call := m.Stop()
				size := image.Pt(d.Size.X+2*padX, hgt)
				r := image.Rectangle{Max: size}
				switch {
				case open:
					draw.RRect(gtx.Ops, r, gtx.Dp(th.Radius.M), th.Color.Selection)
				case click.Hovered():
					draw.RRect(gtx.Ops, r, gtx.Dp(th.Radius.M), th.Color.SurfaceHover)
				}
				t := op.Offset(image.Pt(padX, (hgt-d.Size.Y)/2)).Push(gtx.Ops)
				call.Add(gtx.Ops)
				t.Pop()
				return layout.Dimensions{Size: size}
			})
		})
	}
}
