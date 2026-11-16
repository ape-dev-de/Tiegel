package controller

import (
	"context"
	"encoding/json"
	"fmt"
	"path"
	"strings"
	"time"

	"github.com/ape-dev-de/tiegel/internal/api"
)

// CodeNav is the state of code navigation in the GUI (D160): jumps to
// definitions, implementations and super methods (nav.*, index and
// language server combined), the type hierarchy, the gutter marks of the
// PHP index, symbol search (Go to Class/Symbol, Search Everywhere) and the
// index progress. The LSP features (usages, highlights, context actions,
// reformat, rename), the structure view and Find in Files live in
// codeintel.go and find.go on the same type.
//
// Like Controller it belongs to the UI goroutine; blocking calls run in
// the background and their results are applied by Controller.Drain.
type CodeNav struct {
	c     *Controller
	unsub func()

	// Choice is a pending choice between several jump targets (popup).
	Choice        *TargetChoice
	ChoiceVersion uint64

	// Hierarchy is the last loaded type hierarchy.
	Hierarchy        *TypeHierarchy
	HierarchyVersion uint64

	// Index is the state of the PHP index (status bar).
	Index        IndexState
	IndexVersion uint64

	// GutterDelay debounces reloading gutter marks after edits and index
	// updates (tests shorten it).
	GutterDelay   time.Duration
	gutter        map[string]*fileGutter
	GutterVersion uint64

	// Symbols is the result of the last SearchSymbols.
	Symbols        SymbolResult
	SymbolsVersion uint64
	symSeq         uint64

	// LSP features (codeintel.go).
	Usages            *Usages
	UsagesVersion     uint64
	Highlights        Highlights
	HighlightsVersion uint64
	hlSeq             uint64
	Actions           *CodeActions
	ActionsVersion    uint64
	Rename            *RenameState
	RenameVersion     uint64
	Structure         *Structure
	StructureVersion  uint64
	structSeq         uint64

	// Find in Files (find.go).
	Find        *FindState
	FindVersion uint64
	findSeq     uint64
}

// NavTarget is a jump target of nav.* (index and/or language server) or
// a symbol of nav.symbols.
type NavTarget struct {
	// Path is the display path, File the absolute path.
	Path  string    `json:"path"`
	File  string    `json:"file"`
	Range TextRange `json:"range"`
	// Kind: class, interface, trait, enum, method, property,
	// classConstant, enumCase, function, constant ("" for LSP results).
	Kind      string `json:"kind,omitempty"`
	Name      string `json:"name,omitempty"`
	FQN       string `json:"fqn,omitempty"`
	Container string `json:"container,omitempty"`
	Detail    string `json:"detail,omitempty"`
	Library   bool   `json:"library,omitempty"`
	// Source is "index", "lsp" or "index+lsp".
	Source string `json:"source,omitempty"`
}

// Location returns the target as a Location.
func (t NavTarget) Location() Location {
	return Location{Path: t.Path, File: t.File, Range: t.Range}
}

// Label is the text shown for the target: "Class::member", the FQN of a
// class, or the file and line for plain LSP locations.
func (t NavTarget) Label() string {
	switch {
	case t.Container != "" && t.Name != "":
		return shortName(t.Container) + "::" + t.Name
	case t.FQN != "":
		return t.FQN
	case t.Name != "":
		return t.Name
	}
	return fmt.Sprintf("%s:%d", path.Base(t.Path), t.Range.Start.Line)
}

// Where is the location text "path:line".
func (t NavTarget) Where() string { return fmt.Sprintf("%s:%d", t.Path, t.Range.Start.Line) }

// shortName returns the last segment of a PHP FQN.
func shortName(fqn string) string {
	if i := strings.LastIndexByte(fqn, '\\'); i >= 0 {
		return fqn[i+1:]
	}
	return fqn
}

// navResult is the result of nav.definition/implementations/super.
type navResult struct {
	Locations []NavTarget `json:"locations"`
	Complete  bool        `json:"complete"`
	LSP       bool        `json:"lsp"`
	LSPError  string      `json:"lspError"`
}

// navParams are the params of the position based nav.* methods.
type navParams struct {
	Path   string `json:"path"`
	At     pos    `json:"at"`
	Source string `json:"source,omitempty"`
}

// Jump kinds of Navigate.
const (
	JumpDefinition      = "definition"
	JumpImplementations = "implementations"
	JumpSuper           = "super"
)

// TargetChoice is a choice between several jump targets.
type TargetChoice struct {
	Title string
	// From is the location the jump started at (history).
	From    Location
	Targets []NavTarget
}

// IndexState is the progress of the PHP index.
type IndexState struct {
	// Phase: "scanning", "indexing", "ready" ("" before the first event).
	Phase       string
	Done, Total int
}

// Busy reports whether the index is being built.
func (s IndexState) Busy() bool { return s.Phase == "scanning" || s.Phase == "indexing" }

// GutterMark is a gutter entry of the PHP index: the declaration at Line
// (1-based) implements/overrides Up and is implemented/overridden by Down.
type GutterMark struct {
	Line      int         `json:"line"`
	Kind      string      `json:"kind"`
	FQN       string      `json:"fqn"`
	Range     TextRange   `json:"range"`
	Up        []NavTarget `json:"up"`
	Down      []NavTarget `json:"down"`
	UpTotal   int         `json:"upTotal"`
	DownTotal int         `json:"downTotal"`
}

type fileGutter struct {
	marks   map[int]GutterMark
	loading bool
	// gen debounces scheduled reloads.
	gen uint64
}

// TypeHierarchy is the result of nav.typeHierarchy.
type TypeHierarchy struct {
	Class      NavTarget       `json:"class"`
	Supertypes []HierarchyNode `json:"supertypes"`
	Subtypes   []HierarchyNode `json:"subtypes"`
	Truncated  bool            `json:"truncated"`
}

// HierarchyNode is a type with its supertypes or subtypes.
type HierarchyNode struct {
	NavTarget
	Children []HierarchyNode `json:"children"`
}

// SymbolResult is the result of SearchSymbols.
type SymbolResult struct {
	Query string
	Kinds []string
	Items []NavTarget
	// Complete is false while the index is still being built.
	Complete bool
	Err      string
}

// NewCodeNav returns the code navigation state of c and subscribes to the
// index and search events. Call Close when done.
func NewCodeNav(c *Controller) *CodeNav {
	n := &CodeNav{c: c, GutterDelay: 400 * time.Millisecond, gutter: map[string]*fileGutter{}}
	unsub, err := c.be.Subscribe(c.ctx, []string{"index.*", "search.*", api.TopicDocEdited}, func(e api.Event) {
		c.post(func() { n.handleEvent(e.Topic, e.Payload) })
	})
	if err != nil {
		unsub = func() {}
	}
	n.unsub = unsub
	return n
}

// Close ends the event subscription.
func (n *CodeNav) Close() { n.unsub() }

// Reset drops all project state (another project was opened).
func (n *CodeNav) Reset() {
	n.Choice, n.Hierarchy, n.Usages, n.Actions, n.Rename, n.Structure, n.Find = nil, nil, nil, nil, nil, nil, nil
	n.Highlights = Highlights{}
	n.Index = IndexState{}
	n.gutter = map[string]*fileGutter{}
	n.Symbols = SymbolResult{}
	n.ChoiceVersion++
	n.HierarchyVersion++
	n.UsagesVersion++
	n.HighlightsVersion++
	n.ActionsVersion++
	n.RenameVersion++
	n.StructureVersion++
	n.FindVersion++
	n.GutterVersion++
	n.IndexVersion++
	n.SymbolsVersion++
}

func (n *CodeNav) handleEvent(topic string, payload json.RawMessage) {
	switch topic {
	case "index.progress":
		var p struct {
			Phase string `json:"phase"`
			Done  int    `json:"done"`
			Total int    `json:"total"`
		}
		if json.Unmarshal(payload, &p) == nil {
			n.Index = IndexState{Phase: p.Phase, Done: p.Done, Total: p.Total}
			n.IndexVersion++
		}
	case "index.updated":
		var u struct {
			Files     []string `json:"files"`
			Truncated bool     `json:"truncated"`
			Reason    string   `json:"reason"`
		}
		if json.Unmarshal(payload, &u) != nil {
			return
		}
		all := u.Truncated || u.Reason == "sync" || u.Reason == "cache"
		changed := map[string]bool{}
		for _, f := range u.Files {
			changed[n.c.Abs(f)] = true
		}
		for file := range n.gutter {
			// Implementations elsewhere change the marks of this file
			// too, so every loaded file is refreshed after a change of
			// declarations; the debounce keeps it cheap.
			if all || changed[file] || len(changed) > 0 {
				n.scheduleGutter(file)
			}
		}
		if n.Index.Phase != "ready" && u.Reason != "doc" {
			n.Index.Phase = "ready"
			n.IndexVersion++
		}
	case api.TopicDocEdited:
		var e struct {
			File string `json:"file"`
			Path string `json:"path"`
		}
		if json.Unmarshal(payload, &e) == nil {
			file := e.File
			if file == "" {
				file = n.c.Abs(e.Path)
			}
			if _, ok := n.gutter[file]; ok {
				n.scheduleGutter(file)
			}
			if n.Highlights.File == file {
				n.Highlights = Highlights{}
				n.HighlightsVersion++
			}
		}
	case api.TopicSearchResults:
		n.handleSearchEvent(payload)
	}
}

// LoadIndexStatus reads the current index state (the window may open
// while the index is already being built).
func (n *CodeNav) LoadIndexStatus() {
	n.c.Go(func(ctx context.Context) func() {
		var st struct {
			State    string `json:"state"`
			Progress struct {
				Phase string `json:"phase"`
				Done  int    `json:"done"`
				Total int    `json:"total"`
			} `json:"progress"`
		}
		if err := n.c.call(ctx, "index.status", nil, &st); err != nil {
			return nil
		}
		return func() {
			if n.Index.Phase != "" {
				return // events were faster
			}
			switch st.State {
			case "ready":
				n.Index = IndexState{Phase: "ready", Done: st.Progress.Done, Total: st.Progress.Total}
			case "indexing", "loading":
				n.Index = IndexState{Phase: orStr(st.Progress.Phase, "indexing"), Done: st.Progress.Done, Total: st.Progress.Total}
			default:
				return
			}
			n.IndexVersion++
		}
	})
}

func orStr(s, def string) string {
	if s == "" {
		return def
	}
	return s
}

// unsupported reports errors meaning "this backend cannot do it" (module
// disabled, method missing, built without Tree-sitter).
func unsupported(err error) bool {
	switch api.ErrorCode(err) {
	case api.CodeMethodNotFound, api.CodeModuleDisabled, api.CodeUnavailable:
		return true
	}
	return false
}

// Navigate jumps from cur to the definition, the implementations or the
// super declarations (kind JumpDefinition, JumpImplementations,
// JumpSuper) of the symbol there. One target is opened directly (the jump
// is recorded in the history); several become Choice. Without the index
// (no cgo, module disabled) definitions and implementations fall back to
// the language server.
func (n *CodeNav) Navigate(cur Location, kind string) {
	c := n.c
	c.Go(func(ctx context.Context) func() {
		var r navResult
		err := c.call(ctx, "nav."+kind, navParams{Path: cur.File, At: pos{Offset: cur.Range.Start.Offset}}, &r)
		return func() {
			if err != nil {
				if unsupported(err) && kind != JumpSuper && c.Status.LSPRunning {
					method := "lsp.definition"
					if kind == JumpImplementations {
						method = "lsp.implementation"
					}
					c.GoToDefinition(cur, method)
					return
				}
				c.notice(err)
				return
			}
			n.jump(cur, kind, r)
		}
	})
}

// jump applies a nav.* result.
func (n *CodeNav) jump(cur Location, kind string, r navResult) {
	c := n.c
	for i := range r.Locations {
		r.Locations[i] = n.normTarget(r.Locations[i])
	}
	switch len(r.Locations) {
	case 0:
		msg := map[string]string{JumpDefinition: "No definition found", JumpImplementations: "No implementations found", JumpSuper: "No super method or type found"}[kind]
		if !r.Complete {
			msg += " (the index is still being built)"
		}
		c.Status.Notice = msg
	case 1:
		c.GoTo(cur, r.Locations[0].Location())
	default:
		title := map[string]string{JumpDefinition: "Choose Declaration", JumpImplementations: "Choose Implementation", JumpSuper: "Choose Super Declaration"}[kind]
		n.Choose(title, cur, r.Locations)
	}
}

// Choose offers targets to choose from (popup list).
func (n *CodeNav) Choose(title string, from Location, targets []NavTarget) {
	n.Choice = &TargetChoice{Title: title, From: from, Targets: targets}
	n.ChoiceVersion++
}

func (n *CodeNav) normTarget(t NavTarget) NavTarget {
	if t.File == "" {
		t.File = n.c.Abs(t.Path)
	}
	if t.Path == "" {
		t.Path = n.c.Rel(t.File)
	}
	return t
}

// ChooseTarget jumps to target i of Choice and closes the choice.
func (n *CodeNav) ChooseTarget(i int) {
	ch := n.Choice
	n.CancelChoice()
	if ch == nil || i < 0 || i >= len(ch.Targets) {
		return
	}
	n.c.GoTo(ch.From, ch.Targets[i].Location())
}

// CancelChoice closes the choice without jumping.
func (n *CodeNav) CancelChoice() {
	if n.Choice != nil {
		n.Choice = nil
		n.ChoiceVersion++
	}
}

// ---- type hierarchy ----------------------------------------------------------

// LoadTypeHierarchy loads the hierarchy of the class at cur.
func (n *CodeNav) LoadTypeHierarchy(cur Location) {
	c := n.c
	c.Go(func(ctx context.Context) func() {
		var h TypeHierarchy
		err := c.call(ctx, "nav.typeHierarchy", navParams{Path: cur.File, At: pos{Offset: cur.Range.Start.Offset}}, &h)
		return func() {
			if err != nil {
				c.notice(err)
				return
			}
			h.Class = n.normTarget(h.Class)
			n.normNodes(h.Supertypes)
			n.normNodes(h.Subtypes)
			n.Hierarchy = &h
			n.HierarchyVersion++
		}
	})
}

func (n *CodeNav) normNodes(ns []HierarchyNode) {
	for i := range ns {
		if ns[i].File != "" || ns[i].Path != "" {
			ns[i].NavTarget = n.normTarget(ns[i].NavTarget)
		}
		n.normNodes(ns[i].Children)
	}
}

// ---- gutter -----------------------------------------------------------------

// isPHP reports whether file is a PHP source.
func isPHP(file string) bool {
	return strings.EqualFold(path.Ext(file), ".php")
}

// GutterMarks returns the index marks of file by 1-based line, loading
// them on first use (nil until loaded).
func (n *CodeNav) GutterMarks(file string) map[int]GutterMark {
	if !isPHP(file) || !n.c.Project.Open {
		return nil
	}
	g := n.gutter[file]
	if g == nil {
		g = &fileGutter{}
		n.gutter[file] = g
		n.loadGutter(file)
	}
	return g.marks
}

// ForgetGutter drops the marks of a closed file.
func (n *CodeNav) ForgetGutter(file string) { delete(n.gutter, file) }

func (n *CodeNav) scheduleGutter(file string) {
	g := n.gutter[file]
	if g == nil {
		return
	}
	g.gen++
	gen := g.gen
	time.AfterFunc(n.GutterDelay, func() {
		n.c.post(func() {
			if cur := n.gutter[file]; cur == g && g.gen == gen {
				n.loadGutter(file)
			}
		})
	})
}

func (n *CodeNav) loadGutter(file string) {
	g := n.gutter[file]
	if g == nil || g.loading {
		if g != nil {
			// Load again when the running request ends.
			g.gen++
		}
		return
	}
	g.loading = true
	gen := g.gen
	c := n.c
	c.Go(func(ctx context.Context) func() {
		var r struct {
			Marks []GutterMark `json:"marks"`
		}
		err := c.call(ctx, "nav.gutter", pathParams{Path: file}, &r)
		return func() {
			cur := n.gutter[file]
			if cur != g {
				return
			}
			g.loading = false
			if err == nil {
				g.marks = map[int]GutterMark{}
				for _, m := range r.Marks {
					for i := range m.Up {
						m.Up[i] = n.normTarget(m.Up[i])
					}
					for i := range m.Down {
						m.Down[i] = n.normTarget(m.Down[i])
					}
					g.marks[m.Line] = m
				}
				n.GutterVersion++
			} else if g.marks == nil {
				g.marks = map[int]GutterMark{}
			}
			if g.gen != gen && err == nil {
				n.loadGutter(file)
			}
		}
	})
}

// GutterJump opens the targets of the mark at line (1-based) of file: up
// (implemented/overridden declarations) or down (implementations,
// overrides). One target is opened directly, several are offered.
func (n *CodeNav) GutterJump(cur Location, line int, up bool) bool {
	g := n.gutter[cur.File]
	if g == nil {
		return false
	}
	m, ok := g.marks[line]
	if !ok {
		return false
	}
	targets, title := m.Down, "Implementations of "+shortName(m.FQN)
	if up {
		targets, title = m.Up, "Super declarations of "+shortName(m.FQN)
	}
	switch len(targets) {
	case 0:
		return false
	case 1:
		n.c.GoTo(cur, targets[0].Location())
	default:
		n.Choose(title, cur, targets)
	}
	return true
}

// ---- symbols ----------------------------------------------------------------

// SearchSymbols searches the index (nav.symbols; kinds "type" for classes)
// and stores the result in Symbols. Older answers are dropped. Without the
// index the language server's workspace symbols are used.
func (n *CodeNav) SearchSymbols(query string, kinds []string, limit int) {
	n.symSeq++
	seq := n.symSeq
	c := n.c
	if strings.TrimSpace(query) == "" {
		n.Symbols = SymbolResult{Query: query, Kinds: kinds, Complete: true}
		n.SymbolsVersion++
		return
	}
	lsp := c.Status.LSPRunning
	c.Go(func(ctx context.Context) func() {
		var r struct {
			Symbols  []NavTarget `json:"symbols"`
			Complete bool        `json:"complete"`
		}
		err := c.call(ctx, "nav.symbols", map[string]any{"query": query, "kinds": kinds, "limit": limit}, &r)
		if err != nil && unsupported(err) && lsp {
			var ws []struct {
				Name      string     `json:"name"`
				Kind      string     `json:"kind"`
				Container string     `json:"container"`
				Path      string     `json:"path"`
				File      string     `json:"file"`
				Range     *TextRange `json:"range"`
			}
			if err = c.call(ctx, "lsp.workspaceSymbols", map[string]any{"query": query, "limit": limit}, &ws); err == nil {
				r.Complete = true
				r.Symbols = r.Symbols[:0]
				for _, w := range ws {
					t := NavTarget{Name: w.Name, Kind: strings.ToLower(w.Kind), Container: w.Container, Path: w.Path, File: w.File, Source: "lsp"}
					if w.Range != nil {
						t.Range = *w.Range
					}
					if len(kinds) > 0 && kinds[0] == "type" && !isTypeKind(t.Kind) {
						continue
					}
					r.Symbols = append(r.Symbols, t)
				}
			}
		}
		return func() {
			if seq != n.symSeq {
				return
			}
			res := SymbolResult{Query: query, Kinds: kinds, Complete: r.Complete}
			if err != nil {
				res.Err = err.Error()
			}
			for _, s := range r.Symbols {
				res.Items = append(res.Items, n.normTarget(s))
			}
			n.Symbols = res
			n.SymbolsVersion++
		}
	})
}

func isTypeKind(k string) bool {
	switch k {
	case "class", "interface", "trait", "enum", "struct":
		return true
	}
	return false
}

// OpenTarget opens a symbol target from cur (history).
func (n *CodeNav) OpenTarget(cur Location, t NavTarget) {
	t = n.normTarget(t)
	if t.Range.Start.Offset == 0 && t.Range.Start.Line > 1 {
		n.c.GoTo(cur, AtLine(t.File, t.Range.Start.Line))
		return
	}
	n.c.GoTo(cur, t.Location())
}
