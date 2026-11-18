package ideimport

import (
	"path/filepath"
	"regexp"
	"strings"
)

// Macros resolves the path macros of the reference IDE's files.
type Macros struct {
	// ProjectDir replaces $PROJECT_DIR$ and $MODULE_DIR$ ("" leaves them).
	ProjectDir string
	// UserHome replaces $USER_HOME$.
	UserHome string
	// ConfigDir replaces $APPLICATION_CONFIG_DIR$ (the application
	// configuration directory being imported).
	ConfigDir string
}

var macroRe = regexp.MustCompile(`\$[A-Z][A-Z0-9_]*\$`)

// Expand replaces the known macros in s. Paths keep the slashes of the
// file; use Path for a host path.
func (m Macros) Expand(s string) string {
	if !strings.Contains(s, "$") {
		return s
	}
	return macroRe.ReplaceAllStringFunc(s, func(mac string) string {
		var v string
		switch mac {
		case "$PROJECT_DIR$", "$MODULE_DIR$":
			v = m.ProjectDir
		case "$USER_HOME$":
			v = m.UserHome
		case "$APPLICATION_CONFIG_DIR$":
			v = m.ConfigDir
		}
		if v == "" {
			return mac
		}
		return filepath.ToSlash(v)
	})
}

// Unresolved returns the macros left in s after Expand.
func (m Macros) Unresolved(s string) []string {
	return macroRe.FindAllString(m.Expand(s), -1)
}

// Path expands the macros of a path value and returns it as a cleaned
// host path; a "file://" prefix is removed. It returns "" when s is empty
// or still contains a macro.
func (m Macros) Path(s string) string {
	s = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(s), "file://"))
	if s == "" {
		return ""
	}
	s = m.Expand(s)
	if macroRe.MatchString(s) {
		return ""
	}
	return filepath.Clean(filepath.FromSlash(s))
}

// ProjectRel returns path relative to the project directory (slash
// separated) when it lies inside it, else path unchanged. It is the form
// Tiegel stores tool and file settings in, so they also work inside a
// container.
func (m Macros) ProjectRel(path string) string {
	if path == "" || m.ProjectDir == "" {
		return path
	}
	rel, err := filepath.Rel(filepath.Clean(m.ProjectDir), path)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		return path
	}
	return filepath.ToSlash(rel)
}
