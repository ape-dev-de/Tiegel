package docker

import (
	"os"
	"strings"

	"github.com/ape-dev-de/tiegel/internal/ideimport"
)

// Components of the reference IDE's application options with the Docker
// connection and the docker binary.
const (
	refDockerComponent = "DockerSettings"
	refDockerTools     = "DockerToolSettings"
)

// contextOfURI guesses the Docker context of a socket URI: the sockets of
// OrbStack and Colima belong to their contexts; the default socket is
// docker's current context ("").
func contextOfURI(uri string) (string, bool) {
	u := strings.ToLower(uri)
	switch {
	case strings.Contains(u, ".orbstack"):
		return "orbstack", true
	case strings.Contains(u, ".colima"):
		return "colima", true
	case strings.Contains(u, "docker.sock"), strings.Contains(u, "docker_engine"), u == "":
		return "", true
	}
	return "", false
}

// importComponent maps the Docker connection and the docker binary (hook
// import.ide.component, M16).
func importComponent(c ideimport.Component) ([]ideimport.Item, bool) {
	if c.Kind != ideimport.KindComponent {
		return nil, false
	}
	switch c.Name {
	case refDockerComponent:
		uri := ""
		c.Element.Walk(func(e *ideimport.Element) bool {
			if v := e.Attr("uri"); v != "" && uri == "" {
				uri = c.Macros.Expand(v)
			}
			if v, ok := e.Option("uri"); ok && uri == "" {
				uri = c.Macros.Expand(v)
			}
			return true
		})
		ctxName, ok := contextOfURI(uri)
		it := c.Item("Docker connection", uri)
		switch {
		case !ok:
			it.Status, it.Detail = ideimport.StatusUnsupported, uri+": choose the matching Docker context in the settings"
		case ctxName == "":
			it.Status, it.Detail = ideimport.StatusSkipped, "default socket: docker's current context is used"
		default:
			it.Detail = "context " + ctxName
			it.Changes = []ideimport.Change{ideimport.Setting(SettingContext, ideimport.ScopeGlobal, ctxName)}
		}
		return []ideimport.Item{it}, true
	case refDockerTools:
		path, _ := c.Element.Option("DOCKER_PATH")
		if path == "" {
			path, _ = c.Element.Option("dockerPath")
		}
		p := c.Macros.Path(path)
		it := c.Item("Docker binary", p)
		if fi, err := os.Stat(p); p == "" || err != nil || fi.IsDir() {
			it.Status, it.Detail = ideimport.StatusSkipped, "not found here; Tiegel searches docker itself"
		} else {
			it.Changes = []ideimport.Change{ideimport.Setting(SettingPath, ideimport.ScopeGlobal, p)}
		}
		return []ideimport.Item{it}, true
	}
	return nil, false
}
