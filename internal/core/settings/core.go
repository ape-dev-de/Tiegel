package settings

import (
	"encoding/json"
	"fmt"
	"strings"
	"sync"
)

// Core settings registry (decision D190, D210).
//
// Core settings are declared as Specs and registered with RegisterCore,
// usually from an init function of the declaring package. The kernel's own
// specs live in corespecs.go; other core packages (e.g. the keymap)
// contribute theirs the same way:
//
//	func init() { _ = settings.RegisterCore(keymap.SettingSpecs()...) }
//
// Module (plugin) settings are never registered here: modules return them
// from module.SettingsProvider and the module registry collects them.
//
// For core specs, Spec.Group names the page of the settings dialog ("Editor",
// "Appearance", "Files", …) below the "Core" node.

// ReservedPrefix is the namespace of the module on/off states; no spec may
// use it.
const ReservedPrefix = "modules."

var core struct {
	mu    sync.RWMutex
	specs []Spec
	index map[string]int
}

// RegisterCore registers core setting specs. Invalid specs, keys in the
// reserved "modules." namespace and duplicates are rejected; the valid
// specs of the same call are still registered. It is safe for concurrent
// use.
func RegisterCore(specs ...Spec) error {
	core.mu.Lock()
	defer core.mu.Unlock()
	if core.index == nil {
		core.index = map[string]int{}
	}
	var errs []string
	for _, s := range specs {
		if err := s.Validate(); err != nil {
			errs = append(errs, err.Error())
			continue
		}
		if strings.HasPrefix(s.Key, ReservedPrefix) {
			errs = append(errs, fmt.Sprintf("settings: core spec %q uses the reserved namespace %q", s.Key, ReservedPrefix))
			continue
		}
		if _, dup := core.index[s.Key]; dup {
			errs = append(errs, fmt.Sprintf("settings: core spec %q registered twice", s.Key))
			continue
		}
		core.index[s.Key] = len(core.specs)
		core.specs = append(core.specs, cloneSpec(s))
	}
	if len(errs) > 0 {
		return fmt.Errorf("%s", strings.Join(errs, "; "))
	}
	return nil
}

// CoreSpecs returns the registered core specs in registration order.
func CoreSpecs() []Spec {
	core.mu.RLock()
	defer core.mu.RUnlock()
	out := make([]Spec, len(core.specs))
	for i, s := range core.specs {
		out[i] = cloneSpec(s)
	}
	return out
}

// CoreSpec returns the core spec for key.
func CoreSpec(key string) (Spec, bool) {
	core.mu.RLock()
	defer core.mu.RUnlock()
	i, ok := core.index[key]
	if !ok {
		return Spec{}, false
	}
	return cloneSpec(core.specs[i]), true
}

// unregisterCore removes keys again (tests only).
func unregisterCore(keys ...string) {
	core.mu.Lock()
	defer core.mu.Unlock()
	drop := map[string]bool{}
	for _, k := range keys {
		drop[k] = true
	}
	kept := core.specs[:0]
	core.index = map[string]int{}
	for _, s := range core.specs {
		if drop[s.Key] {
			continue
		}
		core.index[s.Key] = len(kept)
		kept = append(kept, s)
	}
	core.specs = kept
}

func cloneSpec(s Spec) Spec {
	s.Options = append([]string(nil), s.Options...)
	if l, ok := s.Default.([]string); ok {
		s.Default = append([]string{}, l...)
	}
	return s
}

// Resolve returns the value of key from the first store that holds a value
// decodable as T (pass the project store before the global one; nil stores
// are skipped), or def.
func Resolve[T any](key string, def T, stores ...*Store) T {
	for _, s := range stores {
		if s == nil {
			continue
		}
		if v, ok, err := Lookup[T](s, key); ok && err == nil {
			return v
		}
	}
	return def
}

// Source tells where an effective value comes from.
type Source string

// Sources of an effective value.
const (
	SourceDefault Source = "default"
	SourceGlobal  Source = "global"
	SourceProject Source = "project"
)

// Effective returns the raw JSON value of key with its source: the project
// store (if not nil and the spec allows project values) wins over the global
// store, which wins over the spec default. A stored value that fails
// spec.Check is skipped. A zero spec (undeclared key) allows both scopes and
// has no default (nil result with SourceDefault).
func Effective(spec Spec, key string, global, project *Store) (json.RawMessage, Source) {
	try := func(s *Store) (json.RawMessage, bool) {
		if s == nil {
			return nil, false
		}
		raw, ok := s.raw(key)
		if !ok {
			return nil, false
		}
		if spec.Kind != "" {
			var v any
			if json.Unmarshal(raw, &v) != nil || spec.Check(v) != nil {
				return nil, false
			}
		}
		return append(json.RawMessage(nil), raw...), true
	}
	if spec.Scope != ScopeGlobal {
		if raw, ok := try(project); ok {
			return raw, SourceProject
		}
	}
	if raw, ok := try(global); ok {
		return raw, SourceGlobal
	}
	if spec.Default == nil {
		return nil, SourceDefault
	}
	raw, err := json.Marshal(spec.Default)
	if err != nil {
		return nil, SourceDefault
	}
	return raw, SourceDefault
}

// Raw returns the stored JSON of key (a copy) and whether it exists.
func (s *Store) Raw(key string) (json.RawMessage, bool) {
	raw, ok := s.raw(key)
	if !ok {
		return nil, false
	}
	return append(json.RawMessage(nil), raw...), true
}

// MigrateKey moves the value stored under from to to when to has no value
// yet (renamed keys, e.g. after moving a setting into a module namespace).
// from is deleted in any case. It reports whether a value was moved.
func MigrateKey(s *Store, from, to string) (bool, error) {
	if s == nil {
		return false, nil
	}
	raw, ok := s.raw(from)
	if !ok {
		return false, nil
	}
	moved := false
	if !s.Has(to) {
		if err := s.setRaw(to, append(json.RawMessage(nil), raw...)); err != nil {
			return false, fmt.Errorf("settings: migrate %q to %q: %w", from, to, err)
		}
		moved = true
	}
	if err := s.Delete(from); err != nil {
		return moved, fmt.Errorf("settings: migrate %q: %w", from, err)
	}
	return moved, nil
}
