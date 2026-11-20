package controller

import (
	"context"

	"github.com/ape-dev-de/tiegel/internal/api"
)

// NewLocalInProcess returns a Backend bound to exactly a, also when a
// hosts the window's workspace (api.App.EnableWorkspace): calls are not
// forwarded to the active project and only a's own events arrive. With
// several projects per window (AP18.5) the GUI keeps one controller per
// project App (api.Workspace.App) built on this backend.
func NewLocalInProcess(a *api.App) *InProcess {
	return &InProcess{InProcess: api.NewLocalInProcess(a)}
}

// AdoptProject shows the project the controller's App has open now: one
// the workspace opened or resumed in this App (projects.open,
// projects.resume) instead of OpenProject. Without a project in the App
// the controller returns to the welcome state; a project already shown
// stays as it is.
func (c *Controller) AdoptProject() {
	c.Go(func(ctx context.Context) func() {
		var info ProjectInfo
		err := c.call(ctx, "project.info", nil, &info)
		return func() {
			switch {
			case err != nil:
				c.notice(err)
			case !info.Open:
				c.ProjectReleased()
			case !c.Project.Open || c.Project.Root != info.Root:
				c.projectOpened(info)
			}
		}
	})
}

// ProjectReleased returns to the welcome state after the workspace
// closed or hibernated the App's project (the backend already saved and
// released it; nothing is called).
func (c *Controller) ProjectReleased() {
	if c.Project.Open {
		c.projectClosed()
	}
}

// Activated is called when the project becomes the window's active
// project: the language server is started unless it runs (lsp.start, as
// on opening).
func (c *Controller) Activated() {
	if c.Project.Open && !c.Status.LSPRunning && c.Status.LSP != lspStarting {
		c.startLSP()
	}
}

// RestoreTabs opens the absolute files as tabs of the active group in
// this order (after a resume: the tabs a hibernated project had) and
// activates active with its caret at offset caret. Files that cannot be
// opened any more are skipped silently; tabs already open stay.
func (c *Controller) RestoreTabs(files []string, active string, caret int) {
	if !c.Project.Open || len(files) == 0 {
		return
	}
	root := c.Project.Root
	c.Go(func(ctx context.Context) func() {
		var tabs []*Tab
		for _, abs := range files {
			c.waitPendingClose(ctx, abs)
			var st struct {
				DocState
				Generation   uint64  `json:"generation"`
				EditorConfig *Indent `json:"editorconfig"`
			}
			if err := c.call(ctx, "doc.open", map[string]any{"path": abs}, &st); err != nil {
				continue
			}
			doc, err := c.be.Document(abs)
			if err != nil {
				continue
			}
			tabs = append(tabs, &Tab{File: abs, Language: st.Language, EOL: st.EOL, Modified: st.Modified, Doc: doc, Generation: st.Generation, Indent: st.EditorConfig})
		}
		return func() {
			if c.Project.Root != root {
				return
			}
			for _, t := range tabs {
				if c.tabIndex(t.File) >= 0 {
					continue
				}
				t.Path = c.Rel(t.File)
				c.insertTab(t)
			}
			if i := c.tabIndex(active); i >= 0 {
				c.Activate(i)
				loc := At(active, caret)
				c.Reveal = &loc
			}
		}
	})
}

// lspStarting is the LSP status while lsp.start or lsp.restart runs.
const lspStarting = "LSP: starting…"
