package gui

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/ape-dev-de/tiegel/internal/app/gui/native"
	"github.com/ape-dev-de/tiegel/internal/ui/atoms"
	"github.com/ape-dev-de/tiegel/internal/ui/organisms/palette"
)

// Projects: File → Open… (native folder dialog on macOS, D110, else a
// folder browser built from the chooser), Recent Projects, Close Project
// and the question "this window or a new window" when a project is
// already open. Each window shows one project; a project that is open in
// another window is brought to the front instead (WindowHost).

// WindowHost lets a View open projects in other windows. The window
// manager of Run implements it; tests and single-window hosts leave it
// nil (projects then always open in the current window).
type WindowHost interface {
	// OpenWindow opens a new window, with the project at path if not
	// empty.
	OpenWindow(path string)
	// FocusProject raises the window that has the project root open and
	// reports whether there is one (other than the asking window).
	FocusProject(v *View, root string) bool
}

// openWindow opens a new window (with project path, if not empty).
func (v *View) openWindow(path string) {
	if v.Host == nil {
		if path != "" {
			v.ctl.OpenProject(path)
		}
		return
	}
	v.Host.OpenWindow(path)
}

// requestProject opens the project dir: in this window when none is open,
// otherwise after asking "this window or a new window".
func (v *View) requestProject(dir string) {
	dir = expandHome(dir)
	if dir == "" {
		return
	}
	if abs, err := filepath.Abs(dir); err == nil {
		dir = abs
	}
	if r, err := filepath.EvalSymlinks(dir); err == nil {
		dir = r
	}
	if v.ctl.Project.Open && v.ctl.Project.Root == dir {
		return
	}
	if v.Host != nil && v.Host.FocusProject(v, dir) {
		return
	}
	if !v.ctl.Project.Open || v.Host == nil {
		v.ctl.Status.Notice = ""
		v.ctl.OpenProject(dir)
		return
	}
	v.OpenPopup(&Popup{
		Title:    "Open Project",
		Detail:   "Open " + filepath.Base(dir) + " in this window (closing " + v.ctl.Project.Name + ") or in a new window?",
		Centered: true,
		Buttons: []*PopupButton{
			{Text: "This Window", Kind: atoms.Secondary, OnClick: func() { v.ctl.OpenProject(dir) }},
			{Text: "New Window", Kind: atoms.Primary, OnClick: func() { v.openWindow(dir) }},
			{Text: "Cancel", Kind: atoms.Ghost},
		},
	})
}

// openProjectDialog shows the folder chooser (File → Open…).
func (v *View) openProjectDialog() {
	start := v.ctl.Project.Root
	if start != "" {
		start = filepath.Dir(start)
	} else {
		start = homeDir()
	}
	if native.FolderDialogSupported() && !v.noNativeDialog {
		post := v.Post
		go func() {
			dir, ok, err := native.ChooseFolder(context.Background(), "Open Project", start)
			post(func() {
				switch {
				case err != nil:
					v.ctl.Status.Notice = "Open: " + err.Error()
				case ok:
					v.requestProject(dir)
				}
			})
		}()
		return
	}
	v.browseFolder(start)
}

// browseFolder is the folder dialog built from the chooser: the query is
// a path, the list shows its subdirectories; choosing one descends into
// it, the first entry opens the folder.
func (v *View) browseFolder(dir string) {
	var cur string
	var names []string
	load := func(q string) []palette.Item {
		q = expandHome(q)
		base, prefix := q, ""
		if st, err := os.Stat(q); err != nil || !st.IsDir() || !strings.HasSuffix(q, string(filepath.Separator)) && q != dir {
			base, prefix = filepath.Dir(q), strings.ToLower(filepath.Base(q))
		}
		cur = base
		names = names[:0]
		entries, _ := os.ReadDir(base)
		for _, e := range entries {
			n := e.Name()
			if !e.IsDir() && e.Type()&os.ModeSymlink == 0 || strings.HasPrefix(n, ".") && !strings.HasPrefix(prefix, ".") {
				continue
			}
			if prefix != "" && !strings.HasPrefix(strings.ToLower(n), prefix) {
				continue
			}
			names = append(names, n)
		}
		sort.Strings(names)
		items := []palette.Item{{Label: "Open " + filepath.Base(base), Detail: base, Glyph: atoms.GlyphFolderOpen}}
		for _, n := range names {
			items = append(items, palette.Item{Label: n, Glyph: atoms.GlyphFolder})
		}
		return items
	}
	start := dir + string(filepath.Separator)
	v.OpenChooser(ChooserSpec{
		Title: "Open Project", Hint: "Folder path (Tab-free: type to filter, choose to descend)", Query: start,
		Items:   load(start),
		OnQuery: load,
		OnChoose: func(i int, q string) {
			switch {
			case i == 0:
				v.requestProject(cur)
			case i > 0 && i-1 < len(names):
				v.browseFolder(filepath.Join(cur, names[i-1]))
			case i < 0 && q != "":
				v.requestProject(q)
			}
		},
	})
}

// openRecentProjects lists the recent projects (File → Recent
// Projects…).
func (v *View) openRecentProjects() {
	v.ctl.ReloadRecentProjects()
	list := append([]string(nil), v.ctl.RecentProjects...)
	items := make([]MenuItem, 0, len(list))
	for _, p := range list {
		items = append(items, MenuItem{Title: filepath.Base(p) + "  —  " + p, Run: func() { v.requestProject(p) }})
	}
	if len(items) == 0 {
		items = append(items, MenuItem{Title: "No recent projects", Disabled: true})
	}
	v.ShowMenu("Recent Projects", items)
}

// closeProject closes the project and shows the welcome screen.
func (v *View) closeProject() {
	if !v.ctl.Project.Open {
		return
	}
	v.closeOverlay()
	v.ctl.CloseProject()
}
