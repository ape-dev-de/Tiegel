package docker

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

// Provider names the engine behind a Docker context.
type Provider string

// Providers detected by DetectProvider.
const (
	ProviderOrbStack      Provider = "orbstack"
	ProviderDockerDesktop Provider = "docker-desktop"
	ProviderColima        Provider = "colima"
	ProviderRancher       Provider = "rancher-desktop"
	ProviderPodman        Provider = "podman"
	// ProviderEngine is a local Docker Engine (e.g. /var/run/docker.sock
	// on Linux) without a recognisable desktop product.
	ProviderEngine Provider = "docker-engine"
	// ProviderRemote is a tcp:// or ssh:// endpoint (another machine).
	ProviderRemote Provider = "remote"
	// ProviderUnknown is anything else.
	ProviderUnknown Provider = "unknown"
)

// ContextInfo is one entry of `docker context ls`.
type ContextInfo struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	// Endpoint is the Docker endpoint, e.g.
	// unix:///Users/me/.orbstack/run/docker.sock.
	Endpoint string `json:"endpoint"`
	// Current marks docker's current context (DOCKER_CONTEXT or the CLI
	// configuration).
	Current bool `json:"current,omitempty"`
	// Error is a configuration error of the context reported by docker.
	Error    string   `json:"error,omitempty"`
	Provider Provider `json:"provider"`
	// Remote is true for tcp:// and ssh:// endpoints: using them means
	// network access to another machine.
	Remote bool `json:"remote,omitempty"`
}

// rawContext matches both the line format (docker ≥ 24) and the array
// format of older CLIs.
type rawContext struct {
	Name           string `json:"Name"`
	Description    string `json:"Description"`
	DockerEndpoint string `json:"DockerEndpoint"`
	Current        bool   `json:"Current"`
	Error          string `json:"Error"`
}

func (r rawContext) info() ContextInfo {
	c := ContextInfo{Name: r.Name, Description: r.Description, Endpoint: r.DockerEndpoint, Current: r.Current, Error: r.Error}
	c.Provider = DetectProvider(r.Name, r.DockerEndpoint)
	c.Remote = IsRemote(r.DockerEndpoint)
	return c
}

// ParseContexts parses `docker context ls --format json`: one JSON object
// per line (docker ≥ 24) or a single JSON array (older CLIs).
func ParseContexts(data []byte) ([]ContextInfo, error) {
	trimmed := bytes.TrimSpace(data)
	var raws []rawContext
	switch {
	case len(trimmed) == 0:
	case trimmed[0] == '[':
		if err := json.Unmarshal(trimmed, &raws); err != nil {
			return nil, fmt.Errorf("docker: parse context ls: %w", err)
		}
	default:
		sc := bufio.NewScanner(bytes.NewReader(trimmed))
		sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
		for sc.Scan() {
			line := bytes.TrimSpace(sc.Bytes())
			if len(line) == 0 {
				continue
			}
			var r rawContext
			if err := json.Unmarshal(line, &r); err != nil {
				return nil, fmt.Errorf("docker: parse context ls: %w", err)
			}
			raws = append(raws, r)
		}
		if err := sc.Err(); err != nil {
			return nil, fmt.Errorf("docker: read context ls: %w", err)
		}
	}
	out := make([]ContextInfo, 0, len(raws))
	for _, r := range raws {
		out = append(out, r.info())
	}
	return out, nil
}

// Contexts runs `docker context ls --format json`. It reads only the local
// CLI configuration; no engine is contacted. The client's own Context is
// not passed (an invalid one would make the listing fail).
func (c Client) Contexts(ctx context.Context) ([]ContextInfo, error) {
	out, err := c.WithContext("").Output(ctx, "context", "ls", "--format", "json")
	if err != nil {
		return nil, err
	}
	return ParseContexts(out)
}

// CurrentContext returns the entry marked current, if any.
func CurrentContext(list []ContextInfo) (ContextInfo, bool) {
	for _, c := range list {
		if c.Current {
			return c, true
		}
	}
	return ContextInfo{}, false
}

// FindContext returns the context called name.
func FindContext(list []ContextInfo, name string) (ContextInfo, bool) {
	for _, c := range list {
		if c.Name == name {
			return c, true
		}
	}
	return ContextInfo{}, false
}

// IsRemote reports whether endpoint points at another machine (tcp://,
// ssh://, http(s)://). Unix sockets and Windows named pipes are local.
func IsRemote(endpoint string) bool {
	e := strings.ToLower(endpoint)
	for _, p := range []string{"tcp://", "ssh://", "http://", "https://"} {
		if strings.HasPrefix(e, p) {
			host := strings.TrimPrefix(e, p)
			if i := strings.LastIndex(host, "@"); i >= 0 {
				host = host[i+1:]
			}
			return !(strings.HasPrefix(host, "localhost") || strings.HasPrefix(host, "127.") || strings.HasPrefix(host, "[::1]"))
		}
	}
	return false
}

// DetectProvider guesses the engine of a context from its name and
// endpoint.
func DetectProvider(name, endpoint string) Provider {
	e := strings.ToLower(endpoint)
	n := strings.ToLower(name)
	switch {
	case strings.Contains(e, ".orbstack") || strings.Contains(e, "orbstack") || n == "orbstack":
		return ProviderOrbStack
	case strings.Contains(e, ".colima") || strings.HasPrefix(n, "colima"):
		return ProviderColima
	case strings.Contains(e, ".rd/") || strings.Contains(e, "rancher-desktop") || n == "rancher-desktop":
		return ProviderRancher
	case strings.Contains(e, "podman") || strings.HasPrefix(n, "podman"):
		return ProviderPodman
	case strings.Contains(e, "docker.raw.sock") || strings.Contains(e, "/.docker/run/docker.sock") ||
		strings.Contains(e, "dockerdesktop") || strings.Contains(e, "docker_engine_linux") || n == "desktop-linux" || n == "desktop-windows":
		return ProviderDockerDesktop
	case IsRemote(endpoint):
		return ProviderRemote
	case strings.HasPrefix(e, "unix://") || strings.HasPrefix(e, "npipe://"):
		return ProviderEngine
	}
	return ProviderUnknown
}
