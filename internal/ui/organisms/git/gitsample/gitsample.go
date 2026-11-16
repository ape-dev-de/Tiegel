// Package gitsample provides realistic example data for the Git UI
// organisms: a status with diffs of a small PHP project, a commit log,
// branches and a three-way merge. Everything is produced by the gitcli
// parsers from embedded fixture files; no repository is touched. It is used
// by the component catalog and by tests.
package gitsample

import (
	"bytes"
	"embed"
	"fmt"
	"strings"
	"time"

	"github.com/ape-dev-de/tiegel/internal/gitcli"
)

//go:embed fixtures
var fixtures embed.FS

func read(name string) []byte {
	b, err := fixtures.ReadFile("fixtures/" + name)
	if err != nil {
		// The files are embedded; a missing one is a build error in tests.
		return nil
	}
	return b
}

// Status parses the embedded `git status --porcelain=v2 -z` fixture.
func Status() (*gitcli.Status, error) {
	raw := bytes.ReplaceAll(bytes.TrimRight(read("status.txt"), "\n"), []byte("\n"), []byte{0})
	return gitcli.ParseStatus(append(raw, 0))
}

// Diffs parses the embedded diffs of tracked files (git diff HEAD).
func Diffs() ([]gitcli.FileDiff, error) { return gitcli.ParseDiff(read("changes.diff")) }

// UntrackedDiff returns the diff of the untracked file
// src/Domain/InvoiceNumber.php (as DiffUntracked would produce it).
func UntrackedDiff() (*gitcli.FileDiff, error) {
	d, err := gitcli.ParseDiff(read("untracked.diff"))
	if err != nil {
		return nil, err
	}
	if len(d) != 1 {
		return nil, fmt.Errorf("gitsample: want 1 untracked diff, got %d", len(d))
	}
	return &d[0], nil
}

// Change pairs a status entry with its diff (nil when not loaded).
type Change struct {
	Status gitcli.FileStatus
	Diff   *gitcli.FileDiff
}

// Changes returns the status entries with their diffs. Untracked files
// come without diff, so the commit panel requests it.
func Changes() ([]Change, error) {
	st, err := Status()
	if err != nil {
		return nil, err
	}
	diffs, err := Diffs()
	if err != nil {
		return nil, err
	}
	byPath := make(map[string]*gitcli.FileDiff, len(diffs))
	for i := range diffs {
		byPath[diffs[i].Path()] = &diffs[i]
	}
	out := make([]Change, 0, len(st.Files))
	for _, f := range st.Files {
		out = append(out, Change{Status: f, Diff: byPath[f.Path]})
	}
	return out, nil
}

// MergeVersions returns base, ours and theirs of the conflicting file.
func MergeVersions() (base, ours, theirs []byte) {
	return read("base.php"), read("ours.php"), read("theirs.php")
}

// Merge runs gitcli.Merge3 on the embedded versions of
// src/Billing/PriceCalculator.php.
func Merge() *gitcli.MergeResult {
	b, o, t := MergeVersions()
	return gitcli.Merge3(b, o, t)
}

// MergeLabels names the sides of Merge.
var MergeLabels = gitcli.MarkerLabels{Ours: "feature/invoices", Base: "merge base", Theirs: "main"}

var authors = []struct{ name, mail string }{
	{"Ada Weber", "ada@example.org"}, {"Jonas Klein", "jonas@example.org"},
	{"Mira Hoffmann", "mira@example.org"}, {"Lukas Brandt", "lukas@example.org"},
}

var subjects = []string{
	"Add invoice aggregate and numbering",
	"Send welcome mail after registration",
	"Use readonly promoted properties in controllers",
	"Fix rounding of gross prices",
	"Move Str helper to Support namespace",
	"Bump symfony/mailer to 7.2",
	"Remove legacy mailer test",
	"Validate discount percentage",
	"Document invoice workflow",
	"Merge branch 'main' into feature/invoices",
	"Configure autowiring bindings",
	"Add JSON response for user show",
}

// Commits returns n synthetic commits starting at offset (newest first),
// as a paging host would deliver them. Refs decorate the first commits.
func Commits(offset, n int) []gitcli.Commit {
	base := time.Date(2026, 11, 14, 17, 30, 0, 0, time.UTC)
	out := make([]gitcli.Commit, 0, n)
	for i := offset; i < offset+n; i++ {
		a := authors[i%len(authors)]
		h := fakeHash(i + 1)
		c := gitcli.Commit{
			Hash: h, AuthorName: a.name, AuthorEmail: a.mail,
			AuthorDate: base.Add(-time.Duration(i) * 97 * time.Minute),
			Subject:    subjects[i%len(subjects)],
			Parents:    []string{fakeHash(i + 2)},
		}
		c.CommitterName, c.CommitterEmail, c.CommitDate = c.AuthorName, c.AuthorEmail, c.AuthorDate
		if strings.HasPrefix(c.Subject, "Merge") {
			c.Parents = append(c.Parents, fakeHash(i+100_000))
		}
		switch i {
		case 0:
			c.Refs = []string{"HEAD -> feature/invoices"}
		case 2:
			c.Refs = []string{"origin/feature/invoices"}
		case 5:
			c.Refs = []string{"tag: v1.4.0", "origin/main", "main"}
		}
		out = append(out, c)
	}
	return out
}

// fakeHash returns a stable 40 hex digit pseudo object name for n.
func fakeHash(n int) string {
	a := uint64(n) * 0x9e3779b97f4a7c15
	return fmt.Sprintf("%016x%016x%08x", a, a^0xc2b2ae3d27d4eb4f, uint32(a>>13))
}

// Branches returns local and remote branches.
func Branches() []gitcli.Branch {
	d := time.Date(2026, 11, 14, 17, 30, 0, 0, time.UTC)
	return []gitcli.Branch{
		{Name: "feature/invoices", Ref: "refs/heads/feature/invoices", Current: true, Upstream: "origin/feature/invoices", Ahead: 2, Subject: subjects[0], CommitDate: d},
		{Name: "main", Ref: "refs/heads/main", Upstream: "origin/main", Behind: 3, Subject: subjects[5], CommitDate: d.Add(-48 * time.Hour)},
		{Name: "bugfix/rounding", Ref: "refs/heads/bugfix/rounding", Upstream: "origin/bugfix/rounding", UpstreamGone: true, Subject: subjects[3], CommitDate: d.Add(-96 * time.Hour)},
		{Name: "spike/php85-pipes", Ref: "refs/heads/spike/php85-pipes", Subject: "Try the pipe operator", CommitDate: d.Add(-200 * time.Hour)},
		{Name: "origin/feature/invoices", Ref: "refs/remotes/origin/feature/invoices", Remote: true, Subject: subjects[2]},
		{Name: "origin/main", Ref: "refs/remotes/origin/main", Remote: true, Subject: subjects[5]},
		{Name: "origin/release/1.4", Ref: "refs/remotes/origin/release/1.4", Remote: true, Subject: "Release 1.4.0"},
	}
}

// LargeDiff returns a parsed diff of a generated PHP file with roughly n
// lines where every seventh line is changed, for virtualization tests and
// benchmarks.
func LargeDiff(n int) (*gitcli.FileDiff, error) {
	var b strings.Builder
	b.WriteString("diff --git a/src/Generated/Big.php b/src/Generated/Big.php\n")
	b.WriteString("index 1111111..2222222 100644\n--- a/src/Generated/Big.php\n+++ b/src/Generated/Big.php\n")
	var body strings.Builder
	oldN, newN := 0, 0
	for i := 0; i < n; i++ {
		if i%7 == 3 {
			fmt.Fprintf(&body, "-    $value%d = compute(%d, $input);\n", i, i)
			fmt.Fprintf(&body, "+    $value%d = compute(%d, $input, strict: true);\n", i, i)
			oldN++
			newN++
			continue
		}
		fmt.Fprintf(&body, "     // line %d of the generated file\n", i)
		oldN++
		newN++
	}
	fmt.Fprintf(&b, "@@ -1,%d +1,%d @@ final class Big\n", oldN, newN)
	b.WriteString(body.String())
	d, err := gitcli.ParseDiff([]byte(b.String()))
	if err != nil {
		return nil, err
	}
	return &d[0], nil
}
