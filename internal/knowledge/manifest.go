package knowledge

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Layout of the knowledge directory (outside the project):
//
//	index.md                 table of contents (all levels)
//	overview.md              level 0: structure, stack, C4-style diagram
//	entrypoints/<kind>.md    level 1: routes, commands, consumers …
//	processes/<id>.md        level 2: one data flow per process
//	components/<id>.md       level 3: data contracts of central classes
//	questions.md             open and answered questions
//	questions.json           the question store
//	data/<id>.json           validated JSON per page (context for later levels)
//	manifest.json            what was generated from which sources
//	vectors/                 the vector store (FileStore)
//
// Version 1 directories (units/<id>.md, D560) are treated as stale: the
// manifest starts empty and the old pages are removed after the first
// run of the new pipeline.

// ManifestVersion is the version of manifest.json.
const ManifestVersion = 2

// Item states (ItemState.Status).
const (
	StatusDone   = "done"
	StatusFailed = "failed"
)

// Staleness reasons (Stale).
const (
	StaleNew     = "new"
	StaleChanged = "changed"
	StaleFailed  = "failed"
	StaleMissing = "missing"
)

// ItemState is what the manifest remembers of a generated page.
type ItemState struct {
	ID    string `json:"id"`
	Level string `json:"level"`
	Title string `json:"title"`
	Group string `json:"group,omitempty"`
	// Files maps the sources (the item's files, the files the validated
	// page cites and "@"-inputs) to their hashes at generation.
	Files map[string]string `json:"files"`
	// Commit is the git HEAD at generation ("" without git).
	Commit      string    `json:"commit,omitempty"`
	GeneratedAt time.Time `json:"generatedAt"`
	Confidence  float64   `json:"confidence"`
	Status      string    `json:"status"`
	Error       string    `json:"error,omitempty"`
	// MD is the page relative to the knowledge directory, Data its
	// validated JSON.
	MD   string `json:"md,omitempty"`
	Data string `json:"data,omitempty"`
	// Summary is the first sentences of the page (index.md).
	Summary string `json:"summary,omitempty"`
	// Model is the agent model that wrote it.
	Model string `json:"model,omitempty"`
	// Unverified counts references whose file or symbol was not found.
	Unverified int `json:"unverified,omitempty"`
	// Mechanical is set when the page was rendered without the agent
	// (the agent failed; the mechanical facts are still useful).
	Mechanical bool `json:"mechanical,omitempty"`
}

// Manifest is manifest.json.
type Manifest struct {
	Version int                   `json:"version"`
	Project string                `json:"project"`
	Items   map[string]*ItemState `json:"items"`
	LastRun time.Time             `json:"lastRun,omitzero"`
	// Legacy is set when an old (version 1) manifest was found: its
	// units are dropped and rebuilt.
	Legacy bool `json:"-"`
}

func emptyManifest() *Manifest {
	return &Manifest{Version: ManifestVersion, Items: map[string]*ItemState{}}
}

// LoadManifest reads dir/manifest.json; a missing file yields an empty
// manifest, a broken one an empty manifest and the error, an old one an
// empty manifest with Legacy set.
func LoadManifest(dir string) (*Manifest, error) {
	m := emptyManifest()
	b, err := os.ReadFile(filepath.Join(dir, "manifest.json"))
	if errors.Is(err, os.ErrNotExist) {
		if _, err := os.Stat(filepath.Join(dir, "units")); err == nil {
			m.Legacy = true
		}
		return m, nil
	}
	if err != nil {
		return m, fmt.Errorf("knowledge manifest: %w", err)
	}
	var head struct {
		Version int             `json:"version"`
		Units   json.RawMessage `json:"units"`
	}
	if err := json.Unmarshal(b, &head); err != nil {
		return emptyManifest(), fmt.Errorf("knowledge manifest is broken (starting over): %w", err)
	}
	if head.Version < ManifestVersion || len(head.Units) > 0 {
		m.Legacy = true
		return m, nil
	}
	if err := json.Unmarshal(b, m); err != nil {
		return emptyManifest(), fmt.Errorf("knowledge manifest is broken (starting over): %w", err)
	}
	if m.Items == nil {
		m.Items = map[string]*ItemState{}
	}
	return m, nil
}

// Save writes dir/manifest.json atomically.
func (m *Manifest) Save(dir string) error {
	m.Version = ManifestVersion
	b, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	return WriteFileAtomic(filepath.Join(dir, "manifest.json"), b)
}

// Clone returns a deep enough copy (the item states are copied).
func (m *Manifest) Clone() *Manifest {
	c := *m
	c.Items = make(map[string]*ItemState, len(m.Items))
	for k, v := range m.Items {
		s := *v
		c.Items[k] = &s
	}
	return &c
}

// Sorted returns the item states by level, then ID.
func (m *Manifest) Sorted() []*ItemState {
	out := make([]*ItemState, 0, len(m.Items))
	for _, u := range m.Items {
		out = append(out, u)
	}
	sort.Slice(out, func(i, j int) bool {
		if a, b := LevelRank(out[i].Level), LevelRank(out[j].Level); a != b {
			return a < b
		}
		return out[i].ID < out[j].ID
	})
	return out
}

// Level returns the item states of a level (sorted by ID).
func (m *Manifest) Level(level string) []*ItemState {
	var out []*ItemState
	for _, s := range m.Sorted() {
		if s.Level == level {
			out = append(out, s)
		}
	}
	return out
}

// HashFile returns the hex SHA-256 of a file ("" when unreadable).
func HashFile(file string) string {
	f, err := os.Open(file)
	if err != nil {
		return ""
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return ""
	}
	return hex.EncodeToString(h.Sum(nil))[:16]
}

// HashString returns a short hash of s.
func HashString(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:8])
}

// ItemHashes hashes the item's files below root (filling it.Bytes) and
// adds its "@"-inputs.
func ItemHashes(root string, it *Item) map[string]string {
	out := make(map[string]string, len(it.Files)+len(it.Inputs))
	it.Bytes = 0
	for _, f := range it.Files {
		abs := filepath.Join(root, filepath.FromSlash(f))
		out[f] = HashFile(abs)
		if st, err := os.Stat(abs); err == nil {
			it.Bytes += st.Size()
		}
	}
	for k, v := range it.Inputs {
		out[k] = v
	}
	return out
}

// Stale tells why item it must be (re)generated, "" when it is current.
// hashes are the current hashes of the item's files; the further
// sources the page cites are re-hashed below root.
func (m *Manifest) Stale(dir, root string, it Item, hashes map[string]string) string {
	st := m.Items[it.ID]
	switch {
	case st == nil:
		return StaleNew
	case st.Status != StatusDone:
		return StaleFailed
	case st.MD != "":
		if _, err := os.Stat(filepath.Join(dir, filepath.FromSlash(st.MD))); err != nil {
			return StaleMissing
		}
	}
	for f, h := range hashes {
		if st.Files[f] != h {
			return StaleChanged
		}
	}
	for f, h := range st.Files {
		if _, ok := hashes[f]; ok {
			continue
		}
		if strings.HasPrefix(f, "@") {
			return StaleChanged // an input is gone
		}
		if HashFile(filepath.Join(root, filepath.FromSlash(f))) != h {
			return StaleChanged
		}
	}
	return ""
}

// WriteFileAtomic writes b to file through a temporary file and rename
// (creating the directory).
func WriteFileAtomic(file string, b []byte) error {
	if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(file), ".tmp-"+filepath.Base(file)+"-*")
	if err != nil {
		return err
	}
	if _, err := tmp.Write(b); err != nil {
		tmp.Close()
		os.Remove(tmp.Name())
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmp.Name())
		return err
	}
	if err := os.Rename(tmp.Name(), file); err != nil {
		os.Remove(tmp.Name())
		return err
	}
	return nil
}
