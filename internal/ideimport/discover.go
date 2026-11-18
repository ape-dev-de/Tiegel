package ideimport

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// VendorDir is the name of the reference IDE vendor's directory below the
// per-user configuration root (a filesystem path, not a product name).
const VendorDir = "JetBrains"

// Source is an application configuration directory of the reference IDE.
type Source struct {
	// Dir is the absolute directory (it contains options/, keymaps/, …).
	Dir string `json:"dir"`
	// Name is the directory name (product and version as the vendor
	// writes it), shown to the user.
	Name string `json:"name"`
	// Product and Version are parsed from Name ("" when it does not
	// follow the <Product><Year>.<N> pattern).
	Product string `json:"product,omitempty"`
	Version string `json:"version,omitempty"`
	// PHP reports a PHP configuration (options/php.xml): the product used
	// for PHP work, preferred as default.
	PHP bool `json:"php"`
}

// DiscoverOptions locate the configuration roots. Tests set Roots (or
// Home/AppData) to fixture directories; the real user's directories are
// used only when the user starts an import.
type DiscoverOptions struct {
	// Roots are vendor directories to search (each containing
	// <Product><Version> directories). Non-empty Roots replace the
	// OS-specific defaults.
	Roots []string
	// GOOS selects the default roots; Home, AppData (Windows %APPDATA%)
	// and XDGConfig ($XDG_CONFIG_HOME) are their bases.
	GOOS      string
	Home      string
	AppData   string
	XDGConfig string
}

// DefaultRoots returns the vendor directories for the options' OS:
// macOS ~/Library/Application Support/<vendor>, Windows
// %APPDATA%\<vendor>, others $XDG_CONFIG_HOME/<vendor> (default
// ~/.config/<vendor>).
func DefaultRoots(o DiscoverOptions) []string {
	switch o.GOOS {
	case "darwin":
		if o.Home == "" {
			return nil
		}
		return []string{filepath.Join(o.Home, "Library", "Application Support", VendorDir)}
	case "windows":
		if o.AppData == "" {
			if o.Home == "" {
				return nil
			}
			o.AppData = filepath.Join(o.Home, "AppData", "Roaming")
		}
		return []string{filepath.Join(o.AppData, VendorDir)}
	}
	base := o.XDGConfig
	if base == "" {
		if o.Home == "" {
			return nil
		}
		base = filepath.Join(o.Home, ".config")
	}
	return []string{filepath.Join(base, VendorDir)}
}

var productRe = regexp.MustCompile(`^([A-Za-z][A-Za-z-]*?)(\d{4}\.\d+(?:\.\d+)?)$`)

// FindSources lists the configuration directories below the roots,
// newest version first (directories with a PHP configuration before
// others of the same version). Missing roots are no error.
func FindSources(o DiscoverOptions) ([]Source, error) {
	roots := o.Roots
	if len(roots) == 0 {
		roots = DefaultRoots(o)
	}
	var out []Source
	var errs []error
	for _, root := range roots {
		entries, err := os.ReadDir(root)
		if err != nil {
			if !errors.Is(err, os.ErrNotExist) {
				errs = append(errs, fmt.Errorf("ideimport: %w", err))
			}
			continue
		}
		for _, e := range entries {
			if !e.IsDir() {
				continue
			}
			src, err := SourceAt(filepath.Join(root, e.Name()))
			if err != nil {
				continue
			}
			out = append(out, src)
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if c := compareVersions(out[i].Version, out[j].Version); c != 0 {
			return c > 0
		}
		if out[i].PHP != out[j].PHP {
			return out[i].PHP
		}
		return out[i].Name < out[j].Name
	})
	return out, errors.Join(errs...)
}

// DefaultSource returns the source an import uses without a choice: the
// newest with a PHP configuration, else the newest.
func DefaultSource(list []Source) (Source, bool) {
	for _, s := range list {
		if s.PHP {
			return s, true
		}
	}
	if len(list) > 0 {
		return list[0], true
	}
	return Source{}, false
}

// ErrNoConfig marks a directory that is no configuration directory of
// the reference IDE.
var ErrNoConfig = errors.New("not a configuration directory of the reference IDE (no options/ or keymaps/)")

// SourceAt describes the configuration directory dir (given explicitly,
// e.g. with --from).
func SourceAt(dir string) (Source, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return Source{}, fmt.Errorf("ideimport: %w", err)
	}
	if !isDir(filepath.Join(abs, "options")) && !isDir(filepath.Join(abs, "keymaps")) {
		return Source{}, fmt.Errorf("ideimport: %s: %w", abs, ErrNoConfig)
	}
	s := Source{Dir: abs, Name: filepath.Base(abs)}
	if m := productRe.FindStringSubmatch(s.Name); m != nil {
		s.Product, s.Version = m[1], m[2]
	}
	_, err = os.Stat(filepath.Join(abs, "options", "php.xml"))
	s.PHP = err == nil
	return s, nil
}

// compareVersions compares dotted numeric versions ("" is lowest).
func compareVersions(a, b string) int {
	if a == b {
		return 0
	}
	if a == "" {
		return -1
	}
	if b == "" {
		return 1
	}
	pa, pb := strings.Split(a, "."), strings.Split(b, ".")
	for i := 0; i < max(len(pa), len(pb)); i++ {
		var x, y int
		if i < len(pa) {
			x, _ = strconv.Atoi(pa[i])
		}
		if i < len(pb) {
			y, _ = strconv.Atoi(pb[i])
		}
		if x != y {
			if x > y {
				return 1
			}
			return -1
		}
	}
	return 0
}

func isDir(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && fi.IsDir()
}
