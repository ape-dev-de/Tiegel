package gui

import (
	"path"
	"path/filepath"
	"strings"

	"github.com/ape-dev-de/tiegel/internal/ui/organisms/filetree"
	"github.com/ape-dev-de/tiegel/internal/ui/organisms/statusbar"
)

// The footer is the one status bar of the window (D464): the breadcrumb
// project › directories › file of the active editor on the left, the
// status items on the right. Clicking a segment shows the project tree
// and selects the directory or file there.

// breadcrumb returns the crumbs of the active editor and the
// project-relative path of every crumb ("" = project root).
func (v *View) breadcrumb() ([]statusbar.Crumb, []string) {
	c := v.ctl
	if !c.Project.Open {
		return nil, nil
	}
	name := c.Project.Name
	if name == "" {
		name = filepath.Base(c.Project.Root)
	}
	crumbs := []statusbar.Crumb{{Text: name, Tooltip: homeShort(c.Project.Root)}}
	paths := []string{""}
	t := c.ActiveTab()
	if t == nil || t.Virtual != "" || t.Path == "" || strings.HasPrefix(t.Path, "..") || filepath.IsAbs(t.Path) {
		return crumbs, paths
	}
	rel := filepath.ToSlash(t.Path)
	parts := strings.Split(rel, "/")
	for i, p := range parts {
		cur := path.Join(parts[:i+1]...)
		last := i == len(parts)-1
		cr := statusbar.Crumb{Text: p, Glyph: v.treeIcon(&filetree.Node{Name: p, Path: cur, Dir: !last}, false)}
		if last {
			cr.Tooltip = "Select in Project View"
		}
		crumbs = append(crumbs, cr)
		paths = append(paths, cur)
	}
	return crumbs, paths
}

// crumbClicked reveals breadcrumb segment i in the project tree.
func (v *View) crumbClicked(i int) {
	if i < 0 || i >= len(v.crumbPaths) {
		return
	}
	p := v.crumbPaths[i]
	v.ShowTool(ToolProject)
	if p == "" {
		if v.tree.Root != nil {
			v.tree.Select(v.tree.Root.Path)
		}
		return
	}
	v.tree.Select(p)
	v.ctl.LoadAncestors(p)
	if i < len(v.crumbPaths)-1 {
		// A directory: expand it as well.
		v.tree.SetExpanded(p, true)
		v.ctl.LoadDir(p)
	}
}
