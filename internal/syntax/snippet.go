package syntax

import (
	"context"
	"strings"
)

// Snippet highlighting: code fragments without a file, such as fenced code
// blocks of a Markdown document ("```php"). The info string of the fence
// (or any common alias of a language) selects either a Tree-sitter grammar
// (PHP, JSON, YAML, XML/HTML, dotenv) or the small built-in lexer for
// languages Tiegel has no grammar for (JavaScript/TypeScript, Go, SQL,
// shell, make, CSS, Python, C-family, INI/TOML, Dockerfile, ignore files,
// diff).

// fenceAliases maps lower-case fence names to a snippet language. Values
// are either a Tree-sitter Language or a lexer name (see lexers).
var fenceAliases = map[string]string{
	"php": "php", "php5": "php", "php7": "php", "php8": "php", "phtml": "php", "inc": "php",
	"json": "json", "jsonc": "json", "json5": "json", "geojson": "json",
	"yaml": "yaml", "yml": "yaml",
	"xml": "xml", "html": "xml", "htm": "xml", "xhtml": "xml", "svg": "xml", "xsd": "xml", "xsl": "xml", "vue": "xml",
	"env": "env", "dotenv": "env",
	"js": "js", "javascript": "js", "jsx": "js", "mjs": "js", "cjs": "js", "node": "js",
	"ts": "js", "typescript": "js", "tsx": "js",
	"go": "go", "golang": "go",
	"sql": "sql", "mysql": "sql", "pgsql": "sql", "postgres": "sql", "postgresql": "sql", "sqlite": "sql", "plsql": "sql",
	"sh": "sh", "bash": "sh", "shell": "sh", "zsh": "sh", "console": "sh", "shell-session": "sh",
	"shellsession": "sh", "terminal": "sh", "fish": "sh",
	"make": "make", "makefile": "make", "mk": "make",
	"gitignore": "ignore", "dockerignore": "ignore", "ignore": "ignore",
	"css": "css", "scss": "css", "sass": "css", "less": "css",
	"python": "py", "py": "py", "python3": "py",
	"c": "c", "h": "c", "cpp": "c", "c++": "c", "cc": "c", "hpp": "c", "java": "c", "kotlin": "c", "kt": "c",
	"cs": "c", "csharp": "c", "c#": "c", "rust": "c", "rs": "c", "swift": "c", "dart": "c", "scala": "c",
	"ini": "ini", "toml": "ini", "conf": "ini", "cfg": "ini", "properties": "ini", "editorconfig": "ini", "neon": "yaml",
	"dockerfile": "docker", "docker": "docker", "containerfile": "docker",
	"diff": "diff", "patch": "diff",
}

// SnippetLanguage returns the normalized snippet language for a fence info
// word ("yml" → "yaml", "shell" → "sh") and whether it is highlighted.
func SnippetLanguage(fence string) (string, bool) {
	l, ok := fenceAliases[strings.ToLower(strings.TrimSpace(fence))]
	return l, ok
}

// HighlightSnippet highlights src written in the language of the fence
// info word lang. It returns the non-plain spans (byte offsets into src,
// sorted, non-overlapping) or nil for unknown languages. Tree-sitter
// languages fall back to nil when Tree-sitter is unavailable (no cgo).
// PHP snippets without an opening tag are highlighted as PHP code (the
// grammar would otherwise read them as HTML text).
func HighlightSnippet(ctx context.Context, lang string, src []byte) []Span {
	l, ok := SnippetLanguage(lang)
	if !ok || len(src) == 0 {
		return nil
	}
	switch Language(l) {
	case LangPHP, LangJSON, LangYAML, LangXML, LangEnv:
		return treeSnippet(ctx, Language(l), src)
	}
	if l == "docker" {
		return dockerfileSpans(src)
	}
	lx := lexers[l]
	if lx == nil {
		return nil
	}
	return lx.highlight(src)
}

func treeSnippet(ctx context.Context, lang Language, src []byte) []Span {
	shift := 0
	if lang == LangPHP && !strings.HasPrefix(strings.TrimSpace(string(src)), "<?") {
		const open = "<?php "
		src = append([]byte(open), src...)
		shift = len(open)
	}
	d, err := NewDocument(ctx, lang, src)
	if err != nil {
		return nil
	}
	defer d.Close()
	var out []Span
	for _, s := range d.Highlights(0, len(src)) {
		if s.Role == RolePlain {
			continue
		}
		s.Start, s.End = max(s.Start-shift, 0), s.End-shift
		if s.End <= s.Start {
			continue
		}
		if n := len(out); n > 0 && out[n-1].End == s.Start && out[n-1].Role == s.Role {
			out[n-1].End = s.End
			continue
		}
		out = append(out, s)
	}
	return out
}
