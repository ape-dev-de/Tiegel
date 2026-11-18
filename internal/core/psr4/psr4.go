// Package psr4 reads the PSR-4 autoload mappings of a Composer project
// and maps between directories and namespaces. It is the shared helper of
// the kernel (New PHP Class, file templates) and plugins (Go to Test); it
// does not import Gio.
package psr4

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// Mapping is one autoload entry of composer.json: namespace prefix →
// directory (project relative, slash separated, "" for the root).
type Mapping struct {
	// Prefix is the namespace prefix without leading or trailing
	// backslash ("" is the fallback mapping).
	Prefix string `json:"prefix"`
	Dir    string `json:"dir"`
	// Dev marks an autoload-dev entry.
	Dev bool `json:"dev,omitempty"`
}

// ErrComposer wraps unreadable or invalid composer.json files.
var ErrComposer = errors.New("composer.json")

// Read returns the psr-4 mappings of autoload and autoload-dev in
// root/composer.json, autoload first, then by directory and prefix. A
// missing file gives no mappings and no error.
func Read(root string) ([]Mapping, error) {
	data, err := os.ReadFile(filepath.Join(root, "composer.json"))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrComposer, err)
	}
	type autoload struct {
		PSR4 map[string]json.RawMessage `json:"psr-4"`
	}
	var c struct {
		Autoload    autoload `json:"autoload"`
		AutoloadDev autoload `json:"autoload-dev"`
	}
	if err := json.Unmarshal(data, &c); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrComposer, err)
	}
	var out []Mapping
	for _, sec := range []struct {
		m   map[string]json.RawMessage
		dev bool
	}{{c.Autoload.PSR4, false}, {c.AutoloadDev.PSR4, true}} {
		for prefix, raw := range sec.m {
			var dirs []string
			var one string
			if json.Unmarshal(raw, &one) == nil {
				dirs = []string{one}
			} else if json.Unmarshal(raw, &dirs) != nil {
				continue
			}
			for _, d := range dirs {
				out = append(out, Mapping{Prefix: strings.Trim(prefix, `\`), Dir: NormDir(d), Dev: sec.dev})
			}
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Dev != out[j].Dev {
			return !out[i].Dev
		}
		if out[i].Dir != out[j].Dir {
			return out[i].Dir < out[j].Dir
		}
		return out[i].Prefix < out[j].Prefix
	})
	return out, nil
}

// NormDir normalises a composer directory to "a/b" ("" for the root).
func NormDir(d string) string {
	d = filepath.ToSlash(filepath.Clean(filepath.FromSlash(d)))
	d = strings.TrimPrefix(d, "./")
	if d == "." || d == "/" {
		return ""
	}
	return strings.TrimSuffix(d, "/")
}

// Ident matches a PHP identifier (one namespace segment or class name).
var Ident = regexp.MustCompile(`^[A-Za-z_\x80-\xff][A-Za-z0-9_\x80-\xff]*$`)

// ValidNamespace reports whether ns is empty or a\b\c of identifiers.
func ValidNamespace(ns string) bool {
	if ns == "" {
		return true
	}
	for _, seg := range strings.Split(ns, `\`) {
		if !Ident.MatchString(seg) {
			return false
		}
	}
	return true
}

// SegmentError reports a directory name that cannot be a namespace
// segment.
type SegmentError struct {
	Segment string
	Mapping Mapping
}

func (e *SegmentError) Error() string {
	return fmt.Sprintf("directory %q is not a valid namespace segment (PSR-4 %s → %s); pass a namespace explicitly", e.Segment, e.Mapping.Prefix, e.Mapping.Dir)
}

// NamespaceFor returns the namespace of the project directory rel (slash
// separated, "" for the root) and the mapping that covers it. The longest
// mapped directory wins; autoload beats autoload-dev for the same
// directory. found is false when no mapping covers rel.
func NamespaceFor(maps []Mapping, rel string) (ns string, m Mapping, found bool, err error) {
	best := -1
	for i, c := range maps {
		if c.Dir != "" && rel != c.Dir && !strings.HasPrefix(rel, c.Dir+"/") {
			continue
		}
		if best < 0 || len(c.Dir) > len(maps[best].Dir) {
			best = i
		}
	}
	if best < 0 {
		return "", Mapping{}, false, nil
	}
	m = maps[best]
	rest := strings.TrimPrefix(strings.TrimPrefix(rel, m.Dir), "/")
	var parts []string
	if m.Prefix != "" {
		parts = append(parts, m.Prefix)
	}
	if rest != "" {
		for _, seg := range strings.Split(rest, "/") {
			if !Ident.MatchString(seg) {
				return "", m, false, &SegmentError{Segment: seg, Mapping: m}
			}
			parts = append(parts, seg)
		}
	}
	return strings.Join(parts, `\`), m, true, nil
}
