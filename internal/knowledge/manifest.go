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
	"time"
)

// Layout of the knowledge directory (outside the project):
//
//	index.md        table of contents
//	units/<id>.md   one document per unit
//	manifest.json   what was generated from which sources
//	vectors/        the vector store (FileStore)

// Unit states (UnitState.Status).
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

// UnitState is what the manifest remembers of a generated unit.
type UnitState struct {
	ID    string `json:"id"`
	Title string `json:"title"`
	Dir   string `json:"dir"`
	// Files maps the source files to their content hashes at generation.
	Files map[string]string `json:"files"`
	// Commit is the git HEAD at generation ("" without git).
	Commit      string    `json:"commit,omitempty"`
	GeneratedAt time.Time `json:"generatedAt"`
	Confidence  float64   `json:"confidence"`
	Status      string    `json:"status"`
	Error       string    `json:"error,omitempty"`
	// MD is the document relative to the knowledge directory.
	MD string `json:"md,omitempty"`
	// Model is the agent model that wrote it.
	Model string `json:"model,omitempty"`
	// Unverified counts steps whose file or symbol was not found.
	Unverified int `json:"unverified,omitempty"`
}

// Manifest is manifest.json.
type Manifest struct {
	Version int                   `json:"version"`
	Project string                `json:"project"`
	Units   map[string]*UnitState `json:"units"`
	LastRun time.Time             `json:"lastRun,omitzero"`
}

// LoadManifest reads dir/manifest.json; a missing file yields an empty
// manifest, a broken one an empty manifest and the error.
func LoadManifest(dir string) (*Manifest, error) {
	m := &Manifest{Version: 1, Units: map[string]*UnitState{}}
	b, err := os.ReadFile(filepath.Join(dir, "manifest.json"))
	if errors.Is(err, os.ErrNotExist) {
		return m, nil
	}
	if err != nil {
		return m, fmt.Errorf("knowledge manifest: %w", err)
	}
	if err := json.Unmarshal(b, m); err != nil {
		return &Manifest{Version: 1, Units: map[string]*UnitState{}}, fmt.Errorf("knowledge manifest is broken (starting over): %w", err)
	}
	if m.Units == nil {
		m.Units = map[string]*UnitState{}
	}
	return m, nil
}

// Save writes dir/manifest.json atomically.
func (m *Manifest) Save(dir string) error {
	b, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	return WriteFileAtomic(filepath.Join(dir, "manifest.json"), b)
}

// Sorted returns the unit states by ID.
func (m *Manifest) Sorted() []*UnitState {
	out := make([]*UnitState, 0, len(m.Units))
	for _, u := range m.Units {
		out = append(out, u)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
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

// HashFiles hashes the project relative files below root and fills
// u.Bytes.
func HashFiles(root string, u *Unit) map[string]string {
	out := make(map[string]string, len(u.Files))
	u.Bytes = 0
	for _, f := range u.Files {
		abs := filepath.Join(root, filepath.FromSlash(f))
		out[f] = HashFile(abs)
		if st, err := os.Stat(abs); err == nil {
			u.Bytes += st.Size()
		}
	}
	return out
}

// Stale tells why unit u must be (re)generated, "" when it is current.
// hashes are the current hashes of u's files; dir is the knowledge
// directory.
func (m *Manifest) Stale(dir string, u Unit, hashes map[string]string) string {
	st := m.Units[u.ID]
	switch {
	case st == nil:
		return StaleNew
	case st.Status != StatusDone:
		return StaleFailed
	case st.MD == "":
		return StaleMissing
	}
	if _, err := os.Stat(filepath.Join(dir, filepath.FromSlash(st.MD))); err != nil {
		return StaleMissing
	}
	if len(st.Files) != len(hashes) {
		return StaleChanged
	}
	for f, h := range hashes {
		if st.Files[f] != h {
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
