package orchestrate

import (
	"bufio"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/ape-dev-de/tiegel/internal/api"
)

// State is everything persisted about a plan (plans/<id>/plan.json): the
// document and the orchestration state, so that a plan can be resumed
// after a cancel or a restart. Agent sessions are not resumed – the
// document is the memory (fresh sessions get the sections they need).
type State struct {
	Doc       api.PlanDocument   `json:"doc"`
	Iteration int                `json:"iteration"`
	Round     int                `json:"round"`
	Roles     []api.PlanRole     `json:"roles,omitempty"`
	Metrics   api.PlanMetrics    `json:"metrics"`
	Workspace *api.PlanWorkspace `json:"workspace,omitempty"`
	Notes     []string           `json:"notes,omitempty"`
	Error     string             `json:"error,omitempty"`
	Started   time.Time          `json:"started,omitempty"`
	// Subagents and Mediator are the settings the plan runs with.
	Subagents bool   `json:"subagents"`
	Mediator  string `json:"mediator"`
	// Failures are the compact test failures of the last run (fed back
	// to DEV).
	Failures []string `json:"failures,omitempty"`
}

// Clone returns a deep enough copy for publishing.
func (s *State) Clone() State {
	b, _ := json.Marshal(s)
	var c State
	_ = json.Unmarshal(b, &c)
	return c
}

// Note adds a remark once.
func (s *State) Note(n string) {
	for _, x := range s.Notes {
		if x == n {
			return
		}
	}
	s.Notes = append(s.Notes, n)
}

// Store keeps the plans of one project in a directory outside the
// repository (<config>/projects/<hash>/plans).
type Store struct {
	Dir string
}

// NewID returns a new plan ID (date and random suffix).
func NewID(now time.Time) string {
	var b [3]byte
	_, _ = rand.Read(b[:])
	return now.Format("20060102-1504") + "-" + hex.EncodeToString(b[:])
}

func validID(id string) bool {
	return id != "" && !strings.ContainsAny(id, `/\.`) && len(id) < 64
}

// DocPath is the Markdown file of plan id.
func (s *Store) DocPath(id string) string { return filepath.Join(s.Dir, id+".md") }

// PlanDir is the directory with the state, history and transcripts.
func (s *Store) PlanDir(id string) string { return filepath.Join(s.Dir, id) }

func (s *Store) statePath(id string) string { return filepath.Join(s.PlanDir(id), "plan.json") }

// Save writes the state and the Markdown document; when the Markdown
// changed, the revision counter is increased and the new revision is
// kept in history/NNN.md.
func (s *Store) Save(st *State) error {
	id := st.Doc.ID
	if !validID(id) {
		return fmt.Errorf("orchestrate: invalid plan id %q", id)
	}
	if err := os.MkdirAll(filepath.Join(s.PlanDir(id), "history"), 0o755); err != nil {
		return fmt.Errorf("orchestrate: save: %w", err)
	}
	old, _ := os.ReadFile(s.DocPath(id))
	md := Render(st.Doc)
	if string(old) != md && !sameExceptMeta(string(old), md) {
		st.Doc.Revision++
		md = Render(st.Doc)
		hist := filepath.Join(s.PlanDir(id), "history", fmt.Sprintf("%03d.md", st.Doc.Revision))
		if err := writeAtomic(hist, []byte(md)); err != nil {
			return err
		}
	}
	if string(old) != md {
		if err := writeAtomic(s.DocPath(id), []byte(md)); err != nil {
			return err
		}
	}
	b, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return fmt.Errorf("orchestrate: save: %w", err)
	}
	return writeAtomic(s.statePath(id), b)
}

// sameExceptMeta compares two renderings ignoring the front matter
// (updated and revision change on every save).
func sameExceptMeta(a, b string) bool {
	return stripMeta(a) == stripMeta(b)
}

func stripMeta(s string) string {
	if !strings.HasPrefix(s, "---\n") {
		return s
	}
	if i := strings.Index(s[4:], "\n---\n"); i >= 0 {
		return s[4+i+5:]
	}
	return s
}

// Load reads the state of plan id.
func (s *Store) Load(id string) (*State, error) {
	if !validID(id) {
		return nil, fmt.Errorf("orchestrate: invalid plan id %q", id)
	}
	b, err := os.ReadFile(s.statePath(id))
	if err != nil {
		return nil, fmt.Errorf("orchestrate: load %s: %w", id, err)
	}
	var st State
	if err := json.Unmarshal(b, &st); err != nil {
		return nil, fmt.Errorf("orchestrate: load %s: %w", id, err)
	}
	return &st, nil
}

// List returns the IDs of all plans, newest first.
func (s *Store) List() []string {
	es, err := os.ReadDir(s.Dir)
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range es {
		if e.IsDir() {
			if _, err := os.Stat(s.statePath(e.Name())); err == nil {
				out = append(out, e.Name())
			}
		}
	}
	sort.Sort(sort.Reverse(sort.StringSlice(out)))
	return out
}

// History returns the revision files of plan id, oldest first.
func (s *Store) History(id string) []string {
	m, _ := filepath.Glob(filepath.Join(s.PlanDir(id), "history", "*.md"))
	sort.Strings(m)
	return m
}

// Remove deletes everything of plan id.
func (s *Store) Remove(id string) error {
	if !validID(id) {
		return fmt.Errorf("orchestrate: invalid plan id %q", id)
	}
	_ = os.Remove(s.DocPath(id))
	return os.RemoveAll(s.PlanDir(id))
}

// SaveTranscript stores the transcript of a role.
func (s *Store) SaveTranscript(id, role string, entries []api.AgentEntry) error {
	dir := filepath.Join(s.PlanDir(id), "transcripts")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("orchestrate: transcript: %w", err)
	}
	b, err := json.Marshal(entries)
	if err != nil {
		return fmt.Errorf("orchestrate: transcript: %w", err)
	}
	return writeAtomic(filepath.Join(dir, safeName(role)+".json"), b)
}

// LoadTranscript reads the stored transcript of a role.
func (s *Store) LoadTranscript(id, role string) ([]api.AgentEntry, error) {
	b, err := os.ReadFile(filepath.Join(s.PlanDir(id), "transcripts", safeName(role)+".json"))
	if err != nil {
		return nil, fmt.Errorf("orchestrate: transcript: %w", err)
	}
	var out []api.AgentEntry
	if err := json.Unmarshal(b, &out); err != nil {
		return nil, fmt.Errorf("orchestrate: transcript: %w", err)
	}
	return out, nil
}

func safeName(s string) string {
	return strings.Map(func(r rune) rune {
		if r == '/' || r == '\\' || r == '.' || r == ':' {
			return '_'
		}
		return r
	}, s)
}

// ---- MCP inbox ----------------------------------------------------------------

// inboxPath is the file MCP tools of role append proposals to.
func (s *Store) inboxPath(id, role string) string {
	return filepath.Join(s.PlanDir(id), "inbox", safeName(role)+".jsonl")
}

// Propose appends a proposal of an MCP tool call (another process may
// write it; Tiegel merges after the role's turn).
func (s *Store) Propose(p api.PlanProposeParams) error {
	if !validID(p.ID) || p.Role == "" {
		return errors.New("orchestrate: propose needs plan id and role")
	}
	path := s.inboxPath(p.ID, p.Role)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("orchestrate: propose: %w", err)
	}
	b, err := json.Marshal(p)
	if err != nil {
		return fmt.Errorf("orchestrate: propose: %w", err)
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("orchestrate: propose: %w", err)
	}
	defer f.Close()
	if _, err := f.Write(append(b, '\n')); err != nil {
		return fmt.Errorf("orchestrate: propose: %w", err)
	}
	return nil
}

// TakeInbox returns and removes the proposals of role.
func (s *Store) TakeInbox(id, role string) []api.PlanProposeParams {
	path := s.inboxPath(id, role)
	tmp := path + ".taking"
	if err := os.Rename(path, tmp); err != nil {
		return nil
	}
	defer os.Remove(tmp)
	f, err := os.Open(tmp)
	if err != nil {
		return nil
	}
	defer f.Close()
	var out []api.PlanProposeParams
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64<<10), 8<<20)
	for sc.Scan() {
		var p api.PlanProposeParams
		if json.Unmarshal(sc.Bytes(), &p) == nil {
			out = append(out, p)
		}
	}
	return out
}

func writeAtomic(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("orchestrate: write %s: %w", path, err)
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".tmp-*")
	if err != nil {
		return fmt.Errorf("orchestrate: write %s: %w", path, err)
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmp.Name())
		return fmt.Errorf("orchestrate: write %s: %w", path, err)
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmp.Name())
		return fmt.Errorf("orchestrate: write %s: %w", path, err)
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		os.Remove(tmp.Name())
		return fmt.Errorf("orchestrate: write %s: %w", path, err)
	}
	return nil
}
