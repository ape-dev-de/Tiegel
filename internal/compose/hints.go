package compose

import (
	"fmt"
	"sort"
	"strings"
)

// Severity of a diagnostic hint.
type Severity int

// Hint severities.
const (
	SeverityInfo Severity = iota
	SeverityWarning
)

// String returns "info" or "warning".
func (s Severity) String() string {
	if s == SeverityWarning {
		return "warning"
	}
	return "info"
}

// Hint codes, stable for tests and for linking documentation.
const (
	HintUnknownService   = "unknown-service"
	HintNoHostGateway    = "no-host-gateway"
	HintNoXdebugEnv      = "no-xdebug-env"
	HintXdebugModeNoDbg  = "xdebug-mode-without-debug"
	HintClientHostLocal  = "client-host-loopback"
	HintNoPathMapping    = "no-path-mapping"
	HintProjectNotMapped = "project-not-mapped"
)

// Hint is a diagnostic about the Xdebug setup of a service.
type Hint struct {
	Severity Severity
	Code     string
	Message  string
}

// XdebugHints checks common Xdebug-in-Docker pitfalls for service. goos is
// the host OS (runtime.GOOS); host.docker.internal only needs extra_hosts on
// Linux, Docker Desktop provides it elsewhere.
func XdebugHints(p *Project, service, goos string) []Hint {
	svc, ok := p.Service(service)
	if !ok {
		return []Hint{{SeverityWarning, HintUnknownService, fmt.Sprintf("service %q is not defined or not enabled", service)}}
	}
	var hints []Hint

	if goos == "linux" && !hasHostGateway(svc.ExtraHosts) {
		hints = append(hints, Hint{SeverityWarning, HintNoHostGateway,
			`host.docker.internal does not resolve on Linux; add extra_hosts: ["host.docker.internal:host-gateway"] to the service`})
	}

	env := map[string]string{}
	keys := make([]string, 0)
	for k, v := range svc.Environment {
		if !strings.HasPrefix(strings.ToUpper(k), "XDEBUG_") {
			continue
		}
		val := ""
		if v != nil {
			val = *v
		}
		env[strings.ToUpper(k)] = val
		keys = append(keys, k)
	}
	sort.Strings(keys)
	if len(keys) == 0 {
		hints = append(hints, Hint{SeverityInfo, HintNoXdebugEnv,
			"no XDEBUG_* environment variables; make sure xdebug.mode=debug and xdebug.client_host are set in the image's php.ini"})
	}
	if mode, ok := env["XDEBUG_MODE"]; ok && mode != "" && !strings.Contains(mode, "debug") {
		hints = append(hints, Hint{SeverityWarning, HintXdebugModeNoDbg,
			fmt.Sprintf("XDEBUG_MODE=%q does not enable step debugging; include \"debug\"", mode)})
	}
	if cfg, ok := env["XDEBUG_CONFIG"]; ok {
		for _, part := range strings.Fields(cfg) {
			k, v, _ := strings.Cut(part, "=")
			if k == "client_host" && isLoopback(v) {
				hints = append(hints, Hint{SeverityWarning, HintClientHostLocal,
					fmt.Sprintf("XDEBUG_CONFIG client_host=%s points at the container itself; use host.docker.internal", v)})
			}
		}
	}

	m := ServiceMappings(p.Dir, svc)
	if len(m) == 0 {
		hints = append(hints, Hint{SeverityWarning, HintNoPathMapping,
			"the service has no bind mounts, so breakpoints cannot be mapped; add a path mapping manually"})
	} else if _, ok := m.ToRemote(p.Dir); !ok {
		hints = append(hints, Hint{SeverityInfo, HintProjectNotMapped,
			"the project root is not bind-mounted; only files below the mounted directories can be debugged"})
	}
	return hints
}

// hasHostGateway reports whether host.docker.internal is mapped, usually to
// "host-gateway" (any explicit IP works as well).
func hasHostGateway(h map[string][]string) bool {
	for _, ip := range h["host.docker.internal"] {
		if ip != "" {
			return true
		}
	}
	return false
}

func isLoopback(host string) bool {
	switch strings.ToLower(host) {
	case "localhost", "127.0.0.1", "::1", "0.0.0.0":
		return true
	}
	return false
}
