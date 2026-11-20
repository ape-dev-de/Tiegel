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
// switcher shows the current project and the recent projects
// (toolbarProjects); switching, hibernating and resuming go through
// ProjectSessions so the multi-project workspace (AP18.5, api.Workspace)
// can be wired in without touching the header.

// ActBranches opens the branch popup of the header.
const ActBranches keymap.Action = "git.branches"

// ProjectSessions performs the project switcher's life-cycle intents.
// IDs are the toolbar.Project IDs returned by toolbarProjects (today the
// project root).
type ProjectSessions interface {
	// Switch shows project id in this window.
	Switch(id string)
	// Hibernate releases the processes and memory of project id.
	Hibernate(id string)
	// Resume brings a hibernated project back.
	Resume(id string)
}

// singleProject is the ProjectSessions of a window with one project:
// switching opens the project (asking "this window or new window"),
// hibernation is not available yet.
type singleProject struct{ v *View }

func (s singleProject) Switch(id string) { s.v.requestProject(id) }

func (s singleProject) Hibernate(string) {
	s.v.ctl.Status.Notice = "Hibernate: needs the multi-project workspace (not wired in this window yet)"
}

func (s singleProject) Resume(id string) { s.v.requestProject(id) }

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

// toolbarProjects is the adapter between the project model and the
// project switcher: the open projects (today only the current one) and
// the recent projects. AP18.5 replaces it with the workspace's sessions
// (api.Workspace: background and hibernated projects).
func (h *header) toolbarProjects() (open []toolbar.Project, current string, recent []toolbar.RecentProject) {
	c := h.v.ctl
	if c.Project.Open {
		name := c.Project.Name
		if name == "" {
			name = filepath.Base(c.Project.Root)
		}
		open = append(open, toolbar.Project{ID: c.Project.Root, Name: name, Path: homeShort(c.Project.Root), State: toolbar.ProjectActive})
		current = c.Project.Root
	}
	for _, p := range c.RecentProjects {
		if p == current {
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
	b.Branch = ""
	git := c.Vcs != nil && c.Vcs.Available && c.Project.Git
	if git != h.git {
		h.git = git
		if git {
			h.v.Registry.AddAction(h.branchesAction())
		} else {
			h.v.Registry.RemoveAction(ActBranches)
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
		if e.ID != c.Project.Root {
			h.Sessions.Switch(e.ID)
		}
	case toolbar.HibernateProject:
		h.Sessions.Hibernate(e.ID)
	case toolbar.ResumeProject:
		h.Sessions.Resume(e.ID)
	case toolbar.OpenProject:
		v.openProjectDialog()
	case toolbar.OpenRecentProject:
		v.requestProject(expandHome(e.Path))
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
		c.Status.Notice = "Merge “" + e.Name + "”: not available yet (no merge command in the API)"
	case toolbar.RebaseOnto:
		c.Status.Notice = "Rebase onto “" + e.Name + "”: not available yet (no rebase command in the API)"
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
