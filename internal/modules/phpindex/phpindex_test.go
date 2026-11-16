//go:build cgo

package phpindex

import (
	"context"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/ape-dev-de/tiegel/internal/api"
	"github.com/ape-dev-de/tiegel/internal/core/bus"
	"github.com/ape-dev-de/tiegel/internal/core/module"
	"github.com/ape-dev-de/tiegel/internal/modules/modtest"
)

var files = map[string]string{
	"src/Shape.php":       "<?php\nnamespace App;\n\ninterface Shape\n{\n    public function area(): float;\n}\n",
	"src/Square.php":      "<?php\nnamespace App;\n\nclass Square implements Shape\n{\n    public function area(): float { return 1.0; }\n}\n",
	"src/Use.php":         "<?php\nnamespace App\\Other;\n\nuse App\\Square;\n\nfunction make(): Square\n{\n    return new Square();\n}\n",
	"vendor/lib/Base.php": "<?php\nnamespace Lib;\n\nabstract class Base\n{\n    abstract public function run(): void;\n}\n",
	".gitignore":          "vendor/\n",
}

func newApp(t *testing.T, opts Options, mod func(*api.Options)) (*api.App, *Module) {
	t.Helper()
	m := New(opts)
	a := modtest.NewApp(t, []module.Module{m}, mod)
	return a, m
}

func fqnsOf(locs []Location) []string {
	var out []string
	for _, l := range locs {
		out = append(out, l.FQN+"@"+l.Source)
	}
	slices.Sort(out)
	return out
}

func TestNavigation(t *testing.T) {
	a, _ := newApp(t, Options{}, nil)
	dir := modtest.Project(t, a, files)
	updated := modtest.Record(t, a, TopicUpdated)

	var st Status
	modtest.Call(t, a, "index.status", StatusParams{Wait: true}, &st)
	if st.State != "ready" || st.Files != 4 || st.Stats.Libraries != 1 || !st.Libraries || st.Root != dir || st.LastSync == "" {
		t.Fatalf("status %+v", st)
	}

	var res NavResult
	modtest.Call(t, a, "nav.definition", PosParams{Path: "src/Use.php", At: api.Pos{Line: 8, Col: 17}}, &res)
	if len(res.Locations) != 1 || !res.Complete || res.LSP {
		t.Fatalf("definition %+v", res)
	}
	l := res.Locations[0]
	if l.FQN != `App\Square` || l.Path != "src/Square.php" || l.File != filepath.Join(dir, "src/Square.php") || l.Range.Start.Line != 4 || l.Range.Start.Col != 7 || l.Kind != "class" || l.Source != "index" || l.DeclRange == nil {
		t.Fatalf("location %+v", l)
	}

	modtest.Call(t, a, "nav.implementations", PosParams{Path: "src/Shape.php", At: api.Pos{Line: 6, Col: 22}, Source: "index"}, &res)
	if got := fqnsOf(res.Locations); !slices.Equal(got, []string{`App\Square::area@index`}) {
		t.Fatalf("implementations %q", got)
	}
	modtest.Call(t, a, "nav.super", PosParams{Path: "src/Square.php", At: api.Pos{Line: 6, Col: 22}}, &res)
	if got := fqnsOf(res.Locations); !slices.Equal(got, []string{`App\Shape::area@index`}) {
		t.Fatalf("super %q", got)
	}

	var h HierarchyResult
	modtest.Call(t, a, "nav.typeHierarchy", HierarchyParams{FQN: `App\Shape`}, &h)
	if h.Class.FQN != `App\Shape` || len(h.Subtypes) != 1 || h.Subtypes[0].FQN != `App\Square` || !h.Complete {
		t.Fatalf("hierarchy %+v", h)
	}
	modtest.Call(t, a, "nav.typeHierarchy", HierarchyParams{Path: "src/Use.php", At: &api.Pos{Line: 8, Col: 17}}, &h)
	if h.Class.FQN != `App\Square` || len(h.Supertypes) != 1 {
		t.Fatalf("hierarchy at %+v", h)
	}
	modtest.WantCode(t, a, "nav.typeHierarchy", HierarchyParams{FQN: "Nope"}, api.CodeNotFound)
	modtest.WantCode(t, a, "nav.typeHierarchy", HierarchyParams{}, api.CodeInvalidParams)
	modtest.WantCode(t, a, "nav.typeHierarchy", HierarchyParams{Path: "src/Use.php", At: &api.Pos{Line: 1}}, api.CodeNotFound)
	modtest.WantCode(t, a, "nav.typeHierarchy", HierarchyParams{Path: "missing.php", At: &api.Pos{Line: 1}}, api.CodeNotFound)

	var sy SymbolsResult
	modtest.Call(t, a, "nav.symbols", SymbolsParams{Query: "sq", Kinds: []string{"type"}}, &sy)
	if len(sy.Symbols) != 1 || sy.Symbols[0].FQN != `App\Square` || !sy.Complete {
		t.Fatalf("symbols %+v", sy)
	}
	modtest.Call(t, a, "nav.symbols", SymbolsParams{Query: "run"}, &sy)
	if len(sy.Symbols) != 1 || !sy.Symbols[0].Library {
		t.Fatalf("library symbols %+v", sy)
	}
	no := false
	modtest.Call(t, a, "nav.symbols", SymbolsParams{Query: "run", Libraries: &no}, &sy)
	if len(sy.Symbols) != 0 {
		t.Fatalf("libraries=false %+v", sy)
	}
	modtest.Call(t, a, "nav.symbols", SymbolsParams{Query: "", Kinds: []string{"function"}}, &sy)
	if len(sy.Symbols) != 1 || sy.Symbols[0].Kind != "function" {
		t.Fatalf("functions %+v", sy)
	}
	modtest.WantCode(t, a, "nav.symbols", SymbolsParams{Kinds: []string{"bogus"}}, api.CodeInvalidParams)

	var g GutterResult
	modtest.Call(t, a, "nav.gutter", GutterParams{Path: "src/Shape.php"}, &g)
	if g.Path != "src/Shape.php" || len(g.Marks) != 2 || g.Marks[0].Line != 4 || g.Marks[0].DownTotal != 1 || g.Marks[1].Kind != "method" || g.Marks[1].Down[0].Path != "src/Square.php" {
		t.Fatalf("gutter %+v", g)
	}
	modtest.WantCode(t, a, "nav.gutter", GutterParams{}, api.CodeInvalidParams)

	// errors
	modtest.WantCode(t, a, "nav.definition", PosParams{}, api.CodeInvalidParams)
	modtest.WantCode(t, a, "nav.definition", PosParams{Path: "src/Use.php", At: api.Pos{Line: 99}}, api.CodeInvalidParams)
	modtest.WantCode(t, a, "nav.definition", PosParams{Path: "src/Use.php", Source: "x"}, api.CodeInvalidParams)
	modtest.WantCode(t, a, "nav.super", PosParams{Path: "src/Use.php", Source: "lsp"}, api.CodeInvalidParams)
	modtest.WantCode(t, a, "nav.definition", PosParams{Path: "missing.php"}, api.CodeNotFound)

	// a full rebuild re-indexes everything and announces it
	modtest.Call(t, a, "index.rebuild", StatusParams{Full: true, Wait: true}, &st)
	if st.Files != 4 || st.State != "ready" {
		t.Fatalf("rebuild %+v", st)
	}
	updated.Wait(t, TopicUpdated, nil)
}

func TestLSPMerge(t *testing.T) {
	a, _ := newApp(t, Options{}, nil)
	dir := modtest.Project(t, a, files)
	modtest.Call(t, a, "index.status", StatusParams{Wait: true}, nil)

	// without a provider: auto = index only, lsp reports the missing server
	var res NavResult
	modtest.Call(t, a, "nav.definition", PosParams{Path: "src/Use.php", At: api.Pos{Line: 8, Col: 17}, Source: "lsp"}, &res)
	if len(res.Locations) != 0 || res.LSP || res.LSPError == "" {
		t.Fatalf("lsp without server %+v", res)
	}

	sq := filepath.Join(dir, "src/Square.php")
	api.HookNavLSP.On(a.Hooks(), 0, func(_ context.Context, q api.NavLSPQuery) (api.NavLSPQuery, error) {
		q.Answered = true
		q.Locations = []api.NavLSPLocation{
			{Path: "src/Square.php", File: sq, Range: api.TextRange{Start: api.TextPos{Line: 4, Col: 1}}},                                 // same as index
			{Path: "src/Other.php", File: filepath.Join(dir, "src/Other.php"), Range: api.TextRange{Start: api.TextPos{Line: 2, Col: 1}}}, // lsp only
		}
		return q, nil
	})
	modtest.Call(t, a, "nav.definition", PosParams{Path: "src/Use.php", At: api.Pos{Line: 8, Col: 17}}, &res)
	if len(res.Locations) != 2 || !res.LSP || res.Locations[0].Source != "index+lsp" || res.Locations[1].Source != "lsp" || res.Locations[1].Path != "src/Other.php" {
		t.Fatalf("merged %+v", res)
	}
	modtest.Call(t, a, "nav.implementations", PosParams{Path: "src/Use.php", At: api.Pos{Line: 8, Col: 17}, Source: "lsp"}, &res)
	if len(res.Locations) != 2 || res.Locations[0].Source != "lsp" {
		t.Fatalf("lsp only %+v", res)
	}
	modtest.Call(t, a, "nav.definition", PosParams{Path: "src/Use.php", At: api.Pos{Line: 8, Col: 17}, Source: "index"}, &res)
	if len(res.Locations) != 1 || res.LSP {
		t.Fatalf("index only %+v", res)
	}
}

func TestIncrementalUpdates(t *testing.T) {
	a, _ := newApp(t, Options{DocDelay: 20 * time.Millisecond, SaveDelay: 20 * time.Millisecond}, nil)
	dir := modtest.Project(t, a, files)
	modtest.Call(t, a, "index.status", StatusParams{Wait: true}, nil)
	updated := modtest.Record(t, a, TopicUpdated)

	impls := func() []string {
		var res NavResult
		modtest.Call(t, a, "nav.implementations", PosParams{Path: "src/Shape.php", At: api.Pos{Line: 4, Col: 12}}, &res)
		return fqnsOf(res.Locations)
	}
	if got := impls(); !slices.Equal(got, []string{`App\Square@index`}) {
		t.Fatalf("start %q", got)
	}

	// a new file announced by the file watcher
	modtest.WriteFile(t, filepath.Join(dir, "src/Circle.php"), "<?php\nnamespace App;\nclass Circle implements Shape { public function area(): float { return 3.0; } }\n")
	circle := filepath.Join(dir, "src/Circle.php")
	a.Bus().Publish(TopicFilesChanged, api.FilesChangedEvent{Changes: []api.FileChange{{Kind: api.FileCreated, Path: "src/Circle.php", File: circle, Source: api.FileSourceWatcher}}})
	modtest.Eventually(t, "Circle via files.changed", func() bool { return slices.Contains(impls(), `App\Circle@index`) })

	// a directory change (full scan)
	modtest.WriteFile(t, filepath.Join(dir, "src/sub/Tri.php"), "<?php\nnamespace App;\nclass Tri implements Shape {}\n")
	a.Bus().Publish(TopicFilesChanged, api.FilesChangedEvent{Changes: []api.FileChange{{Kind: api.FileCreated, Path: "src/sub", File: filepath.Join(dir, "src/sub"), Dir: true, Source: api.FileSourceWatcher}}})
	modtest.Eventually(t, "Tri via directory change", func() bool { return slices.Contains(impls(), `App\Tri@index`) })

	// an edit of an open document (debounced), without saving
	modtest.Call(t, a, "doc.open", api.DocOpenParams{Path: "src/Square.php"}, nil)
	modtest.Call(t, a, "doc.replace", api.DocReplaceParams{Path: "src/Square.php", Start: &api.Pos{Line: 4}, End: &api.Pos{Line: 5}, Text: "class Square\n"}, nil)
	modtest.Eventually(t, "Square gone after edit", func() bool { return !slices.Contains(impls(), `App\Square@index`) })
	updated.Wait(t, TopicUpdated, func(e bus.Event) bool { return e.Payload.(UpdatedEvent).Reason == "doc" })

	// the gutter flushes a pending edit immediately
	modtest.Call(t, a, "doc.replace", api.DocReplaceParams{Path: "src/Square.php", Start: &api.Pos{Line: 4}, End: &api.Pos{Line: 5}, Text: "class Square implements Shape\n"}, nil)
	var g GutterResult
	modtest.Call(t, a, "nav.gutter", GutterParams{Path: "src/Square.php"}, &g)
	if len(g.Marks) != 2 {
		t.Fatalf("gutter after edit %+v", g)
	}
	// closing without saving restores the disk state (implements Shape)
	modtest.Call(t, a, "doc.replace", api.DocReplaceParams{Path: "src/Square.php", Start: &api.Pos{Line: 4}, End: &api.Pos{Line: 5}, Text: "class Square\n"}, nil)
	modtest.Eventually(t, "edit again", func() bool { return !slices.Contains(impls(), `App\Square@index`) })
	modtest.Call(t, a, "doc.close", api.DocCloseParams{Path: "src/Square.php", Discard: true}, nil)
	modtest.Eventually(t, "disk state after close", func() bool { return slices.Contains(impls(), `App\Square@index`) })

	// a git operation triggers a scan
	modtest.WriteFile(t, filepath.Join(dir, "src/Hex.php"), "<?php\nnamespace App;\nclass Hex implements Shape {}\n")
	a.Bus().Publish(api.TopicGitChanged, api.GitChangedEvent{Op: "checkout"})
	modtest.Eventually(t, "Hex via git.changed", func() bool { return slices.Contains(impls(), `App\Hex@index`) })

	// a saved file
	modtest.WriteFile(t, filepath.Join(dir, "src/Hex.php"), "<?php\nnamespace App;\nclass Hex {}\n")
	a.Bus().Publish(api.TopicFileSaved, api.FileSavedEvent{Path: "src/Hex.php", File: filepath.Join(dir, "src/Hex.php")})
	modtest.Eventually(t, "Hex gone via file.saved", func() bool { return !slices.Contains(impls(), `App\Hex@index`) })
}

func TestCacheAndLifecycle(t *testing.T) {
	cfgDir := filepath.Join(t.TempDir(), "config")
	setCfg := func(o *api.Options) { o.ConfigDir = cfgDir }
	a, _ := newApp(t, Options{}, setCfg)
	dir := modtest.Project(t, a, files)
	var st Status
	modtest.Call(t, a, "index.status", StatusParams{Wait: true}, &st)
	if st.FromCache {
		t.Fatal("cache on first run")
	}
	if err := a.Close(); err != nil {
		t.Fatal(err)
	}

	// second run: the cache is loaded, nothing re-parsed
	a2, _ := newApp(t, Options{Lazy: true}, setCfg)
	if _, err := a2.OpenProject(context.Background(), dir); err != nil {
		t.Fatal(err)
	}
	modtest.Call(t, a2, "index.status", nil, &st)
	modtest.Call(t, a2, "index.status", StatusParams{Wait: true}, &st)
	if !st.FromCache || st.Files != 4 {
		t.Fatalf("second run %+v", st)
	}
	// the settings switch vendor off (a new option set ignores the cache)
	var cfg Config
	modtest.Call(t, a2, "index.config", nil, &cfg)
	if !cfg.Libraries || cfg.IncludeIgnored || len(cfg.Exclude) != 0 {
		t.Fatalf("config %+v", cfg)
	}
	no, yes := false, true
	modtest.Call(t, a2, "index.config", ConfigParams{Libraries: &no, IncludeIgnored: &yes, Exclude: &[]string{"x"}}, &cfg)
	if cfg.Libraries || !cfg.IncludeIgnored || cfg.Exclude[0] != "x" {
		t.Fatalf("config set %+v", cfg)
	}
	modtest.Call(t, a2, "index.config", ConfigParams{IncludeIgnored: &no}, &cfg)
	modtest.Call(t, a2, "project.close", nil, nil)
	modtest.WantCode(t, a2, "nav.symbols", SymbolsParams{Query: "x"}, api.CodeNoProject)
	if _, err := a2.OpenProject(context.Background(), dir); err != nil {
		t.Fatal(err)
	}
	modtest.Call(t, a2, "index.status", StatusParams{Wait: true}, &st)
	if st.Files != 3 || st.Libraries || st.FromCache {
		t.Fatalf("without vendor %+v", st)
	}
	modtest.Call(t, a2, "index.rebuild", StatusParams{Wait: false}, nil)

	// disabled module
	modtest.Call(t, a2, "modules.disable", api.ModuleParams{ID: ID}, nil)
	modtest.WantCode(t, a2, "nav.symbols", SymbolsParams{Query: "x"}, api.CodeModuleDisabled)
	modtest.Call(t, a2, "modules.enable", api.ModuleParams{ID: ID}, nil)
	modtest.Call(t, a2, "nav.symbols", SymbolsParams{Query: "Square", Wait: true}, nil)

	// a cancelled wait
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := a2.CallJSON(ctx, "index.rebuild", StatusParams{Full: true, Wait: true}); err == nil {
		t.Log("rebuild finished before the cancellation was seen")
	}
}

func TestUnavailable(t *testing.T) {
	if !strings.Contains(unavailable().Error(), "cgo") {
		t.Fatal("unavailable")
	}
}
