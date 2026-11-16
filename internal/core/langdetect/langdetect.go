// Package langdetect determines the language of a file from its name,
// shebang line and content.
//
// Detection order:
//
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
