package phpunit

import (
	"context"
	"path/filepath"
	"sort"
	"strings"

	"github.com/ape-dev-de/tiegel/internal/api"
	"github.com/ape-dev-de/tiegel/internal/core/psr4"
	"github.com/ape-dev-de/tiegel/internal/index"
)

// psr4Entry is one prefix of composer.json's autoload(-dev).psr-4.
type psr4Entry struct {
	Prefix string   // with trailing backslash ("" = fallback)
	Dirs   []string // absolute
	Dev    bool
}

// readPSR4 reads the PSR-4 maps of composer.json (the kernel's helper
// psr4.Read), one entry per prefix with all its directories, longest
// prefix first.
func readPSR4(root string) []psr4Entry {
	maps, err := psr4.Read(root)
	if err != nil {
		return nil
	}
	var out []psr4Entry
	idx := map[psr4.Mapping]int{} // prefix + dev (Dir cleared) → entry
	for _, m := range maps {
		key := psr4.Mapping{Prefix: m.Prefix, Dev: m.Dev}
		i, ok := idx[key]
		if !ok {
			e := psr4Entry{Prefix: m.Prefix, Dev: m.Dev}
			if e.Prefix != "" {
				e.Prefix += `\`
			}
			i = len(out)
			idx[key] = i
			out = append(out, e)
		}
		out[i].Dirs = append(out[i].Dirs, filepath.Join(root, filepath.FromSlash(m.Dir)))
	}
	sort.SliceStable(out, func(i, j int) bool {
		if len(out[i].Prefix) != len(out[j].Prefix) {
			return len(out[i].Prefix) > len(out[j].Prefix)
		}
		return out[i].Prefix < out[j].Prefix
	})
	return out
}

// match returns the entries whose prefix starts fqn (dev or not),
// longest first.
func matchPSR4(entries []psr4Entry, fqn string, dev bool) []psr4Entry {
	var out []psr4Entry
	for _, e := range entries {
		if e.Dev == dev && strings.HasPrefix(strings.ToLower(fqn), strings.ToLower(e.Prefix)) {
			out = append(out, e)
		}
	}
	return out
}

// files returns the candidate files of fqn under e.
func (e psr4Entry) files(fqn string) []string {
	relName := strings.ReplaceAll(fqn[len(e.Prefix):], `\`, string(filepath.Separator)) + ".php"
	out := make([]string, 0, len(e.Dirs))
	for _, d := range e.Dirs {
		out = append(out, filepath.Join(d, relName))
	}
	return out
}

// testSubNamespaces are the usual test kinds below the test namespace.
var testSubNamespaces = []string{"", `Unit\`, `Feature\`, `Integration\`, `Functional\`}

// declaredClass returns the main class-like declaration of path (the
// one named like the file, else the first).
func (m *Module) declaredClass(root, path string) (index.Class, bool, error) {
	if !index.TreeSitter {
		return index.Class{}, false, api.Errorf(api.CodeUnavailable, "needs Tree-sitter (this build has no cgo)")
	}
	src, err := m.source(path)
	if err != nil {
		return index.Class{}, false, api.Errorf(api.CodeNotFound, "%s: %v", path, err)
	}
	ps := newParser(root)
	defer ps.close()
	pf, err := ps.parse(path, src)
	if err != nil {
		return index.Class{}, false, api.Errorf(api.CodeFailed, "%w", err)
	}
	if len(pf.f.Classes) == 0 {
		return index.Class{}, false, nil
	}
	base := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	for _, c := range pf.f.Classes {
		if strings.EqualFold(c.Name, base) {
			return c, strings.Contains(string(src), "strict_types=1"), nil
		}
	}
	return pf.f.Classes[0], strings.Contains(string(src), "strict_types=1"), nil
}

func (m *Module) gotoTest(_ context.Context, p api.TestGotoParams) (api.TestGotoResult, error) {
	root, err := m.root()
	if err != nil {
		return api.TestGotoResult{}, err
	}
	if p.Path == "" {
		return api.TestGotoResult{}, api.Errorf(api.CodeInvalidParams, "path is required")
	}
	path := abs(root, p.Path)
	c, strict, err := m.declaredClass(root, path)
	if err != nil {
		return api.TestGotoResult{}, err
	}
	if c.FQN == "" {
		return api.TestGotoResult{}, api.Errorf(api.CodeNotFound, "%s declares no class", p.Path)
	}
	base := m.phpRoot(root) // PSR-4 of the PHP project root (D515)
	entries := readPSR4(base)
	if strings.HasSuffix(c.Name, "Test") && c.Kind == index.KindClass {
		return subjectsOf(base, c.FQN, entries), nil
	}
	res := testsOf(base, c.FQN, entries)
	plan := m.proposal(base, c.FQN, entries, strict)
	res.Create = &plan
	return res, nil
}

// devEntries returns the autoload-dev entries, or the convention
// Tests\ → tests/ when composer.json declares none.
func devEntries(root string, entries []psr4Entry) []psr4Entry {
	var out []psr4Entry
	for _, e := range entries {
		if e.Dev {
			out = append(out, e)
		}
	}
	if len(out) == 0 {
		out = []psr4Entry{{Prefix: `Tests\`, Dirs: []string{filepath.Join(root, "tests")}, Dev: true}}
	}
	return out
}

// relativeName returns fqn without its PSR-4 prefix (production code).
func relativeName(fqn string, entries []psr4Entry) string {
	if ms := matchPSR4(entries, fqn, false); len(ms) > 0 {
		return fqn[len(ms[0].Prefix):]
	}
	return fqn
}

// testsOf lists the test candidates of class fqn.
func testsOf(root, fqn string, entries []psr4Entry) api.TestGotoResult {
	res := api.TestGotoResult{Direction: "test", Class: fqn, Targets: []api.TestTarget{}}
	relName := relativeName(fqn, entries)
	seen := map[string]bool{}
	var missing []api.TestTarget
	for _, d := range devEntries(root, entries) {
		for _, sub := range testSubNamespaces {
			tf := d.Prefix + sub + relName + "Test"
			for _, f := range d.files(tf) {
				if seen[f] {
					continue
				}
				seen[f] = true
				t := api.TestTarget{File: f, Class: tf, Exists: isFile(f)}
				if t.Exists {
					res.Targets = append(res.Targets, t)
				} else {
					missing = append(missing, t)
				}
			}
		}
	}
	// Tests elsewhere that follow the naming convention (<Name>Test.php).
	short := relName
	if i := strings.LastIndexByte(short, '\\'); i >= 0 {
		short = short[i+1:]
	}
	for _, d := range devEntries(root, entries) {
		for _, dir := range d.Dirs {
			for _, f := range phpFiles(context.Background(), []string{dir}) {
				if seen[f] || filepath.Base(f) != short+"Test.php" {
					continue
				}
				seen[f] = true
				res.Targets = append(res.Targets, api.TestTarget{File: f, Class: classOfFile(f, d), Exists: true})
			}
		}
	}
	if len(res.Targets) == 0 {
		res.Targets = append(res.Targets, missing...)
	}
	return res
}

// classOfFile derives the FQN of file under entry e (PSR-4 reverse).
func classOfFile(file string, e psr4Entry) string {
	for _, d := range e.Dirs {
		if within(d, file) {
			r, _ := filepath.Rel(d, strings.TrimSuffix(file, filepath.Ext(file)))
			return e.Prefix + strings.ReplaceAll(filepath.ToSlash(r), "/", `\`)
		}
	}
	return ""
}

// subjectsOf lists the classes under test of test class fqn.
func subjectsOf(root, fqn string, entries []psr4Entry) api.TestGotoResult {
	res := api.TestGotoResult{Direction: "subject", Class: fqn, Targets: []api.TestTarget{}}
	relName := strings.TrimSuffix(fqn, "Test")
	for _, d := range devEntries(root, entries) {
		if strings.HasPrefix(strings.ToLower(fqn), strings.ToLower(d.Prefix)) {
			relName = strings.TrimSuffix(fqn[len(d.Prefix):], "Test")
			break
		}
	}
	names := []string{relName}
	for _, sub := range testSubNamespaces[1:] {
		if strings.HasPrefix(relName, sub) {
			names = append(names, relName[len(sub):])
		}
	}
	seen := map[string]bool{}
	var missing []api.TestTarget
	for _, e := range entries {
		if e.Dev {
			continue
		}
		for _, n := range names {
			cls := e.Prefix + n
			for _, f := range e.files(cls) {
				if seen[f] {
					continue
				}
				seen[f] = true
				t := api.TestTarget{File: f, Class: cls, Exists: isFile(f)}
				if t.Exists {
					res.Targets = append(res.Targets, t)
				} else {
					missing = append(missing, t)
				}
			}
		}
	}
	if len(res.Targets) == 0 {
		res.Targets = append(res.Targets, missing...)
	}
	return res
}

// proposal returns the file and skeleton of a new test for class fqn:
// the first autoload-dev prefix, below Unit\ when the test directory has
// a Unit folder.
func (m *Module) proposal(root, fqn string, entries []psr4Entry, strict bool) api.TestCreatePlan {
	d := devEntries(root, entries)[0]
	sub := ""
	if isDir(filepath.Join(d.Dirs[0], "Unit")) {
		sub = `Unit\`
	}
	tf := d.Prefix + sub + relativeName(fqn, entries) + "Test"
	file := d.files(tf)[0]
	base := testCaseFQN
	if isFile(filepath.Join(d.Dirs[0], "TestCase.php")) {
		base = d.Prefix + "TestCase" // e.g. Laravel's Tests\TestCase
	}
	return api.TestCreatePlan{File: file, Class: tf, Content: Skeleton(tf, fqn, base, strict), Exists: isFile(file)}
}

// Skeleton renders a test class testFQN for subject (FQN) extending base.
func Skeleton(testFQN, subject, base string, strict bool) string {
	ns, name := splitFQN(testFQN)
	subNS, subName := splitFQN(subject)
	baseNS, baseName := splitFQN(base)
	var b strings.Builder
	b.WriteString("<?php\n\n")
	if strict {
		b.WriteString("declare(strict_types=1);\n\n")
	}
	if ns != "" {
		b.WriteString("namespace " + ns + ";\n\n")
	}
	var uses []string
	if subNS != ns && subject != "" {
		uses = append(uses, subject)
	}
	if baseNS != ns {
		uses = append(uses, base)
	}
	sort.Strings(uses)
	for _, u := range uses {
		b.WriteString("use " + u + ";\n")
	}
	if len(uses) > 0 {
		b.WriteString("\n")
	}
	b.WriteString("final class " + name + " extends " + baseName + "\n{\n")
	b.WriteString("    public function testSomething(): void\n    {\n")
	b.WriteString("        self::markTestIncomplete('Write tests for ' . " + subName + "::class . '.');\n")
	b.WriteString("    }\n}\n")
	return b.String()
}

func splitFQN(fqn string) (ns, name string) {
	fqn = strings.TrimPrefix(fqn, `\`)
	if i := strings.LastIndexByte(fqn, '\\'); i >= 0 {
		return fqn[:i], fqn[i+1:]
	}
	return "", fqn
}

func (m *Module) createTest(ctx context.Context, p api.TestCreateParams) (api.TestCreatePlan, error) {
	root, err := m.root()
	if err != nil {
		return api.TestCreatePlan{}, err
	}
	if p.Path == "" {
		return api.TestCreatePlan{}, api.Errorf(api.CodeInvalidParams, "path is required")
	}
	c, strict, err := m.declaredClass(root, abs(root, p.Path))
	if err != nil {
		return api.TestCreatePlan{}, err
	}
	if c.FQN == "" {
		return api.TestCreatePlan{}, api.Errorf(api.CodeNotFound, "%s declares no class", p.Path)
	}
	base := m.phpRoot(root)
	entries := readPSR4(base)
	plan := m.proposal(base, c.FQN, entries, strict)
	if p.File != "" {
		plan.File = abs(root, p.File)
		plan.Class = ""
		for _, d := range devEntries(base, entries) {
			if cls := classOfFile(plan.File, d); cls != "" {
				plan.Class = cls
				break
			}
		}
		if plan.Class == "" {
			plan.Class = strings.TrimSuffix(filepath.Base(plan.File), filepath.Ext(plan.File))
		}
		base := testCaseFQN
		if d := devEntries(base, entries)[0]; isFile(filepath.Join(d.Dirs[0], "TestCase.php")) {
			base = d.Prefix + "TestCase"
		}
		plan.Content = Skeleton(plan.Class, c.FQN, base, strict)
		plan.Exists = isFile(plan.File)
	}
	if plan.Exists || p.DryRun {
		return plan, nil
	}
	content := plan.Content
	_, err = m.app.ApplyFileOps(ctx, "Create Test", []api.FileOp{
		{Kind: api.FileOpCreate, Path: plan.File},
		{Kind: api.FileOpEdit, Path: plan.File, Changes: func([]byte) ([]api.TextChange, error) {
			return []api.TextChange{{Start: 0, End: 0, Text: content}}, nil
		}},
	})
	if err != nil {
		return api.TestCreatePlan{}, err
	}
	return plan, nil
}
