package orchestrate

import (
	"context"
	"encoding/xml"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/ape-dev-de/tiegel/internal/api"
)

// Mechanical gates (D568): checks Tiegel runs itself instead of trusting
// a role's "done". Each returns gate issues (review items) with evidence.

// GateIssue is a problem found by a gate.
type GateIssue struct {
	Gate     string
	To       string // dev or qa
	Target   string
	Severity string
	Claim    string
	Evidence string
	Suggest  string
}

func (g GateIssue) item() api.PlanReviewItem {
	return api.PlanReviewItem{From: "gate:" + g.Gate, To: g.To, Target: g.Target, Severity: g.Severity, Claim: g.Claim,
		Evidence: g.Evidence, Suggestion: g.Suggest, Mechanical: true}
}

// ---- hollow tests ---------------------------------------------------------------

var (
	assertCallRE = regexp.MustCompile(`(?:\$this->|self::|static::|Assert::|\b)(assert[A-Za-z]*|expect[A-Za-z]*|fail|verify|shouldReceive|shouldHaveReceived|expects)\s*\(`)
	helperCallRE = regexp.MustCompile(`(?:\$this->|self::|static::)([A-Za-z_][A-Za-z0-9_]*)\s*\(`)
	skipRE       = regexp.MustCompile(`\bmarkTest(?:Skipped|Incomplete)\s*\(`)
	catchAllRE   = regexp.MustCompile(`catch\s*\(\s*\\?(?:Throwable|Exception)\b[^)]*\)\s*\{`)
	newRE        = regexp.MustCompile(`^\s*new\s`)
	assignNewRE  = regexp.MustCompile(`(\$[A-Za-z_][A-Za-z0-9_]*)\s*=\s*new\s`)
	assignRE     = regexp.MustCompile(`(\$[A-Za-z_][A-Za-z0-9_]*)\s*=[^=>]`)
)

// HollowTests checks the test methods of a PHP test file. path is used
// for the evidence; lines limits the check to tests that overlap these
// lines (nil = all).
func HollowTests(path string, src []byte, lines []int) []GateIssue {
	funcs := ScanPHP(src)
	// Helper methods of the file that assert (assertValid(), check…()).
	asserting := map[string]bool{}
	for _, f := range funcs {
		if !f.IsTest() && assertCallRE.MatchString(f.Code) {
			asserting[strings.ToLower(f.Name)] = true
		}
	}
	var out []GateIssue
	for _, f := range funcs {
		if !f.IsTest() || f.Abstract || !overlaps(f.Line, f.EndLine, lines) {
			continue
		}
		ev := fmt.Sprintf("%s:%d", path, f.Line)
		issue := func(sev, claim, suggest string) {
			out = append(out, GateIssue{Gate: "hollow", To: api.RoleQA, Target: api.TargetTests, Severity: sev,
				Claim: fmt.Sprintf("%s::%s %s", f.Class, f.Name, claim), Evidence: ev, Suggest: suggest})
		}
		code := strings.TrimSpace(f.Code)
		switch {
		case code == "":
			issue(api.SeverityBlocker, "has an empty body", "write the test: arrange, act, assert observable behaviour")
			continue
		case skipRE.MatchString(code):
			issue(api.SeverityBlocker, "is skipped or marked incomplete", "write the test instead of skipping it")
			continue
		}
		if m := catchAllRE.FindStringIndex(f.Code); m != nil {
			open := m[1] - 1
			if end := matching([]byte(f.Code), open); end > open {
				block := f.Code[open+1 : end]
				if !strings.Contains(block, "throw") && !assertCallRE.MatchString(block) {
					issue(api.SeverityBlocker, "catches Throwable/Exception and swallows the failure", "let the exception propagate or use expectException()")
				}
			}
		}
		if sut := strings.TrimSuffix(f.Class, "Test"); sut != "" && sut != f.Class {
			mockRE := regexp.MustCompile(`(?:createMock|createStub|createPartialMock|getMockBuilder|createConfiguredMock|Mockery::mock|mock|prophesize)\s*\(\s*(?:\\?[A-Za-z0-9_]+\\)*` + regexp.QuoteMeta(sut) + `::class`)
			if mockRE.MatchString(f.Code) {
				issue(api.SeverityBlocker, "mocks the class under test ("+sut+")", "test the real "+sut+"; mock only its collaborators")
			}
		}
		calls := callsIn(f, src, assertCallRE)
		helper := false
		for _, c := range callsIn(f, src, helperCallRE) {
			helper = helper || asserting[strings.ToLower(c.Name)]
		}
		if len(calls) == 0 && !helper {
			issue(api.SeverityBlocker, "has no assertion or expectation", "assert the behaviour the test case describes")
			continue
		}
		if !helper && allTrivial(calls, f.Code) {
			issue(api.SeverityBlocker, "asserts only trivialities ("+calls[0].Name+"(…) on constants or fresh objects)", "assert the result of the code under test")
		}
	}
	return out
}

func overlaps(from, to int, lines []int) bool {
	if lines == nil {
		return true
	}
	for _, l := range lines {
		if l >= from && l <= to {
			return true
		}
	}
	return false
}

// allTrivial tells whether every assertion is trivially true: constants
// (assertTrue(true), assertEquals(1, 1)) or checks of fresh objects
// (assertNotNull(new X), $x = new X; assertInstanceOf(X::class, $x)).
func allTrivial(calls []PHPCall, code string) bool {
	// A variable is fresh when every assignment to it is "= new …".
	news, all := map[string]int{}, map[string]int{}
	for _, m := range assignNewRE.FindAllStringSubmatch(code, -1) {
		news[m[1]]++
	}
	for _, m := range assignRE.FindAllStringSubmatch(code, -1) {
		all[m[1]]++
	}
	fresh := map[string]bool{}
	for v, n := range news {
		fresh[v] = n == all[v]
	}
	isFresh := func(a string) bool { return newRE.MatchString(a) || fresh[strings.TrimSpace(a)] }
	for _, c := range calls {
		args := splitArgs(c.Args)
		name := strings.ToLower(c.Name)
		switch {
		case (name == "asserttrue" || name == "assertfalse") && len(args) >= 1:
			a := strings.ToLower(args[0])
			if a != "true" && a != "false" && a != "1" && a != "0" {
				return false
			}
		case (name == "assertnotnull" || name == "assertisobject" || name == "assertnotempty") && len(args) >= 1:
			if !isFresh(args[0]) {
				return false
			}
		case name == "assertinstanceof" && len(args) >= 2:
			if !isFresh(args[1]) {
				return false
			}
		case (name == "assertequals" || name == "assertsame") && len(args) >= 2:
			if strings.TrimSpace(args[0]) != strings.TrimSpace(args[1]) {
				return false
			}
		default:
			return false
		}
	}
	return true
}

// ---- unfinished work ------------------------------------------------------------

var (
	todoRE    = regexp.MustCompile(`(?i)\b(TODO|FIXME|XXX|HACK)\b`)
	notImplRE = regexp.MustCompile(`(?i)not\s+(yet\s+)?implemented`)
	stubRE    = regexp.MustCompile(`^\s*return\s*(null|\[\s*\]|''|""|false|0|array\(\s*\))?\s*;\s*$`)
)

// Unfinished checks DEV's changes of non-test files: TODO/FIXME added,
// "not implemented", empty or stub bodies of new functions.
func Unfinished(changes []FileChange) []GateIssue {
	var out []GateIssue
	for _, f := range changes {
		if f.Test || f.Kind == api.ChangeDeleted {
			continue
		}
		added := f.AddedLines()
		set := map[int]bool{}
		for _, l := range added {
			set[l] = true
		}
		lines := strings.Split(string(f.New), "\n")
		for _, l := range added {
			if l-1 >= len(lines) {
				continue
			}
			t := lines[l-1]
			switch {
			case todoRE.MatchString(t):
				out = append(out, GateIssue{Gate: "unfinished", To: api.RoleDev, Target: api.TargetImpl, Severity: api.SeverityMajor,
					Claim: "added " + strings.ToUpper(todoRE.FindString(t)) + ": " + strings.TrimSpace(t), Evidence: fmt.Sprintf("%s:%d", f.Path, l),
					Suggest: "finish the work instead of leaving a marker"})
			case notImplRE.MatchString(t):
				out = append(out, GateIssue{Gate: "unfinished", To: api.RoleDev, Target: api.TargetImpl, Severity: api.SeverityBlocker,
					Claim: "not implemented: " + strings.TrimSpace(t), Evidence: fmt.Sprintf("%s:%d", f.Path, l), Suggest: "implement it"})
			}
		}
		if !strings.HasSuffix(f.Path, ".php") {
			continue
		}
		for _, fn := range ScanPHP(f.New) {
			if fn.Abstract || !set[fn.Line] {
				continue // only functions declared by the change
			}
			code := strings.TrimSpace(fn.Code)
			ev := fmt.Sprintf("%s:%d", f.Path, fn.Line)
			switch {
			case code == "" && !strings.EqualFold(fn.Name, "__construct"):
				out = append(out, GateIssue{Gate: "unfinished", To: api.RoleDev, Target: api.TargetImpl, Severity: api.SeverityMajor,
					Claim: "new function " + fn.Name + "() has an empty body", Evidence: ev, Suggest: "implement it, or reject with the reason it must stay empty"})
			case stubRE.MatchString(code):
				out = append(out, GateIssue{Gate: "unfinished", To: api.RoleDev, Target: api.TargetImpl, Severity: api.SeverityMajor,
					Claim: "new function " + fn.Name + "() is a stub (" + code + ")", Evidence: ev, Suggest: "implement it, or reject with the reason the constant result is right"})
			}
		}
	}
	return out
}

// StepsDone checks DEV's step reports against the diff: steps not done
// and steps marked done whose files did not change.
func StepsDone(steps []api.PlanStep, changes []FileChange) []GateIssue {
	changed := map[string]bool{}
	for _, c := range changes {
		changed[c.Path] = true
	}
	var out []GateIssue
	for _, s := range steps {
		switch s.Status {
		case "done":
			if len(s.Files) == 0 {
				continue
			}
			any := false
			for _, f := range s.Files {
				any = any || changed[filepath.ToSlash(filepath.Clean(f))]
			}
			if !any {
				out = append(out, GateIssue{Gate: "unfinished", To: api.RoleDev, Target: api.TargetImpl, Severity: api.SeverityMajor,
					Claim:    fmt.Sprintf("step %s is reported done, but none of its files changed (%s)", s.ID, strings.Join(s.Files, ", ")),
					Evidence: s.Files[0], Suggest: "implement the step or explain where it was done"})
			}
		default:
			out = append(out, GateIssue{Gate: "unfinished", To: api.RoleDev, Target: api.TargetImpl, Severity: api.SeverityBlocker,
				Claim: fmt.Sprintf("step %s (%s) is %s", s.ID, s.Title, orDash(s.Status)), Evidence: strings.Join(s.Files, ", "), Suggest: "finish the step"})
		}
	}
	return out
}

// ---- mutation-lite ---------------------------------------------------------------

// Mutant is a small change of a changed file.
type Mutant struct {
	Path string
	Line int
	// What describes the mutation ("flip ==", "revert hunk").
	What string
	Src  []byte
}

var mutOps = []struct{ from, to, what string }{
	{"===", "!==", "=== → !=="}, {"!==", "===", "!== → ==="}, {" == ", " != ", "== → !="}, {" != ", " == ", "!= → =="},
	{" >= ", " < ", ">= → <"}, {" <= ", " > ", "<= → >"}, {" > ", " <= ", "> → <="}, {" < ", " >= ", "< → >="},
	{"&&", "||", "&& → ||"}, {"||", "&&", "|| → &&"}, {"true", "false", "true → false"}, {"false", "true", "false → true"},
}

var ifRE = regexp.MustCompile(`\bif\s*\(`)

// Mutants generates up to max mutants of the changed non-test PHP files:
// first one revert per changed region, then operator flips and negated
// conditions on added lines (in the code view, not inside strings).
func Mutants(changes []FileChange, max int) []Mutant {
	var reverts, ops []Mutant
	for _, f := range changes {
		if f.Test || f.Kind == api.ChangeDeleted || !strings.HasSuffix(f.Path, ".php") {
			continue
		}
		oldL, newL := splitKeep(f.Old), splitKeep(f.New)
		for _, c := range f.Changes {
			if c.NewEnd == c.NewStart || f.Kind == api.ChangeCreated {
				continue
			}
			var b strings.Builder
			b.WriteString(strings.Join(newL[:c.NewStart], ""))
			b.WriteString(strings.Join(oldL[c.OldStart:c.OldEnd], ""))
			b.WriteString(strings.Join(newL[c.NewEnd:], ""))
			reverts = append(reverts, Mutant{Path: f.Path, Line: c.NewStart + 1, What: "revert the change", Src: []byte(b.String())})
		}
		code := splitKeep(CodeView(f.New))
		for _, l := range f.AddedLines() {
			if l-1 >= len(code) || l-1 >= len(newL) {
				continue
			}
			cl := code[l-1]
			if loc := ifRE.FindStringIndex(cl); loc != nil {
				open := loc[1] - 1
				if end := matching([]byte(cl), open); end > open {
					line := newL[l-1]
					m := line[:open+1] + "!(" + line[open+1:end] + ")" + line[end:]
					ops = append(ops, mutate(f, newL, l, m, "negate the condition"))
					continue
				}
			}
			for _, op := range mutOps {
				i := strings.Index(cl, op.from)
				if i < 0 || isWordBoundaryViolation(cl, i, op.from) {
					continue
				}
				line := newL[l-1]
				m := line[:i] + op.to + line[i+len(op.from):]
				ops = append(ops, mutate(f, newL, l, m, op.what))
				break
			}
		}
	}
	// Interleave: reverts first (they show whether the change is tested
	// at all), then operators.
	out := append(reverts, ops...)
	if max > 0 && len(out) > max {
		half := min(len(reverts), max/2)
		out = append(append([]Mutant(nil), reverts[:half]...), ops[:min(len(ops), max-half)]...)
		if len(out) < max {
			out = append(out, reverts[half:min(len(reverts), half+max-len(out))]...)
		}
	}
	return out
}

func isWordBoundaryViolation(line string, i int, tok string) bool {
	if tok != "true" && tok != "false" {
		return false
	}
	before := i > 0 && isWord(line[i-1])
	after := i+len(tok) < len(line) && isWord(line[i+len(tok)])
	return before || after
}

func mutate(f FileChange, lines []string, l int, repl, what string) Mutant {
	cp := slices.Clone(lines)
	cp[l-1] = repl
	return Mutant{Path: f.Path, Line: l, What: what, Src: []byte(strings.Join(cp, ""))}
}

func splitKeep(b []byte) []string {
	return strings.SplitAfter(string(b), "\n")
}

// ---- coverage ------------------------------------------------------------------

// ParseClover reads a Clover coverage report: file → line → covered, for
// statement lines. root maps the report's (absolute, maybe container)
// paths to project relative ones by suffix.
func ParseClover(data []byte, mapPath func(string) string) (map[string]map[int]bool, error) {
	var doc struct {
		Files []struct {
			Name  string `xml:"name,attr"`
			Lines []struct {
				Num   int    `xml:"num,attr"`
				Type  string `xml:"type,attr"`
				Count int    `xml:"count,attr"`
			} `xml:"line"`
		} `xml:"project>file"`
		Packages []struct {
			Files []struct {
				Name  string `xml:"name,attr"`
				Lines []struct {
					Num   int    `xml:"num,attr"`
					Type  string `xml:"type,attr"`
					Count int    `xml:"count,attr"`
				} `xml:"line"`
			} `xml:"file"`
		} `xml:"project>package"`
	}
	if err := xml.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("orchestrate: clover: %w", err)
	}
	out := map[string]map[int]bool{}
	add := func(name string, num int, typ string, count int) {
		if typ != "stmt" {
			return
		}
		p := name
		if mapPath != nil {
			p = mapPath(name)
		}
		if out[p] == nil {
			out[p] = map[int]bool{}
		}
		out[p][num] = out[p][num] || count > 0
	}
	for _, f := range doc.Files {
		for _, l := range f.Lines {
			add(f.Name, l.Num, l.Type, l.Count)
		}
	}
	for _, p := range doc.Packages {
		for _, f := range p.Files {
			for _, l := range f.Lines {
				add(f.Name, l.Num, l.Type, l.Count)
			}
		}
	}
	return out, nil
}

// Uncovered returns the added statement lines of changed non-test files
// that no test executed.
func Uncovered(changes []FileChange, cov map[string]map[int]bool) []GateIssue {
	var out []GateIssue
	for _, f := range changes {
		if f.Test || f.Kind == api.ChangeDeleted || !strings.HasSuffix(f.Path, ".php") {
			continue
		}
		lines := cov[f.Path]
		var miss []int
		for _, l := range f.AddedLines() {
			if covered, stmt := lines[l]; stmt && !covered {
				miss = append(miss, l)
			}
		}
		if len(miss) == 0 {
			continue
		}
		out = append(out, GateIssue{Gate: "coverage", To: api.RoleQA, Target: api.TargetTests, Severity: api.SeverityMinor,
			Claim: fmt.Sprintf("changed lines not executed by any test: %s", ranges(miss)), Evidence: fmt.Sprintf("%s:%d", f.Path, miss[0]),
			Suggest: "add a test case that runs this code"})
	}
	return out
}

func ranges(ls []int) string {
	var parts []string
	for i := 0; i < len(ls); {
		j := i
		for j+1 < len(ls) && ls[j+1] == ls[j]+1 {
			j++
		}
		if i == j {
			parts = append(parts, strconv.Itoa(ls[i]))
		} else {
			parts = append(parts, fmt.Sprintf("%d-%d", ls[i], ls[j]))
		}
		i = j + 1
	}
	return strings.Join(parts, ", ")
}

// ---- evidence ----------------------------------------------------------------------

// Symbols finds the files that declare a symbol (the PHP index,
// nav.symbols); nil skips the lookup.
type Symbols func(ctx context.Context, name string) []string

var (
	evidenceRE = regexp.MustCompile(`^\s*` + "`?" + `([^\s:#` + "`" + `]+?)(?::(\d+)(?:-(\d+))?|#L(\d+))?` + "`?" + `\s*$`)
	symbolRE   = regexp.MustCompile("`([A-Za-z_\\\\][A-Za-z0-9_\\\\]*(?:::|->)?[A-Za-z0-9_]*)(?:\\(\\))?`")
)

// ParseEvidence splits "path:line", "path:line-line" or "path#Lline".
func ParseEvidence(ev string) (path string, line int, ok bool) {
	m := evidenceRE.FindStringSubmatch(ev)
	if m == nil {
		return "", 0, false
	}
	for _, s := range []string{m[2], m[4]} {
		if s != "" {
			line, _ = strconv.Atoi(s)
		}
	}
	return m[1], line, true
}

// VerifyEvidence checks a finding mechanically against the files under
// root: the file exists, the line exists, symbols named in backticks in
// the claim occur in the file (near the line). It returns refuted with a
// note when the evidence is wrong, unknown when it cannot tell (no
// evidence), and "" when nothing speaks against the claim (the
// validator decides).
func VerifyEvidence(ctx context.Context, root, evidence, claim string, syms Symbols) (status, note, fixed string) {
	if strings.TrimSpace(evidence) == "" {
		return api.FindingUnknown, "no evidence given", ""
	}
	path, line, ok := ParseEvidence(evidence)
	if !ok {
		return api.FindingUnknown, "evidence is not path:line", ""
	}
	abs := filepath.Join(root, filepath.FromSlash(path))
	if filepath.IsAbs(path) {
		abs = path
	}
	data, err := os.ReadFile(abs)
	if err != nil {
		note = "file " + path + " does not exist"
		for _, s := range symbolRE.FindAllStringSubmatch(claim, 3) {
			if syms == nil {
				break
			}
			name := s[1]
			if i := strings.Index(name, "::"); i >= 0 {
				name = name[:i]
			}
			if i := strings.Index(name, "->"); i >= 0 {
				name = name[:i]
			}
			if files := syms(ctx, strings.TrimPrefix(name, `\`)); len(files) > 0 {
				note += "; " + name + " is declared in " + strings.Join(files, ", ")
				fixed = files[0]
				break
			}
		}
		return api.FindingRefuted, note, fixed
	}
	lines := strings.Count(string(data), "\n") + 1
	if line > lines {
		return api.FindingRefuted, fmt.Sprintf("%s has only %d lines (cited line %d)", path, lines, line), ""
	}
	text := string(data)
	for _, s := range symbolRE.FindAllStringSubmatch(claim, 5) {
		sym := s[1]
		short := sym
		for _, sep := range []string{"::", "->", `\`} {
			if i := strings.LastIndex(short, sep); i >= 0 {
				short = short[i+len(sep):]
			}
		}
		if short == "" || len(short) < 3 {
			continue
		}
		if !strings.Contains(text, short) {
			return api.FindingRefuted, "`" + sym + "` does not occur in " + path, ""
		}
		if line > 0 {
			found := 0
			for i, l := range strings.Split(text, "\n") {
				if strings.Contains(l, short) {
					found = i + 1
					if abs := found - line; abs < 25 && abs > -25 {
						found = -1
						break
					}
				}
			}
			if found > 0 {
				note = fmt.Sprintf("`%s` is at %s:%d, not near line %d", sym, path, found, line)
				fixed = fmt.Sprintf("%s:%d", path, found)
			}
		}
	}
	return "", note, fixed
}
