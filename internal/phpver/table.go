package phpver

import (
	_ "embed"
	"errors"
	"fmt"

	"go.yaml.in/yaml/v3"
)

//go:embed features.yaml
var defaultTableYAML []byte

// Severity of a diagnostic.
type Severity int

// Severities, from most to least severe.
const (
	SeverityError Severity = iota
	SeverityWarning
	SeverityInfo
)

// String returns "error", "warning" or "info".
func (s Severity) String() string {
	switch s {
	case SeverityWarning:
		return "warning"
	case SeverityInfo:
		return "info"
	}
	return "error"
}

// UnmarshalYAML parses "error", "warning" or "info".
func (s *Severity) UnmarshalYAML(n *yaml.Node) error {
	switch n.Value {
	case "", "error":
		*s = SeverityError
	case "warning":
		*s = SeverityWarning
	case "info":
		*s = SeverityInfo
	default:
		return fmt.Errorf("unknown severity %q (line %d)", n.Value, n.Line)
	}
	return nil
}

// Examples are PHP snippets documenting and testing a query.
type Examples struct {
	Match   string `yaml:"match"`
	NoMatch string `yaml:"nomatch"`
}

// Feature is a language feature that needs a minimum PHP version.
type Feature struct {
	ID          string   `yaml:"id"`
	Description string   `yaml:"description"`
	Min         string   `yaml:"min"`
	Severity    Severity `yaml:"severity"`
	Note        string   `yaml:"note"`
	// Query is a Tree-sitter query for the PHP grammar; the capture
	// @feature marks the reported range. Empty means "not detectable".
	Query    string   `yaml:"query"`
	Examples Examples `yaml:"examples"`
	LintSkip bool     `yaml:"lint_skip"`

	min Version
}

// MinVersion returns the parsed Min field.
func (f Feature) MinVersion() Version { return f.min }

// Deprecation is a construct that is deprecated or removed in some version.
type Deprecation struct {
	ID          string   `yaml:"id"`
	Description string   `yaml:"description"`
	Deprecated  string   `yaml:"deprecated"`
	Removed     string   `yaml:"removed"`
	Severity    Severity `yaml:"severity"` // severity while deprecated (default warning)
	Note        string   `yaml:"note"`
	Query       string   `yaml:"query"`
	Examples    Examples `yaml:"examples"`
	LintSkip    bool     `yaml:"lint_skip"`

	deprecated Version
	removed    Version // zero if not removed
}

// DeprecatedVersion returns the parsed Deprecated field.
func (d Deprecation) DeprecatedVersion() Version { return d.deprecated }

// RemovedVersion returns the parsed Removed field; ok is false if the
// construct has not been removed.
func (d Deprecation) RemovedVersion() (Version, bool) { return d.removed, d.Removed != "" }

// Table is the feature and deprecation table.
type Table struct {
	Features     []Feature     `yaml:"features"`
	Deprecations []Deprecation `yaml:"deprecations"`
}

// ParseTable parses and validates a YAML table.
func ParseTable(data []byte) (*Table, error) {
	var t Table
	// Deprecation severity defaults to warning; yaml leaves absent fields
	// untouched, so pre-scan raw nodes for explicit values.
	var raw struct {
		Deprecations []map[string]any `yaml:"deprecations"`
	}
	if err := yaml.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("phpver: parse table: %w", err)
	}
	if err := yaml.Unmarshal(data, &t); err != nil {
		return nil, fmt.Errorf("phpver: parse table: %w", err)
	}
	seen := map[string]bool{}
	var errs []error
	check := func(id string) {
		if id == "" {
			errs = append(errs, errors.New("entry without id"))
		} else if seen[id] {
			errs = append(errs, fmt.Errorf("duplicate id %q", id))
		}
		seen[id] = true
	}
	for i := range t.Features {
		f := &t.Features[i]
		check(f.ID)
		v, err := ParseVersion(f.Min)
		if err != nil {
			errs = append(errs, fmt.Errorf("feature %s: min: %w", f.ID, err))
		}
		f.min = v
	}
	for i := range t.Deprecations {
		d := &t.Deprecations[i]
		check(d.ID)
		if _, explicit := raw.Deprecations[i]["severity"]; !explicit {
			d.Severity = SeverityWarning
		}
		v, err := ParseVersion(d.Deprecated)
		if err != nil {
			errs = append(errs, fmt.Errorf("deprecation %s: deprecated: %w", d.ID, err))
		}
		d.deprecated = v
		if d.Removed != "" {
			r, err := ParseVersion(d.Removed)
			if err != nil {
				errs = append(errs, fmt.Errorf("deprecation %s: removed: %w", d.ID, err))
			}
			d.removed = r
		}
	}
	if err := errors.Join(errs...); err != nil {
		return nil, fmt.Errorf("phpver: invalid table: %w", err)
	}
	return &t, nil
}

// DefaultTable returns a freshly parsed copy of the embedded table.
func DefaultTable() (*Table, error) { return ParseTable(defaultTableYAML) }
