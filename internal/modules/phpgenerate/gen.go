package phpgenerate

import (
	"context"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/ape-dev-de/tiegel/internal/api"
	"github.com/ape-dev-de/tiegel/internal/index"
)

// ApplyParams are the params of generate.apply.
type ApplyParams struct {
	Path string  `json:"path"`
	Pos  api.Pos `json:"pos"`
	// Kind is constructor, getters, setters or gettersSetters.
	Kind string `json:"kind"`
	// Properties limits the generator to these properties (default: all
	// candidates, see generate.list).
	Properties []string `json:"properties,omitempty"`
	// Apply writes the code (default true).
	Apply *bool `json:"apply,omitempty"`
}

// ApplyResult is the result of generate.apply.
type ApplyResult struct {
	Class string `json:"class"`
	// Offset is where Text is inserted (in the text before the change).
	Offset int    `json:"offset"`
	Text   string `json:"text"`
	// Methods are the generated method names.
	Methods []string `json:"methods"`
	// Caret is a suggested caret offset after the change (inside the
	// first generated method).
	Caret   int    `json:"caret"`
	Applied bool   `json:"applied"`
	Version uint64 `json:"version,omitempty"`
}

func (m *Module) apply(ctx context.Context, p ApplyParams) (ApplyResult, error) {
	cls, err := m.classAt(p.Path, p.Pos)
	if err != nil {
		return ApplyResult{}, err
	}
	var action *GenerateAction
	acts := builtinActions(cls)
	for i := range acts {
		if acts[i].Kind == p.Kind {
			action = &acts[i]
		}
	}
	if action == nil {
		return ApplyResult{}, api.Errorf(api.CodeInvalidParams, "kind must be %s, %s, %s or %s", KindConstructor, KindGetters, KindSetters, KindGettersSetters)
	}
	if action.Disabled != "" {
		return ApplyResult{}, api.Errorf(api.CodeNotFound, "%s: %s", action.Title, action.Disabled)
	}
	names := action.Properties
	if len(p.Properties) > 0 {
		names = nil
		for _, want := range p.Properties {
			want = strings.TrimPrefix(want, "$")
			found := false
			for _, n := range action.Properties {
				if n == want {
					names, found = append(names, n), true
				}
			}
			if !found {
				return ApplyResult{}, api.Errorf(api.CodeInvalidParams, "%s: property %q is not a candidate (candidates: %s)", action.Title, want, strings.Join(action.Properties, ", "))
			}
		}
	}
	props := map[string]Property{}
	for _, pr := range cls.props {
		props[pr.Name] = pr
	}
	unit := m.indentUnit()
	ind := lineIndent(cls.text, cls.c.Range.Start.Offset) + unit
	var methods []string
	var names2 []string
	switch p.Kind {
	case KindConstructor:
		methods = append(methods, constructor(names, props, ind, unit))
		names2 = append(names2, "__construct")
	default:
		for _, n := range names {
			pr := props[n]
			if p.Kind != KindSetters && !pr.HasGetter {
				methods = append(methods, getter(pr, ind, unit))
				names2 = append(names2, getterName(pr))
			}
			if p.Kind != KindGetters && !pr.HasSetter && !pr.Readonly {
				methods = append(methods, setter(pr, ind, unit))
				names2 = append(names2, "set"+upperFirst(pr.Name))
			}
		}
	}
	off, text := insertion(cls, p.Kind == KindConstructor, methods)
	res := ApplyResult{Class: cls.c.FQN, Offset: off, Text: text, Methods: names2}
	res.Caret = off + strings.Index(text, "{\n") + 2
	if p.Apply != nil && !*p.Apply {
		return res, nil
	}
	app := m.application()
	steps, err := app.ApplyFileOps(ctx, "Generate "+action.Title, []api.FileOp{{
		Kind: api.FileOpEdit, Path: cls.file,
		Changes: func(cur []byte) ([]api.TextChange, error) {
			if string(cur) != string(cls.text) {
				return nil, api.Errorf(api.CodeConflict, "the file changed while generating; try again")
			}
			return []api.TextChange{{Start: off, End: off, Text: text}}, nil
		},
	}})
	if err != nil {
		return ApplyResult{}, err
	}
	res.Applied = true
	if len(steps) > 0 {
		res.Version = steps[0].Version
	}
	return res, nil
}

// insertion returns where and what to insert: a constructor after the
// last property, constant or trait use before the first method (else at
// the body start), accessors before the closing brace.
func insertion(cls classInfo, ctor bool, methods []string) (int, string) {
	body := strings.Join(methods, "\n\n")
	text := cls.text
	open := strings.IndexByte(string(text[cls.c.NameRange.End.Offset:cls.c.Range.End.Offset]), '{')
	bodyStart := cls.c.NameRange.End.Offset + open + 1 // after "{"
	closing := cls.c.Range.End.Offset - 1              // the "}"
	if ctor {
		after := -1
		for _, mem := range cls.c.Members {
			if mem.Kind == index.KindMethod {
				break
			}
			after = max(after, mem.Range.End.Offset)
		}
		// inline breaks the line at p (before the next code on it) when
		// the end of p's line is no safe place: it lies behind the
		// closing brace ("class A { private $x; }") or inside a member
		// that starts on the same line.
		inline := func(p int) (int, string, bool) {
			eol := lineEnd(text, p)
			unsafe := eol > closing
			for _, mem := range cls.c.Members {
				if s := mem.Range.Start.Offset; s >= p && s < eol {
					unsafe = true
				}
			}
			if !unsafe {
				return 0, "", false
			}
			for p < closing && (text[p] == ' ' || text[p] == '\t') {
				p++
			}
			return p, "\n" + body + "\n" + lineIndent(text, cls.c.Range.Start.Offset), true
		}
		if after > 0 {
			if off, s, ok := inline(after); ok {
				return off, s
			}
			// After the member's line; a blank line before the
			// constructor, the following code keeps its own.
			return lineEnd(text, after), "\n\n" + body
		}
		if len(cls.c.Members) > 0 || strings.TrimSpace(string(text[bodyStart:closing])) != "" {
			if off, s, ok := inline(bodyStart); ok {
				return off, s
			}
			return lineEnd(text, bodyStart), "\n" + body + "\n"
		}
	}
	ls := lineStart(text, closing)
	if strings.TrimSpace(string(text[ls:closing])) != "" || closing < bodyStart || ls <= bodyStart {
		// "{}" or "{ … }" on one line: open the body.
		return closing, "\n" + body + "\n" + lineIndent(text, cls.c.Range.Start.Offset)
	}
	if strings.TrimSpace(string(text[bodyStart:closing])) == "" {
		return ls, body + "\n"
	}
	return ls, "\n" + body + "\n"
}

func lineStart(text []byte, off int) int {
	for off > 0 && text[off-1] != '\n' {
		off--
	}
	return off
}

func lineEnd(text []byte, off int) int {
	for off < len(text) && text[off] != '\n' {
		off++
	}
	return off
}

func lineIndent(text []byte, off int) string {
	ls := lineStart(text, off)
	i := ls
	for i < len(text) && (text[i] == ' ' || text[i] == '\t') {
		i++
	}
	return string(text[ls:i])
}

// upperFirst upper-cases the first rune of s. Invalid UTF-8 at the start
// is left unchanged.
func upperFirst(s string) string {
	r, size := utf8.DecodeRuneInString(s)
	if r == utf8.RuneError {
		return s
	}
	return string(unicode.ToUpper(r)) + s[size:]
}

// getterName is isX for bool properties, getX otherwise.
func getterName(p Property) string {
	if t := strings.TrimPrefix(strings.ToLower(p.Type), "?"); t == "bool" {
		return "is" + upperFirst(p.Name)
	}
	return "get" + upperFirst(p.Name)
}

func typed(t, s string) string {
	if t == "" {
		return s
	}
	return t + " " + s
}

func access(p Property) string {
	if p.Static {
		return "self::$" + p.Name
	}
	return "$this->" + p.Name
}

func static(p Property) string {
	if p.Static {
		return "static "
	}
	return ""
}

func constructor(names []string, props map[string]Property, ind, unit string) string {
	params := make([]string, len(names))
	var body strings.Builder
	for i, n := range names {
		p := props[n]
		params[i] = typed(p.Type, "$"+n)
		body.WriteString(ind + unit + "$this->" + n + " = $" + n + ";\n")
	}
	return ind + "public function __construct(" + strings.Join(params, ", ") + ")\n" + ind + "{\n" + body.String() + ind + "}"
}

func getter(p Property, ind, unit string) string {
	ret := ""
	if p.Type != "" {
		ret = ": " + p.Type
	}
	return ind + "public " + static(p) + "function " + getterName(p) + "()" + ret + "\n" + ind + "{\n" +
		ind + unit + "return " + access(p) + ";\n" + ind + "}"
}

func setter(p Property, ind, unit string) string {
	return ind + "public " + static(p) + "function set" + upperFirst(p.Name) + "(" + typed(p.Type, "$"+p.Name) + "): void\n" + ind + "{\n" +
		ind + unit + access(p) + " = $" + p.Name + ";\n" + ind + "}"
}
