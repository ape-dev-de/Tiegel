package syntax

import (
	"bytes"
	"embed"
	"fmt"
	"path/filepath"
	"strings"
	"sync"

	tsxml "github.com/tree-sitter-grammars/tree-sitter-xml/bindings/go"
	tsyaml "github.com/tree-sitter-grammars/tree-sitter-yaml/bindings/go"
	ts "github.com/tree-sitter/go-tree-sitter"
	tsjson "github.com/tree-sitter/tree-sitter-json/bindings/go"
	tsphp "github.com/tree-sitter/tree-sitter-php/bindings/go"
)

// Language identifies a supported source language.
type Language string

// Supported languages. LangPlain has no highlighting at all.
const (
	LangPlain Language = ""
	LangPHP   Language = "php"
	LangJSON  Language = "json"
	LangYAML  Language = "yaml"
	LangXML   Language = "xml"
	LangEnv   Language = "env"
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

// HasTree reports whether documents of this language are backed by a
// Tree-sitter syntax tree (false for .env and plain text).
func (l Language) HasTree() bool {
	_, ok := grammars[l]
	return ok
}

// DetectLanguage guesses the language from the file name and, if needed, the
// first bytes of the content (shebang or "<?php").
func DetectLanguage(path string, head []byte) Language {
	base := strings.ToLower(filepath.Base(path))
	switch {
	case base == ".env" || strings.HasPrefix(base, ".env.") || strings.HasSuffix(base, ".env"):
		return LangEnv
	}
	switch strings.ToLower(filepath.Ext(base)) {
	case ".php", ".phtml", ".php3", ".php4", ".php5", ".php7", ".php8", ".phps", ".inc":
		return LangPHP
	case ".json", ".jsonc", ".lock":
		if filepath.Ext(base) == ".lock" && base != "composer.lock" {
			break
		}
		return LangJSON
	case ".yml", ".yaml":
		return LangYAML
	case ".xml", ".xsd", ".xsl", ".xslt", ".svg", ".xlf", ".xliff":
		return LangXML
	case ".dist":
		// phpunit.xml.dist, phpcs.xml.dist, ...
		return DetectLanguage(strings.TrimSuffix(path, filepath.Ext(path)), head)
	}
	if bytes.HasPrefix(head, []byte("#!")) {
		line, _, _ := bytes.Cut(head, []byte("\n"))
		if bytes.Contains(line, []byte("php")) {
			return LangPHP
		}
	}
	if bytes.HasPrefix(bytes.TrimSpace(head), []byte("<?php")) {
		return LangPHP
	}
	if bytes.HasPrefix(bytes.TrimSpace(head), []byte("<?xml")) {
		return LangXML
	}
	return LangPlain
}
