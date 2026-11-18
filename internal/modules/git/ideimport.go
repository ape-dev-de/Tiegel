package git

import (
	"strings"

	"github.com/ape-dev-de/tiegel/internal/ideimport"
)

// Components of the reference IDE's configuration this plugin imports.
const (
	refVcsMappings = "VcsDirectoryMappings"
	refGitSettings = "Git.Settings"
)

// importComponent maps the update method and reports the version control
// roots (hook import.ide.component, M16).
func importComponent(c ideimport.Component) ([]ideimport.Item, bool) {
	if c.Kind != ideimport.KindComponent {
		return nil, false
	}
	switch c.Name {
	case refVcsMappings:
		var roots, other []string
		for _, m := range c.Element.All("mapping") {
			dir := c.Path(m.Attr("directory"))
			if dir == "" {
				dir = "."
			}
			if strings.EqualFold(m.Attr("vcs"), "Git") {
				roots = append(roots, dir)
			} else if m.Attr("vcs") != "" {
				other = append(other, dir+" ("+m.Attr("vcs")+")")
			}
		}
		it := c.Item("Version control roots", strings.Join(roots, ", "))
		it.Status = ideimport.StatusSkipped
		it.Detail = ideimport.JoinDetail(it.Detail, "Tiegel uses the Git repository of the project root")
		if len(other) > 0 {
			it.Status, it.Detail = ideimport.StatusUnsupported, "only Git is supported: "+strings.Join(other, ", ")
		}
		return []ideimport.Item{it}, true
	case refGitSettings:
		v, ok := c.Element.Option("UPDATE_TYPE")
		if !ok {
			return nil, true
		}
		method := map[string]string{"MERGE": "merge", "REBASE": "rebase"}[strings.ToUpper(v)]
		it := c.Item("Git update method", method)
		if method == "" {
			it.Status, it.Detail = ideimport.StatusUnsupported, "update type "+v+" (merge and rebase are supported)"
		} else {
			it.Changes = []ideimport.Change{c.Setting(SettingUpdateMethod, method)}
		}
		return []ideimport.Item{it}, true
	}
	return nil, false
}
