package phpunit

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/ape-dev-de/tiegel/internal/api"
	"github.com/ape-dev-de/tiegel/internal/index"
	"github.com/ape-dev-de/tiegel/internal/teamcity"
)

// testCaseFQN is the base class of all PHPUnit tests.
const testCaseFQN = `PHPUnit\Framework\TestCase`

// Attribute FQNs (PHPUnit 10+).
const (
	attrTest             = `PHPUnit\Framework\Attributes\Test`
	attrDataProvider     = `PHPUnit\Framework\Attributes\DataProvider`
	attrDataProviderExt  = `PHPUnit\Framework\Attributes\DataProviderExternal`
	maxDiscoverFiles     = 20000
	maxDiscoverFileBytes = 2 << 20
)

// parsedFile is a PHP file parsed by the Tree-sitter index.
type parsedFile struct {
	f   *index.File
	src []byte
}

// parser parses PHP files with a private index (Tree-sitter); it is not
// the project's index, which only knows saved files and may be off.
type parser struct {
	ix *index.Index
}

func newParser(root string) *parser {
	return &parser{ix: index.New(index.Options{Root: root, IncludeIgnored: true, Workers: 1})}
}

func (p *parser) close() { p.ix.Close() }

// parse parses src as path.
func (p *parser) parse(path string, src []byte) (parsedFile, error) {
	if _, err := p.ix.UpdateText(path, src); err != nil {
		return parsedFile{}, err
	}
	f, ok := p.ix.File(path)
	if !ok {
		return parsedFile{}, os.ErrNotExist
	}
	return parsedFile{f: f, src: src}, nil
}

// source returns the text of path: the open document if any (unsaved
// edits count), else the file.
func (m *Module) source(path string) ([]byte, error) {
	m.mu.Lock()
	app := m.app
	m.mu.Unlock()
	if app != nil {
		if b, _, err := app.ReadText(path); err == nil {
			return b, nil
		}
	}
	return os.ReadFile(path)
}

func (m *Module) discover(ctx context.Context, p api.TestDiscoverParams) (api.TestDiscovery, error) {
	if !index.TreeSitter {
		return api.TestDiscovery{}, api.Errorf(api.CodeUnavailable, "test discovery needs Tree-sitter (this build has no cgo)")
	}
	root, err := m.root()
	if err != nil {
		return api.TestDiscovery{}, err
	}
	var files []string
	if p.Path != "" {
		target := abs(root, p.Path)
		st, err := os.Stat(target)
		switch {
		case err != nil:
			if _, open, rerr := m.app.ReadText(target); rerr != nil || !open {
				return api.TestDiscovery{}, api.Errorf(api.CodeNotFound, "%s: %v", p.Path, err)
			}
			files = []string{target}
		case st.IsDir():
			files = phpFiles(ctx, []string{target})
		default:
			files = []string{target}
		}
	} else {
		files = phpFiles(ctx, m.detect(root).testDirs())
	}
	res := api.TestDiscovery{Classes: []api.TestItem{}}
	ps := newParser(root)
	defer ps.close()
	var parsed []parsedFile
	for _, f := range files {
		if ctx.Err() != nil {
			return res, api.Errorf(api.CodeFailed, "%w", ctx.Err())
		}
		src, err := m.source(f)
		if err != nil || len(src) > maxDiscoverFileBytes {
			continue
		}
		pf, err := ps.parse(f, src)
		if err != nil {
			res.Errors = append(res.Errors, rel(root, f)+": "+err.Error())
			continue
		}
		parsed = append(parsed, pf)
	}
	res.Files = len(parsed)
	anc := m.newAncestry(ctx, parsed)
	m.mu.Lock()
	last := m.last
	for _, pf := range parsed {
		for _, c := range testClasses(pf, anc) {
			fillStatus(&c, last)
			res.Classes = append(res.Classes, c)
		}
	}
	m.mu.Unlock()
	sort.SliceStable(res.Classes, func(i, j int) bool {
		if res.Classes[i].File != res.Classes[j].File {
			return res.Classes[i].File < res.Classes[j].File
		}
		return res.Classes[i].Line < res.Classes[j].Line
	})
	return res, nil
}

func fillStatus(c *api.TestItem, last map[string]teamcity.Status) {
	c.Status = last[c.ID]
	for i := range c.Children {
		c.Children[i].Status = last[c.Children[i].ID]
	}
}

// phpFiles lists the .php files below dirs (no vendor/, node_modules/,
// hidden directories), sorted.
func phpFiles(ctx context.Context, dirs []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, d := range dirs {
		_ = filepath.WalkDir(d, func(p string, e fs.DirEntry, err error) error {
			if err != nil || ctx.Err() != nil || len(out) >= maxDiscoverFiles {
				if e != nil && e.IsDir() && err != nil {
					return fs.SkipDir
				}
				if ctx.Err() != nil || len(out) >= maxDiscoverFiles {
					return fs.SkipAll
				}
				return nil
			}
			name := e.Name()
			if e.IsDir() {
				if p != d && (strings.HasPrefix(name, ".") || name == "vendor" || name == "node_modules") {
					return fs.SkipDir
				}
				return nil
			}
			if strings.EqualFold(filepath.Ext(name), ".php") && !seen[p] {
				seen[p] = true
				out = append(out, p)
			}
			return nil
		})
	}
	sort.Strings(out)
	return out
}

// ancestry decides whether a class has TestCase as ancestor: through the
// classes parsed here, the project's PHP index (nav.typeHierarchy, when
// the index runs) and – for parents known nowhere – the convention that
// base test classes end in "TestCase".
type ancestry struct {
	local   map[string]*index.Class // folded FQN
	indexed map[string]bool         // descendants of TestCase in the index
	memo    map[string]bool
}

func (m *Module) newAncestry(ctx context.Context, parsed []parsedFile) *ancestry {
	a := &ancestry{local: map[string]*index.Class{}, indexed: map[string]bool{}, memo: map[string]bool{}}
	for _, pf := range parsed {
		for i := range pf.f.Classes {
			c := &pf.f.Classes[i]
			a.local[strings.ToLower(c.FQN)] = c
		}
	}
	m.mu.Lock()
	app := m.app
	m.mu.Unlock()
	if app == nil {
		return a
	}
	// One query for all subclasses of TestCase in the project index.
	type node struct {
		FQN      string `json:"fqn"`
		Children []node `json:"children"`
	}
	var h struct {
		Subtypes []node `json:"subtypes"`
	}
	params := map[string]any{"fqn": testCaseFQN}
	if err := api.NewInProcess(app).Call(ctx, "nav.typeHierarchy", params, &h); err == nil {
		var walk func([]node)
		walk = func(ns []node) {
			for _, n := range ns {
				a.indexed[strings.ToLower(n.FQN)] = true
				walk(n.Children)
			}
		}
		walk(h.Subtypes)
	}
	return a
}

// isTestCase reports whether class fqn descends from TestCase.
func (a *ancestry) isTestCase(fqn string) bool {
	return a.check(strings.ToLower(strings.TrimPrefix(fqn, `\`)), map[string]bool{})
}

func (a *ancestry) check(k string, path map[string]bool) bool {
	if v, ok := a.memo[k]; ok {
		return v
	}
	if k == strings.ToLower(testCaseFQN) || a.indexed[k] {
		return true
	}
	if path[k] {
		return false
	}
	path[k] = true
	v := false
	if c := a.local[k]; c != nil {
		if len(c.Extends) > 0 {
			v = a.check(strings.ToLower(c.Extends[0]), path)
		}
	} else {
		// Unknown here and not a TestCase descendant in the index (or no
		// index): base test classes are called …TestCase by convention.
		short := k
		if i := strings.LastIndexByte(k, '\\'); i >= 0 {
			short = k[i+1:]
		}
		v = strings.HasSuffix(short, "testcase")
	}
	a.memo[k] = v
	return v
}

// testClasses returns the concrete test classes of a file with their
// test methods.
func testClasses(pf parsedFile, anc *ancestry) []api.TestItem {
	var out []api.TestItem
	for _, c := range pf.f.Classes {
		if c.Kind != index.KindClass || c.Abstract || len(c.Extends) == 0 || !anc.isTestCase(c.FQN) {
			continue
		}
		item := api.TestItem{ID: c.FQN, Kind: "class", Name: c.Name, Class: c.FQN, File: pf.f.Path, Line: c.NameRange.Start.Line}
		prevEnd := c.NameRange.End.Offset
		for _, mem := range c.Members {
			if mem.Kind != index.KindMethod {
				prevEnd = max(prevEnd, mem.Range.End.Offset)
				continue
			}
			head := header(pf.src, prevEnd, mem.NameRange.Start.Offset)
			prevEnd = max(prevEnd, mem.Range.End.Offset)
			if mem.Static || mem.Abstract || (mem.Visibility != "" && mem.Visibility != "public") {
				continue
			}
			scope := pf.f.ScopeAt(mem.Range.Start.Offset)
			info := methodInfo(head, scope)
			if !info.test && !strings.HasPrefix(mem.Name, "test") {
				continue
			}
			item.Children = append(item.Children, api.TestItem{
				ID: c.FQN + "::" + mem.Name, Kind: "method", Name: mem.Name, Class: c.FQN, Method: mem.Name,
				File: pf.f.Path, Line: mem.NameRange.Start.Line, DataProviders: info.providers,
			})
		}
		out = append(out, item)
	}
	return out
}

// header returns src[from:to] (clamped).
func header(src []byte, from, to int) string {
	from, to = max(0, from), min(len(src), to)
	if from >= to {
		return ""
	}
	return string(src[from:to])
}

// methodMeta is what the doc comment and attributes say about a method.
type methodMeta struct {
	test      bool
	providers []string
}

// methodInfo reads the last doc comment and the attributes of a method
// header (the text between the previous member and the method name).
func methodInfo(head string, scope index.Scope) methodMeta {
	var mm methodMeta
	// Doc comment: the last /** … */ of the header.
	if i := strings.LastIndex(head, "/**"); i >= 0 {
		if j := strings.Index(head[i:], "*/"); j >= 0 {
			doc := head[i : i+j]
			for _, l := range strings.Split(doc, "\n") {
				l = strings.TrimLeft(strings.TrimSpace(l), "/* \t")
				switch {
				case l == "@test" || strings.HasPrefix(l, "@test ") || strings.HasPrefix(l, "@test\t"):
					mm.test = true
				case strings.HasPrefix(l, "@dataProvider"):
					if f := strings.Fields(strings.TrimPrefix(l, "@dataProvider")); len(f) > 0 {
						mm.providers = append(mm.providers, f[0])
					}
				}
			}
			head = head[i+j+2:]
		}
	}
	for _, a := range attributes(head) {
		fqn := index.ResolveClass(a.name, scope)
		short := a.name
		if i := strings.LastIndexByte(short, '\\'); i >= 0 {
			short = short[i+1:]
		}
		switch {
		case strings.EqualFold(fqn, attrTest) || strings.EqualFold(short, "Test"):
			mm.test = true
		case strings.EqualFold(fqn, attrDataProvider) || strings.EqualFold(short, "DataProvider"):
			if s := lastString(a.args); s != "" {
				mm.providers = append(mm.providers, s)
			}
		case strings.EqualFold(fqn, attrDataProviderExt) || strings.EqualFold(short, "DataProviderExternal"):
			if s := lastString(a.args); s != "" {
				mm.providers = append(mm.providers, s)
			}
		}
	}
	return mm
}

// attribute is one attribute of a #[…] group.
type attribute struct {
	name string
	args string
}

// attributes parses the #[…] groups in s: names and raw argument text,
// with nested brackets, parentheses and strings.
func attributes(s string) []attribute {
	var out []attribute
	for i := 0; i < len(s); i++ {
		if s[i] == '/' && i+1 < len(s) && (s[i+1] == '/' || s[i+1] == '*') {
			i = skipComment(s, i)
			continue
		}
		if s[i] != '#' || i+1 >= len(s) || s[i+1] != '[' {
			continue
		}
		depth, start := 0, i+2
		var parts []string
		j := i + 1
	scan:
		for ; j < len(s); j++ {
			switch c := s[j]; c {
			case '\'', '"':
				j = skipString(s, j)
			case '[', '(':
				depth++
			case ')':
				depth--
			case ']':
				depth--
				if depth == 0 {
					parts = append(parts, s[start:j])
					break scan
				}
			case ',':
				if depth == 1 {
					parts = append(parts, s[start:j])
					start = j + 1
				}
			}
		}
		for _, p := range parts {
			p = strings.TrimSpace(p)
			if p == "" {
				continue
			}
			name, args, _ := strings.Cut(p, "(")
			out = append(out, attribute{name: strings.TrimSpace(name), args: strings.TrimSuffix(strings.TrimSpace(args), ")")})
		}
		i = j
	}
	return out
}

// skipString returns the index of the closing quote of the string at i.
func skipString(s string, i int) int {
	q := s[i]
	for j := i + 1; j < len(s); j++ {
		switch s[j] {
		case '\\':
			j++
		case q:
			return j
		}
	}
	return len(s)
}

// skipComment returns the index of the last byte of the comment at i.
func skipComment(s string, i int) int {
	if s[i+1] == '/' {
		if j := strings.IndexByte(s[i:], '\n'); j >= 0 {
			return i + j
		}
		return len(s)
	}
	if j := strings.Index(s[i+2:], "*/"); j >= 0 {
		return i + 2 + j + 1
	}
	return len(s)
}

// lastString returns the last string literal of an argument list.
func lastString(args string) string {
	last := ""
	for i := 0; i < len(args); i++ {
		if args[i] == '\'' || args[i] == '"' {
			j := skipString(args, i)
			if j > i && j <= len(args) {
				last = args[i+1 : min(j, len(args))]
			}
			i = j
		}
	}
	return last
}
