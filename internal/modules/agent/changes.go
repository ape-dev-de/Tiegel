package agent

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/ape-dev-de/tiegel/internal/api"
	"github.com/ape-dev-de/tiegel/internal/gitcli"
)

// Change tracking (D550): for every file the agent touches, the content
// before its first change (snapshot taken while the agent waits for the
// edit permission, or the diff's old text) and after its last change
// (read when the tool call completed). Revert writes the first back –
// unless the file changed since the agent's last change.

// tracked is one file changed by the agent.
type tracked struct {
	abs string
	// orig is the content before the agent's first change; existed tells
	// whether the file existed then.
	orig    []byte
	existed bool
	// last is the content after the agent's last change (observed),
	// lastExists whether the file existed then; seen is false until the
	// first change completed.
	last       []byte
	lastExists bool
	seen       bool
}

// tracker holds the changed files in first-change order. It is not
// synchronised; the module's mutex guards it.
type tracker struct {
	files map[string]*tracked
	order []string
}

func newTracker() *tracker { return &tracker{files: map[string]*tracked{}} }

// before records the original content of abs unless it is tracked.
func (t *tracker) before(abs string, content []byte, exists bool) {
	if _, ok := t.files[abs]; ok {
		return
	}
	t.files[abs] = &tracked{abs: abs, orig: bytes.Clone(content), existed: exists}
	t.order = append(t.order, abs)
}

// tracking reports whether abs is tracked.
func (t *tracker) tracking(abs string) bool { return t.files[abs] != nil }

// after records the content of abs after an agent change. A file back at
// its original state is dropped.
func (t *tracker) after(abs string, content []byte, exists bool) {
	f := t.files[abs]
	if f == nil {
		return
	}
	f.last, f.lastExists, f.seen = bytes.Clone(content), exists, true
	if exists == f.existed && bytes.Equal(content, f.orig) {
		t.drop(abs)
	}
}

// drop forgets abs.
func (t *tracker) drop(abs string) {
	delete(t.files, abs)
	for i, p := range t.order {
		if p == abs {
			t.order = append(t.order[:i], t.order[i+1:]...)
			break
		}
	}
}

// clear forgets everything (changes accepted).
func (t *tracker) clear() {
	t.files = map[string]*tracked{}
	t.order = nil
}

// changed returns the tracked files whose change completed, in order.
func (t *tracker) changed() []*tracked {
	var out []*tracked
	for _, p := range t.order {
		if f := t.files[p]; f.seen {
			out = append(out, f)
		}
	}
	return out
}

// kind describes the change of f.
func (f *tracked) kind() string {
	switch {
	case !f.existed:
		return api.ChangeCreated
	case !f.lastExists:
		return api.ChangeDeleted
	}
	return api.ChangeModified
}

// conflict reports whether the current state differs from the agent's
// last change.
func (f *tracked) conflict(cur []byte, exists bool) bool {
	return exists != f.lastExists || !bytes.Equal(cur, f.last)
}

// diffStat counts added and removed lines between a and b.
func diffStat(a, b []byte) (added, removed int) {
	for _, c := range gitcli.DiffLines(gitcli.SplitLines(a), gitcli.SplitLines(b)) {
		added += c.NewEnd - c.NewStart
		removed += c.OldEnd - c.OldStart
	}
	return added, removed
}

// unified returns a unified diff of a and b with 3 context lines.
func unified(path string, a, b []byte, existed, exists bool) string {
	oldL, newL := gitcli.SplitLines(a), gitcli.SplitLines(b)
	changes := gitcli.DiffLines(oldL, newL)
	if len(changes) == 0 {
		return ""
	}
	var w strings.Builder
	from, to := "a/"+path, "b/"+path
	if !existed {
		from = "/dev/null"
	}
	if !exists {
		to = "/dev/null"
	}
	fmt.Fprintf(&w, "--- %s\n+++ %s\n", from, to)
	const ctx = 3
	line := func(prefix, s string) {
		w.WriteString(prefix + strings.TrimSuffix(s, "\n") + "\n")
		if !strings.HasSuffix(s, "\n") {
			w.WriteString("\\ No newline at end of file\n")
		}
	}
	for i := 0; i < len(changes); {
		// Group changes whose context overlaps into one hunk.
		j := i
		for j+1 < len(changes) && changes[j+1].OldStart-changes[j].OldEnd <= 2*ctx {
			j++
		}
		os := max(0, changes[i].OldStart-ctx)
		oe := min(len(oldL), changes[j].OldEnd+ctx)
		ns := changes[i].NewStart - (changes[i].OldStart - os)
		ne := changes[j].NewEnd + (oe - changes[j].OldEnd)
		fmt.Fprintf(&w, "@@ -%s +%s @@\n", hunkRange(os, oe-os), hunkRange(ns, ne-ns))
		pos := os
		for k := i; k <= j; k++ {
			c := changes[k]
			for ; pos < c.OldStart; pos++ {
				line(" ", oldL[pos])
			}
			for p := c.OldStart; p < c.OldEnd; p++ {
				line("-", oldL[p])
			}
			for p := c.NewStart; p < c.NewEnd; p++ {
				line("+", newL[p])
			}
			pos = c.OldEnd
		}
		for ; pos < oe; pos++ {
			line(" ", oldL[pos])
		}
		i = j + 1
	}
	return w.String()
}

func hunkRange(start, n int) string {
	if n == 0 {
		return fmt.Sprintf("%d,0", start)
	}
	if n == 1 {
		return fmt.Sprintf("%d", start+1)
	}
	return fmt.Sprintf("%d,%d", start+1, n)
}

// storedFile is the persisted form of tracked.
type storedFile struct {
	Path       string `json:"path"`
	Orig       []byte `json:"orig,omitempty"`
	Existed    bool   `json:"existed"`
	Last       []byte `json:"last,omitempty"`
	LastExists bool   `json:"lastExists"`
}

// save writes the completed changes to path (removed when empty), so
// they survive a restart of the IDE and separate headless runs.
func (t *tracker) save(path string) error {
	var out []storedFile
	for _, f := range t.changed() {
		out = append(out, storedFile{Path: f.abs, Orig: f.orig, Existed: f.existed, Last: f.last, LastExists: f.lastExists})
	}
	if len(out) == 0 {
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("agent changes: %w", err)
		}
		return nil
	}
	b, err := json.Marshal(out)
	if err != nil {
		return fmt.Errorf("agent changes: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("agent changes: %w", err)
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return fmt.Errorf("agent changes: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		return fmt.Errorf("agent changes: %w", err)
	}
	return nil
}

// loadTracker reads the changes saved at path (empty when missing or
// unreadable).
func loadTracker(path string) *tracker {
	t := newTracker()
	b, err := os.ReadFile(path)
	if err != nil {
		return t
	}
	var in []storedFile
	if json.Unmarshal(b, &in) != nil {
		return t
	}
	for _, f := range in {
		t.before(f.Path, f.Orig, f.Existed)
		tf := t.files[f.Path]
		tf.last, tf.lastExists, tf.seen = f.Last, f.LastExists, true
	}
	return t
}
