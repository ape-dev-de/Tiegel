package api

import (
	"context"
	"path/filepath"

	"github.com/ape-dev-de/tiegel/internal/core/autosave"
	"github.com/ape-dev-de/tiegel/internal/core/editorconfig"
	"github.com/ape-dev-de/tiegel/internal/core/hooks"
	"github.com/ape-dev-de/tiegel/internal/core/langdetect"
	"github.com/ape-dev-de/tiegel/internal/core/settings"
)

// File-type associations (files.associations) and .editorconfig support
// (files.editorconfig), decision D213/D214.

// editorConfigPriority runs the .editorconfig save filter after other
// file.beforeSave filters such as formatters.
const editorConfigPriority = 1000

// associations returns the parsed files.associations of the effective
// settings. It must not be called with a.mu held.
func (a *App) associations() []langdetect.Association {
	as, _ := langdetect.ParseAssociations(Setting[[]string](a, settings.KeyFileAssociations, nil))
	return as
}

// detectLang detects the language of abs honouring files.associations. It
// must not be called with a.mu held.
func (a *App) detectLang(abs string, head []byte) langdetect.Language {
	return langdetect.DetectWith(a.associations(), abs, head)
}

// DetectLanguage detects the language of the file abs from its name and
// first bytes (langdetect.HeadSize), honouring the user's file type
// associations (setting files.associations).
func (a *App) DetectLanguage(abs string, head []byte) langdetect.Language {
	return a.detectLang(abs, head)
}

// editorConfig resolves .editorconfig for abs (stopping at the project
// root). ok is false when the setting files.editorconfig is off, nothing
// applies or the files cannot be read.
func (a *App) editorConfig(abs string) (editorconfig.Props, bool) {
	if !Setting(a, settings.KeyEditorConfig, true) {
		return editorconfig.Props{}, false
	}
	stop := a.ProjectRoot()
	if stop == "" || !within(stop, abs) {
		stop = filepath.Dir(abs)
	}
	p, err := editorconfig.Resolve(abs, stop)
	if err != nil {
		a.log.Debug("editorconfig", "file", abs, "err", err)
		return editorconfig.Props{}, false
	}
	return p, !p.Empty()
}

// within reports whether path is root or below it.
func within(root, path string) bool {
	rel, err := filepath.Rel(root, path)
	return err == nil && rel != ".." && !filepath.IsAbs(rel) && (len(rel) < 3 || rel[:3] != ".."+string(filepath.Separator))
}

// registerFileConfig installs the .editorconfig save filter and the
// files.editorconfig method.
func (a *App) registerFileConfig() {
	autosave.BeforeSave.On(a.hooks, editorConfigPriority, func(_ context.Context, ev autosave.SaveEvent) (autosave.SaveEvent, error) {
		if p, ok := a.editorConfig(ev.Path); ok {
			ev.Content = p.Apply(ev.Content)
		}
		return ev, nil
	}, hooks.WithOwner("kernel"))
	mustHandle(a, "files.editorconfig", "Effective .editorconfig properties of a file (indent, tab width, final newline, trailing whitespace, charset check)", a.filesEditorConfig)
}

// EditorConfigResult is the result of "files.editorconfig".
type EditorConfigResult struct {
	Path string `json:"path"`
	// Enabled is the setting files.editorconfig.
	Enabled bool `json:"enabled"`
	editorconfig.Props
	// CharsetWarning reports a mismatch between the file content and the
	// charset property (Tiegel edits UTF-8 only).
	CharsetWarning string `json:"charsetWarning,omitempty"`
}

func (a *App) filesEditorConfig(_ context.Context, p PathParams) (EditorConfigResult, error) {
	abs, err := a.resolvePath(p.Path)
	if err != nil {
		return EditorConfigResult{}, err
	}
	res := EditorConfigResult{Path: a.displayPath(abs), Enabled: Setting(a, settings.KeyEditorConfig, true)}
	if props, ok := a.editorConfig(abs); ok {
		res.Props = props
		if props.Charset != "" {
			if _, data, err := readDisk(abs); err == nil {
				res.CharsetWarning = editorconfig.CheckCharset(props.Charset, data)
			}
		}
	}
	return res, nil
}
