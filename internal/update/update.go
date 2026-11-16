// Package update implements Tiegel's opt-in check for new releases.
//
// Privacy rules (see docs/konzept.md):
//   - The check is disabled by default and only runs when the user enabled it
//     (Config.Enabled) or explicitly asked for it.
//   - The request carries no identifiers: a fixed generic User-Agent, no
//     cookies, no query parameters, no device or installation IDs, and not even
//     the current version.
//   - Nothing is downloaded or installed. The result only contains the newest
//     version, a link to its release page and the release notes.
//
// The source defaults to the GitHub Releases API of ape-dev-de/Tiegel but can be
// any URL serving a compatible JSON document: either a single release object or
// an array of them, each with at least "tag_name" and "html_url" (optional
// "body", "draft", "prerelease").
package update

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// DefaultSource is the release feed used when Config.Source is empty.
const DefaultSource = "https://api.github.com/repos/ape-dev-de/Tiegel/releases/latest"

// UserAgent is sent with every request. It intentionally contains neither a
// version number nor anything else that could identify an installation.
const UserAgent = "Tiegel-Update-Check"

// maxBody limits how much of the response is read (1 MiB).
const maxBody = 1 << 20

// defaultTimeout bounds a check when no HTTP client is injected.
const defaultTimeout = 15 * time.Second

// ErrDisabled is returned by Checker.Check when update checks are switched off.
var ErrDisabled = errors.New("update check disabled")

// ErrNoRelease is returned when the source lists no usable release.
var ErrNoRelease = errors.New("no release found")

// Doer performs HTTP requests; *http.Client satisfies it.
type Doer interface {
	Do(req *http.Request) (*http.Response, error)
}

// Config controls the update check. The zero value is a disabled check.
type Config struct {
	// Enabled switches the check on. Default: off.
	Enabled bool
	// Source is the URL of the release feed; empty means DefaultSource.
	// Only https is accepted, except for loopback hosts (self-hosted testing).
	Source string
	// IncludePrereleases also offers pre-release versions.
	IncludePrereleases bool
}

// Result describes the outcome of a check.
type Result struct {
	// Available is true if Version is newer than the running version.
	Available bool
	// Version is the newest release found, without "v" prefix.
	Version string
	// URL links to the release page (http or https only).
	URL string
	// Notes are the release notes as published by the source.
	Notes string
}

// Checker queries a release feed.
type Checker struct {
	cfg    Config
	client Doer
}

// New returns a Checker for cfg. If client is nil, a dedicated *http.Client
// with a timeout and without cookie jar is used.
func New(cfg Config, client Doer) *Checker {
	if client == nil {
		client = &http.Client{Timeout: defaultTimeout}
	}
	return &Checker{cfg: cfg, client: client}
}

// Source returns the effective feed URL.
func (c *Checker) Source() string {
	if s := strings.TrimSpace(c.cfg.Source); s != "" {
		return s
	}
	return DefaultSource
}

// Check compares current (e.g. version.Version) with the newest release of the
// configured source. It returns ErrDisabled if the check is switched off.
//
// If current cannot be parsed (development builds such as "dev"), any release
// is reported as available.
func (c *Checker) Check(ctx context.Context, current string) (Result, error) {
	if !c.cfg.Enabled {
		return Result{}, ErrDisabled
	}
	return c.CheckNow(ctx, current)
}

// CheckNow runs the check regardless of Config.Enabled. Use it only for an
// explicit user action such as a "Check for updates" menu entry.
func (c *Checker) CheckNow(ctx context.Context, current string) (Result, error) {
	src := c.Source()
	if err := validateSource(src); err != nil {
		return Result{}, err
	}
	rels, err := c.fetch(ctx, src)
	if err != nil {
		return Result{}, err
	}
	best, bestV, ok := pickLatest(rels, c.cfg.IncludePrereleases)
	if !ok {
		return Result{}, ErrNoRelease
	}
	res := Result{Version: bestV.String(), Notes: best.Body}
	if isWebURL(best.HTMLURL) {
		res.URL = best.HTMLURL
	}
	cur, err := ParseVersion(current)
	res.Available = err != nil || bestV.Compare(cur) > 0
	return res, nil
}

// release is the subset of the GitHub release object Tiegel understands.
type release struct {
	TagName    string `json:"tag_name"`
	HTMLURL    string `json:"html_url"`
	Body       string `json:"body"`
	Draft      bool   `json:"draft"`
	Prerelease bool   `json:"prerelease"`
}

func (c *Checker) fetch(ctx context.Context, src string) ([]release, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, src, nil)
	if err != nil {
		return nil, fmt.Errorf("update: build request: %w", err)
	}
	req.Header.Set("User-Agent", UserAgent)
	req.Header.Set("Accept", "application/json")

	resp, err := c.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("update: fetch %s: %w", src, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("update: fetch %s: unexpected status %s", src, resp.Status)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxBody+1))
	if err != nil {
		return nil, fmt.Errorf("update: read response: %w", err)
	}
	if len(data) > maxBody {
		return nil, fmt.Errorf("update: response larger than %d bytes", maxBody)
	}
	return decodeReleases(data)
}

func decodeReleases(data []byte) ([]release, error) {
	trimmed := strings.TrimSpace(string(data))
	if strings.HasPrefix(trimmed, "[") {
		var list []release
		if err := json.Unmarshal(data, &list); err != nil {
			return nil, fmt.Errorf("update: decode release list: %w", err)
		}
		return list, nil
	}
	var one release
	if err := json.Unmarshal(data, &one); err != nil {
		return nil, fmt.Errorf("update: decode release: %w", err)
	}
	return []release{one}, nil
}

// pickLatest returns the highest-versioned acceptable release.
func pickLatest(rels []release, includePre bool) (release, Version, bool) {
	var (
		best  release
		bestV Version
		found bool
	)
	for _, r := range rels {
		if r.Draft {
			continue
		}
		v, err := ParseVersion(r.TagName)
		if err != nil {
			continue
		}
		if !includePre && (r.Prerelease || v.IsPrerelease()) {
			continue
		}
		if !found || v.Compare(bestV) > 0 {
			best, bestV, found = r, v, true
		}
	}
	return best, bestV, found
}

// validateSource only allows https, or http to loopback hosts.
func validateSource(src string) error {
	u, err := url.Parse(src)
	if err != nil {
		return fmt.Errorf("update: invalid source %q: %w", src, err)
	}
	switch u.Scheme {
	case "https":
		if u.Host == "" {
			return fmt.Errorf("update: invalid source %q: missing host", src)
		}
		return nil
	case "http":
		if isLoopback(u.Hostname()) {
			return nil
		}
		return fmt.Errorf("update: source %q must use https", src)
	}
	return fmt.Errorf("update: unsupported scheme in source %q", src)
}

func isLoopback(host string) bool {
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func isWebURL(s string) bool {
	u, err := url.Parse(s)
	return err == nil && (u.Scheme == "https" || u.Scheme == "http") && u.Host != ""
}
