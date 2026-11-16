package compose

import (
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/compose-spec/compose-go/v2/types"
)

// PathMapping maps a host path to a path inside the container.
type PathMapping struct {
	// Host is an absolute host path (OS separators).
	Host string
	// Remote is an absolute container path (forward slashes).
	Remote string
}

// Mappings is an ordered, user-editable list of path mappings. Lookups use
// the longest matching prefix, so order does not affect results.
type Mappings []PathMapping

// ServiceMappings derives mappings from the bind mounts of svc. Relative
// sources are resolved against projectDir and "~" against the home directory.
// Named volumes and tmpfs mounts are ignored.
func ServiceMappings(projectDir string, svc types.ServiceConfig) Mappings {
	var m Mappings
	for _, v := range svc.Volumes {
		if v.Type != types.VolumeTypeBind || v.Source == "" || v.Target == "" {
			continue
		}
		m = m.Set(resolveHostPath(projectDir, v.Source), v.Target)
	}
	return m
}

func resolveHostPath(projectDir, src string) string {
	if src == "~" || strings.HasPrefix(src, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			src = filepath.Join(home, src[1:])
		}
	}
	if !filepath.IsAbs(src) {
		src = filepath.Join(projectDir, src)
	}
	return filepath.Clean(src)
}

// Set returns a copy of m where the mapping for host is replaced by remote,
// or added if host was not mapped yet.
func (m Mappings) Set(host, remote string) Mappings {
	host = filepath.Clean(host)
	remote = path.Clean(remote)
	out := make(Mappings, 0, len(m)+1)
	replaced := false
	for _, pm := range m {
		if pm.Host == host {
			if !replaced {
				out = append(out, PathMapping{Host: host, Remote: remote})
				replaced = true
			}
			continue
		}
		out = append(out, pm)
	}
	if !replaced {
		out = append(out, PathMapping{Host: host, Remote: remote})
	}
	return out
}

// Remove returns a copy of m without the mapping for host.
func (m Mappings) Remove(host string) Mappings {
	host = filepath.Clean(host)
	out := make(Mappings, 0, len(m))
	for _, pm := range m {
		if pm.Host != host {
			out = append(out, pm)
		}
	}
	return out
}

// ToRemote translates a host path into the container. ok is false when no
// mapping covers hostPath.
func (m Mappings) ToRemote(hostPath string) (remote string, ok bool) {
	hostPath = filepath.Clean(hostPath)
	best := -1
	for i, pm := range m {
		if hasPrefix(hostPath, pm.Host, string(filepath.Separator)) &&
			(best < 0 || len(pm.Host) > len(m[best].Host)) {
			best = i
		}
	}
	if best < 0 {
		return "", false
	}
	rel := filepath.ToSlash(strings.TrimPrefix(strings.TrimPrefix(hostPath, m[best].Host), string(filepath.Separator)))
	return path.Join(m[best].Remote, rel), true
}

// ToHost translates a container path to the host. ok is false when no
// mapping covers remote.
func (m Mappings) ToHost(remote string) (host string, ok bool) {
	remote = path.Clean(remote)
	best := -1
	for i, pm := range m {
		if hasPrefix(remote, pm.Remote, "/") &&
			(best < 0 || len(pm.Remote) > len(m[best].Remote)) {
			best = i
		}
	}
	if best < 0 {
		return "", false
	}
	rel := strings.TrimPrefix(strings.TrimPrefix(remote, m[best].Remote), "/")
	return filepath.Join(m[best].Host, filepath.FromSlash(rel)), true
}

// hasPrefix reports whether p equals prefix or lies below it, comparing whole
// path segments ("/app" does not prefix "/application").
func hasPrefix(p, prefix, sep string) bool {
	if p == prefix {
		return true
	}
	if !strings.HasSuffix(prefix, sep) {
		prefix += sep
	}
	return strings.HasPrefix(p, prefix)
}
