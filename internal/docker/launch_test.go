package docker_test

import (
	"reflect"
	"strings"
	"testing"

	"github.com/ape-dev-de/tiegel/internal/docker"
)

// cmdS simulates `cmd /s /k "<line>"` for lines built by CmdLine: /s
// strips the first character and the last quote; then outside quotes a
// caret escapes the next character and a %NAME% whose variable is
// defined in env is expanded; inside quotes everything is literal except
// %NAME% expansion. It returns the program and the remaining command line.
func cmdS(t *testing.T, quoted string, env map[string]string) (string, string) {
	t.Helper()
	if !strings.HasPrefix(quoted, `"`) || !strings.HasSuffix(quoted, `"`) {
		t.Fatalf("not quoted: %s", quoted)
	}
	line := quoted[1 : len(quoted)-1]
	// Phase 1: percent expansion (only defined variables).
	var exp strings.Builder
	for i := 0; i < len(line); i++ {
		if line[i] == '%' {
			if j := strings.IndexByte(line[i+1:], '%'); j >= 0 {
				if v, ok := env[line[i+1:i+1+j]]; ok {
					exp.WriteString(v)
					i += j + 1
					continue
				}
			}
		}
		exp.WriteByte(line[i])
	}
	line = exp.String()
	// Phase 2: quotes and carets; unescaped metacharacters outside quotes
	// would be operators.
	var out strings.Builder
	quote := false
	for i := 0; i < len(line); i++ {
		c := line[i]
		switch {
		case quote:
			if c == '"' {
				quote = false
			}
		case c == '"':
			quote = true
		case c == '^':
			i++
			c = line[i]
		case strings.IndexByte("&|<>()", c) >= 0:
			t.Fatalf("unescaped operator %q in %s", c, line)
		}
		out.WriteByte(c)
	}
	if quote {
		t.Fatalf("unbalanced quotes in %s", line)
	}
	res := out.String()
	// The program is the leading quoted token.
	end := strings.IndexByte(res[1:], '"') + 1
	return res[1:end], strings.TrimPrefix(res[end+1:], " ")
}

// argvW splits a command line like CommandLineToArgvW (arguments after
// the program).
func argvW(s string) []string {
	args := []string{}
	var cur strings.Builder
	in, inQ := false, false
	for i := 0; i < len(s); {
		c := s[i]
		switch {
		case c == '\\':
			n := 0
			for i < len(s) && s[i] == '\\' {
				n++
				i++
			}
			if i < len(s) && s[i] == '"' {
				cur.WriteString(strings.Repeat(`\`, n/2))
				if n%2 == 1 {
					cur.WriteByte('"')
					i++
				}
			} else {
				cur.WriteString(strings.Repeat(`\`, n))
			}
			in = true
			continue
		case c == '"':
			inQ = !inQ
			in = true
		case (c == ' ' || c == '\t') && !inQ:
			if in {
				args = append(args, cur.String())
				cur.Reset()
				in = false
			}
		default:
			cur.WriteByte(c)
			in = true
		}
		i++
	}
	if in {
		args = append(args, cur.String())
	}
	return args
}

func TestWindowsCmdLine(t *testing.T) {
	env := map[string]string{"PATH": `C:\Windows`, "USERNAME": "me"}
	prog := `C:\Program Files\Docker\Docker\resources\bin\docker.exe`
	for _, args := range [][]string{
		{"compose", "--project-directory", `C:\Users\Jo Doe\my proj`, "-f", `C:\Users\Jo Doe\my proj\compose.yaml`, "exec", "web", "sh"},
		{"--context", "a&b", "exec", "-it", "c|d", "x>y", "(z)", "^caret"},
		{"%PATH%", "100%", "%USERNAME", `x%USERNAME%y`, "!bang!"},
		{"semi;colon", "", `quote"inside`, `trailing\`, `C:\dir with space\`, `back\\"slash`},
	} {
		argv := append([]string{prog}, args...)
		got, err := docker.TerminalCommand(docker.TerminalOptions{GOOS: "windows"}, argv)
		if err != nil || !docker.WindowsRawCmd(got) {
			t.Fatalf("%q: %q %v", args, got, err)
		}
		p, rest := cmdS(t, got[len(got)-1], env)
		if p != prog {
			t.Fatalf("program %q", p)
		}
		if a := argvW(rest); !reflect.DeepEqual(a, args) {
			t.Fatalf("args\n got %q\nwant %q\nline %s", a, args, got[len(got)-1])
		}
	}
	// Exact line for the common case: docker under Program Files, project
	// path with spaces.
	line, err := docker.CmdLine([]string{prog, "-f", `C:\my proj\compose.yaml`, "exec", "web", "sh"})
	if err != nil || line != `"`+prog+`" -f ^"C:\my proj\compose.yaml^" exec web sh` {
		t.Fatalf("line %s %v", line, err)
	}
	for _, bad := range [][]string{nil, {""}, {`C:\a"b.exe`}, {"docker", "a\nb"}} {
		if _, err := docker.CmdLine(bad); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
	if q := docker.WindowsQuote(`a b\`); q != `"a b\\"` {
		t.Fatalf("quote %s", q)
	}
}
