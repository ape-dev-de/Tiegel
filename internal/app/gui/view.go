package gui

import (
	"fmt"
	"path"
	"runtime"
	"strings"
	"sync"
	"time"

	"gioui.org/io/key"
	"gioui.org/layout"
	"gioui.org/text"

	"github.com/ape-dev-de/tiegel/internal/app/gui/controller"
	"github.com/ape-dev-de/tiegel/internal/editor/keymap"
	"github.com/ape-dev-de/tiegel/internal/ui/atoms"
	"github.com/ape-dev-de/tiegel/internal/ui/molecules"
	"github.com/ape-dev-de/tiegel/internal/ui/organisms/completion"
	"github.com/ape-dev-de/tiegel/internal/ui/organisms/filetree"
	"github.com/ape-dev-de/tiegel/internal/ui/organisms/findbar"
	"github.com/ape-dev-de/tiegel/internal/ui/organisms/importwizard"
	"github.com/ape-dev-de/tiegel/internal/ui/organisms/palette"
	"github.com/ape-dev-de/tiegel/internal/ui/organisms/problems"
	rtui "github.com/ape-dev-de/tiegel/internal/ui/organisms/runtime"
	settingsui "github.com/ape-dev-de/tiegel/internal/ui/organisms/settings"
	"github.com/ape-dev-de/tiegel/internal/ui/organisms/statusbar"
	"github.com/ape-dev-de/tiegel/internal/ui/organisms/welcome"
	"github.com/ape-dev-de/tiegel/internal/ui/templates"
	"github.com/ape-dev-de/tiegel/internal/ui/tokens"
	"github.com/ape-dev-de/tiegel/internal/version"
)

// toolProblems is the ID of the built-in Problems tool window.
const toolProblems = "problems"

// Status bar item IDs.
const (
	statusCaret = "caret"
	statusEOL   = "eol"
	statusLang  = "lang"
	statusLSP   = "lsp"
	statusMsg   = "notice"
)

// chooser modes.
const (
	chooseNone = iota
	chooseFiles
	chooseRecent
	chooseLine
	chooseLocations
	choosePHP
	chooseCustom
)

// View is the main window content: it lays out the workbench from the
// controller state, routes input to the organisms and turns their events
// into controller calls. All methods run on the UI goroutine.
type View struct {
	ctl *controller.Controller
	// Registry holds tool windows, gutter providers, actions and status
	// items (docking points for further features).
	Registry *Registry
	// Invalidate requests a frame (set by the window).
	Invalidate func()

	goos   string
	shaper *text.Shaper
	th     *tokens.Theme
	theme  string // resolved preference the theme was built for
	// systemDark reports the OS appearance for the "system" theme.
	systemDark   func() bool
	systemAt     time.Time
	systemIsDark bool

	bench        templates.Workbench
	tree         filetree.Tree
	treeSeen     [2]uint64 // tree and git versions applied to the organism
	tabBars      map[*controller.EditorGroup]*groupView
	splitViews   map[*controller.SplitNode]*molecules.Split
	status       statusbar.Bar
	crumbPaths   []string // project paths of the breadcrumb segments
	menuBar      menuBar
	toolsFor     *controller.ToolLayout
	toolsSeen    uint64
	toolIDs      map[string]bool
	toolsVersion uint64
	// maxDrawable tells whether the maximized tool window (ToolLayout
	// MaxPanel) could be drawn when the slots were last filled, i.e. is
	// registered with content; only then does the workbench dropping it
	// clear the stored maximize.
	maxDrawable bool
	// activeTool is the tool window the user worked with last
	// (Shift+Escape hides it).
	activeTool string
	// pendingEdit is an editor keymap action chosen in the Edit menu; the
	// active editor runs it in its next layout.
	pendingEdit keymap.Action
	// zoomDelta is the temporary editor font size change (Cmd+=/Cmd+-).
	zoomDelta float32
	fontKey   tokens.FontPrefs
	escKeys   [2]key.Filter

	// Host opens other project windows (nil: single window).
	Host WindowHost
	// sessions are the projects of the window when it hosts several
	// (nil: one project per window, the project lifecycle goes through
	// the controller).
	sessions *Sessions
	// restoreCarets are caret offsets by file applied when the file's
	// editor is created (tabs restored after hibernation).
	restoreCarets map[string]int
	// Quit closes all windows (native app menu); nil: unsupported.
	Quit func()
	// MenuPresenter replaces the default menu presentation (a context
	// menu component, AP10.4).
	MenuPresenter MenuPresenter
	// noNativeDialog forces the Gio folder browser (tests).
	noNativeDialog bool

	postMu   sync.Mutex
	posted   []func()
	php      rtui.StatusbarVersion
	find     findbar.Bar
	findOpen bool
	probs    problems.List
	probData []controller.Diagnostic
	probSeen uint64
	compl    completion.List

	chooser     palette.Chooser
	chooseMode  int
	chooseFiles []string // candidates of the files/recent chooser
	chooseIdx   []int    // chooser item → candidate index

	settings     settingsui.Panel
	settingsOpen bool
	keymapSeen   uint64 // KeymapEdit.Version shown by the keymap page
	mappings     []controller.Mapping

	welcome welcome.View
	// importView is the body of the settings import wizard.
	importView importwizard.View

	editors map[sessionKey]*editorSession

	global, editorKeys shortcuts
	lastActive         string
	// focusEditor moves the keyboard focus to the editor on the next
	// frame (after choosers and jumps).
	focusEditor bool

	// Raise brings the window to the front (set by the window loop).
	Raise func()

	hdr   *header
	gitd  *gitDock
	dbgd  *debugDock
	navd  *navDock
	tmpl  *templateDock
	lspd  *lspDock
	svcd  *servicesDock
	rund  *runDock
	termd *termDock
	tstd  *testDock
	cmpd  *composerDock
	extd  *toolsDock
	// termGlobal are the window shortcuts while the terminal has the
	// focus (terminal.overrideIdeShortcuts).
	termGlobal shortcuts
	// work is the file side: tree menu, external changes, diff and
	// history tabs, remote git (filesdock.go and friends).
	work *workDock

	recovery *recoveryBar
	// phpStart: the PHP chooser starts with "Start container & detect".
	phpStart bool

	custom    *ChooserSpec
	popup     *Popup
	clipboard *string

	// keys is the state of keymap sync, pointer navigation and the M13
	// popups (keynav.go).
	keys keyNav
}

// NewView returns the view for ctl. goos selects the keymap ("darwin":
// Cmd shortcuts).
func NewView(ctl *controller.Controller, goos string) *View {
	return newView(ctl, goos, nil)
}

// newView is NewView with a text shaper shared by the views of one
// window (nil: a new one). A shaper belongs to one UI goroutine.
func newView(ctl *controller.Controller, goos string, sh *text.Shaper) *View {
	if goos == "" {
		goos = runtime.GOOS
	}
	if sh == nil {
		sh = newShaper()
	}
	v := &View{
		ctl: ctl, goos: goos, Registry: NewRegistry(), shaper: sh,
		editors: map[sessionKey]*editorSession{}, Invalidate: func() {},
		systemDark: systemDarkMode, Raise: func() {}, recovery: newRecoveryBar(),
		tabBars: map[*controller.EditorGroup]*groupView{}, splitViews: map[*controller.SplitNode]*molecules.Split{},
		toolIDs: map[string]bool{},
	}
	v.Registry.SetGOOS(goos)
	v.tree.Icon = treeIcon
	for _, a := range defaultActions() {
		v.Registry.AddAction(a)
	}
	v.installProjectTool()
	v.Registry.AddToolWindow(ToolWindow{
		ID: toolProblems, Title: "Problems", Glyph: atoms.GlyphWarning, Order: 10,
		Mac: "Cmd+6", PC: "Alt+6", ActionID: ActProblemsView,
		Badge: func() (string, atoms.BadgeKind) {
			e, w := ctl.ProblemCounts()
			switch {
			case e > 0:
				return fmt.Sprint(e + w), atoms.BadgeDanger
			case w > 0:
				return fmt.Sprint(w), atoms.BadgeWarning
			}
			return "", 0
		},
		Layout: v.layoutProblems,
	})
	v.welcome.Version = version.Version
	v.applyTheme()
	v.gitd = installGit(v)
	v.dbgd = installDebug(v)
	v.work = installWork(v)
	v.navd = installNav(v)
	v.tmpl = installTemplates(v)
	v.installKeyNav()
	v.lspd = installLSP(v)
	v.svcd = installServices(v)
	v.rund = installRun(v)
	v.tstd = installTests(v)
	v.cmpd = installComposer(v)
	v.extd = installExternalTools(v)
	v.termd = installTerminal(v)
	v.hdr = installHeader(v)
	return v
}

// Controller returns the controller.
func (v *View) Controller() *controller.Controller { return v.ctl }

// Theme returns the current theme.
func (v *View) Theme() *tokens.Theme { return v.th }

// RunAction runs a registered action (shortcut processing; editor-scope
// actions are run by the editor with its document locked).
func (v *View) RunAction(id keymap.Action) {
	if a, ok := v.Registry.Action(id); ok && a.Run != nil {
		a.Run(v)
	}
}

// Post runs fn on the UI goroutine during the next frame (from any
// goroutine) and requests that frame.
func (v *View) Post(fn func()) {
	v.postMu.Lock()
	v.posted = append(v.posted, fn)
	v.postMu.Unlock()
	if v.Invalidate != nil {
		v.Invalidate()
	}
}

func (v *View) runPosted() {
	v.postMu.Lock()
	q := v.posted
	v.posted = nil
	v.postMu.Unlock()
	for _, fn := range q {
		fn()
	}
}

// fontPrefs returns the font preferences with the zoom applied.
func (v *View) fontPrefs() tokens.FontPrefs {
	p := v.ctl.Prefs
	fp := tokens.FontPrefs{EditorFamily: p.EditorFont, EditorSize: p.EditorFontSize, LineHeight: p.LineHeight, UISize: p.UIFontSize}.Normalized()
	if v.zoomDelta != 0 {
		fp.EditorSize = min(max(fp.EditorSize+v.zoomDelta, 6), 48)
	}
	return fp
}

// zoom changes the editor font size temporarily (delta 0 resets).
func (v *View) zoom(delta int) {
	if delta == 0 {
		v.zoomDelta = 0
		return
	}
	base := tokens.FontPrefs{EditorSize: v.ctl.Prefs.EditorFontSize}.Normalized().EditorSize
	v.zoomDelta = min(max(v.zoomDelta+float32(delta), 6-base), 48-base)
}

// toggleSoftWrap switches soft wrap of the active editor (only this
// editor; the setting editor.softWrap stays as it is, D285).
func (v *View) toggleSoftWrap() {
	s := v.activeSession()
	if s == nil {
		return
	}
	want := !s.softWrap(v.ctl.Prefs)
	s.wrapToggle = 0 // back to the setting when that is what is wanted
	switch {
	case want == s.softWrap(v.ctl.Prefs):
	case want:
		s.wrapToggle = 1
	default:
		s.wrapToggle = -1
	}
	s.ed.SoftWrap = want
	v.focusEditor = true
}

// toggleWhitespace switches editor.showWhitespace between none and all
// (any other mode counts as shown and switches it off).
func (v *View) toggleWhitespace() {
	next := "all"
	if v.ctl.Prefs.ShowWhitespace != "" && v.ctl.Prefs.ShowWhitespace != "none" {
		next = "none"
	}
	v.ctl.Prefs.ShowWhitespace = next
	v.ctl.SetSetting(controller.KeyShowWhitespace, "global", next)
	v.focusEditor = true
}

// applyTheme rebuilds the theme when the preference changed.
func (v *View) applyTheme() {
	pref := v.ctl.Prefs.Theme
	mode := tokens.ModeDark
	switch pref {
	case "light":
		mode = tokens.ModeLight
	case "system":
		if v.systemDark != nil && time.Since(v.systemAt) > 5*time.Second {
			v.systemAt, v.systemIsDark = time.Now(), v.systemDark()
		}
		if !v.systemIsDark {
			mode = tokens.ModeLight
		}
	}
	fp := v.fontPrefs()
	if v.th != nil && v.theme == pref && v.th.Mode == mode && v.fontKey == fp {
		return
	}
	v.theme, v.fontKey = pref, fp
	v.th = tokens.New(mode, v.shaper).WithFonts(fp)
}

// expandHome replaces a leading ~ with the home directory.
func expandHome(p string) string {
	p = strings.TrimSpace(p)
	if strings.HasPrefix(p, "~") {
		if h := homeDir(); h != "" {
			return h + p[1:]
		}
	}
	return p
}

func (v *View) handleTree(gtx layout.Context) {
	ct := v.ctl.Tree
	if ct == nil {
		v.tree.Root = nil
		return
	}
	if seen := [2]uint64{ct.Version, v.ctl.GitVersion}; seen != v.treeSeen || v.tree.Root == nil {
		v.treeSeen = seen
		v.tree.Root = v.convertNode(ct.Root)
		// A revealed file expanded its parents without an ExpandEvent;
		// load them level by level as the tree grows.
		v.ctl.LoadAncestors(v.tree.Selected())
	}
	for _, e := range v.tree.Update(gtx) {
		switch e := e.(type) {
		case filetree.ExpandEvent:
			if e.Expanded && !e.Loaded {
				v.ctl.LoadDir(e.Path)
			}
		case filetree.OpenEvent:
			v.ctl.OpenFile(e.Path, nil)
		case filetree.ContextEvent, filetree.KeyEvent:
			v.treeEvent(e)
		}
	}
}

func (v *View) convertNode(n *controller.Node) *filetree.Node {
	fn := &filetree.Node{Name: n.Name, Path: n.Path, Dir: n.Dir, Loaded: n.Loaded, Library: n.Library, Ignored: n.Ignored,
		Status: filetree.StatusFromGit(v.ctl.GitState(n.Path))}
	for _, ch := range n.Children {
		fn.Children = append(fn.Children, v.convertNode(ch))
	}
	return fn
}

func (v *View) handleStatus(gtx layout.Context) {
	st := v.ctl.Status
	v.php.Version, v.php.Source, v.php.Interpreter = st.PHP.Short, st.PHP.Source, st.Interpreter
	if st.PHP.Detail != "" {
		v.php.Source = st.PHP.Source + ": " + st.PHP.Detail
	}
	v.php.Override = st.PHP.Source == "override"
	v.php.Hint, v.php.HintBadge = "", ""
	if h := st.PHP.Hint; h != nil {
		v.php.Hint, v.php.HintBadge = h.Message, "hint"
		if h.Code == controller.HintContainerNotRunning {
			v.php.HintBadge = "container stopped"
			v.php.Hint += " – click: Start container & detect"
		}
	}
	for _, e := range v.php.Update(gtx) {
		if _, ok := e.(rtui.OverrideEvent); ok {
			v.openPHPChooser()
		}
	}
	left := []statusbar.Item{}
	if st.Notice != "" {
		left = append(left, statusbar.Item{ID: statusMsg, Text: firstLine(st.Notice), Tooltip: st.Notice + " (click to dismiss)", Tone: statusbar.ToneMuted, Clickable: true})
	}
	var right []statusbar.Item
	if s := v.activeSession(); s != nil {
		right = append(right,
			statusbar.Item{ID: statusCaret, Text: s.caretText, Tooltip: "Go to line", Clickable: true},
			statusbar.Item{ID: statusEOL, Text: "UTF-8 · " + strings.ToUpper(orDefault(s.tab.EOL, "lf"))},
			statusbar.Item{ID: statusLang, Text: languageName(s.tab.Language)},
		)
	}
	right = append(right, statusbar.Item{ID: "php", Widget: &v.php})
	if st.LSP != "" {
		tone := statusbar.ToneMuted
		g := atoms.GlyphInfo
		if !st.LSPRunning {
			tone, g = statusbar.ToneWarning, atoms.GlyphWarning
		}
		right = append(right, statusbar.Item{ID: statusLSP, Text: st.LSP, Tooltip: st.LSPHint, Glyph: g, Tone: tone, Clickable: true})
	}
	for _, p := range v.Registry.status {
		right = append(right, p()...)
	}
	v.status.Left, v.status.Right = left, right
	v.status.Path, v.crumbPaths = v.breadcrumb()
	for _, ev := range v.status.Update(gtx) {
		if c, ok := ev.(statusbar.CrumbEvent); ok {
			v.crumbClicked(c.Index)
			continue
		}
		e, ok := ev.(statusbar.ClickEvent)
		if !ok {
			continue
		}
		switch e.ID {
		case statusGitID:
			// The branch popup of the header replaces the chooser.
			v.RunAction(ActBranches)
		case statusCaret:
			v.openGotoLine()
		case statusLSP:
			v.lspd.openMenu()
		case statusMsg:
			if !v.lspd.noticeClick() {
				v.ctl.Status.Notice = ""
			}
		default:
			for _, h := range v.Registry.statusClicks {
				if h(v, e.ID) {
					break
				}
			}
		}
	}
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	if len(s) > 120 {
		s = s[:117] + "…"
	}
	return s
}

func orDefault(s, def string) string {
	if s == "" {
		return def
	}
	return s
}

func languageName(l string) string {
	switch l {
	case "php":
		return "PHP"
	case "json", "xml", "yaml":
		return strings.ToUpper(l)
	case "dotenv", "env":
		return ".env"
	case "":
		return "Text"
	}
	return strings.ToUpper(l[:1]) + l[1:]
}

func (v *View) handleProblems(gtx layout.Context) {
	if v.probSeen != v.ctl.DiagVersion || v.probData == nil {
		v.probSeen = v.ctl.DiagVersion
		v.probData = v.ctl.Problems()
		if v.probData == nil {
			v.probData = []controller.Diagnostic{}
		}
		v.probs.Items = v.probs.Items[:0]
		for _, d := range v.probData {
			v.probs.Items = append(v.probs.Items, problems.Problem{Path: d.Path, Line: d.Line, Col: d.Col, Severity: problems.Severity(d.Severity), Message: d.Message, Source: d.Source})
		}
	}
	for _, e := range v.probs.Update(gtx) {
		if o, ok := e.(problems.OpenEvent); ok && o.Index < len(v.probData) {
			d := v.probData[o.Index]
			v.ctl.GoTo(v.currentLocation(), controller.At(d.File, d.Start))
			v.focusEditor = true
		}
	}
}

func (v *View) layoutProblems(gtx layout.Context, th *tokens.Theme) layout.Dimensions {
	return v.probs.Layout(gtx, th)
}

// ---- overlays: chooser and settings -------------------------------------------

func (v *View) closeOverlay() {
	if p := v.popup; p != nil {
		v.popup = nil
		if p.OnClose != nil {
			p.OnClose()
		}
	}
	v.custom = nil
	v.chooseMode = chooseNone
	v.ctl.Locations = nil
	if v.settingsOpen {
		v.settingsOpen = false
	}
	v.focusEditor = true
}

func (v *View) openQuickOpen() {
	if !v.ctl.Project.Open {
		return
	}
	v.settingsOpen = false
	v.chooseMode = chooseFiles
	v.chooser.Open("Go to File", "File name (fuzzy)", "")
	v.ctl.LoadFileIndex()
	v.filterFiles("")
}

func (v *View) openRecent() {
	if !v.ctl.Project.Open {
		return
	}
	v.settingsOpen = false
	v.chooseMode = chooseRecent
	v.chooser.Open("Recent Files", "Filter", "")
	v.filterFiles("")
	// The current file is first; preselect the previous one.
	if len(v.chooser.Items) > 1 && v.ctl.ActiveTab() != nil {
		v.chooser.Selected = 1
	}
}

func (v *View) openGotoLine() {
	s := v.activeSession()
	if s == nil {
		return
	}
	v.settingsOpen = false
	v.chooseMode = chooseLine
	v.chooser.Open("Go to Line", "line[:column]", "")
	v.chooser.NoList = true
	v.chooser.Items = nil
}

func (v *View) openPHPChooser() {
	v.settingsOpen = false
	v.chooseMode = choosePHP
	v.chooser.Open("PHP Version", "Filter", "")
	v.chooser.Items = v.chooser.Items[:0]
	h := v.ctl.Status.PHP.Hint
	v.phpStart = h != nil && h.Code == controller.HintContainerNotRunning
	if v.phpStart {
		v.chooser.Items = append(v.chooser.Items, palette.Item{Label: "Start container & detect", Detail: "starts the stopped compose service (docker may pull its image)", Glyph: atoms.GlyphContainer})
	}
	v.chooser.Items = append(v.chooser.Items, palette.Item{Label: "Detect automatically", Detail: "interpreter, then composer.json", Glyph: atoms.GlyphRefresh})
	for _, ver := range controller.PHPVersions {
		v.chooser.Items = append(v.chooser.Items, palette.Item{Label: "PHP " + ver, Glyph: atoms.GlyphTag})
	}
}

func (v *View) openSettings() {
	v.chooseMode = chooseNone
	v.settingsOpen = true
	v.settings.Keymap.CancelRecording()
	v.ctl.LoadKeymapList()
	v.ctl.LoadSettingsData()
	v.ctl.LoadMonoFonts(tokens.MonospaceFamilies)
	v.mappings = nil
}

func (v *View) filterFiles(q string) {
	var cands []string
	switch v.chooseMode {
	case chooseFiles:
		cands = v.ctl.Files
	case chooseRecent:
		for _, f := range v.ctl.RecentFiles {
			cands = append(cands, v.ctl.Rel(f))
		}
	}
	v.chooseFiles = cands
	v.chooseIdx = v.chooseIdx[:0]
	v.chooser.Items = v.chooser.Items[:0]
	for _, m := range controller.FilterFuzzy(q, cands, 200) {
		p := cands[m.Index]
		dir := path.Dir(p)
		if dir == "." {
			dir = ""
		}
		v.chooseIdx = append(v.chooseIdx, m.Index)
		v.chooser.Items = append(v.chooser.Items, palette.Item{Label: path.Base(p), Detail: dir, Glyph: fileGlyph(p)})
	}
	if v.chooseMode == chooseFiles && len(v.ctl.Files) == 0 {
		v.chooser.Message = "Indexing project files…"
	} else {
		v.chooser.Message = ""
	}
}

func (v *View) handleOverlay(gtx layout.Context) {
	if v.ctl.Locations != nil && v.chooseMode != chooseLocations {
		v.chooseMode = chooseLocations
		v.chooser.Open(v.ctl.LocationsTitle, "Filter", "")
		v.chooser.Items = v.chooser.Items[:0]
		for _, l := range v.ctl.Locations {
			v.chooser.Items = append(v.chooser.Items, palette.Item{Label: fmt.Sprintf("%s:%d", path.Base(l.Path), l.Range.Start.Line), Detail: l.Path, Glyph: atoms.GlyphFileCode})
		}
	}
	if v.settingsOpen {
		v.handleSettings(gtx)
		return
	}
	if v.popup != nil {
		v.handlePopup(gtx)
		return
	}
	if v.chooseMode == chooseNone {
		return
	}
	// The file index may arrive after the chooser opened.
	if v.chooseMode == chooseFiles && len(v.chooseFiles) != len(v.ctl.Files) {
		v.filterFiles(v.chooser.Query())
	}
	for _, e := range v.chooser.Update(gtx) {
		switch e := e.(type) {
		case palette.CancelEvent:
			v.closeOverlay()
		case palette.QueryEvent:
			if v.chooseMode == chooseFiles || v.chooseMode == chooseRecent {
				v.filterFiles(e.Query)
			}
			if v.chooseMode == chooseCustom && v.custom != nil && v.custom.OnQuery != nil {
				v.chooser.Items = v.custom.OnQuery(e.Query)
			}
		case palette.ChooseEvent:
			v.choose(e)
		case palette.TabEvent:
			if v.chooseMode == chooseCustom && v.custom != nil && v.custom.OnTab != nil {
				v.chooser.Items = v.custom.OnTab(e.Index, v.chooser.Query())
			}
		}
	}
}

func (v *View) choose(e palette.ChooseEvent) {
	mode := v.chooseMode
	v.chooseMode = chooseNone
	v.focusEditor = true
	switch mode {
	case chooseFiles, chooseRecent:
		if e.Index >= 0 && e.Index < len(v.chooseIdx) {
			v.ctl.OpenFile(v.chooseFiles[v.chooseIdx[e.Index]], nil)
		}
	case chooseLocations:
		v.ctl.ChooseLocation(e.Index)
	case chooseLine:
		if line, col, ok := controller.ParseLineCol(e.Query); ok {
			v.gotoLine(line, col)
		}
	case chooseCustom:
		if cs := v.custom; cs != nil {
			v.custom = nil
			if cs.OnChoose != nil {
				cs.OnChoose(e.Index, e.Query)
			}
		}
	case choosePHP:
		i := e.Index
		if v.phpStart {
			if i == 0 {
				v.ctl.DetectPHPStarting()
				break
			}
			i--
		}
		switch {
		case i == 0:
			v.ctl.SetPHPOverride("")
		case i > 0 && i <= len(controller.PHPVersions):
			v.ctl.SetPHPOverride(controller.PHPVersions[i-1])
		}
	}
}

func (v *View) handleSettings(gtx layout.Context) {
	p := &v.settings
	c := v.ctl
	p.NoProject = !c.Project.Open
	if p.NoProject {
		p.Scope = settingsui.ScopeGlobal
	}
	p.Pages = settingsPages(c)
	p.UpdateStatus = ""
	if strings.Contains(c.Status.Notice, "pdate") {
		p.UpdateStatus = c.Status.Notice
	}
	p.Interpreter.Items = p.Interpreter.Items[:0]
	p.Interpreter.Selected = ""
	for _, i := range c.Interpreters {
		p.Interpreter.Items = append(p.Interpreter.Items, rtui.Interpreter{ID: i.ID, Label: i.Label, Kind: rtui.Kind(i.Kind), Version: i.Version,
			Detail: orDefault(i.Path, orDefault(i.Service, i.Image)), Score: i.Score, Context: i.Context, State: i.State})
		if i.Selected {
			p.Interpreter.Selected = i.ID
		}
	}
	setProbePanel(&p.Interpreter.Info, c.Probe)
	if v.mappings == nil {
		v.mappings = append([]controller.Mapping{}, c.Mappings.Mappings...)
	}
	p.Mappings.Items = p.Mappings.Items[:0]
	for _, m := range v.mappings {
		p.Mappings.Items = append(p.Mappings.Items, rtui.Mapping{Host: m.Host, Remote: m.Remote})
	}
	p.Hints.Items = p.Hints.Items[:0]
	for _, h := range c.Diagnosis.Hints {
		sev := rtui.SeverityInfo
		switch h.Severity {
		case "warning":
			sev = rtui.SeverityWarning
		case "error":
			sev = rtui.SeverityError
		}
		p.Hints.Items = append(p.Hints.Items, rtui.Hint{Severity: sev, Code: h.Code, Message: h.Message})
	}
	v.fillKeymapEditor()
	for !p.Recording() { // a recording keymap editor gets Escape itself
		ev, ok := gtx.Event(key.Filter{Name: key.NameEscape})
		if !ok {
			break
		}
		if e, ok := ev.(key.Event); ok && e.State == key.Press {
			v.closeOverlay()
			return
		}
	}
	for _, e := range p.Update(gtx) {
		v.handleSettingsEvent(e)
	}
}

func (v *View) handleRuntimeEvent(e rtui.Event) {
	switch e := e.(type) {
	case rtui.SelectInterpreterEvent:
		v.ctl.SelectInterpreter(e.ID)
		v.mappings = nil
	case rtui.ProbeRefreshEvent:
		v.ctl.RefreshProbe(e.ID)
	case rtui.MappingAddEvent:
		v.mappings = append(v.mappings, controller.Mapping{})
	case rtui.MappingChangeEvent:
		if e.Index < len(v.mappings) {
			v.mappings[e.Index] = controller.Mapping{Host: e.Host, Remote: e.Remote}
			v.saveMappings()
		}
	case rtui.MappingRemoveEvent:
		if e.Index < len(v.mappings) {
			v.mappings = append(v.mappings[:e.Index], v.mappings[e.Index+1:]...)
			v.saveMappings()
		}
	}
}

// saveMappings stores the complete, valid mappings.
func (v *View) saveMappings() {
	var ms []controller.Mapping
	for _, m := range v.mappings {
		if m.Host != "" && m.Remote != "" && rtui.RemoteValid(m.Remote) {
			ms = append(ms, m)
		}
	}
	v.ctl.SetMappings(ms, len(v.mappings) == 0)
}
