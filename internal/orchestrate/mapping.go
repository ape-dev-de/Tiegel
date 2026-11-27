package orchestrate

import (
	"path/filepath"
	"strings"

	"github.com/ape-dev-de/tiegel/internal/api"
)

// MappingCovers tells whether a container with the host/container path
// mappings ms sees the host directory dir (the plan worktrees inside the
// project). Without a covering mapping the plan executes in place.
func MappingCovers(ms []api.MappingInfo, dir string) (bool, string) {
	for _, m := range ms {
		if m.Host != "" && inside(filepath.Clean(m.Host), filepath.Clean(dir)) {
			return true, ""
		}
	}
	return false, "the container interpreter does not mount " + dir + " (check the path mappings of the PHP interpreter)"
}

// MapToHost maps a path reported from inside a container to the host
// (the longest remote prefix wins); other paths stay.
func MapToHost(ms []api.MappingInfo, p string) string {
	best, host := "", ""
	for _, m := range ms {
		r := strings.TrimSuffix(filepath.ToSlash(m.Remote), "/")
		if r != "" && (p == r || strings.HasPrefix(p, r+"/")) && len(r) > len(best) {
			best, host = r, m.Host
		}
	}
	if best == "" {
		return p
	}
	return filepath.Join(host, filepath.FromSlash(strings.TrimPrefix(p, best)))
}

// RelativeTo makes a reported path (host or container) relative to dir:
// mapped to the host first, then the longest suffix of it that exists
// under dir is used (container roots differ from the host).
func RelativeTo(dir, p string, ms []api.MappingInfo) string {
	if p == "" {
		return ""
	}
	p = MapToHost(ms, filepath.ToSlash(p))
	if filepath.IsAbs(p) {
		if r, err := filepath.Rel(dir, p); err == nil && !strings.HasPrefix(r, "..") {
			return filepath.ToSlash(r)
		}
	}
	parts := strings.Split(filepath.ToSlash(p), "/")
	for i := range parts {
		cand := strings.Join(parts[i:], "/")
		if cand != "" && fileExists(dir, cand) {
			return cand
		}
	}
	return filepath.ToSlash(p)
}
