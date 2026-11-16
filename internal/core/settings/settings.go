// Package settings is Tiegel's internal settings store (decision D8).
//
// Settings are never kept inside the project and are not meant to be edited
// by hand. They live in the OS application directory:
//
//	<os.UserConfigDir()>/Tiegel/settings.json                     global
//	<os.UserConfigDir()>/Tiegel/projects/<sha256(path)>/settings.json  per project
//
// The base directory is injectable (OpenGlobal/OpenProject take it as an
// argument) so tests never touch the real user directory.
//
// Values are stored as JSON and accessed with the typed helpers Get, Lookup
// and Set, or through a declared Key:
//
//	var IdleDelay = settings.Key[time.Duration]{Name: "autosave.idleDelay", Default: 0}
//	d := IdleDelay.Get(store)
//
// Every Set writes the file atomically and, when the value actually changed,
// publishes a Change on the bus topic Changed ("settings.changed").
//
// Module on/off states are kept here as well (ModuleEnabled /
// SetModuleEnabled), which makes *Store a module.StateStore.
package settings

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"github.com/ape-dev-de/tiegel/internal/core/bus"
	"github.com/ape-dev-de/tiegel/internal/core/internal/atomicfile"
)

// AppDirName is the name of Tiegel's directory below os.UserConfigDir().
const AppDirName = "Tiegel"

// FileName is the name of the settings file inside a store directory.
const FileName = "settings.json"

// projectPathFile records the original project path inside a project
// directory, so the hashed directories remain identifiable.
const projectPathFile = "project-path"

// formatVersion is written into every settings file.
const formatVersion = 1

// Scope tells whether a store holds global or project settings.
type Scope string

// Scopes.
const (
	ScopeGlobal  Scope = "global"
	ScopeProject Scope = "project"
)

// Change is published on Changed whenever a key's value changes.
type Change struct {
	Scope   Scope
	Project string // project path, empty for global settings
	Key     string
	Deleted bool
}

// Changed is the bus topic for setting changes.
const Changed = bus.Topic[Change]("settings.changed")

// DefaultBaseDir returns <os.UserConfigDir()>/Tiegel.
func DefaultBaseDir() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("settings: user config dir: %w", err)
	}
	return filepath.Join(dir, AppDirName), nil
}

// ProjectDir returns the per-project directory below baseDir for
// projectPath: baseDir/projects/<hex sha256 of the absolute, cleaned path>.
// Other per-project data (e.g. local history) lives below it, too.
func ProjectDir(baseDir, projectPath string) (string, error) {
	abs, err := filepath.Abs(projectPath)
	if err != nil {
		return "", fmt.Errorf("settings: project path: %w", err)
	}
	sum := sha256.Sum256([]byte(filepath.Clean(abs)))
	return filepath.Join(baseDir, "projects", hex.EncodeToString(sum[:])), nil
}

// Store is one settings file (global or per project). It is safe for
// concurrent use.
type Store struct {
	dir     string
	scope   Scope
	project string

	busMu sync.Mutex
	buses []*bus.Bus

	mu        sync.RWMutex
	values    map[string]json.RawMessage
	recovered string
}

type fileFormat struct {
	Version int                        `json:"version"`
	Values  map[string]json.RawMessage `json:"values"`
}

// OpenGlobal opens (or creates on first write) the global store in baseDir.
// b may be nil, in which case no change events are published.
func OpenGlobal(baseDir string, b *bus.Bus) (*Store, error) {
	return open(baseDir, ScopeGlobal, "", b)
}

// OpenProject opens the store for projectPath below baseDir (see
// ProjectDir). b may be nil.
func OpenProject(baseDir, projectPath string, b *bus.Bus) (*Store, error) {
	dir, err := ProjectDir(baseDir, projectPath)
	if err != nil {
		return nil, err
	}
	abs, _ := filepath.Abs(projectPath) // cannot fail: ProjectDir succeeded
	s, err := open(dir, ScopeProject, filepath.Clean(abs), b)
	if err != nil {
		return nil, err
	}
	marker := filepath.Join(dir, projectPathFile)
	if old, err := os.ReadFile(marker); err != nil || string(old) != s.project {
		if err := atomicfile.Write(marker, []byte(s.project), 0o600); err != nil {
			return nil, fmt.Errorf("settings: write project marker: %w", err)
		}
	}
	return s, nil
}

func open(dir string, scope Scope, project string, b *bus.Bus) (*Store, error) {
	s := &Store{dir: dir, scope: scope, project: project, values: map[string]json.RawMessage{}}
	if b != nil {
		s.buses = []*bus.Bus{b}
	}
	data, err := os.ReadFile(s.Path())
	switch {
	case errors.Is(err, os.ErrNotExist):
		return s, nil
	case err != nil:
		return nil, fmt.Errorf("settings: read %s: %w", s.Path(), err)
	}
	var ff fileFormat
	if err := json.Unmarshal(data, &ff); err != nil {
		// A corrupt file must not keep the IDE from starting: move it aside
		// and continue with defaults.
		backup := fmt.Sprintf("%s.corrupt-%d", s.Path(), time.Now().UnixNano())
		if rerr := os.Rename(s.Path(), backup); rerr != nil {
			return nil, fmt.Errorf("settings: corrupt %s (%v) and backup failed: %w", s.Path(), err, rerr)
		}
		s.recovered = backup
		return s, nil
	}
	for k, v := range ff.Values {
		// The file is indented; keep values compact so that change
		// detection in setRaw compares like with like.
		var buf bytes.Buffer
		if err := json.Compact(&buf, v); err == nil {
			v = buf.Bytes()
		}
		s.values[k] = v
	}
	return s, nil
}

// Dir returns the directory holding the store's files.
func (s *Store) Dir() string { return s.dir }

// Path returns the settings file path.
func (s *Store) Path() string { return filepath.Join(s.dir, FileName) }

// Scope returns the store's scope.
func (s *Store) Scope() Scope { return s.scope }

// Project returns the absolute project path (empty for the global store).
func (s *Store) Project() string { return s.project }

// Recovered returns the path a corrupt settings file was moved to while
// opening, or "" if the file was fine.
func (s *Store) Recovered() string { return s.recovered }

// Has reports whether key has a stored value.
func (s *Store) Has(key string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	_, ok := s.values[key]
	return ok
}

// Keys returns all stored keys in sorted order.
func (s *Store) Keys() []string {
	s.mu.RLock()
	keys := make([]string, 0, len(s.values))
	for k := range s.values {
		keys = append(keys, k)
	}
	s.mu.RUnlock()
	sort.Strings(keys)
	return keys
}

// raw returns the stored JSON for key.
func (s *Store) raw(key string) (json.RawMessage, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	v, ok := s.values[key]
	return v, ok
}

// setRaw stores (or with value == nil deletes) key and persists the file.
func (s *Store) setRaw(key string, value json.RawMessage) error {
	s.mu.Lock()
	old, existed := s.values[key]
	if (value == nil && !existed) || (value != nil && existed && bytes.Equal(old, value)) {
		s.mu.Unlock()
		return nil
	}
	if value == nil {
		delete(s.values, key)
	} else {
		s.values[key] = value
	}
	err := s.persistLocked()
	if err != nil {
		// roll back so memory matches disk
		if existed {
			s.values[key] = old
		} else {
			delete(s.values, key)
		}
	}
	s.mu.Unlock()
	if err != nil {
		return err
	}
	s.busMu.Lock()
	buses := append([]*bus.Bus(nil), s.buses...)
	s.busMu.Unlock()
	for _, b := range buses {
		Changed.Publish(b, Change{Scope: s.scope, Project: s.project, Key: key, Deleted: value == nil})
	}
	return nil
}

// AttachBus additionally publishes change events on b (one global store
// shared by several App instances of one process, e.g. one per project
// window). The returned function detaches b again.
func (s *Store) AttachBus(b *bus.Bus) (detach func()) {
	s.busMu.Lock()
	s.buses = append(s.buses, b)
	s.busMu.Unlock()
	return func() {
		s.busMu.Lock()
		defer s.busMu.Unlock()
		for i, x := range s.buses {
			if x == b {
				s.buses = append(s.buses[:i], s.buses[i+1:]...)
				return
			}
		}
	}
}

func (s *Store) persistLocked() error {
	data, err := json.MarshalIndent(fileFormat{Version: formatVersion, Values: s.values}, "", "  ")
	if err != nil {
		return fmt.Errorf("settings: encode: %w", err)
	}
	if err := atomicfile.Write(s.Path(), data, 0o600); err != nil {
		return fmt.Errorf("settings: save: %w", err)
	}
	return nil
}

// Delete removes key. Deleting a missing key is a no-op.
func (s *Store) Delete(key string) error { return s.setRaw(key, nil) }

// Lookup returns the value stored under key decoded as T. ok is false if
// the key is missing; err is non-nil if the stored value cannot be decoded
// as T.
func Lookup[T any](s *Store, key string) (v T, ok bool, err error) {
	raw, ok := s.raw(key)
	if !ok {
		return v, false, nil
	}
	if err := json.Unmarshal(raw, &v); err != nil {
		return v, true, fmt.Errorf("settings: decode %q: %w", key, err)
	}
	return v, true, nil
}

// Get returns the value stored under key, or def if it is missing or
// cannot be decoded as T.
func Get[T any](s *Store, key string, def T) T {
	v, ok, err := Lookup[T](s, key)
	if !ok || err != nil {
		return def
	}
	return v
}

// Set stores v under key and persists the store. If the encoded value is
// unchanged nothing is written and no event is published.
func Set[T any](s *Store, key string, v T) error {
	raw, err := json.Marshal(v)
	if err != nil {
		return fmt.Errorf("settings: encode %q: %w", key, err)
	}
	return s.setRaw(key, raw)
}

// Key is a typed, named setting with a default value.
type Key[T any] struct {
	Name    string
	Default T
}

// Get returns the key's value in s, or its Default.
func (k Key[T]) Get(s *Store) T { return Get(s, k.Name, k.Default) }

// Set stores v for the key in s.
func (k Key[T]) Set(s *Store, v T) error { return Set(s, k.Name, v) }

func moduleKey(id string) string { return "modules." + id + ".enabled" }

// ModuleEnabled returns the stored on/off state of a module. known is false
// if no state has been stored yet.
func (s *Store) ModuleEnabled(id string) (enabled, known bool) {
	v, ok, err := Lookup[bool](s, moduleKey(id))
	if !ok || err != nil {
		return false, false
	}
	return v, true
}

// SetModuleEnabled stores the on/off state of a module.
func (s *Store) SetModuleEnabled(id string, enabled bool) error {
	return Set(s, moduleKey(id), enabled)
}
