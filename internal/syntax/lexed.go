package syntax

import "sort"

// Lexed languages: editor documents of languages without a Tree-sitter
// grammar are highlighted with the built-in lexers (lexer.go,
// dockerfile.go). A Document of such a language tokenizes the whole text
// once per Reparse (the lexers are linear and allocation-light, so this is
// cheap for files of ordinary size) and answers Highlights from the cached
// spans. No syntax tree, folds or outline; works without cgo.

// lexedLanguages maps each lexed language to its tokenizer, which returns
// sorted, non-overlapping, non-plain spans.
var lexedLanguages = map[Language]func([]byte) []Span{
	LangDockerfile: dockerfileSpans,
	LangShell:      func(src []byte) []Span { return lexers["sh"].highlight(src) },
	LangMake:       func(src []byte) []Span { return lexers["make"].highlight(src) },
	LangINI:        func(src []byte) []Span { return lexers["ini"].highlight(src) },
	LangSQL:        func(src []byte) []Span { return lexers["sql"].highlight(src) },
	LangIgnore:     func(src []byte) []Span { return lexers["ignore"].highlight(src) },
}

// Lexed reports whether documents of this language are highlighted by a
// built-in lexer instead of Tree-sitter.
func (l Language) Lexed() bool { return lexedLanguages[l] != nil }

// lex tokenizes src for a lexed language (nil for all other languages).
func lex(lang Language, src []byte) []Span {
	if f := lexedLanguages[lang]; f != nil {
		return f(src)
	}
	return nil
}

// tile returns the spans covering [start, end) exactly: the parts of the
// sorted, non-overlapping spans within the range and RolePlain gaps.
func tile(spans []Span, start, end int) []Span {
	var out []Span
	pos := start
	for i := sort.Search(len(spans), func(i int) bool { return spans[i].End > start }); i < len(spans) && spans[i].Start < end; i++ {
		s := spans[i]
		s.Start, s.End = max(s.Start, start), min(s.End, end)
		if s.Start > pos {
			out = append(out, Span{Start: pos, End: s.Start, Role: RolePlain})
		}
		out = append(out, s)
		pos = s.End
	}
	if pos < end {
		out = append(out, Span{Start: pos, End: end, Role: RolePlain})
	}
	return out
}
