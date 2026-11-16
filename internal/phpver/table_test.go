package phpver

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ape-dev-de/tiegel/internal/syntax"
)

func parsePHP(t *testing.T, src string) *syntax.Document {
	t.Helper()
	d, err := syntax.NewDocument(context.Background(), syntax.LangPHP, []byte(src))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(d.Close)
	return d
}

// matchCount runs a single entry's query and returns the number of @feature
// captures; it also fails on parse errors in the snippet.
func matchCount(t *testing.T, query, src string) int {
	t.Helper()
	d := parsePHP(t, src)
	if d.HasError() {
		t.Fatalf("snippet has syntax errors: %s\n%s", src, d.SExpr())
	}
	q, err := syntax.NewQuery(syntax.LangPHP, query)
	if err != nil {
		t.Fatal(err)
	}
	defer q.Close()
	ms, err := d.Matches(q)
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, m := range ms {
		for _, c := range m.Captures {
			if c.Name == "feature" {
				n++
			}
		}
	}
	return n
}

func TestDefaultTableValid(t *testing.T) {
	tab, err := DefaultTable()
	if err != nil {
		t.Fatal(err)
	}
	if len(tab.Features) < 30 || len(tab.Deprecations) < 10 {
		t.Fatalf("table too small: %d features, %d deprecations", len(tab.Features), len(tab.Deprecations))
	}
	want := []string{
		"typed_properties", "arrow_functions", "null_coalescing_assignment", "spread_in_arrays",
		"named_arguments", "match_expression", "nullsafe_operator", "union_types", "constructor_promotion",
		"attributes", "static_return_type", "mixed_type", "throw_expression",
		"enums", "readonly_properties", "first_class_callable_syntax", "never_type", "new_in_initializers", "pure_intersection_types",
		"readonly_classes", "dnf_types", "standalone_true_false_null", "constants_in_traits",
		"typed_class_constants", "dynamic_class_constant_fetch", "override_attribute",
		"property_hooks", "asymmetric_visibility", "new_without_parentheses", "deprecated_attribute",
		"pipe_operator",
	}
	have := map[string]bool{}
	for _, f := range tab.Features {
		have[f.ID] = true
	}
	for _, id := range want {
		if !have[id] {
			t.Errorf("missing feature %s", id)
		}
	}
}

func TestFeatureExamples(t *testing.T) {
	tab, err := DefaultTable()
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range tab.Features {
		t.Run(f.ID, func(t *testing.T) {
			if f.Query == "" {
				t.Skip("no query")
			}
			if f.Examples.Match == "" || f.Examples.NoMatch == "" {
				t.Fatal("feature needs match and nomatch examples")
			}
			if n := matchCount(t, f.Query, f.Examples.Match); n == 0 {
				t.Errorf("query does not match positive example %q", f.Examples.Match)
			}
			if n := matchCount(t, f.Query, f.Examples.NoMatch); n != 0 {
				t.Errorf("query matches negative example %q (%d times)", f.Examples.NoMatch, n)
			}
		})
	}
	for _, d := range tab.Deprecations {
		t.Run(d.ID, func(t *testing.T) {
			if d.Examples.Match == "" || d.Examples.NoMatch == "" {
				t.Fatal("deprecation needs match and nomatch examples")
			}
			if n := matchCount(t, d.Query, d.Examples.Match); n == 0 {
				t.Errorf("query does not match positive example %q", d.Examples.Match)
			}
			if n := matchCount(t, d.Query, d.Examples.NoMatch); n != 0 {
				t.Errorf("query matches negative example %q (%d times)", d.Examples.NoMatch, n)
			}
		})
	}
}

// TestExamplesLint verifies with a real PHP binary (if present) that every
// example is valid PHP, so the queries are tested against real syntax.
func TestExamplesLint(t *testing.T) {
	php, err := exec.LookPath("php")
	if err != nil {
		t.Skip("php not installed")
	}
	if testing.Short() {
		t.Skip("short mode")
	}
	tab, err := DefaultTable()
	if err != nil {
		t.Fatal(err)
	}
	v, _, err := InterpreterVersion(context.Background(), LocalRunner{Binary: php})
	if err != nil {
		t.Skip(err)
	}
	dir := t.TempDir()
	lint := func(t *testing.T, name, src string) {
		p := filepath.Join(dir, name+".php")
		if err := os.WriteFile(p, []byte(src), 0o600); err != nil {
			t.Fatal(err)
		}
		out, err := exec.Command(php, "-n", "-l", p).CombinedOutput()
		if err != nil {
			t.Errorf("php -l %s: %v\n%s\n%s", name, err, src, out)
		}
	}
	for _, f := range tab.Features {
		if v.Less(f.MinVersion()) || f.LintSkip {
			continue
		}
		lint(t, f.ID+"_match", f.Examples.Match)
		lint(t, f.ID+"_nomatch", f.Examples.NoMatch)
	}
	for _, d := range tab.Deprecations {
		if r, ok := d.RemovedVersion(); !(ok && !v.Less(r)) && !d.LintSkip {
			lint(t, d.ID+"_match", d.Examples.Match)
		}
		lint(t, d.ID+"_nomatch", d.Examples.NoMatch)
	}
}

func TestParseTableErrors(t *testing.T) {
	cases := map[string]string{
		"yaml":        "features: [",
		"no id":       "features:\n  - min: '8.0'\n",
		"dup id":      "features:\n  - {id: a, min: '8.0'}\n  - {id: a, min: '8.1'}\n",
		"bad min":     "features:\n  - {id: a, min: 'x'}\n",
		"bad dep":     "deprecations:\n  - {id: a, deprecated: 'x'}\n",
		"bad removed": "deprecations:\n  - {id: a, deprecated: '8.0', removed: 'y'}\n",
		"severity":    "features:\n  - {id: a, min: '8.0', severity: fatal}\n",
	}
	for name, src := range cases {
		if _, err := ParseTable([]byte(src)); err == nil {
			t.Errorf("%s: expected error", name)
		}
	}
	tab, err := ParseTable([]byte("deprecations:\n  - {id: a, deprecated: '8.0', severity: info}\n  - {id: b, deprecated: '8.0'}\n"))
	if err != nil {
		t.Fatal(err)
	}
	if tab.Deprecations[0].Severity != SeverityInfo || tab.Deprecations[1].Severity != SeverityWarning {
		t.Fatalf("severities: %v %v", tab.Deprecations[0].Severity, tab.Deprecations[1].Severity)
	}
	if _, ok := tab.Deprecations[0].RemovedVersion(); ok {
		t.Fatal("not removed")
	}
	if tab.Deprecations[0].DeprecatedVersion() != (Version{8, 0, 0}) {
		t.Fatal("deprecated version")
	}
	for _, s := range []Severity{SeverityError, SeverityWarning, SeverityInfo} {
		if strings.TrimSpace(s.String()) == "" {
			t.Fatal("empty severity name")
		}
	}
}
