package phpver

import (
	"fmt"
	"sort"
	"strings"
	"sync"

	"github.com/ape-dev-de/tiegel/internal/syntax"
)

// Range is the source range of a diagnostic.
type Range struct {
	StartByte int
	EndByte   int
	Start     syntax.Point
	End       syntax.Point
}

// Diagnostic reports a feature that the target PHP version does not
// support, or a deprecated/removed construct.
type Diagnostic struct {
	Range     Range
	FeatureID string
	Message   string
	Severity  Severity
}

// Checker runs the table's queries against PHP documents. It is safe for
// concurrent use.
type Checker struct {
	table *Table
	query *syntax.Query
	// entries[i] describes the table entry whose query text starts at
	// starts[i] in the combined query source.
	starts  []int
	entries []entry
}

type entry struct {
	feature     *Feature
	deprecation *Deprecation
}

// NewChecker compiles all queries of t into one Tree-sitter query, so a
// check is a single pass over the tree.
func NewChecker(t *Table) (*Checker, error) {
	c := &Checker{table: t}
	var src strings.Builder
	add := func(id, q string, e entry) error {
		q = strings.TrimSpace(q)
		if q == "" {
			return nil
		}
		// Compile alone first for a precise error message.
		single, err := syntax.NewQuery(syntax.LangPHP, q)
		if err != nil {
			return fmt.Errorf("phpver: query of %s: %w", id, err)
		}
		hasFeature := false
		for _, n := range single.CaptureNames() {
			hasFeature = hasFeature || n == "feature"
		}
		single.Close()
		if !hasFeature {
			return fmt.Errorf("phpver: query of %s has no @feature capture", id)
		}
		c.starts = append(c.starts, src.Len())
		c.entries = append(c.entries, e)
		src.WriteString(q)
		src.WriteString("\n")
		return nil
	}
	for i := range t.Features {
		f := &t.Features[i]
		if err := add(f.ID, f.Query, entry{feature: f}); err != nil {
			return nil, err
		}
	}
	for i := range t.Deprecations {
		d := &t.Deprecations[i]
		if err := add(d.ID, d.Query, entry{deprecation: d}); err != nil {
			return nil, err
		}
	}
	if src.Len() == 0 {
		return c, nil
	}
	q, err := syntax.NewQuery(syntax.LangPHP, src.String())
	if err != nil {
		return nil, fmt.Errorf("phpver: combined query: %w", err)
	}
	c.query = q
	return c, nil
}

// Table returns the checker's table.
func (c *Checker) Table() *Table { return c.table }

// Close releases the compiled query.
func (c *Checker) Close() {
	if c.query != nil {
		c.query.Close()
	}
}

// entryFor maps a pattern index of the combined query to its table entry.
func (c *Checker) entryFor(pattern int) entry {
	off := c.query.PatternStart(pattern)
	i := sort.Search(len(c.starts), func(i int) bool { return c.starts[i] > off }) - 1
	return c.entries[i]
}

// Check reports constructs in doc that version v does not support, and
// deprecated or removed constructs. Non-PHP documents yield no diagnostics.
// The check runs on a snapshot, so the document stays usable meanwhile.
func (c *Checker) Check(doc *syntax.Document, v Version) ([]Diagnostic, error) {
	if doc.Language() != syntax.LangPHP || c.query == nil {
		return nil, nil
	}
	snap, err := doc.Snapshot()
	if err != nil {
		return nil, fmt.Errorf("phpver: check: %w", err)
	}
	defer snap.Close()
	matches, err := snap.Matches(c.query)
	if err != nil {
		return nil, fmt.Errorf("phpver: check: %w", err)
	}
	type key struct {
		start, end int
		id         string
	}
	seen := map[key]bool{}
	var out []Diagnostic
	for _, m := range matches {
		e := c.entryFor(m.Pattern)
		for _, cap := range m.Captures {
			if cap.Name != "feature" {
				continue
			}
			d, ok := diagnose(e, v)
			if !ok {
				continue
			}
			k := key{cap.StartByte, cap.EndByte, d.FeatureID}
			if seen[k] {
				continue
			}
			seen[k] = true
			d.Range = Range{StartByte: cap.StartByte, EndByte: cap.EndByte, Start: cap.Start, End: cap.End}
			out = append(out, d)
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Range.StartByte != out[j].Range.StartByte {
			return out[i].Range.StartByte < out[j].Range.StartByte
		}
		return out[i].FeatureID < out[j].FeatureID
	})
	return out, nil
}

// diagnose returns the diagnostic (without range) for entry e under v.
func diagnose(e entry, v Version) (Diagnostic, bool) {
	if f := e.feature; f != nil {
		if !v.Less(f.min) {
			return Diagnostic{}, false
		}
		msg := fmt.Sprintf("%s requires PHP %s (project targets %s)", f.Description, f.min.Short(), v.Short())
		if f.Note != "" {
			msg += ". " + f.Note
		}
		return Diagnostic{FeatureID: f.ID, Message: msg, Severity: f.Severity}, true
	}
	d := e.deprecation
	if r, removed := d.RemovedVersion(); removed && !v.Less(r) {
		return Diagnostic{
			FeatureID: d.ID,
			Message:   fmt.Sprintf("%s was removed in PHP %s (deprecated since %s)", d.Description, r.Short(), d.deprecated.Short()),
			Severity:  SeverityError,
		}, true
	}
	if v.Less(d.deprecated) {
		return Diagnostic{}, false
	}
	msg := fmt.Sprintf("%s is deprecated since PHP %s", d.Description, d.deprecated.Short())
	if d.Note != "" {
		msg += ". " + d.Note
	}
	return Diagnostic{FeatureID: d.ID, Message: msg, Severity: d.Severity}, true
}

var defaultChecker = sync.OnceValues(func() (*Checker, error) {
	t, err := DefaultTable()
	if err != nil {
		return nil, err
	}
	return NewChecker(t)
})

// Check runs the embedded default table against doc for version v.
func Check(doc *syntax.Document, v Version) ([]Diagnostic, error) {
	c, err := defaultChecker()
	if err != nil {
		return nil, err
	}
	return c.Check(doc, v)
}
