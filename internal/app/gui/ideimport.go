package gui

import (
	"fmt"
	"sort"

	"gioui.org/layout"

	"github.com/ape-dev-de/tiegel/internal/app/gui/controller"
	"github.com/ape-dev-de/tiegel/internal/ui/atoms"
	"github.com/ape-dev-de/tiegel/internal/ui/organisms/importwizard"
	"github.com/ape-dev-de/tiegel/internal/ui/tokens"
)

// ActImportSettings opens the settings import wizard (File menu, welcome
// screen, offer for projects with a .idea directory).
const ActImportSettings = "app.importSettings"

// openImportSettings opens the wizard: a preview of what the reference
// IDE's configuration (and the project's .idea) would change. The
// previous global settings are saved before an import changes them.
func (v *View) openImportSettings() {
	v.OpenPopup(&Popup{
		Title:    "Import Settings",
		Detail:   "Settings of the reference IDE on this computer and of the project's .idea directory. Nothing changes until you click Import; your current global settings are backed up first.",
		Body:     v.layoutImportWizard,
		Width:    760,
		Centered: true, BodyHeight: 520,
		OnClose: v.ctl.CloseIDEImport,
	})
	v.importView = importwizard.View{}
	v.ctl.OpenIDEImport()
}

// openProjectImportSettings opens the wizard for the project's .idea
// only (the offer for a project): theme, fonts, keymap and the other
// global settings stay as they are (D441).
func (v *View) openProjectImportSettings() {
	v.OpenPopup(&Popup{
		Title:    "Import Project Settings",
		Detail:   "Settings of the project's .idea directory only; your global settings stay as they are (File › Import Settings… imports those). Nothing changes until you click Import.",
		Body:     v.layoutImportWizard,
		Width:    760,
		Centered: true, BodyHeight: 520,
		OnClose: v.ctl.CloseIDEImport,
	})
	v.importView = importwizard.View{}
	v.ctl.OpenProjectIDEImport()
}

// syncImportView copies the controller state into the wizard.
func syncImportView(w *importwizard.View, st controller.IDEImportState) {
	w.Busy, w.Done, w.Error = st.Busy, st.Done, st.Err
	w.Executables, w.AllowExecutables = st.Executables(), st.AllowExecutables
	w.Source, w.Project, w.Notes, w.Areas, w.Items = "", "", nil, w.Areas[:0], w.Items[:0]
	rep := st.Report
	if rep == nil {
		return
	}
	if rep.Source != nil {
		w.Source = rep.Source.Name + " (" + rep.Source.Dir + ")"
	}
	w.Project, w.Notes = rep.Project, rep.Notes
	for _, a := range rep.Areas {
		w.Areas = append(w.Areas, importwizard.Area{ID: a.ID, Label: a.Label, Count: a.Count, Total: a.Total, Selected: st.Selected[a.ID]})
	}
	for _, it := range rep.Items {
		detail := it.Detail
		if it.Plugin != "" {
			detail = fmt.Sprintf("%s (plugin %s)", detail, it.Plugin)
		}
		w.Items = append(w.Items, importwizard.Item{Area: it.Area, Name: it.Name, Status: string(it.Status), Detail: detail, Source: it.Source, Executable: it.Executable})
	}
	// Group the report by area (in the order of the checkboxes).
	order := map[string]int{}
	for i, a := range rep.Areas {
		order[a.ID] = i
	}
	sort.SliceStable(w.Items, func(i, j int) bool { return order[w.Items[i].Area] < order[w.Items[j].Area] })
	if st.Done && st.Err == "" {
		k := rep.Counts
		w.Error = ""
		if k.Failed > 0 {
			w.Error = fmt.Sprintf("%d imported, %d failed – see the report.", k.Imported, k.Failed)
		}
	}
}

// layoutImportWizard is the popup body of the wizard.
func (v *View) layoutImportWizard(gtx layout.Context, th *tokens.Theme) layout.Dimensions {
	w := &v.importView
	syncImportView(w, v.ctl.IDEImport)
	for _, e := range w.Update(gtx) {
		switch e := e.(type) {
		case importwizard.ToggleEvent:
			v.ctl.SetIDEImportArea(e.ID, e.Selected)
		case importwizard.AllowExecutablesEvent:
			v.ctl.SetIDEImportAllowExecutables(e.Allowed)
		case importwizard.ImportEvent:
			v.ctl.ApplyIDEImport()
		case importwizard.CloseEvent:
			v.ClosePopup()
			v.ctl.CloseIDEImport()
			return layout.Dimensions{Size: gtx.Constraints.Min}
		}
	}
	syncImportView(w, v.ctl.IDEImport)
	return w.Layout(gtx, th)
}

// offerImportSettings asks once per project with a .idea directory
// whether to import its settings.
func (v *View) offerImportSettings() {
	if !v.ctl.IDEImport.Offer || v.popup != nil || v.settingsOpen || v.ctl.IDEImport.Open {
		return
	}
	v.ctl.DismissIDEImportOffer()
	v.OpenPopup(&Popup{
		Title:    "Import project settings?",
		Detail:   "This project has a .idea directory of the reference IDE. Its interpreter, run configurations and tool settings can be imported; you see a preview first.",
		Centered: true,
		Buttons: []*PopupButton{
			{Text: "Import Settings…", Kind: atoms.Primary, OnClick: v.openProjectImportSettings},
			{Text: "Not Now", Kind: atoms.Secondary},
		},
	})
}
