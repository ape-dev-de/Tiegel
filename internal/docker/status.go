package docker

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// VersionInfo is the parsed result of `docker version --format json`.
type VersionInfo struct {
	// Client is the CLI version, e.g. "27.3.1".
	Client string `json:"client"`
	// Context is the context the CLI used (newer CLIs only).
	Context string `json:"context,omitempty"`
	// Server is the engine version; empty when the engine did not answer.
	Server     string `json:"server,omitempty"`
	APIVersion string `json:"apiVersion,omitempty"`
	// Platform is the engine's product name, e.g. "Docker Desktop 4.34.2"
	// or "Docker Engine - Community".
	Platform string `json:"platform,omitempty"`
	OS       string `json:"os,omitempty"`
	Arch     string `json:"arch,omitempty"`
	Kernel   string `json:"kernel,omitempty"`
}

type rawVersion struct {
	Client *struct {
		Version string `json:"Version"`
		Context string `json:"Context"`
	} `json:"Client"`
	Server *struct {
		Platform struct {
			Name string `json:"Name"`
		} `json:"Platform"`
		Version       string `json:"Version"`
		APIVersion    string `json:"ApiVersion"`
		Os            string `json:"Os"`
		Arch          string `json:"Arch"`
		KernelVersion string `json:"KernelVersion"`
	} `json:"Server"`
}

// ParseVersion parses `docker version --format json`. Output written by a
// CLI whose engine is down (Server null) parses fine with Server empty.
func ParseVersion(data []byte) (VersionInfo, error) {
	data = bytes.TrimSpace(data)
	if i := bytes.IndexByte(data, '{'); i > 0 {
		data = data[i:] // skip warnings printed before the JSON
	}
	var r rawVersion
	if err := json.Unmarshal(data, &r); err != nil {
		return VersionInfo{}, fmt.Errorf("docker: parse version: %w", err)
	}
	var v VersionInfo
	if r.Client != nil {
		v.Client, v.Context = r.Client.Version, r.Client.Context
	}
	if r.Server != nil {
		s := r.Server
		v.Server, v.APIVersion, v.Platform, v.OS, v.Arch, v.Kernel = s.Version, s.APIVersion, s.Platform.Name, s.Os, s.Arch, s.KernelVersion
	}
	return v, nil
}

// ProviderFromVersion refines a provider guess with the engine's answer
// (OrbStack reports an "orbstack" kernel, Docker Desktop its product name).
// It returns fallback when nothing is recognised.
func ProviderFromVersion(v VersionInfo, fallback Provider) Provider {
	p := strings.ToLower(v.Platform)
	k := strings.ToLower(v.Kernel)
	switch {
	case strings.Contains(k, "orbstack") || strings.Contains(p, "orbstack"):
		return ProviderOrbStack
	case strings.Contains(p, "docker desktop") || strings.Contains(k, "linuxkit"):
		return ProviderDockerDesktop
	case strings.Contains(p, "podman"):
		return ProviderPodman
	}
	return fallback
}

// DefaultTimeout bounds a reachability check.
const DefaultTimeout = 5 * time.Second

// Ping runs `docker version --format json` and reports whether the engine
// answered within timeout (0: DefaultTimeout). The returned VersionInfo
// holds at least the client version when the CLI ran. Ping contacts the
// engine of c's context: call it only for a user action or an execution.
func (c Client) Ping(ctx context.Context, timeout time.Duration) (VersionInfo, error) {
	if timeout <= 0 {
		timeout = DefaultTimeout
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	out, err := c.Output(ctx, "version", "--format", "json")
	v, perr := ParseVersion(out)
	if err != nil {
		return v, err
	}
	if perr != nil {
		return v, perr
	}
	if v.Server == "" {
		return v, fmt.Errorf("docker: engine of context %q did not report a version", c.Context)
	}
	return v, nil
}
