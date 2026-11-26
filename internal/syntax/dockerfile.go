package syntax

import (
	"regexp"
	"strings"
)

// Dockerfile highlighting without a Tree-sitter grammar (D555). The
// tokenizer works line by line and carries the state a Dockerfile needs
// across lines: the instruction of a logical line continued with the
// escape character ("\" or the one set by "# escape=`"), and pending
// heredocs (RUN <<EOF … EOF). Shell-form RUN, CMD, ENTRYPOINT and
// HEALTHCHECK CMD arguments (and RUN heredoc bodies) are highlighted with
// the shell lexer; exec-form arguments (["php", "-v"]) as JSON-like
// strings.

// dockerInstructions are the Dockerfile instructions (lower case).
var dockerInstructions = words(`from run cmd label maintainer expose env add copy entrypoint volume user workdir arg
onbuild stopsignal healthcheck shell`)

// dockerShellForm are instructions whose arguments are a shell command.
var dockerShellForm = map[string]bool{"run": true, "cmd": true, "entrypoint": true, "shell": true, "healthcheck": true}

// dockerFlags are instructions taking --flag[=value] options.
var dockerFlags = map[string]bool{"from": true, "run": true, "copy": true, "add": true, "healthcheck": true}

// heredocRE matches a heredoc start: <<EOF, <<-EOF, <<"EOF", <<'EOF'.
var heredocRE = regexp.MustCompile(`<<(-?)\s*(["']?)([A-Za-z_][A-Za-z0-9_]*)(["']?)`)

var escapeDirectiveRE = regexp.MustCompile(`^#\s*(?i:escape)\s*=\s*(\S)`)

// heredoc is a pending heredoc body.
type heredoc struct {
	word  string
	strip bool // <<- strips leading tabs before the terminator
	shell bool // RUN bodies are shell scripts; others are strings
}

// dockerState is the tokenizer state between lines.
type dockerState struct {
	esc       byte
	directive bool   // still in the parser directive header
	instr     string // instruction of the current logical line
	cont      bool   // the previous line ended with the escape character
	heredocs  []heredoc
	sh        lexState
}

// dockerfileSpans highlights a Dockerfile and returns the non-plain spans,
// sorted and non-overlapping.
func dockerfileSpans(src []byte) []Span {
	s := string(src)
	paint := make([]Role, len(s))
	st := dockerState{esc: '\\', directive: true}
	for off := 0; off <= len(s); {
		end := strings.IndexByte(s[off:], '\n')
		if end < 0 {
			end = len(s) - off
		}
		st.line(s[off:off+end], off, paint)
		off += end + 1
	}
	var out []Span
	for _, sp := range compress(paint, 0) {
		if sp.Role != RolePlain {
			out = append(out, sp)
		}
	}
	return out
}

func paintRange(paint []Role, s, e int, r Role) {
	for i := s; i < e; i++ {
		paint[i] = r
	}
}

// line tokenizes one line starting at byte base of the source.
func (st *dockerState) line(ln string, base int, paint []Role) {
	ln = strings.TrimSuffix(ln, "\r")
	if len(st.heredocs) > 0 {
		st.heredocLine(ln, base, paint)
		return
	}
	lead := len(ln) - len(strings.TrimLeft(ln, " \t"))
	body := ln[lead:]
	if body == "" {
		return // blank lines do not end a continuation
	}
	if body[0] == '#' {
		// Comments (also inside continuations, where Docker drops them)
		// and parser directives.
		if st.directive {
			if m := escapeDirectiveRE.FindStringSubmatch(body); m != nil && (m[1] == "`" || m[1] == "\\") {
				st.esc = m[1][0]
			}
		}
		paintRange(paint, base+lead, base+len(ln), RoleComment)
		return
	}
	st.directive = false
	// A trailing escape character continues the logical line.
	argEnd := len(ln)
	trimmed := strings.TrimRight(ln, " \t")
	cont := len(trimmed) > lead && trimmed[len(trimmed)-1] == st.esc
	if cont {
		argEnd = len(trimmed) - 1
		paintRange(paint, base+argEnd, base+argEnd+1, RolePunctuation)
	}
	i := lead
	if !st.cont {
		st.instr = ""
		st.sh = lexState{}
		w := wordEnd(ln, i)
		if kw := strings.ToLower(ln[i:w]); dockerInstructions[kw] {
			paintRange(paint, base+i, base+w, RoleKeyword)
			st.instr = kw
			i = skipSpaces(ln, w)
			if kw == "onbuild" { // ONBUILD <instruction> …
				w = wordEnd(ln, i)
				if kw2 := strings.ToLower(ln[i:w]); dockerInstructions[kw2] {
					paintRange(paint, base+i, base+w, RoleKeyword)
					st.instr = kw2
					i = skipSpaces(ln, w)
				}
			}
		}
	}
	if i < argEnd {
		st.args(ln[:argEnd], i, base, paint)
	}
	st.cont = cont
}

// heredocLine paints one line of a heredoc body or its terminator.
func (st *dockerState) heredocLine(ln string, base int, paint []Role) {
	h := st.heredocs[0]
	cmp := ln
	if h.strip {
		cmp = strings.TrimLeft(ln, "\t")
	}
	if cmp == h.word {
		paintRange(paint, base+len(ln)-len(cmp), base+len(ln), RoleKeyword)
		st.heredocs = st.heredocs[1:]
		st.sh = lexState{}
		return
	}
	if !h.shell {
		paintRange(paint, base, base+len(ln), RoleString)
		return
	}
	st.shell(ln, 0, len(ln), base, paint)
}

// shell paints ln[s:e] with the shell lexer; variables inside
// double-quoted strings stay visible.
func (st *dockerState) shell(ln string, s, e, base int, paint []Role) {
	var spans []Span
	lexers["sh"].lineSpans(ln[s:e], base+s, &st.sh, &spans)
	for _, sp := range spans {
		paintRange(paint, sp.Start, sp.End, sp.Role)
		if sp.Role == RoleString && ln[sp.Start-base] == '"' {
			variables(ln, sp.Start-base, sp.End-base, base, paint)
		}
	}
}

// args paints the arguments ln[i:] of the current instruction.
func (st *dockerState) args(ln string, i, base int, paint []Role) {
	if dockerFlags[st.instr] {
		i = flags(ln, i, base, paint)
	}
	if i >= len(ln) {
		return
	}
	switch st.instr {
	case "from":
		st.from(ln, i, base, paint)
	case "env", "arg", "label":
		keyValues(ln, i, base, paint)
	case "healthcheck":
		w := wordEnd(ln, i)
		switch strings.ToLower(ln[i:w]) {
		case "cmd":
			paintRange(paint, base+i, base+w, RoleKeyword)
			st.command(ln, skipSpaces(ln, w), base, paint)
		case "none":
			paintRange(paint, base+i, base+w, RoleKeyword)
		default:
			st.command(ln, i, base, paint)
		}
	default:
		if dockerShellForm[st.instr] || st.instr == "copy" || st.instr == "add" {
			st.command(ln, i, base, paint)
			return
		}
		values(ln, i, len(ln), base, paint)
	}
}

// command paints shell-form or exec-form arguments and registers heredocs
// (RUN, COPY, ADD).
func (st *dockerState) command(ln string, i, base int, paint []Role) {
	if i >= len(ln) {
		return
	}
	if !st.cont && ln[i] == '[' {
		execForm(ln, i, base, paint)
		return
	}
	shell := dockerShellForm[st.instr]
	pos := i
	var docs [][]int
	if st.instr == "run" || st.instr == "copy" || st.instr == "add" {
		docs = heredocRE.FindAllStringSubmatchIndex(ln[i:], -1)
	}
	for _, m := range docs {
		s, e := i+m[0], i+m[1]
		if s > 0 && ln[s-1] == '<' { // <<< here-string
			continue
		}
		if q1, q2 := ln[i+m[4]:i+m[5]], ln[i+m[8]:i+m[9]]; q1 != q2 {
			continue
		}
		st.segment(ln, pos, s, base, paint, shell)
		paintRange(paint, base+s, base+e, RoleKeyword)
		st.heredocs = append(st.heredocs, heredoc{word: ln[i+m[6] : i+m[7]], strip: m[3] > m[2], shell: st.instr == "run"})
		pos = e
	}
	st.segment(ln, pos, len(ln), base, paint, shell)
}

// segment paints ln[s:e] as shell code or as plain values.
func (st *dockerState) segment(ln string, s, e, base int, paint []Role, shell bool) {
	if s >= e {
		return
	}
	if !shell {
		values(ln, s, e, base, paint)
		return
	}
	st.shell(ln, s, e, base, paint)
}

// from paints "image[:tag|@digest] [AS name]".
func (st *dockerState) from(ln string, i, base int, paint []Role) {
	w := tokenEnd(ln, i)
	paintRange(paint, base+i, base+w, RoleType)
	variables(ln, i, w, base, paint)
	i = skipSpaces(ln, w)
	w = wordEnd(ln, i)
	if strings.EqualFold(ln[i:w], "as") {
		paintRange(paint, base+i, base+w, RoleKeyword)
		i = skipSpaces(ln, w)
		paintRange(paint, base+i, base+tokenEnd(ln, i), RoleConstant)
	}
}

// flags paints leading --name[=value] options and returns the offset after
// them. The value of --from names a build stage or image.
func flags(ln string, i, base int, paint []Role) int {
	for {
		i = skipSpaces(ln, i)
		if !strings.HasPrefix(ln[i:], "--") {
			return i
		}
		e := tokenEnd(ln, i)
		name, _, hasValue := strings.Cut(ln[i:e], "=")
		paintRange(paint, base+i, base+i+len(name), RoleAttribute)
		if hasValue {
			eq := i + len(name)
			paintRange(paint, base+eq, base+eq+1, RoleOperator)
			role := RoleString
			if name == "--from" {
				role = RoleConstant
			}
			paintRange(paint, base+eq+1, base+e, role)
			variables(ln, eq+1, e, base, paint)
		}
		i = e
	}
}

// keyValues paints "KEY=value …" pairs (ENV, ARG, LABEL) and the legacy
// "ENV KEY value" form.
func keyValues(ln string, i, base int, paint []Role) {
	first := true
	for i < len(ln) {
		i = skipSpaces(ln, i)
		if i >= len(ln) {
			return
		}
		k := i
		if ln[k] == '"' || ln[k] == '\'' {
			k = stringEnd(ln, k, rune(ln[k]))
		} else {
			for k < len(ln) && ln[k] != '=' && ln[k] != ' ' && ln[k] != '\t' {
				k++
			}
		}
		paintRange(paint, base+i, base+k, RoleProperty)
		if k >= len(ln) {
			return
		}
		if ln[k] != '=' {
			if first { // ENV KEY value
				paintValue(ln, skipSpaces(ln, k), len(ln), base, paint)
				return
			}
			i = k
			continue
		}
		first = false
		paintRange(paint, base+k, base+k+1, RoleOperator)
		v := k + 1
		e := v
		if v < len(ln) && (ln[v] == '"' || ln[v] == '\'') {
			e = stringEnd(ln, v, rune(ln[v]))
		} else {
			e = tokenEnd(ln, v)
		}
		paintValue(ln, v, e, base, paint)
		i = e
	}
}

// values paints ln[s:e] as whitespace-separated values: strings, numbers,
// variables and JSON punctuation.
func values(ln string, s, e, base int, paint []Role) {
	i := s
	for i < e {
		i = skipSpaces(ln, i)
		if i >= e {
			return
		}
		var j int
		switch c := ln[i]; {
		case c == '"' || c == '\'':
			j = min(stringEnd(ln, i, rune(c)), e)
			paintValue(ln, i, j, base, paint)
		case strings.IndexByte("[],", c) >= 0:
			j = i + 1
			paintRange(paint, base+i, base+j, RolePunctuation)
		default:
			j = min(tokenEnd(ln, i), e)
			if isNumberToken(ln[i:j]) {
				paintRange(paint, base+i, base+j, RoleNumber)
			} else {
				variables(ln, i, j, base, paint)
			}
		}
		i = j
	}
}

// paintValue paints a quoted string (with variables inside double quotes)
// or an unquoted value (string with variables).
func paintValue(ln string, s, e, base int, paint []Role) {
	if s >= e {
		return
	}
	paintRange(paint, base+s, base+e, RoleString)
	if ln[s] != '\'' {
		variables(ln, s, e, base, paint)
	}
}

// execForm paints a JSON array of strings (["php", "-v"]).
func execForm(ln string, i, base int, paint []Role) {
	for i < len(ln) {
		switch c := ln[i]; {
		case c == '"':
			j := stringEnd(ln, i, '"')
			paintRange(paint, base+i, base+j, RoleString)
			i = j
		case strings.IndexByte("[],", c) >= 0:
			paintRange(paint, base+i, base+i+1, RolePunctuation)
			i++
		default:
			i++
		}
	}
}

// variables paints $NAME and ${NAME…} references in ln[s:e].
func variables(ln string, s, e, base int, paint []Role) {
	for i := s; i < e-1; i++ {
		if ln[i] != '$' || (i > s && ln[i-1] == '\\') {
			continue
		}
		j := i + 1
		if ln[j] == '{' {
			k := strings.IndexByte(ln[j:e], '}')
			if k < 0 {
				continue
			}
			j += k + 1
		} else {
			for j < e && isVarByte(ln[j]) {
				j++
			}
			if j == i+1 {
				continue
			}
		}
		paintRange(paint, base+i, base+j, RoleVariable)
		i = j - 1
	}
}

func isVarByte(c byte) bool {
	return c == '_' || (c >= '0' && c <= '9') || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
}

func isNumberToken(t string) bool {
	if t == "" || t[0] < '0' || t[0] > '9' {
		return false
	}
	// 80, 8080/tcp, 9000-9003
	for _, c := range t {
		if !(c >= '0' && c <= '9' || c == '/' || c == '-' || c >= 'a' && c <= 'z') {
			return false
		}
	}
	return true
}

func skipSpaces(ln string, i int) int {
	for i < len(ln) && (ln[i] == ' ' || ln[i] == '\t') {
		i++
	}
	return i
}

// wordEnd returns the end of the letter run starting at i.
func wordEnd(ln string, i int) int {
	for i < len(ln) && (ln[i] >= 'a' && ln[i] <= 'z' || ln[i] >= 'A' && ln[i] <= 'Z') {
		i++
	}
	return i
}

// tokenEnd returns the end of the whitespace-delimited token at i.
func tokenEnd(ln string, i int) int {
	for i < len(ln) && ln[i] != ' ' && ln[i] != '\t' {
		i++
	}
	return i
}
