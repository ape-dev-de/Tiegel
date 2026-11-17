// Package langdetect determines the language of a file from its name,
// shebang line and content.
//
// Detection order:
//
//  0. User file type associations (DetectWith; setting files.associations).
//  1. Well-known file names (composer.lock, .env, .env.local, …).
//  2. File extension (.php, .json, .yml, .xml, …); a trailing ".dist"
//     (phpunit.xml.dist) is ignored.
//  3. Shebang line (#!/usr/bin/env php, #!/usr/bin/php, php8.3, …).
//  4. Content markers (<?php, <?xml).
//  5. PlainText otherwise.
//
// Ambiguous extensions such as .inc only count as PHP if the content says
// so; otherwise detection continues with the shebang and content rules.
package langdetect

import (
	"bytes"
	"fmt"
	"path"
	"path/filepath"
	"regexp"
	"strings"
)

// Language identifies a supported language. Values match the language
// module IDs ("php" → lang-php).
type Language string

// Supported languages.
const (
	PHP       Language = "php"
	JSON      Language = "json"
	YAML      Language = "yaml"
	XML       Language = "xml"
	DotEnv    Language = "dotenv"
	PlainText Language = "plaintext"
)

// All lists every supported language.
var All = []Language{PHP, JSON, YAML, XML, DotEnv, PlainText}

// HeadSize is the number of leading bytes Detect needs at most. Callers can
// read just this much of a file.
const HeadSize = 4096

var byExt = map[string]Language{
	".php": PHP, ".phtml": PHP, ".php3": PHP, ".php4": PHP, ".php5": PHP,
	".php7": PHP, ".php8": PHP, ".phps": PHP, ".phpt": PHP,
	".json": JSON, ".jsonc": JSON, ".json5": JSON,
	".yaml": YAML, ".yml": YAML, ".neon": YAML, // NEON (PHPStan) is close enough to YAML for highlighting
	".xml": XML, ".xsd": XML, ".xsl": XML, ".xslt": XML, ".svg": XML, ".xlf": XML, ".xliff": XML,
	".env": DotEnv,
	".txt": PlainText, ".text": PlainText, ".log": PlainText,
}

var byName = map[string]Language{
	"composer.lock": JSON,
	".php_cs":       PHP,
}

// ambiguousExt are extensions that are PHP only if the content confirms it.
var ambiguousExt = map[string]bool{".inc": true, ".module": true}

// ByPath detects the language from the file name alone. ok is false when
// the name gives no (unambiguous) hint.
func ByPath(path string) (lang Language, ok bool) {
	name := strings.ToLower(filepath.Base(path))
	if l, ok := byName[name]; ok {
		return l, true
	}
	// .env, .env.local, .env.example, .env.dist, …
	if name == ".env" || strings.HasPrefix(name, ".env.") {
		return DotEnv, true
	}
	name = strings.TrimSuffix(name, ".dist")
	if l, ok := byName[name]; ok {
		return l, true
	}
	if l, ok := byExt[filepath.Ext(name)]; ok {
		return l, true
	}
	return "", false
}

// shebangRE matches interpreter lines invoking PHP directly or via env,
// with optional version suffix (php8.3) and env flags (env -S php -d …).
var shebangRE = regexp.MustCompile(`^#!\s*(?:\S*/)?(?:env\s+(?:-\S+\s+)*)?php[0-9.]*(?:\s|$)`)

// ByShebang inspects the first line of content for a PHP shebang.
func ByShebang(head []byte) (lang Language, ok bool) {
	line := head
	if i := bytes.IndexByte(line, '\n'); i >= 0 {
		line = line[:i]
	}
	line = bytes.TrimRight(line, "\r")
	if shebangRE.Match(line) {
		return PHP, true
	}
	return "", false
}

var utf8BOM = []byte{0xEF, 0xBB, 0xBF}

// ByContent inspects leading content for language markers: "<?php" anywhere
// in head (PHP files may start with HTML) and a leading "<?xml".
func ByContent(head []byte) (lang Language, ok bool) {
	if len(head) > HeadSize {
		head = head[:HeadSize]
	}
	trimmed := bytes.TrimLeft(bytes.TrimPrefix(head, utf8BOM), " \t\r\n")
	if bytes.HasPrefix(trimmed, []byte("<?xml")) {
		return XML, true
	}
	if containsFold(head, []byte("<?php")) {
		return PHP, true
	}
	if bytes.HasPrefix(trimmed, []byte("<?=")) {
		return PHP, true
	}
	return "", false
}

func containsFold(s, sub []byte) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if bytes.EqualFold(s[i:i+len(sub)], sub) {
			return true
		}
	}
	return false
}

// Detect returns the language of a file given its path and (optionally) its
// first bytes (HeadSize is enough). It never fails; unknown files are
// PlainText.
func Detect(path string, head []byte) Language {
	ext := strings.ToLower(filepath.Ext(path))
	if !ambiguousExt[ext] {
		if l, ok := ByPath(path); ok {
			return l
		}
	}
	if l, ok := ByShebang(head); ok {
		return l
	}
	if l, ok := ByContent(head); ok {
		return l
	}
	return PlainText
}

// Association maps a file name pattern to a language (user setting
// files.associations, entries "pattern=language").
//
// A pattern without "/" is matched against the base name (case-insensitive
// path.Match syntax: *.tpl, Jenkinsfile, .php_cs*). A pattern with "/" is
// matched against the trailing path elements of the slash-separated path
// (templates/*.html).
type Association struct {
	Pattern  string   `json:"pattern"`
	Language Language `json:"language"`
}

// ParseAssociations parses "pattern=language" entries. Invalid entries
// (missing "=", bad pattern, unknown language) are skipped and reported.
func ParseAssociations(entries []string) ([]Association, []error) {
	var out []Association
	var errs []error
	for _, e := range entries {
		pat, lang, ok := strings.Cut(e, "=")
		pat, lang = strings.TrimSpace(pat), strings.ToLower(strings.TrimSpace(lang))
		if !ok || pat == "" || lang == "" {
			errs = append(errs, fmt.Errorf("langdetect: association %q: want pattern=language", e))
			continue
		}
		if _, err := path.Match(strings.ToLower(pat), ""); err != nil {
			errs = append(errs, fmt.Errorf("langdetect: association %q: %w", e, err))
			continue
		}
		if !known(Language(lang)) {
			errs = append(errs, fmt.Errorf("langdetect: association %q: unknown language %q (known: %v)", e, lang, All))
			continue
		}
		out = append(out, Association{Pattern: pat, Language: Language(lang)})
	}
	return out, errs
}

func known(l Language) bool {
	for _, k := range All {
		if k == l {
			return true
		}
	}
	return false
}

// Match returns the language of the first association matching p.
func Match(as []Association, p string) (Language, bool) {
	slash := strings.ToLower(filepath.ToSlash(p))
	for _, a := range as {
		pat := strings.ToLower(a.Pattern)
		n := strings.Count(pat, "/") + 1
		parts := strings.Split(slash, "/")
		if len(parts) < n {
			continue
		}
		tail := strings.Join(parts[len(parts)-n:], "/")
		if ok, _ := path.Match(pat, tail); ok {
			return a.Language, true
		}
	}
	return "", false
}

// DetectWith is Detect with user associations taking precedence.
func DetectWith(as []Association, p string, head []byte) Language {
	if l, ok := Match(as, p); ok {
		return l
	}
	return Detect(p, head)
}
