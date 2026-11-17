package run

import (
	"errors"
	"runtime"
	"strings"
)

// ErrUnterminatedQuote is returned by SplitWords for an open quote.
var ErrUnterminatedQuote = errors.New("run: unterminated quote")

// SplitWords splits a command line into words like a POSIX shell does,
// without expansion: whitespace separates words, single quotes keep
// everything literally, double quotes keep whitespace and allow \" and \\,
// a backslash outside quotes escapes the next character. On Windows a
// backslash is literal (paths), except for \" inside double quotes.
// Empty input yields no words.
func SplitWords(s string) ([]string, error) { return splitWords(s, runtime.GOOS == "windows") }

func splitWords(s string, literalBackslash bool) ([]string, error) {
	var (
		words []string
		cur   strings.Builder
		inTok bool
	)
	flush := func() {
		if inTok {
			words = append(words, cur.String())
			cur.Reset()
			inTok = false
		}
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c == ' ' || c == '\t' || c == '\n' || c == '\r':
			flush()
		case c == '\'':
			inTok = true
			end := strings.IndexByte(s[i+1:], '\'')
			if end < 0 {
				return nil, ErrUnterminatedQuote
			}
			cur.WriteString(s[i+1 : i+1+end])
			i += end + 1
		case c == '"':
			inTok = true
			i++
			closed := false
			for ; i < len(s); i++ {
				if s[i] == '"' {
					closed = true
					break
				}
				if s[i] == '\\' && i+1 < len(s) && (s[i+1] == '"' || s[i+1] == '\\' && !literalBackslash) {
					i++
				}
				cur.WriteByte(s[i])
			}
			if !closed {
				return nil, ErrUnterminatedQuote
			}
		case c == '\\' && i+1 < len(s) && !literalBackslash:
			inTok = true
			i++
			cur.WriteByte(s[i])
		default:
			inTok = true
			cur.WriteByte(c)
		}
	}
	flush()
	return words, nil
}

// QuoteWords renders argv as shell text, quoting words that need it.
func QuoteWords(argv []string) string {
	out := make([]string, len(argv))
	for i, a := range argv {
		if a != "" && !strings.ContainsAny(a, " \t\n'\"\\$`;&|<>(){}[]*?!#~") {
			out[i] = a
			continue
		}
		out[i] = "'" + strings.ReplaceAll(a, "'", `'\''`) + "'"
	}
	return strings.Join(out, " ")
}
