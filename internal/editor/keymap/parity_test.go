package keymap

import (
	"bufio"
	"os"
	"regexp"
	"slices"
	"strings"
	"testing"
)

type fixtureRow struct {
	line   int
	desc   string
	chords []string
	target string
}

func readFixture(t *testing.T) []fixtureRow {
	t.Helper()
	f, err := os.Open("testdata/reference-mac.tsv")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	var rows []fixtureRow
	sc := bufio.NewScanner(f)
	n := 0
	for sc.Scan() {
		n++
		l := sc.Text()
		if strings.TrimSpace(l) == "" || strings.HasPrefix(l, "#") {
			continue
		}
		cols := strings.Split(l, "\t")
		if len(cols) != 3 {
			t.Fatalf("line %d: want 3 tab separated columns, got %d", n, len(cols))
		}
		var chords []string
		for _, c := range strings.Split(cols[1], " | ") {
			chords = append(chords, strings.TrimSpace(c))
		}
		rows = append(rows, fixtureRow{line: n, desc: cols[0], chords: chords, target: strings.TrimSpace(cols[2])})
	}
	if err := sc.Err(); err != nil {
		t.Fatal(err)
	}
	return rows
}

var laterRe = regexp.MustCompile(`^later:M\d+$`)

// TestReferenceMacParity checks every row of the reference macOS keymap:
// it is bound to its action in the mac preset or explicitly deferred to a
// milestone (AP13.2).
func TestReferenceMacParity(t *testing.T) {
	s := NewScheme(PresetMac, nil)
	rows := readFixture(t)
	if len(rows) < 80 {
		t.Fatalf("fixture too small: %d rows", len(rows))
	}
	bound, later := 0, 0
	for _, r := range rows {
		cs := make([]Chord, 0, len(r.chords))
		for _, str := range r.chords {
			c, err := ParseChord(str)
			if err != nil {
				t.Errorf("line %d (%s): %v", r.line, r.desc, err)
				continue
			}
			cs = append(cs, c)
		}
		if strings.HasPrefix(r.target, "later:") {
			if !laterRe.MatchString(r.target) {
				t.Errorf("line %d (%s): bad marker %q", r.line, r.desc, r.target)
			}
			later++
			continue
		}
		e, ok := s.Entry(Action(r.target))
		if !ok {
			t.Errorf("line %d (%s): unknown action %q", r.line, r.desc, r.target)
			continue
		}
		for _, c := range cs {
			if !slices.Contains(e.Chords, c) {
				t.Errorf("line %d (%s): %s not bound to %s (has %v)", r.line, r.desc, c, r.target, e.Chords)
			}
			if a, ok := s.Lookup(e.Context, c); !ok || a != e.ID {
				t.Errorf("line %d (%s): lookup %s in %s = %q", r.line, r.desc, c, e.Context, a)
			}
		}
		bound++
	}
	t.Logf("reference rows: %d bound, %d later", bound, later)
}

// The PC preset binds every action the mac preset binds.
func TestPCPresetCoversMac(t *testing.T) {
	mac, pc := NewScheme(PresetMac, nil), NewScheme(PresetPC, nil)
	// The reference PC chord of Column Selection Mode (Alt+Shift+Insert)
	// uses the Insert key, which the UI toolkit does not report.
	macOnly := map[Action]bool{ColumnSelectionMode: true}
	for _, e := range mac.Entries() {
		if len(e.Chords) > 0 && !macOnly[e.ID] && len(pc.Chords(e.ID)) == 0 {
			t.Errorf("%s bound on mac (%v) but not on pc", e.ID, e.Chords)
		}
	}
}
