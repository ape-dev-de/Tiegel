package gui

import (
	"path/filepath"
	"strings"

	"gioui.org/layout"

	"github.com/ape-dev-de/tiegel/internal/app/gui/controller"
	"github.com/ape-dev-de/tiegel/internal/ui/organisms/projectsetup"
	rtui "github.com/ape-dev-de/tiegel/internal/ui/organisms/runtime"
	"github.com/ape-dev-de/tiegel/internal/ui/tokens"
)

// ActProjectSetup opens the project setup sheet (File menu, Project
// settings page; offered once when a project opens, D526).
const ActProjectSetup = "app.projectSetup"

// openProjectSetup shows the sheet with a fresh proposal.
func (v *View) openProjectSetup() {
	if !v.ctl.Project.Open {
		v.ctl.Status.Notice = "Open a project to set it up"
		return
	}
	v.showSetupPopup()
	v.ctl.OpenProjectSetup()
}

func (v *View) showSetupPopup() {
	v.setupView = projectsetup.View{}
	v.setupVersion = -1
	v.OpenPopup(&Popup{
		Title:    "Project Setup",
		Detail:   "Found in this project (local files only). Check what to use; nothing changes until you click Apply.",
		Body:     v.layoutProjectSetup,
		Width:    640,
		Centered: true, BodyHeight: 420,
		OnClose: v.ctl.CloseProjectSetup,
	})
	v.ctl.Setup.Open = true
}

// offerProjectSetup shows the sheet once when a newly opened project has
// proposals and the user did not dismiss it for the project.
func (v *View) offerProjectSetup() {
	s := &v.ctl.Setup
	if !s.Offer || v.noSetupOffer || v.popup != nil || v.settingsOpen || v.ctl.IDEImport.Open || v.ctl.IDEImport.Offer {
		return
	}
	s.Offer = false
	v.showSetupPopup()
}

// syncSetupView copies the controller state into the sheet.
func (v *View) syncSetupView() {
	s := v.ctl.Setup
	w := &v.setupView
	w.Busy, w.Message, w.Error = s.Busy, s.Message, s.Error
	if v.setupVersion == s.Version {
		return
	}
	v.setupVersion = s.Version
	w.Items = w.Items[:0]
	for _, it := range s.Items {
		item := projectsetup.Item{Key: it.Key, Label: it.Label}
		for _, c := range it.Choices {
			item.Choices = append(item.Choices, projectsetup.Choice{Value: c.Value, Label: c.Label, Detail: c.Detail, Recommended: c.Recommended})
		}
		w.Items = append(w.Items, item)
	}
}

// layoutProjectSetup is the popup body.
func (v *View) layoutProjectSetup(gtx layout.Context, th *tokens.Theme) layout.Dimensions {
	v.syncSetupView()
	for _, e := range v.setupView.Update(gtx) {
		switch e := e.(type) {
		case projectsetup.ApplyEvent:
			vals := make([]controller.SetupValue, len(e.Values))
			for i, x := range e.Values {
				vals[i] = controller.SetupValue{Key: x.Key, Value: x.Value}
			}
			v.ctl.ApplySetup(vals)
		case projectsetup.LaterEvent:
			v.ClosePopup()
			return layout.Dimensions{Size: gtx.Constraints.Min}
		case projectsetup.DismissEvent:
			v.ctl.DismissSetup()
			v.ClosePopup()
			return layout.Dimensions{Size: gtx.Constraints.Min}
		}
	}
	if !v.ctl.Setup.Open && !v.ctl.Setup.Busy {
		// Applied successfully.
		v.ClosePopup()
		return layout.Dimensions{Size: gtx.Constraints.Min}
	}
	v.syncSetupView()
	return v.setupView.Layout(gtx, th)
}

// fillDiscovered converts the discovered interpreters for the
// Interpreter page.
func fillDiscovered(l *rtui.DiscoveredList, c *controller.Controller) {
	l.Loading = c.Discovered.Loading
	l.Items = l.Items[:0]
	root := c.Project.Root
	rel := func(p string) string {
		if r, err := filepath.Rel(root, p); err == nil && root != "" {
			return filepath.ToSlash(r)
		}
		return p
	}
	for _, it := range c.Discovered.Items {
		d := rtui.Discovered{ID: it.ID, Label: it.Label, Kind: rtui.Kind(it.Kind), Score: it.Score, Reasons: it.Reasons}
		if len(it.Files) > 0 {
			names := make([]string, len(it.Files))
			for i, f := range it.Files {
				names[i] = rel(f)
			}
			d.Where = strings.Join(names, " + ")
		}
		if len(it.Mappings) > 0 {
			d.Mapping = rel(it.Mappings[0].Host) + " → " + it.Mappings[0].Remote
		} else if it.Image != "" {
			d.Mapping = ". → " + it.Remote
		}
		l.Items = append(l.Items, d)
	}
}
