// Package phpgenerate is the plugin "php-generate" (AP17.3): Generate
// (Cmd+N) in PHP classes. It offers the language server's generating code
// actions (implement/override methods, constructors, accessors – whatever
// the server provides, through lsp.codeActions of lang-php) and generates
// a constructor, getters and setters itself from the Tree-sitter index of
// the file when no server is running or as an alternative. Generated code
// is one undoable edit and follows the indentation settings.
package phpgenerate

import (
	"context"
	"errors"
	"regexp"
	"strings"
	"sync"

	"github.com/ape-dev-de/tiegel/internal/api"
	"github.com/ape-dev-de/tiegel/internal/core/module"
	"github.com/ape-dev-de/tiegel/internal/core/settings"
	"github.com/ape-dev-de/tiegel/internal/index"
)

// ID is the module ID.
const ID = "php-generate"

// Built-in generators (GenerateAction.Kind).
const (
	KindConstructor    = "constructor"
	KindGetters        = "getters"
	KindSetters        = "setters"
	KindGettersSetters = "gettersSetters"
)

// Module is the Generate plugin.
type Module struct {
	mu  sync.Mutex
	app *api.App
}

// New returns the plugin.
func New() *Module { return &Module{} }

// ID implements module.Module.
func (m *Module) ID() string { return ID }

// DependsOn implements module.Module (the language server is optional and
// reached through the API).
func (m *Module) DependsOn() []string { return nil }

// Claims implements api.Claimer.
func (m *Module) Claims() []string { return []string{"generate.*"} }

// Start implements module.Module.
func (m *Module) Start(ctx module.Context) error {
	app, ok := api.AppFrom(ctx)
	if !ok {
		return errors.New("php-generate: needs an api.App context")
	}
	m.mu.Lock()
	m.app = app
	m.mu.Unlock()
	p := api.NewProvider(ctx)
	api.Add(p, "generate.list", "Generate (Cmd+N): what can be generated in the class at a position – the language server's code actions and constructor/getters/setters from the syntax tree", m.list)
	api.Add(p, "generate.apply", "Generate a constructor, getters and/or setters for properties of the class at a position (one undoable edit)", m.apply)
	return p.Err()
}

// Stop implements module.Module.
func (m *Module) Stop() error { return nil }

func (m *Module) application() *api.App {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.app
}

// Property is a property of the class that a generator can use.
type Property struct {
	Name     string `json:"name"`
	Type     string `json:"type,omitempty"`
	Static   bool   `json:"static,omitempty"`
	Readonly bool   `json:"readonly,omitempty"`
	// HasGetter and HasSetter report existing accessors.
	HasGetter bool `json:"hasGetter,omitempty"`
	HasSetter bool `json:"hasSetter,omitempty"`
	// Promoted marks a constructor-promoted property.
	Promoted bool `json:"promoted,omitempty"`
}

// GenerateAction is one entry of the Generate menu.
type GenerateAction struct {
	// Source is "builtin" (generate.apply with Kind) or "lsp" (apply
	// with lsp.applyCodeAction and ID).
	Source string `json:"source"`
	Kind   string `json:"kind,omitempty"`
	ID     string `json:"id,omitempty"`
	Title  string `json:"title"`
	// Properties are the candidates of a built-in generator (all are
	// used when generate.apply names none).
	Properties []string `json:"properties,omitempty"`
	Disabled   string   `json:"disabled,omitempty"`
}

// ListParams are the params of generate.list.
type ListParams struct {
	Path string  `json:"path"`
	Pos  api.Pos `json:"pos"`
	// NoLSP skips the language server.
	NoLSP bool `json:"noLsp,omitempty"`
}

// ListResult is the result of generate.list.
type ListResult struct {
	// Class is the FQN of the class at the position ("" outside a class).
	Class      string           `json:"class"`
	Properties []Property       `json:"properties"`
	Actions    []GenerateAction `json:"actions"`
	// LSP tells why no server actions are listed ("" when asked).
	LSP string `json:"lsp,omitempty"`
}

// lspWords pick the server's code actions that generate code.
var lspWords = regexp.MustCompile(`(?i)generat|implement|override|constructor|getter|setter|accessor|mutator|add missing|complete`)

func (m *Module) list(ctx context.Context, p ListParams) (ListResult, error) {
	cls, err := m.classAt(p.Path, p.Pos)
	if err != nil {
		return ListResult{}, err
	}
	res := ListResult{Class: cls.c.FQN, Properties: cls.props, Actions: []GenerateAction{}}
	if !p.NoLSP {
		res.Actions, res.LSP = m.lspActions(ctx, p)
	}
	res.Actions = append(res.Actions, builtinActions(cls)...)
	return res, nil
}

// lspActions asks lang-php for the server's generating code actions.
func (m *Module) lspActions(ctx context.Context, p ListParams) ([]GenerateAction, string) {
	app := m.application()
	var acts []struct {
		ID       string `json:"id"`
		Title    string `json:"title"`
		Kind     string `json:"kind"`
		Disabled string `json:"disabled"`
	}
	err := app.CallLocal(ctx, "lsp.codeActions", map[string]any{"path": p.Path, "start": p.Pos}, &acts)
	if err != nil {
		return []GenerateAction{}, err.Error()
	}
	out := []GenerateAction{}
	for _, a := range acts {
		if strings.HasPrefix(a.Kind, "quickfix") && !lspWords.MatchString(a.Title) {
			continue
		}
		if !lspWords.MatchString(a.Title) && !strings.HasPrefix(a.Kind, "source.generate") {
			continue
		}
		out = append(out, GenerateAction{Source: "lsp", ID: a.ID, Title: a.Title, Kind: a.Kind, Disabled: a.Disabled})
	}
	return out, ""
}

func builtinActions(cls classInfo) []GenerateAction {
	var ctor, get, set []string
	for _, p := range cls.props {
		if !p.Static && !p.Promoted {
			ctor = append(ctor, p.Name)
		}
		if !p.HasGetter {
			get = append(get, p.Name)
		}
		if !p.HasSetter && !p.Readonly {
			set = append(set, p.Name)
		}
	}
	var both []string
	for _, n := range get {
		for _, s := range set {
			if n == s {
				both = append(both, n)
			}
		}
	}
	add := func(out []GenerateAction, kind, title string, props []string, why string) []GenerateAction {
		a := GenerateAction{Source: "builtin", Kind: kind, Title: title, Properties: props}
		if len(props) == 0 {
			a.Disabled = why
		}
		return append(out, a)
	}
	var out []GenerateAction
	if cls.hasCtor {
		out = append(out, GenerateAction{Source: "builtin", Kind: KindConstructor, Title: "Constructor", Disabled: "the class has a constructor"})
	} else {
		out = add(out, KindConstructor, "Constructor", ctor, "no properties to initialise")
	}
	out = add(out, KindGetters, "Getters", get, "every property has a getter")
	out = add(out, KindSetters, "Setters", set, "every writable property has a setter")
	out = add(out, KindGettersSetters, "Getters and Setters", both, "no property lacks both")
	if cls.c.Kind != index.KindClass && cls.c.Kind != index.KindTrait {
		for i := range out {
			if out[i].Disabled == "" {
				out[i].Disabled = "only in classes and traits"
			}
		}
	}
	return out
}

// classInfo is the class at a position with its source.
type classInfo struct {
	file    string
	text    []byte
	c       index.Class
	props   []Property
	hasCtor bool
	methods map[string]bool // lower-case names
}

var readonlyRe = regexp.MustCompile(`\breadonly\b`)

// classAt parses path (open document or file) and returns the class
// containing pos.
func (m *Module) classAt(path string, pos api.Pos) (classInfo, error) {
	if !index.TreeSitter {
		return classInfo{}, api.Errorf(api.CodeUnavailable, "Generate needs Tree-sitter (this build has no cgo)")
	}
	app := m.application()
	if path == "" {
		return classInfo{}, api.Errorf(api.CodeInvalidParams, "path is required")
	}
	abs, err := app.ResolvePath(path)
	if err != nil {
		return classInfo{}, err
	}
	text, _, err := app.ReadText(abs)
	if err != nil {
		return classInfo{}, err
	}
	off, err := pos.ByteOffset(text)
	if err != nil {
		return classInfo{}, err
	}
	f, err := parse(abs, text)
	if err != nil {
		return classInfo{}, api.Errorf(api.CodeFailed, "%v", err)
	}
	var best *index.Class
	for i := range f.Classes {
		c := &f.Classes[i]
		if c.Range.Start.Offset <= off && off <= c.Range.End.Offset && (best == nil || c.Range.Start.Offset > best.Range.Start.Offset) {
			best = c
		}
	}
	if best == nil {
		return classInfo{}, api.Errorf(api.CodeNotFound, "no class at this position")
	}
	ci := classInfo{file: abs, text: text, c: *best, methods: map[string]bool{}}
	var methodRanges []index.Range
	for _, mem := range best.Members {
		if mem.Kind == index.KindMethod {
			ci.methods[strings.ToLower(mem.Name)] = true
			methodRanges = append(methodRanges, mem.Range)
			if strings.EqualFold(mem.Name, "__construct") {
				ci.hasCtor = true
			}
		}
	}
	classReadonly := readonlyRe.Match(text[best.Range.Start.Offset:best.NameRange.Start.Offset])
	for _, mem := range best.Members {
		if mem.Kind != index.KindProperty {
			continue
		}
		p := Property{Name: mem.Name, Type: mem.Detail, Static: mem.Static}
		for _, r := range methodRanges {
			if r.Start.Offset <= mem.Range.Start.Offset && mem.Range.End.Offset <= r.End.Offset {
				p.Promoted = true
			}
		}
		decl := text[mem.Range.Start.Offset:mem.NameRange.Start.Offset]
		p.Readonly = classReadonly || readonlyRe.Match(decl)
		p.HasGetter = ci.methods[strings.ToLower(getterName(p))] || ci.methods["get"+strings.ToLower(p.Name)]
		p.HasSetter = ci.methods["set"+strings.ToLower(p.Name)]
		ci.props = append(ci.props, p)
	}
	return ci, nil
}

// parse indexes text as path with a private index (unsaved edits count).
func parse(path string, text []byte) (*index.File, error) {
	ix := index.New(index.Options{IncludeIgnored: true, Workers: 1})
	defer ix.Close()
	if _, err := ix.UpdateText(path, text); err != nil {
		return nil, err
	}
	f, ok := ix.File(path)
	if !ok {
		return nil, errors.New("file not indexed")
	}
	return f, nil
}

// indentUnit is one indentation level from the editor settings.
func (m *Module) indentUnit() string {
	app := m.application()
	if app == nil || !api.Setting(app, settings.KeyEditorInsertSpace, true) {
		return "\t"
	}
	return strings.Repeat(" ", max(api.Setting(app, settings.KeyEditorTabWidth, 4), 1))
}
