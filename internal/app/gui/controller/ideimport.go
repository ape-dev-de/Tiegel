package controller

import (
	"context"

	"github.com/ape-dev-de/tiegel/internal/api"
)

// Settings import from the reference IDE (M16) as seen by the GUI: the
// wizard previews with import.ide.scan (all areas checked, so every item
// shows its outcome), the user picks areas and import.ide.apply writes
// them. A project with a .idea directory is offered the import once
// (project setting KeyIDEImportOffered).

// KeyIDEImportOffered marks a project whose import was offered (client
// state, project settings).
const KeyIDEImportOffered = "ui.ideImportOffered"

// IDEImportState is the state of the import wizard.
type IDEImportState struct {
	Open bool
	Busy bool
	// Done: Report is the result of an import (not a preview).
	Done   bool
	Err    string
	Report *api.IDEImportReport
	// Selected are the chosen areas (initialised from their defaults).
	Selected map[string]bool
	// Offer: the open project has a .idea directory and was not offered
	// the import yet.
	Offer bool
}

// OpenIDEImport opens the wizard and starts the preview.
func (c *Controller) OpenIDEImport() {
	c.IDEImport = IDEImportState{Open: true, Busy: true, Offer: c.IDEImport.Offer}
	c.Go(func(ctx context.Context) func() {
		var rep api.IDEImportReport
		err := c.call(ctx, "import.ide.scan", api.IDEImportParams{Areas: []string{"all"}}, &rep)
		return func() {
			if !c.IDEImport.Open {
				return
			}
			c.IDEImport.Busy = false
			if err != nil {
				c.IDEImport.Err = err.Error()
				return
			}
			c.IDEImport.Report = &rep
			c.IDEImport.Selected = map[string]bool{}
			for _, a := range rep.Areas {
				c.IDEImport.Selected[a.ID] = a.Default && a.Total > 0
			}
		}
	})
}

// SetIDEImportArea selects or deselects an area of the wizard.
func (c *Controller) SetIDEImportArea(id string, on bool) {
	if c.IDEImport.Selected == nil {
		c.IDEImport.Selected = map[string]bool{}
	}
	c.IDEImport.Selected[id] = on
}

// ApplyIDEImport imports the selected areas and reloads what the import
// may have changed (preferences, keymap, recent projects, runtime).
func (c *Controller) ApplyIDEImport() {
	st := &c.IDEImport
	if st.Busy || st.Report == nil {
		return
	}
	var areas []string
	for _, a := range st.Report.Areas {
		if st.Selected[a.ID] {
			areas = append(areas, a.ID)
		}
	}
	if len(areas) == 0 {
		st.Err = "Select at least one area."
		return
	}
	st.Busy, st.Err = true, ""
	c.Go(func(ctx context.Context) func() {
		var rep api.IDEImportReport
		err := c.call(ctx, "import.ide.apply", api.IDEImportParams{Areas: areas}, &rep)
		p := c.loadPrefs(ctx)
		return func() {
			c.IDEImport.Busy = false
			if err != nil {
				c.IDEImport.Err = err.Error()
				return
			}
			c.IDEImport.Report, c.IDEImport.Done = &rep, true
			c.Prefs = p
			c.ReloadRecentProjects()
			c.LoadKeymap()
			c.RefreshQA()
			if c.Project.Open {
				c.refreshRuntime()
			}
		}
	})
}

// CloseIDEImport closes the wizard.
func (c *Controller) CloseIDEImport() {
	offer := c.IDEImport.Offer
	c.IDEImport = IDEImportState{Offer: offer}
}

// checkIDEImportOffer asks whether the opened project has a .idea
// directory that was not offered for import yet.
func (c *Controller) checkIDEImportOffer() {
	root := c.Project.Root
	c.Go(func(ctx context.Context) func() {
		var st api.IDEImportStatus
		if err := c.call(ctx, "import.ide.status", nil, &st); err != nil || !st.ProjectConfig {
			return nil
		}
		offered := getSetting[bool](ctx, c.be, KeyIDEImportOffered, "project", false)
		return func() {
			if c.Project.Root == root && !offered {
				c.IDEImport.Offer = true
			}
		}
	})
}

// DismissIDEImportOffer remembers that the project was offered the import.
func (c *Controller) DismissIDEImportOffer() {
	c.IDEImport.Offer = false
	c.Go(func(ctx context.Context) func() {
		err := c.call(ctx, "settings.set", map[string]any{"key": KeyIDEImportOffered, "scope": "project", "value": true}, nil)
		return func() { c.notice(err) }
	})
}
