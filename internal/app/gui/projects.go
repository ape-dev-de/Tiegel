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
// folder browser built from the chooser), Recent Projects, Close Project.
// A window holds several projects (Sessions, D477): by default
// (projects.openIn = this, D547) another project opens in this window and
// the one shown before is hibernated (projects.onSwitch; terminals keep
// running) or keeps running in the background. "Open in New Window" is in
// the context menus of the project switcher and the recent projects and
// on Shift+click; projects.openIn = ask asks every time. A project that
// is open in another window is brought to the front instead
// (WindowHost).

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
			v.openHere(path)
		}
		return
	}
	v.Host.OpenWindow(path)
}

// openHere opens dir in this window: next to the other projects with a
// workspace (projects.open), else in place of the current one.
func (v *View) openHere(dir string) {
	if v.sessions != nil {
		v.sessions.Open(dir)
		return
	}
	v.ctl.OpenProject(dir)
}

// projectDir normalises a project path (home, absolute, symlinks).
func projectDir(dir string) string {
	dir = expandHome(dir)
	if dir == "" {
		return ""
	}
	if abs, err := filepath.Abs(dir); err == nil {
		dir = abs
	}
	if r, err := filepath.EvalSymlinks(dir); err == nil {
		dir = r
	}
	return dir
}

// requestProjectNewWindow opens the project dir in a new window ("Open in
// New Window"): one open in another window is brought to the front, one
// open in this window moves (closed here, after asking when running work
// blocks it).
func (v *View) requestProjectNewWindow(dir string) {
	dir = projectDir(dir)
	if dir == "" {
		return
	}
	if v.Host == nil {
		v.requestProject(dir)
		return
	}
	if v.Host.FocusProject(v, dir) {
		return
	}
	if v.sessions != nil && v.sessions.Has(dir) {
		v.sessions.MoveToNewWindow(dir, v.openWindow)
		return
	}
	if v.sessions == nil && v.ctl.Project.Open && v.ctl.Project.Root == dir {
		return
	}
	v.openWindow(dir)
}

// requestProject opens the project dir: in this window (projects.openIn
// = this, the default), in a new window (new) or after asking (ask).
func (v *View) requestProject(dir string) {
	dir = projectDir(dir)
	if dir == "" {
		return
	}
	if v.ctl.Project.Open && v.ctl.Project.Root == dir {
		return
	}
	if v.sessions != nil && v.sessions.Has(dir) {
		v.sessions.Switch(dir) // resumes a hibernated one
		return
	}
	if v.Host != nil && v.Host.FocusProject(v, dir) {
		return
	}
	if !v.ctl.Project.Open || v.Host == nil {
		v.ctl.Status.Notice = ""
		v.openHere(dir)
		return
	}
	switch v.ctl.Prefs.OpenProjectIn { // setting projects.openIn
	case "new":
		v.openWindow(dir)
		return
	case "ask":
	default: // "this" and before the preferences are loaded
		v.openHere(dir)
		return
	}
	detail := "Open " + filepath.Base(dir) + " in this window (closing " + v.ctl.Project.Name + ") or in a new window?"
	if v.sessions != nil {
		detail = "Open " + filepath.Base(dir) + " in this window (" + v.ctl.Project.Name + " is hibernated or keeps running in the background, switch back with the project switcher) or in a new window?"
	}
	v.OpenPopup(&Popup{
		Title:    "Open Project",
		Detail:   detail,
		Centered: true,
		Buttons: []*PopupButton{
			{Text: "This Window", Kind: atoms.Primary, OnClick: func() { v.openHere(dir) }},
			{Text: "New Window", Kind: atoms.Secondary, OnClick: func() { v.openWindow(dir) }},
			{Text: "Cancel", Kind: atoms.Ghost},
		},
	})
}

// openProjectDialog shows the folder chooser (File → Open…).
func (v *View) openProjectDialog() { v.openProjectDialogIn(false) }

// openProjectDialogIn shows the folder chooser; newWindow opens the
// chosen folder in a new window (Shift+click on "Open…").
func (v *View) openProjectDialogIn(newWindow bool) {
	request := v.requestProject
	if newWindow {
		request = v.requestProjectNewWindow
	}
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
					request(dir)
				}
			})
		}()
		return
	}
	v.browseFolder(start, request)
}

// browseFolder is the folder dialog built from the chooser: the query is
// a path, the list shows its subdirectories; choosing one descends into
// it, the first entry opens the folder.
func (v *View) browseFolder(dir string, request func(string)) {
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
				request(cur)
			case i > 0 && i-1 < len(names):
				v.browseFolder(filepath.Join(cur, names[i-1]), request)
			case i < 0 && q != "":
				request(q)
			}
		},
	})
}

// openRecentProjects lists the recent projects (File → Recent
// Projects…).
func (v *View) openRecentProjects() {
	v.ctl.ReloadRecentProjects()
	list := append([]string(nil), v.ctl.RecentProjects...)
	items := make([]MenuItem, 0, len(list)+2)
	var newWin []MenuItem
	for _, p := range list {
		title := filepath.Base(p) + "  —  " + p
		items = append(items, MenuItem{Title: title, Run: func() { v.requestProject(p) }})
		newWin = append(newWin, MenuItem{Title: title, Run: func() { v.requestProjectNewWindow(p) }})
	}
	if len(items) == 0 {
		items = append(items, MenuItem{Title: "No recent projects", Disabled: true})
	} else if v.Host != nil {
		items = append(items, MenuItem{Separator: true}, MenuItem{Title: "Open in New Window", Children: newWin})
	}
	v.ShowMenu("Recent Projects", items)
}

// recentProjectMenu is the context menu of a recent project (welcome
// screen): open it here or in a new window.
func (v *View) recentProjectMenu(path string) {
	items := []MenuItem{{Title: "Open in This Window", Run: func() { v.requestProject(path) }}}
	if v.Host != nil {
		items = append(items, MenuItem{Title: "Open in New Window", Run: func() { v.requestProjectNewWindow(path) }})
	}
	items = append(items, MenuItem{Separator: true}, MenuItem{Title: "Remove from Recent Projects", Run: func() { v.ctl.RemoveRecentProject(path) }})
	v.ShowMenu(filepath.Base(path), items)
}

// closeProject closes the project and shows the next open project of
// the window or the welcome screen.
func (v *View) closeProject() {
	if !v.ctl.Project.Open {
		return
	}
	v.closeOverlay()
	if v.sessions != nil {
		// Running processes and debug sessions: listed, stopped on
		// confirmation (projects.busy); unsaved changes that cannot be
		// saved become recovery points as with project.close.
		v.sessions.Close(v.ctl.Project.Root)
		return
	}
	// Running processes: ask, terminate or keep (run.confirmClose).
	v.rund.confirmClose(v.ctl.CloseProject)
}

// hibernateProject hibernates the shown project (header, Find Action).
func (v *View) hibernateProject() {
	if v.ctl.Project.Open {
		v.hdr.Sessions.Hibernate(v.ctl.Project.Root)
	}
}

// openProjectSwitcher is "Switch Project…": the open projects with their
// state, then the recent ones; choosing one switches to it (resuming a
// hibernated one) or opens it.
func (v *View) openProjectSwitcher() {
	open, current := v.hdr.Sessions.Projects()
	isOpen := map[string]bool{}
	var ids []string
	var items []palette.Item
	for _, p := range open {
		isOpen[p.ID] = true
		if p.ID == current {
			continue
		}
		ids = append(ids, p.ID)
		items = append(items, palette.Item{Label: p.Name, Detail: p.State.String() + " · " + p.Path, Glyph: atoms.GlyphFolderOpen})
	}
	for _, r := range v.ctl.RecentProjects {
		if isOpen[r] {
			continue
		}
		ids = append(ids, "recent:"+r)
		items = append(items, palette.Item{Label: filepath.Base(r), Detail: "recent · " + homeShort(r), Glyph: atoms.GlyphFolder})
	}
	allIDs, shown := ids, ids
	v.OpenChooser(ChooserSpec{
		Title: "Switch Project", Hint: "Project name", Items: items,
		OnQuery: func(q string) []palette.Item {
			shown = nil
			var out []palette.Item
			q = strings.ToLower(q)
			for i, it := range items {
				if q == "" || strings.Contains(strings.ToLower(it.Label+" "+it.Detail), q) {
					shown = append(shown, allIDs[i])
					out = append(out, it)
				}
			}
			return out
		},
		OnChoose: func(i int, _ string) {
			if i < 0 || i >= len(shown) {
				return
			}
			if r, ok := strings.CutPrefix(shown[i], "recent:"); ok {
				v.requestProject(r)
				return
			}
			v.hdr.Sessions.Switch(shown[i])
		},
	})
}
