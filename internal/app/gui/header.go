package gui

import (
	"path/filepath"
	"strings"

	"gioui.org/layout"

	"github.com/ape-dev-de/tiegel/internal/actions"
	"github.com/ape-dev-de/tiegel/internal/editor/keymap"
	"github.com/ape-dev-de/tiegel/internal/ui/atoms"
	"github.com/ape-dev-de/tiegel/internal/ui/fileicons"
	"github.com/ape-dev-de/tiegel/internal/ui/organisms/toolbar"
	"github.com/ape-dev-de/tiegel/internal/ui/tokens"
)

// The header (D465, D471): one toolbar.Bar in the workbench's header
// slot, filled from the controller every frame. Its events map to the
// existing commands; "…" entries open the existing dialogs. The project
// switcher shows the window's open projects with their state and the
// recent projects (toolbarProjects); switching, hibernating, resuming and
// closing go through ProjectSessions (the window's Sessions, D477).

// ActBranches opens the branch popup of the header.
const ActBranches keymap.Action = "git.branches"

// Actions for a merge or rebase stopped on conflicts.
const (
	ActGitContinue keymap.Action = "git.continueOperation"
	ActGitAbort    keymap.Action = "git.abortOperation"
)

// ProjectSessions performs the project switcher's life-cycle intents.
// IDs are project roots (toolbar.Project IDs). A window with the
// multi-project workspace uses *Sessions (D477); singleProject is the
// fallback of a view without one (tests, embedders).
type ProjectSessions interface {
	// Projects returns the open projects and the ID of the active one.
	Projects() (open []toolbar.Project, current string)
	// Switch shows project id in this window.
	Switch(id string)
	// Hibernate releases the processes and memory of project id.
	Hibernate(id string)
	// Resume brings a hibernated project back.
	Resume(id string)
	// Close closes project id.
	Close(id string)
	// Cycle switches to the next (1) or previous (-1) running project.
	Cycle(delta int)
}

// singleProject is the ProjectSessions of a view without a workspace:
// switching opens the project (asking "this window or new window"),
// hibernation is not available.
type singleProject struct{ v *View }

func (s singleProject) Projects() ([]toolbar.Project, string) {
	c := s.v.ctl
	if !c.Project.Open {
		return nil, ""
	}
	name := c.Project.Name
	if name == "" {
		name = filepath.Base(c.Project.Root)
	}
	return []toolbar.Project{{ID: c.Project.Root, Name: name, Path: homeShort(c.Project.Root), State: toolbar.ProjectActive}}, c.Project.Root
}

func (s singleProject) Switch(id string) { s.v.requestProject(id) }

func (s singleProject) Hibernate(string) {
	s.v.ctl.Status.Notice = "Hibernate: this window has no multi-project workspace"
}

func (s singleProject) Resume(id string) { s.v.requestProject(id) }

func (s singleProject) Close(string) { s.v.closeProject() }

func (s singleProject) Cycle(int) {}

// header is the state of the header toolbar.
type header struct {
	v   *View
	bar *toolbar.Bar
	// Sessions handles project switching (default: singleProject).
	Sessions ProjectSessions
	// git tells whether the git actions are registered.
	git bool
}

func installHeader(v *View) *header {
	h := &header{v: v, bar: toolbar.New(), Sessions: singleProject{v}}
	h.bar.Icons = headerIcons()
	return h
}

// branchesAction opens the header's branch popup (registered while the
// project is a git repository).
func (h *header) branchesAction() Action {
	return Action{ID: ActBranches, Title: "Branches…", Menu: "Git", Section: "branch", Run: func(v *View) {
		if h.bar.Branch != "" {
			h.bar.Show(toolbar.PopupBranch)
		}
	}}
}

// operationActions continue or abort a merge or rebase in progress.
func (h *header) operationActions() []Action {
	return []Action{
		{ID: ActGitContinue, Title: "Continue Merge or Rebase", Menu: "Git", Section: "branch", Run: func(v *View) {
			if c := v.ctl; c.Vcs != nil {
				c.Vcs.ContinueOp(nil)
			}
		}},
		{ID: ActGitAbort, Title: "Abort Merge or Rebase…", Menu: "Git", Section: "branch", Run: func(v *View) {
			c := v.ctl
			if c.Vcs == nil {
				return
			}
			op := c.Vcs.InProgress()
			if op == "" {
				c.Status.Notice = "No merge or rebase in progress"
				return
			}
			v.Confirm("Abort the "+op+"?", "The branch returns to its state before the "+op+"; conflict resolutions are lost.", "Abort",
				func() { c.Vcs.AbortOp(nil) })
		}},
	}
}

// mergeOrRebase asks for confirmation and merges branch into the
// current branch (rebase: rebases the current branch onto it). Conflicts
// open the first file in a merge window; the Git window lists all.
func (h *header) mergeOrRebase(branch string, rebase bool) {
	v, c := h.v, h.v.ctl
	if c.Vcs == nil || !c.Vcs.Available || h.bar.Branch == "" {
		c.Status.Notice = "Merge and rebase need a git repository"
		return
	}
	if op := c.Vcs.InProgress(); op != "" {
		c.Status.Notice = "A " + op + " is in progress: continue or abort it first (Git menu)"
		return
	}
	cur := h.bar.Branch
	title, detail, ok := "Merge “"+branch+"” into “"+cur+"”?", "Runs git merge with the default behaviour (fast-forward when possible, otherwise a merge commit). Conflicts can be resolved in the merge window.", "Merge"
	if rebase {
		title, detail, ok = "Rebase “"+cur+"” onto “"+branch+"”?", "Replays the commits of “"+cur+"” on top of “"+branch+"” (rewrites them). Conflicts can be resolved in the merge window, then continue the rebase from the Git menu.", "Rebase"
	}
	v.Confirm(title, detail, ok, func() {
		if rebase {
			c.Vcs.Rebase(branch, nil)
		} else {
			c.Vcs.Merge(branch, nil)
		}
	})
}

// headerIcons uses the tool glyphs of the own icon set where it has one.
func headerIcons() toolbar.Icons {
	ic := toolbar.DefaultIcons()
	set := func(dst **atoms.Glyph, n fileicons.ToolName) {
		if g := fileicons.ToolGlyph(n); g != nil {
			*dst = g
		}
	}
	set(&ic.Branch, fileicons.ToolBranch)
	set(&ic.Run, fileicons.ToolRun)
	set(&ic.Debug, fileicons.ToolDebug)
	set(&ic.Stop, fileicons.ToolStop)
	set(&ic.Search, fileicons.ToolSearch)
	set(&ic.Settings, fileicons.ToolSettings)
	set(&ic.Hibernate, fileicons.ToolSleep)
	if g := fileicons.GlyphFor("project", true, fileicons.Hints{}); g != nil {
		ic.Folder = g
	}
	return ic
}

// toolbarProjects is the adapter between the sessions and the project
// switcher: the open projects with their state and the recent projects
// that are not open.
func (h *header) toolbarProjects() (open []toolbar.Project, current string, recent []toolbar.RecentProject) {
	open, current = h.Sessions.Projects()
	isOpen := map[string]bool{}
	for _, p := range open {
		isOpen[p.ID] = true
	}
	for _, p := range h.v.ctl.RecentProjects {
		if isOpen[p] {
			continue
		}
		recent = append(recent, toolbar.RecentProject{Name: filepath.Base(p), Path: homeShort(p)})
	}
	return open, current, recent
}

// homeShort abbreviates the home directory as "~".
func homeShort(p string) string {
	if h := homeDir(); h != "" && strings.HasPrefix(p, h+string(filepath.Separator)) {
		return "~" + p[len(h):]
	}
	return p
}

// sync copies the controller state into the bar.
func (h *header) sync() {
	b, c := h.bar, h.v.ctl
	b.Projects, b.Current, b.Recent = h.toolbarProjects()
	b.NewWindows, b.GOOS = h.v.Host != nil, h.v.goos
	b.Branch = ""
	git := c.Vcs != nil && c.Vcs.Available && c.Project.Git
	if git != h.git {
		h.git = git
		if git {
			h.v.Registry.AddAction(h.branchesAction())
			for _, a := range h.operationActions() {
				h.v.Registry.AddAction(a)
			}
		} else {
			h.v.Registry.RemoveAction(ActBranches)
			for _, a := range h.operationActions() {
				h.v.Registry.RemoveAction(a.ID)
			}
		}
	}
	if g := c.Vcs; git {
		b.Branch = g.Info.Branch
		if g.Info.Detached && len(g.Info.Head) >= 7 {
			b.Branch = "HEAD (" + g.Info.Head[:7] + ")"
		}
		b.Branches = g.Branches
	}
	r := c.Run
	b.Configs = b.Configs[:0]
	b.Config = ""
	for _, cfg := range r.Configs {
		rc := toolbar.RunConfig{ID: cfg.ID, Name: cfg.Name, Detail: cfg.TypeLabel}
		if cfg.Error != "" {
			rc.Detail = cfg.Error
		}
		b.Configs = append(b.Configs, rc)
		if cfg.Selected {
			b.Config = cfg.ID
		}
	}
	b.Running = r.ActiveCount() > 0
	b.NoListen = !c.Debug.Available
	b.Listening = c.Debug.Listening
	m := h.v.Registry.Model()
	b.Quick = quickActions(m.Toolbar(h.v.actionContext(actions.CtxToolbar), nil, h.v.goos))
}

// quickActions converts the toolbar buttons; icon names may also name a
// tool glyph of the own icon set ("agent", "database" …).
func quickActions(bs []actions.ToolbarButton) []toolbar.QuickAction {
	out := toolbar.QuickActions(bs)
	for i, b := range bs {
		if atoms.GlyphNamed(b.Icon) != nil || b.Icon == "" {
			continue
		}
		if g := fileicons.ToolGlyph(fileicons.ToolName(b.Icon)); g != nil {
			out[i].Glyph = g
		}
	}
	return out
}

// update handles the bar's events.
func (h *header) update(gtx layout.Context) {
	h.sync()
	for _, e := range h.bar.Update(gtx) {
		h.handle(e)
	}
}

// handle turns a toolbar event into a command.
func (h *header) handle(ev toolbar.Event) {
	v := h.v
	c := v.ctl
	switch e := ev.(type) {
	case toolbar.PopupOpened:
		switch e.Popup {
		case toolbar.PopupBranch:
			if c.Vcs != nil {
				c.Vcs.LoadBranches()
			}
		case toolbar.PopupRun:
			c.Run.LoadConfigs()
		case toolbar.PopupProject:
			c.ReloadRecentProjects()
		}
	case toolbar.SwitchProject:
		switch {
		case e.NewWindow:
			v.requestProjectNewWindow(e.ID)
		case e.ID != h.bar.Current:
			h.Sessions.Switch(e.ID)
		}
	case toolbar.CloseProject:
		if e.ID == c.Project.Root {
			v.closeProject()
		} else {
			h.Sessions.Close(e.ID)
		}
	case toolbar.HibernateProject:
		h.Sessions.Hibernate(e.ID)
	case toolbar.ResumeProject:
		h.Sessions.Resume(e.ID)
	case toolbar.OpenProject:
		v.openProjectDialogIn(e.NewWindow)
	case toolbar.OpenRecentProject:
		if e.NewWindow {
			v.requestProjectNewWindow(expandHome(e.Path))
		} else {
			v.requestProject(expandHome(e.Path))
		}
	case toolbar.CheckoutBranch:
		c.Vcs.Checkout(e.Name, e.Remote)
	case toolbar.NewBranch:
		h.askBranchName(e.From)
	case toolbar.CheckoutRevision:
		v.OpenChooser(ChooserSpec{Title: "Checkout Tag or Revision", Hint: "Tag, branch or commit", NoList: true,
			OnChoose: func(_ int, q string) {
				if q = strings.TrimSpace(q); q != "" {
					c.Vcs.Checkout(q, false)
				}
			}})
	case toolbar.MergeBranch:
		h.mergeOrRebase(e.Name, false)
	case toolbar.RebaseOnto:
		h.mergeOrRebase(e.Name, true)
	case toolbar.DeleteBranch:
		if e.Remote {
			c.Status.Notice = "Deleting remote branches is not supported"
			break
		}
		name := e.Name
		v.Confirm("Delete branch “"+name+"”?", "Unmerged commits of the branch are kept only if another branch contains them.", "Delete",
			func() { c.Vcs.DeleteBranch(name, false) })
	case toolbar.SelectRunConfig:
		c.Run.SelectConfig(e.ID)
	case toolbar.EditRunConfigs:
		v.RunAction(ActRunConfigs)
	case toolbar.Run:
		if e.Config != "" {
			c.Run.RunConfig(e.Config)
		} else {
			v.RunAction(ActRun)
		}
	case toolbar.Debug:
		v.RunAction(ActDebug)
	case toolbar.Stop:
		h.stopRun()
	case toolbar.ToggleListen:
		if e.On != c.Debug.Listening {
			c.Debug.ToggleListen()
		}
	case toolbar.QuickActionClicked:
		v.RunAction(keymap.Action(e.ID))
	case toolbar.SearchEverywhere:
		v.RunAction(ActSearchEverywhere)
	case toolbar.OpenSettings:
		v.RunAction(ActSettings)
	}
}

// stopRun stops the most recent active run.
func (h *header) stopRun() {
	r := h.v.ctl.Run
	for i := len(r.Tabs) - 1; i >= 0; i-- {
		if t := r.Tabs[i]; t.Active() {
			r.Stop(t.Info.ID)
			return
		}
	}
}

// askBranchName asks for the name of a new branch from start ("" HEAD)
// and checks it out.
func (h *header) askBranchName(start string) {
	c := h.v.ctl
	title := "New Branch"
	if start != "" {
		title = "New Branch from “" + start + "”"
	}
	h.v.OpenChooser(ChooserSpec{Title: title, Hint: "Branch name (checked out after creation)", NoList: true,
		OnChoose: func(_ int, q string) {
			if q = strings.TrimSpace(q); q != "" {
				c.Vcs.CreateBranch(q, start, true)
			}
		}})
}

// layout draws the bar (header slot).
func (h *header) layout(gtx layout.Context, th *tokens.Theme) layout.Dimensions {
	return h.bar.Layout(gtx, th)
}
