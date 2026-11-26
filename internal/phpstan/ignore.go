package phpstan

import (
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
)

// IgnoreRule is one entry of parameters.ignoreErrors: a message pattern
// ("#regex#", message/messages), an exact message (rawMessage/
// rawMessages), identifier(s), path(s) and an optional count.
type IgnoreRule struct {
	Messages    []*regexp.Regexp
	Raw         []string
	Identifiers []string
	// Paths are absolute files, directories or fnmatch patterns.
	Paths []string
	// Count is the expected number of matches (0 = any).
	Count int
	// Source is the configuration file of the entry.
	Source string
	// Text is the entry as shown in messages.
	Text string
}

// newIgnoreRule converts an ignoreErrors entry; abs resolves paths.
func newIgnoreRule(v any, abs func(string) string) (*IgnoreRule, error) {
	r := &IgnoreRule{}
	if s, ok := v.(string); ok {
		re, err := CompilePHPRegex(s)
		if err != nil {
			return nil, err
		}
		r.Messages, r.Text = []*regexp.Regexp{re}, s
		return r, nil
	}
	a, ok := v.(*Array)
	if !ok {
		return nil, fmt.Errorf("unsupported ignoreErrors entry %T", v)
	}
	list := func(one, many string) []string {
		var out []string
		if s := str(field(a, one)); s != "" {
			out = append(out, s)
		}
		if l, ok := field(a, many).(*Array); ok {
			for _, x := range l.Values {
				if s := str(x); s != "" {
					out = append(out, s)
				}
			}
		}
		return out
	}
	for _, m := range list("message", "messages") {
		re, err := CompilePHPRegex(m)
		if err != nil {
			return nil, err
		}
		r.Messages = append(r.Messages, re)
		r.Text = m
	}
	r.Raw = list("rawMessage", "rawMessages")
	if r.Text == "" && len(r.Raw) > 0 {
		r.Text = r.Raw[0]
	}
	r.Identifiers = list("identifier", "identifiers")
	if r.Text == "" && len(r.Identifiers) > 0 {
		r.Text = r.Identifiers[0]
	}
	for _, p := range list("path", "paths") {
		r.Paths = append(r.Paths, abs(p))
	}
	r.Count = num(field(a, "count"))
	if len(r.Messages) == 0 && len(r.Raw) == 0 && len(r.Identifiers) == 0 {
		return nil, errors.New("ignoreErrors entry without message or identifier")
	}
	return r, nil
}

// reModifiers are the PCRE modifiers RE2 understands as flags.
var reModifiers = map[byte]string{'i': "i", 'm': "m", 's': "s", 'u': "", 'U': "U", 'D': ""}

// CompilePHPRegex compiles a PCRE pattern with delimiters ("#…#i",
// "~…~", "{…}") for Go. Patterns using PCRE-only features (lookaround,
// possessive quantifiers, …) are reported as errors.
func CompilePHPRegex(p string) (*regexp.Regexp, error) {
	if len(p) < 2 {
		return nil, fmt.Errorf("bad pattern %q", p)
	}
	open := p[0]
	closeCh := open
	switch open {
	case '(':
		closeCh = ')'
	case '{':
		closeCh = '}'
	case '[':
		closeCh = ']'
	case '<':
		closeCh = '>'
	}
	if strings.IndexByte(`abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789\ `, open) >= 0 {
		return nil, fmt.Errorf("pattern %q has no delimiter", p)
	}
	end := strings.LastIndexByte(p, closeCh)
	if end <= 0 {
		return nil, fmt.Errorf("pattern %q is not closed", p)
	}
	body, mods := p[1:end], p[end+1:]
	if open == closeCh {
		body = strings.ReplaceAll(body, `\`+string(open), string(open))
	}
	flags := ""
	for i := 0; i < len(mods); i++ {
		f, ok := reModifiers[mods[i]]
		if !ok {
			return nil, fmt.Errorf("pattern %q: modifier %q is not supported", p, mods[i])
		}
		flags += f
	}
	if flags != "" {
		body = "(?" + flags + ")" + body
	}
	re, err := regexp.Compile(body)
	if err != nil {
		return nil, fmt.Errorf("pattern %q: %w", p, err)
	}
	return re, nil
}

// matches reports whether r covers an error (message, identifier, path).
func (r *IgnoreRule) matches(e Raw) bool {
	if len(r.Messages) > 0 || len(r.Raw) > 0 {
		ok := false
		for _, re := range r.Messages {
			if re.MatchString(e.Message) {
				ok = true
				break
			}
		}
		for _, m := range r.Raw {
			if m == e.Message {
				ok = true
				break
			}
		}
		if !ok {
			return false
		}
	}
	if len(r.Identifiers) > 0 {
		ok := false
		for _, id := range r.Identifiers {
			if id == e.Identifier {
				ok = true
				break
			}
		}
		if !ok {
			return false
		}
	}
	if len(r.Paths) == 0 {
		return true
	}
	for _, p := range r.Paths {
		if pathMatches(p, e.FilePath) || e.TraitPath != "" && pathMatches(p, e.TraitPath) {
			return true
		}
	}
	return false
}

// pathMatches is PHPStan's FileExcluder: a file, a directory (and all
// below) or an fnmatch pattern ("*" also crosses "/").
func pathMatches(pattern, file string) bool {
	if file == "" {
		return false
	}
	pattern, file = filepath.ToSlash(pattern), filepath.ToSlash(file)
	if strings.ContainsAny(pattern, "*?[") {
		re, err := regexp.Compile("^" + globRegex(pattern) + "$")
		return err == nil && re.MatchString(file)
	}
	pattern = strings.TrimRight(pattern, "/")
	return file == pattern || strings.HasPrefix(file, pattern+"/")
}

func globRegex(g string) string {
	var b strings.Builder
	for i := 0; i < len(g); i++ {
		switch c := g[i]; c {
		case '*':
			b.WriteString(".*")
		case '?':
			b.WriteString(".")
		case '[':
			j := strings.IndexByte(g[i:], ']')
			if j < 0 {
				b.WriteString(`\[`)
				continue
			}
			cls := g[i+1 : i+j]
			if strings.HasPrefix(cls, "!") {
				cls = "^" + cls[1:]
			}
			b.WriteString("[" + strings.ReplaceAll(cls, `\`, `\\`) + "]")
			i += j
		default:
			b.WriteString(regexp.QuoteMeta(string(c)))
		}
	}
	return b.String()
}

// Ignore removes the errors covered by rules (like PHPStan does after an
// analysis, with the baseline). Errors that cannot be ignored stay. The
// messages report entries whose count no longer fits (PHPStan reports
// those as errors of their own).
func Ignore(errs []Raw, rules []*IgnoreRule) (kept []Raw, messages []string) {
	if len(rules) == 0 {
		return errs, nil
	}
	counts := make([]int, len(rules))
	for _, e := range errs {
		ignored := false
		if e.Ignorable {
			for i, r := range rules {
				if r.matches(e) {
					counts[i]++
					ignored = true
					break
				}
			}
		}
		if !ignored {
			kept = append(kept, e)
		}
	}
	for i, r := range rules {
		if r.Count > 0 && counts[i] > r.Count {
			where := ""
			if len(r.Paths) == 1 {
				where = " in " + filepath.Base(r.Paths[0])
			}
			messages = append(messages, fmt.Sprintf("Ignored error pattern %s%s is expected to occur %d times, but occurred %d times.", r.Text, where, r.Count, counts[i]))
		}
	}
	return kept, messages
}
