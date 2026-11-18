package controller

import (
	"context"
	"encoding/json"
)

// Project lifecycle beyond OpenProject: closing (back to the welcome
// screen), the recent project list (shared by all windows through the
// global settings) and the per-project tool window layout.

// CloseProject saves all documents, stops the language server, closes
// the project in the backend (documents that cannot be saved are kept as
// recovery points, see api project.close) and returns to the welcome
// screen.
func (c *Controller) CloseProject() {
	if !c.Project.Open {
		return
	}
	c.Go(func(ctx context.Context) func() {
		err := c.closeBackendProject(ctx)
		return func() {
			c.notice(err)
			c.projectClosed()
		}
	})
}

// closeBackendProject saves, stops the LSP and closes the project
// (background goroutine).
func (c *Controller) closeBackendProject(ctx context.Context) error {
	// Save first; a failing save is preserved as recovery point by
	// project.close, so the close continues.
	_ = c.AutosaveSync(ctx)
	_ = c.call(ctx, "lsp.stop", nil, nil)
	return c.call(ctx, "project.close", nil, nil)
}

// projectClosed resets the window to the welcome state (UI goroutine).
func (c *Controller) projectClosed() {
	c.Project = ProjectInfo{}
	c.IDEImport.Offer = false
	c.resetGroups()
	c.diags = map[string]*fileDiags{}
	c.DiagVersion++
	c.Git = map[string]GitFile{}
	c.GitVersion++
	c.Nav = Navigation{}
	c.Files = nil
	c.Tree = nil
	c.RecentFiles = nil
	c.Completion, c.Signature, c.Hover = Completion{}, SignatureHelp{}, HoverInfo{}
	c.Locations = nil
	c.Reveal = nil
	c.Status = Status{}
	c.RecoveryShown = nil
	c.setRecovery(nil)
	c.Vcs.reset()
	c.Debug.reset()
	c.Tools = NewToolLayout()
	c.toolsReady = false
}

// addRecentProject moves root to the front of the recent projects. The
// list is re-read before writing: other windows share it.
func (c *Controller) addRecentProject(root string) {
	c.RecentProjects = addRecent(c.RecentProjects, root)
	c.updateRecentProjects(func(list []string) []string { return addRecent(list, root) })
}

// RemoveRecentProject removes root from the recent projects.
func (c *Controller) RemoveRecentProject(root string) {
	c.RecentProjects = removeItem(c.RecentProjects, root)
	c.updateRecentProjects(func(list []string) []string { return removeItem(list, root) })
}

// ReloadRecentProjects re-reads the shared list (another window may have
// changed it).
func (c *Controller) ReloadRecentProjects() {
	c.Go(func(ctx context.Context) func() {
		list := getSetting[[]string](ctx, c.be, KeyRecentProjects, "global", nil)
		return func() { c.RecentProjects = list }
	})
}

func (c *Controller) updateRecentProjects(edit func([]string) []string) {
	c.Go(func(ctx context.Context) func() {
		list := edit(getSetting[[]string](ctx, c.be, KeyRecentProjects, "global", nil))
		raw, _ := json.Marshal(list)
		err := c.call(ctx, "settings.set", map[string]any{"key": KeyRecentProjects, "scope": "global", "value": json.RawMessage(raw)}, nil)
		return func() {
			c.notice(err)
			c.RecentProjects = list
		}
	})
}

func removeItem(list []string, item string) []string {
	out := make([]string, 0, len(list))
	for _, x := range list {
		if x != item {
			out = append(out, x)
		}
	}
	return out
}

// loadToolLayout loads the project's tool window layout; without a
// stored one the project tree is shown.
func (c *Controller) loadToolLayout() {
	c.Tools = NewToolLayout()
	c.toolsReady = false
	c.Go(func(ctx context.Context) func() {
		var v settingValue
		err := c.call(ctx, "settings.get", map[string]string{"key": KeyToolLayout, "scope": "project"}, &v)
		return func() {
			loaded := err == nil && v.Found && json.Unmarshal(v.Value, c.Tools) == nil
			if !loaded {
				c.Tools.Show(DefaultOpenTool)
			}
			c.toolsSaved = c.Tools.Version
			c.toolsReady = true
		}
	})
}

// ToolsReady reports whether the stored tool layout was loaded (the view
// waits with default placements until then).
func (c *Controller) ToolsReady() bool { return c.toolsReady }

// PersistToolLayout stores the tool layout if it changed since the last
// call (the view calls it once per frame; dragging a divider is reported
// when it ends).
func (c *Controller) PersistToolLayout() {
	// One write at a time: concurrent writes could land out of order.
	if !c.toolsReady || !c.Project.Open || c.Tools.Version == c.toolsSaved || c.toolsSaving {
		return
	}
	c.toolsSaved = c.Tools.Version
	raw, err := json.Marshal(c.Tools)
	if err != nil {
		return
	}
	c.toolsSaving = true
	c.Go(func(ctx context.Context) func() {
		err := c.call(ctx, "settings.set", map[string]any{"key": KeyToolLayout, "scope": "project", "value": json.RawMessage(raw)}, nil)
		return func() {
			c.toolsSaving = false
			c.notice(err)
			c.PersistToolLayout() // changed meanwhile
		}
	})
}

// LoadMonoFonts fills MonoFonts with list() in the background (the font
// scan may take a moment on first use).
func (c *Controller) LoadMonoFonts(list func() []string) {
	if c.MonoFonts != nil || list == nil {
		return
	}
	c.MonoFonts = []string{}
	c.Go(func(context.Context) func() {
		fs := list()
		return func() { c.MonoFonts = fs }
	})
}
