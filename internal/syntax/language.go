//go:build cgo

package syntax

import (
	"embed"
	"fmt"
	"strings"
	"sync"

	tsxml "github.com/tree-sitter-grammars/tree-sitter-xml/bindings/go"
	tsyaml "github.com/tree-sitter-grammars/tree-sitter-yaml/bindings/go"
	ts "github.com/tree-sitter/go-tree-sitter"
	tsjson "github.com/tree-sitter/tree-sitter-json/bindings/go"
	tsphp "github.com/tree-sitter/tree-sitter-php/bindings/go"
)

//go:embed queries/*.scm
var queryFS embed.FS

// grammar bundles everything needed to parse and highlight one language.
// Compiled queries are immutable and safe for concurrent use; they live for
// the whole process and are never closed.
type grammar struct {
	lang      *ts.Language
	files     []string        // highlight query files, concatenated in order
	overrides map[string]Role // per-language capture overrides
	// firstWins selects the precedence convention of the upstream query
	// for captures of the same node: tree-sitter-highlight style (first
	// pattern wins, used by tree-sitter-json) or Neovim style (last
	// pattern wins, used by the PHP, YAML and XML queries).
	firstWins bool

	kindsOnce sync.Once
	kinds     []string // node kind id -> name
	fieldsMu  sync.Mutex
	fields    map[string]uint16

	once    sync.Once
	hlQuery *ts.Query
	roles   []Role // capture index -> role
	err     error
}

var grammars = map[Language]*grammar{
	LangPHP: {
		lang:  ts.NewLanguage(tsphp.LanguagePHP()),
		files: []string{"queries/php.scm", "queries/php_extra.scm"},
	},
	LangJSON: {
		lang:      ts.NewLanguage(tsjson.Language()),
		files:     []string{"queries/json.scm"},
		firstWins: true,
	},
	LangYAML: {
		lang:  ts.NewLanguage(tsyaml.Language()),
		files: []string{"queries/yaml.scm"},
	},
	LangXML: {
		lang:      ts.NewLanguage(tsxml.LanguageXML()),
		files:     []string{"queries/xml.scm"},
		overrides: map[string]Role{"property": RoleAttribute},
	},
}

// highlightQuery compiles (once) and returns the highlight query and the
// capture-index-to-role table.
func (g *grammar) highlightQuery() (*ts.Query, []Role, error) {
	g.once.Do(func() {
		var src strings.Builder
		for _, f := range g.files {
			b, err := queryFS.ReadFile(f)
			if err != nil {
				g.err = fmt.Errorf("read query %s: %w", f, err)
				return
			}
			src.Write(b)
			src.WriteByte('\n')
		}
		q, qerr := ts.NewQuery(g.lang, src.String())
		if qerr != nil {
			g.err = fmt.Errorf("compile highlight query: %w", qerr)
			return
		}
		names := q.CaptureNames()
		roles := make([]Role, len(names))
		for i, n := range names {
			if r, ok := g.overrides[n]; ok {
				roles[i] = r
				continue
			}
			roles[i] = RoleForCapture(n)
		}
		g.hlQuery, g.roles = q, roles
	})
	return g.hlQuery, g.roles, g.err
}

// kindName returns n.Kind() without a cgo string conversion per call.
func (g *grammar) kindName(n *ts.Node) string {
	g.kindsOnce.Do(func() {
		cnt := g.lang.NodeKindCount()
		g.kinds = make([]string, cnt)
		for i := uint32(0); i < cnt; i++ {
			g.kinds[i] = g.lang.NodeKindForId(uint16(i))
		}
	})
	if id := int(n.KindId()); id < len(g.kinds) {
		return g.kinds[id]
	}
	return n.Kind()
}

// fieldID returns the cached field id for name (0 if unknown).
func (g *grammar) fieldID(name string) uint16 {
	g.fieldsMu.Lock()
	defer g.fieldsMu.Unlock()
	id, ok := g.fields[name]
	if !ok {
		if g.fields == nil {
			g.fields = map[string]uint16{}
		}
		id = g.lang.FieldIdForName(name)
		g.fields[name] = id
	}
	return id
}
