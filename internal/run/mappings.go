package run

import (
	"path"

	"github.com/ape-dev-de/tiegel/internal/compose"
	"github.com/ape-dev-de/tiegel/internal/docker"
	"github.com/ape-dev-de/tiegel/internal/interp"
)

// PathMappings returns the mappings between host and interpreter paths a
// launch on t would use (configured ones first, then those of the compose
// service or image, for WSL the project directory). Results produced
// inside the interpreter (reports, caches) are mapped back with them.
func (t Target) PathMappings() compose.Mappings {
	if len(t.Mappings) > 0 {
		return t.Mappings
	}
	var m compose.Mappings
	switch i := t.Interp.(type) {
	case *compose.Interpreter:
		m = i.Mappings
	case *docker.ImageInterpreter:
		for _, mp := range i.Mappings() {
			m = m.Set(mp.Host, mp.Remote)
		}
	case *interp.WSL:
		if t.ProjectDir != "" && i.Distro != "" {
			if r, ok := interp.WindowsToWSL(t.ProjectDir, i.Distro); ok {
				m = m.Set(t.ProjectDir, path.Clean(r))
			}
		}
	}
	return m
}

// OnHost reports whether t runs processes directly on the host (no
// interpreter or a local PHP), so that host paths and the host's
// temporary directory are the ones the program sees.
func (t Target) OnHost() bool {
	switch t.Interp.(type) {
	case nil, *interp.Local:
		return true
	}
	return false
}
