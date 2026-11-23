package settings

import (
	"fmt"
	"strings"
)

// Kind is the value type of a declared setting (decision D190).
type Kind string

// Kinds.
const (
	KindBool   Kind = "bool"
	KindInt    Kind = "int"
	KindString Kind = "string"
	KindEnum   Kind = "enum"  // one of Spec.Options
	KindPath   Kind = "path"  // file or directory path
	KindList   Kind = "list"  // list of strings
	KindFloat  Kind = "float" // number with fraction (font sizes, line height)
)

// Spec declares one setting. The core and every module declare their
// settings as Specs; the settings dialog, the settings.schema API, the
// headless CLI and the IDE import are generated from them (D190).
//
// Keys of module settings live in the module's namespace: "<moduleID>.<name>"
// (e.g. "qa-pint.path"). Core keys use core namespaces such as "editor.",
// "ui.", "keymap.", "files.".
type Spec struct {
	// Key is the full dotted settings key.
	Key string `json:"key"`
	// Kind is the value type.
	Kind Kind `json:"kind"`
	// Default is the value used when nothing is stored. Its Go type must
	// match Kind: bool, int, float64, string, or []string.
	Default any `json:"default,omitempty"`
	// Scope tells where the value may be stored. ScopeProject means a
	// project value overrides the global one; ScopeGlobal is global only.
	Scope Scope `json:"scope"`
	// Label is the short UI label.
	Label string `json:"label"`
	// Help is an optional one-sentence explanation.
	Help string `json:"help,omitempty"`
	// Group is an optional sub-heading inside the owner's page.
	Group string `json:"group,omitempty"`
	// Options lists the allowed values of a KindEnum setting.
	Options []string `json:"options,omitempty"`
	// Min and Max bound a KindInt or KindFloat setting when non-nil.
	Min *int `json:"min,omitempty"`
	Max *int `json:"max,omitempty"`
	// ReadOnly marks a setting that is shown but written only through a
	// dedicated method of its owner (e.g. one that also fires events):
	// the dialog shows it disabled and settings.set rejects it (D249).
	ReadOnly bool `json:"readOnly,omitempty"`
	// ReadOnlyHint names the way to change a ReadOnly setting (e.g.
	// "use interpreter.select"); it is part of the settings.set error.
	ReadOnlyHint string `json:"readOnlyHint,omitempty"`
	// Executable marks a setting whose value names a program Tiegel runs
	// (a binary, script or shell). Imports from a project's foreign
	// configuration write it only with an explicit opt-in (D440).
	Executable bool `json:"executable,omitempty"`
	// Candidates names where values of this setting can be discovered
	// (e.g. "PHPUnit scripts in the project"): the dialog then offers the
	// result of settings.candidates as a choice list, with free input
	// still possible (D521). settings.schema fills it for keys that have
	// a registered candidate provider.
	Candidates string `json:"candidates,omitempty"`
}

// Validate checks the declaration itself (not a value).
func (s Spec) Validate() error {
	if s.Key == "" || !strings.Contains(s.Key, ".") {
		return fmt.Errorf("settings: spec key %q must be namespaced (\"ns.name\")", s.Key)
	}
	switch s.Kind {
	case KindBool, KindInt, KindFloat, KindString, KindPath, KindList:
	case KindEnum:
		if len(s.Options) == 0 {
			return fmt.Errorf("settings: enum spec %q has no options", s.Key)
		}
	default:
		return fmt.Errorf("settings: spec %q has unknown kind %q", s.Key, s.Kind)
	}
	if s.Scope != ScopeGlobal && s.Scope != ScopeProject {
		return fmt.Errorf("settings: spec %q has unknown scope %q", s.Key, s.Scope)
	}
	if s.Default != nil {
		if err := s.Check(s.Default); err != nil {
			return fmt.Errorf("settings: spec %q default: %w", s.Key, err)
		}
	}
	return nil
}

// Check validates value against the spec. Numbers decoded from JSON
// (float64 without fraction) are accepted for KindInt; []any of strings is
// accepted for KindList.
func (s Spec) Check(value any) error {
	switch s.Kind {
	case KindBool:
		if _, ok := value.(bool); !ok {
			return fmt.Errorf("want bool, got %T", value)
		}
	case KindInt:
		n, ok := asInt(value)
		if !ok {
			return fmt.Errorf("want int, got %T", value)
		}
		if s.Min != nil && n < *s.Min {
			return fmt.Errorf("%d below minimum %d", n, *s.Min)
		}
		if s.Max != nil && n > *s.Max {
			return fmt.Errorf("%d above maximum %d", n, *s.Max)
		}
	case KindFloat:
		f, ok := asFloat(value)
		if !ok {
			return fmt.Errorf("want number, got %T", value)
		}
		if s.Min != nil && f < float64(*s.Min) {
			return fmt.Errorf("%g below minimum %d", f, *s.Min)
		}
		if s.Max != nil && f > float64(*s.Max) {
			return fmt.Errorf("%g above maximum %d", f, *s.Max)
		}
	case KindString, KindPath:
		if _, ok := value.(string); !ok {
			return fmt.Errorf("want string, got %T", value)
		}
	case KindEnum:
		v, ok := value.(string)
		if !ok {
			return fmt.Errorf("want string, got %T", value)
		}
		for _, o := range s.Options {
			if o == v {
				return nil
			}
		}
		return fmt.Errorf("%q not one of %v", v, s.Options)
	case KindList:
		switch l := value.(type) {
		case []string:
		case []any:
			for _, e := range l {
				if _, ok := e.(string); !ok {
					return fmt.Errorf("list element: want string, got %T", e)
				}
			}
		default:
			return fmt.Errorf("want list of strings, got %T", value)
		}
	default:
		return fmt.Errorf("unknown kind %q", s.Kind)
	}
	return nil
}

func asInt(v any) (int, bool) {
	switch n := v.(type) {
	case int:
		return n, true
	case int64:
		return int(n), true
	case float64:
		if n == float64(int(n)) {
			return int(n), true
		}
	}
	return 0, false
}

func asFloat(v any) (float64, bool) {
	switch n := v.(type) {
	case float64:
		return n, true
	case float32:
		return float64(n), true
	case int:
		return float64(n), true
	case int64:
		return float64(n), true
	}
	return 0, false
}

// IntPtr returns a pointer to n, for Spec.Min and Spec.Max.
func IntPtr(n int) *int { return &n }
