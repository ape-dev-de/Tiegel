package gui

import (
	"io/fs"
	"os"
	"path/filepath"
	"testing"

	"github.com/ape-dev-de/tiegel/internal/api"
	"github.com/ape-dev-de/tiegel/internal/ideimport"
	"github.com/ape-dev-de/tiegel/internal/ui/organisms/importwizard"
)

// The settings import wizard: offered for a project with .idea, preview
// with areas, import, report. The application configuration comes from
// the synthetic fixture (roots point to testdata, never to the home).
func TestImportSettingsWizard(t *testing.T) {
	src := filepath.Join("..", "..", "ideimport", "testdata", "project")
	extra := map[string]string{}
	err := filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		data, err := os.ReadFile(p)
		rel, _ := filepath.Rel(src, p)
		extra[rel] = string(data)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	roots, _ := filepath.Abs(filepath.Join("..", "..", "ideimport", "testdata", "config"))
	ideImportRoots = []string{roots}
	t.Cleanup(func() { ideImportRoots = nil })
	e := newEnvWith(t, true, extra, nil)

	// The project has a .idea directory: the import is offered once.
	e.waitFor("offer", func() bool { return e.v.popup != nil })
	if e.ctl.IDEImport.Offer || e.v.popup == nil || e.v.popup.Title != "Import project settings?" {
		t.Fatalf("offer popup = %+v", e.v.popup)
	}
	e.v.popup.Buttons[0].OnClick()
	e.waitFor("preview", func() bool { return e.ctl.IDEImport.Report != nil && !e.ctl.IDEImport.Busy })
	e.frame()
	st := e.ctl.IDEImport
	if st.Done || st.Err != "" || st.Report.Source == nil || st.Report.Project == "" {
		t.Fatalf("preview = %+v", st)
	}
	if st.Selected[ideimport.AreaRecent] || !st.Selected[ideimport.AreaEditor] {
		t.Errorf("default selection = %v", st.Selected)
	}
	if len(e.v.importView.Areas) == 0 || len(e.v.importView.Items) == 0 {
		t.Fatalf("wizard = %+v", e.v.importView)
	}
	e.shot("import-preview")

	// Only the editor area.
	for id := range st.Selected {
		e.ctl.SetIDEImportArea(id, id == ideimport.AreaEditor)
	}
	e.ctl.ApplyIDEImport()
	e.waitFor("import", func() bool { return e.ctl.IDEImport.Done })
	e.frame()
	rep := e.ctl.IDEImport.Report
	imported := 0
	for _, it := range rep.Items {
		if it.Status == ideimport.StatusImported {
			imported++
			if it.Area != ideimport.AreaEditor {
				t.Errorf("imported outside the selection: %+v", it)
			}
		}
	}
	if imported == 0 || e.ctl.Prefs.EditorFont != "Fira Code" {
		t.Errorf("imported %d, editor font %q", imported, e.ctl.Prefs.EditorFont)
	}
	for _, it := range e.v.importView.Items {
		if it.Status == importwizard.StatusFailed {
			t.Errorf("failed: %+v", it)
		}
	}
	e.shot("import-report")
	e.v.ClosePopup()
	if e.ctl.IDEImport.Open {
		t.Error("wizard still open")
	}
	// The offer is remembered for the project.
	var v api.SettingValue
	if err := e.ctl.Backend().Call(t.Context(), "settings.get", api.SettingsParams{Key: "ui.ideImportOffered", Scope: "project"}, &v); err != nil || !v.Found {
		t.Errorf("offer not remembered: %+v %v", v, err)
	}
}
