package git

import (
	"path"
	"strings"

	"golang.org/x/exp/shiny/materialdesign/icons"

	"github.com/ape-dev-de/tiegel/internal/ui/atoms"
)

// Glyphs of the commit window toolbar that the shared atoms set does not
// have (Material Design icons, Apache 2.0, like the atoms glyphs).
var (
	glyphRollback    = atoms.NewGlyph("rollback", icons.ContentUndo)
	glyphUpdate      = atoms.NewGlyph("update", icons.FileFileDownload)
	glyphStash       = atoms.NewGlyph("stash", icons.ContentArchive)
	glyphShowDiff    = atoms.NewGlyph("show-diff", icons.ActionCompareArrows)
	glyphViewOptions = atoms.NewGlyph("view-options", icons.ActionVisibility)
	glyphExpandAll   = atoms.NewGlyph("expand-all", icons.NavigationUnfoldMore)
	glyphCollapseAll = atoms.NewGlyph("collapse-all", icons.NavigationUnfoldLess)
)

// FileGlyphFunc returns the icon of a file in the change tree (by its
// repository path). Hosts plug in the icon set of the project tree; nil
// glyphs fall back to DefaultFileGlyph.
type FileGlyphFunc func(path string) *atoms.Glyph

// codeExt are extensions shown with the code icon by DefaultFileGlyph.
var codeExt = map[string]bool{
	".php": true, ".phtml": true, ".inc": true, ".js": true, ".ts": true, ".css": true, ".scss": true,
	".html": true, ".twig": true, ".blade": true, ".go": true, ".py": true, ".sh": true, ".sql": true,
	".json": true, ".yaml": true, ".yml": true, ".xml": true, ".toml": true, ".neon": true,
}

// textExt are extensions shown with the text icon by DefaultFileGlyph.
var textExt = map[string]bool{".md": true, ".txt": true, ".rst": true, ".csv": true, ".log": true, ".dist": true}

// DefaultFileGlyph is the built-in file icon choice: code, text or a
// plain file.
func DefaultFileGlyph(p string) *atoms.Glyph {
	ext := strings.ToLower(path.Ext(p))
	switch {
	case codeExt[ext]:
		return atoms.GlyphFileCode
	case textExt[ext]:
		return atoms.GlyphFileText
	}
	return atoms.GlyphFile
}
