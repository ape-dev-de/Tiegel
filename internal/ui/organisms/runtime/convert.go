package runtime

import (
	"time"

	"github.com/ape-dev-de/tiegel/internal/compose"
	"github.com/ape-dev-de/tiegel/internal/docker"
	"github.com/ape-dev-de/tiegel/internal/interp"
)

// FromImage converts a Docker image interpreter.
func FromImage(d *docker.ImageInterpreter) Interpreter {
	return Interpreter{ID: d.ID(), Label: d.Label(), Kind: KindDocker, Version: d.CachedVersion(),
		Detail: d.Image + " → " + d.RemoteDir(), Context: d.Client.Context}
}

// FromProbe converts an interpreter probe result; probedAt is shown in
// local time.
func FromProbe(i interp.Info, probedAt time.Time, cached bool) *ProbeInfo {
	p := &ProbeInfo{Version: i.Version, SAPI: i.SAPI, OS: i.OS, Xdebug: i.Xdebug, IniFile: i.IniFile,
		IniScanned: append([]string(nil), i.IniScanned...), Extensions: append([]string(nil), i.Extensions...), Cached: cached}
	if !probedAt.IsZero() {
		p.ProbedAt = probedAt.Local().Format("2006-01-02 15:04")
	}
	return p
}

// This file converts backend values (internal/interp, internal/compose)
// into the view models of this package. Only types are used.

// FromLocal converts a discovered local binary.
func FromLocal(l *interp.Local) Interpreter {
	detail := l.Found
	if detail == "" {
		detail = l.Path
	}
	return Interpreter{ID: l.ID(), Label: "php (" + detail + ")", Kind: KindLocal, Version: l.CachedVersion(), Detail: l.Path}
}

// FromCandidate converts a Compose candidate of project; the ID matches
// compose.Interpreter.ID.
func FromCandidate(project string, c compose.Candidate) Interpreter {
	return Interpreter{
		ID:      "compose:" + project + ":" + c.Service,
		Label:   "compose:" + c.Service,
		Kind:    KindCompose,
		Detail:  c.Image,
		Score:   c.Score,
		Reasons: append([]string(nil), c.Reasons...),
	}
}

// FromInterpreter converts any interp.Interpreter (version unknown unless
// passed).
func FromInterpreter(i interp.Interpreter, version string) Interpreter {
	return Interpreter{ID: i.ID(), Label: i.Label(), Kind: Kind(i.Kind()), Version: version}
}

// MappingsFromCompose converts compose mappings.
func MappingsFromCompose(ms compose.Mappings) []Mapping {
	out := make([]Mapping, len(ms))
	for i, m := range ms {
		out[i] = Mapping{Host: m.Host, Remote: m.Remote}
	}
	return out
}

// ToCompose converts mappings back, dropping incomplete rows.
func ToCompose(ms []Mapping) compose.Mappings {
	var out compose.Mappings
	for _, m := range ms {
		if m.Host != "" && m.Remote != "" {
			out = append(out, compose.PathMapping{Host: m.Host, Remote: m.Remote})
		}
	}
	return out
}

// HintsFromCompose converts compose Xdebug hints.
func HintsFromCompose(hs []compose.Hint) []Hint {
	out := make([]Hint, len(hs))
	for i, h := range hs {
		s := SeverityInfo
		if h.Severity == compose.SeverityWarning {
			s = SeverityWarning
		}
		out[i] = Hint{Severity: s, Code: h.Code, Message: h.Message}
	}
	return out
}
