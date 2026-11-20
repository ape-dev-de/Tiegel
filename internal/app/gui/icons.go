package gui

import (
	"github.com/ape-dev-de/tiegel/internal/app/gui/controller"
	"github.com/ape-dev-de/tiegel/internal/ui/atoms"
	"github.com/ape-dev-de/tiegel/internal/ui/fileicons"
	"github.com/ape-dev-de/tiegel/internal/ui/organisms/filetree"
)

// Icons of the window (D466, D472): files, folders and tool windows use
// the own icon set internal/ui/fileicons through atoms glyph adapters, so
// every organism that takes an *atoms.Glyph draws them.

// fileGlyph returns the icon of a file; p is a project-relative path or a
// base name (directory parts only matter for the test variant).
func fileGlyph(p string) *atoms.Glyph {
	return fileicons.GlyphFor(p, false, fileicons.Hints{})
}

// tabGlyph returns the icon of an editor tab (by its project path).
func tabGlyph(t *controller.Tab) *atoms.Glyph {
	if t.Path != "" {
		return fileGlyph(t.Path)
	}
	return fileGlyph(t.Title())
}

// treeIcon is the icon provider of the project tree. Folder roles come
// from the backend (project.roles: composer.json autoload/autoload-dev
// and vendor-dir, else conventional names; D499).
func (v *View) treeIcon(n *filetree.Node, open bool) *atoms.Glyph {
	h := fileicons.Hints{Open: open, LibraryRoot: n.Library && n.Dir}
	if n.Dir {
		switch {
		case n.Path == "" || n.Path == ".":
			// The project root is a plain folder.
		case n.Ignored:
			h.Excluded = true
		default:
			switch v.ctl.RoleOf(n.Path) {
			case "source":
				h.SourceRoot = true
			case "test":
				h.TestRoot = true
			case "resource":
				h.ResourceRoot = true
			case "library":
				h.LibraryRoot = true
			}
		}
		return fileicons.GlyphFor(n.Name, true, h)
	}
	return fileicons.GlyphFor(n.Path, false, h)
}

// toolIcons maps tool window IDs to tool glyphs of the icon set; windows
// without an entry keep their registered glyph.
var toolIcons = map[string]fileicons.ToolName{
	ToolProject:   fileicons.ToolProject,
	ToolCommit:    fileicons.ToolCommit,
	ToolGit:       fileicons.ToolGit,
	ToolStructure: fileicons.ToolStructure,
	ToolTerminal:  fileicons.ToolTerminal,
	ToolRun:       fileicons.ToolRun,
	ToolDebug:     fileicons.ToolDebug,
	toolProblems:  fileicons.ToolProblems,
	ToolServices:  fileicons.ToolServices,
	ToolTests:     fileicons.ToolTests,
	ToolFind:      fileicons.ToolFind,
	ToolUsages:    fileicons.ToolFind,
	ToolHierarchy: fileicons.ToolStructure,
}

// toolGlyph returns the stripe icon of tool window t.
func toolGlyph(t ToolWindow) *atoms.Glyph {
	if n, ok := toolIcons[t.ID]; ok {
		if g := fileicons.ToolGlyph(n); g != nil {
			return g
		}
	}
	return t.Glyph
}
