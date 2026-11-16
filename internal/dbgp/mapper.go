package dbgp

import (
	"net/url"
	"strings"
)

// PathMapper translates between host paths and the file URIs used by the
// debugger engine (the "remote" side, e.g. a Docker container).
// Implementations must be safe for concurrent use.
type PathMapper interface {
	// HostToRemote converts a host path to a remote file:// URI.
	HostToRemote(hostPath string) string
	// RemoteToHost converts a remote URI to a host path. URIs that are not
	// file:// URIs (e.g. dbgp://1 for eval'd code) are returned unchanged.
	RemoteToHost(remoteURI string) string
}

// Mapping maps a host directory to a remote directory.
type Mapping struct {
	Host   string // e.g. "/Users/me/project" or `C:\work\project`
	Remote string // e.g. "/var/www/html"
}

// PrefixMapper is a [PathMapper] based on a list of directory prefixes. The
// longest matching prefix wins; paths without a match are passed through
// unchanged. Prefixes match whole path segments only. A PrefixMapper is
// immutable and safe for concurrent use. The zero value maps identically.
type PrefixMapper struct {
	mappings []Mapping
}

// NewPrefixMapper returns a mapper for the given mappings. Empty entries are
// ignored.
func NewPrefixMapper(mappings ...Mapping) *PrefixMapper {
	m := &PrefixMapper{}
	for _, mp := range mappings {
		if mp.Host == "" || mp.Remote == "" {
			continue
		}
		m.mappings = append(m.mappings, mp)
	}
	return m
}

// Mappings returns a copy of the configured mappings.
func (m *PrefixMapper) Mappings() []Mapping {
	return append([]Mapping(nil), m.mappings...)
}

// HostToRemote implements [PathMapper].
func (m *PrefixMapper) HostToRemote(hostPath string) string {
	if isFileURI(hostPath) {
		hostPath = URIToPath(hostPath)
	}
	if mp, rest, ok := m.match(hostPath, func(mp Mapping) string { return mp.Host }); ok {
		return PathToURI(joinPrefix(normSlashes(mp.Remote), rest, '/'))
	}
	return PathToURI(hostPath)
}

// RemoteToHost implements [PathMapper].
func (m *PrefixMapper) RemoteToHost(remoteURI string) string {
	p := remoteURI
	if isFileURI(remoteURI) {
		p = URIToPath(remoteURI)
	} else if strings.Contains(remoteURI, "://") {
		return remoteURI
	}
	if mp, rest, ok := m.match(p, func(mp Mapping) string { return mp.Remote }); ok {
		sep := byte('/')
		if isWindowsPath(mp.Host) || strings.Contains(mp.Host, `\`) {
			sep = '\\'
		}
		host := mp.Host
		if sep == '/' {
			host = normSlashes(host)
		}
		return joinPrefix(host, rest, sep)
	}
	return p
}

// match finds the mapping whose prefix (selected by key) is the longest
// segment-wise prefix of p. rest is the remainder of p in slash form,
// starting with "/" or empty.
func (m *PrefixMapper) match(p string, key func(Mapping) string) (Mapping, string, bool) {
	if m == nil {
		return Mapping{}, "", false
	}
	np := normSlashes(p)
	var (
		best     Mapping
		bestRest string
		bestLen  = -1
	)
	for _, mp := range m.mappings {
		prefix := normSlashes(key(mp))
		if len(prefix) <= bestLen {
			continue
		}
		fold := isWindowsPath(prefix) || strings.HasPrefix(prefix, "//")
		if rest, ok := cutPathPrefix(np, prefix, fold); ok {
			best, bestRest, bestLen = mp, rest, len(prefix)
		}
	}
	return best, bestRest, bestLen >= 0
}

// cutPathPrefix reports whether prefix is a segment-wise prefix of p and
// returns the remainder (empty or starting with "/").
func cutPathPrefix(p, prefix string, fold bool) (string, bool) {
	if len(p) < len(prefix) {
		return "", false
	}
	head := p[:len(prefix)]
	if fold {
		if !strings.EqualFold(head, prefix) {
			return "", false
		}
	} else if head != prefix {
		return "", false
	}
	rest := p[len(prefix):]
	if rest == "" || rest[0] == '/' {
		return rest, true
	}
	if strings.HasSuffix(prefix, "/") { // root such as "/" or "C:/"
		return "/" + rest, true
	}
	return "", false
}

// joinPrefix appends rest (slash form) to prefix using sep as separator.
func joinPrefix(prefix, rest string, sep byte) string {
	if rest == "" {
		return prefix
	}
	if sep != '/' {
		rest = strings.ReplaceAll(rest, "/", string(sep))
	}
	return strings.TrimRight(prefix, `/\`) + rest
}

// normSlashes converts Windows paths to forward slashes and removes a
// trailing separator (except for roots such as "/" and "C:/").
func normSlashes(p string) string {
	if isWindowsPath(p) || strings.HasPrefix(p, `\\`) {
		p = strings.ReplaceAll(p, `\`, "/")
	}
	for len(p) > 1 && strings.HasSuffix(p, "/") {
		if len(p) == 3 && p[1] == ':' {
			break
		}
		p = p[:len(p)-1]
	}
	return p
}

// isWindowsPath reports whether p starts with a drive letter ("C:") or is a
// UNC path.
func isWindowsPath(p string) bool {
	if strings.HasPrefix(p, `\\`) {
		return true
	}
	return len(p) >= 2 && p[1] == ':' && isLetter(p[0]) && (len(p) == 2 || p[2] == '/' || p[2] == '\\')
}

func isLetter(c byte) bool { return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') }

func isFileURI(s string) bool {
	return len(s) >= 5 && strings.EqualFold(s[:5], "file:")
}

// PathToURI converts an absolute path to a file:// URI with percent-encoding.
// Windows paths ("C:\dir\a b.php", UNC "\\server\share\x.php") are supported.
// Values that already are file URIs are returned unchanged.
func PathToURI(p string) string {
	if isFileURI(p) {
		return p
	}
	u := url.URL{Scheme: "file"}
	switch {
	case strings.HasPrefix(p, `\\`) || strings.HasPrefix(p, "//"):
		s := strings.ReplaceAll(p, `\`, "/")[2:]
		host, rest, _ := strings.Cut(s, "/")
		u.Host = host
		u.Path = "/" + rest
	case isWindowsPath(p):
		u.Path = "/" + strings.ReplaceAll(p, `\`, "/")
	default:
		u.Path = p
		if !strings.HasPrefix(p, "/") {
			u.Path = "/" + p
		}
	}
	return u.String()
}

// URIToPath converts a file:// URI to a path, decoding percent-escapes.
// Drive-letter and UNC URIs yield Windows paths with backslashes. Other
// values are returned unchanged.
func URIToPath(uri string) string {
	if !isFileURI(uri) {
		return uri
	}
	var host, p string
	if u, err := url.Parse(uri); err == nil {
		host, p = u.Host, u.Path
	} else {
		raw := strings.TrimPrefix(uri[5:], "//")
		if dec, err := url.PathUnescape(raw); err == nil {
			raw = dec
		}
		if !strings.HasPrefix(raw, "/") && !isWindowsPath(raw) {
			host, raw, _ = strings.Cut(raw, "/")
			raw = "/" + raw
		}
		p = raw
	}
	switch {
	case host != "" && isWindowsPath(host+"/"): // file://C:/x
		p = host + p
	case host != "" && !strings.EqualFold(host, "localhost"):
		return `\\` + host + strings.ReplaceAll(p, "/", `\`)
	}
	if len(p) >= 3 && p[0] == '/' && isWindowsPath(p[1:]) {
		p = p[1:]
	}
	if isWindowsPath(p) {
		p = strings.ReplaceAll(p, "/", `\`)
	}
	return p
}
