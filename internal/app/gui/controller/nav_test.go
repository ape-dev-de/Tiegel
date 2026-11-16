package controller

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ape-dev-de/tiegel/internal/api"
)

func navStarted(t *testing.T, f *fakeBackend) (*Controller, *CodeNav) {
	t.Helper()
	c := started(t, f)
	n := NewCodeNav(c)
	t.Cleanup(n.Close)
	n.GutterDelay = 20 * time.Millisecond
	return c, n
}

func target(rel string, line, off int, name string) NavTarget {
	return NavTarget{Path: rel, Range: TextRange{Start: TextPos{Offset: off, Line: line, Col: 1}}, Kind: "class", Name: name, FQN: `App\` + name}
}

func navParamsOf(f *fakeBackend, method string) map[string]any {
	f.mu.Lock()
	defer f.mu.Unlock()
	ps := f.params[method]
	if len(ps) == 0 {
		return nil
	}
	var m map[string]any
	_ = json.Unmarshal(ps[len(ps)-1], &m)
	return m
}

// One target jumps directly (with history), several become a choice,
// none is reported; without the index the language server is asked.
func TestNavigateJumps(t *testing.T) {
	f := projectFake()
	c, n := navStarted(t, f)
	cur := At(filepath.Join(root, "src/Mailer.php"), 7)
	c.OpenFile(cur.File, nil)
	c.Settle(time.Second)

	f.answer("nav.definition", navResult{Locations: []NavTarget{target("index.php", 2, 6, "Mailer")}, Complete: true})
	n.Navigate(cur, JumpDefinition)
	c.Settle(time.Second)
	if tab := c.ActiveTab(); tab == nil || tab.File != filepath.Join(root, "index.php") || !c.Nav.CanBack() {
		t.Fatalf("definition: tab %+v back %v", tab, c.Nav.CanBack())
	}
	if p := navParamsOf(f, "nav.definition"); p["path"] != cur.File || p["at"].(map[string]any)["offset"].(float64) != 7 {
		t.Fatalf("params %v", p)
	}

	f.answer("nav.implementations", navResult{Locations: []NavTarget{target("src/A.php", 3, 10, "A"), target("src/B.php", 4, 12, "B")}, Complete: true})
	n.Navigate(cur, JumpImplementations)
	c.Settle(time.Second)
	if n.Choice == nil || len(n.Choice.Targets) != 2 || n.Choice.Title != "Choose Implementation" || n.Choice.Targets[1].File != filepath.Join(root, "src/B.php") {
		t.Fatalf("choice %+v", n.Choice)
	}
	if n.Choice.Targets[0].Label() != `App\A` || !strings.HasSuffix(n.Choice.Targets[1].Where(), "src/B.php:4") {
		t.Fatalf("labels %q %q", n.Choice.Targets[0].Label(), n.Choice.Targets[1].Where())
	}
	n.ChooseTarget(1)
	c.Settle(time.Second)
	if tab := c.ActiveTab(); tab == nil || tab.File != filepath.Join(root, "src/B.php") || n.Choice != nil {
		t.Fatalf("chosen: %+v", tab)
	}
	if c.Reveal == nil || c.Reveal.Range.Start.Offset != 12 {
		// Reveal is consumed by the view; the controller keeps it.
		t.Fatalf("reveal %+v", c.Reveal)
	}
	// Back returns to where the jump started.
	c.NavigateBack(At(filepath.Join(root, "src/B.php"), 12))
	c.Settle(time.Second)
	if tab := c.ActiveTab(); tab.File != cur.File {
		t.Fatalf("back to %s", tab.File)
	}

	f.answer("nav.super", navResult{Complete: false})
	n.Navigate(cur, JumpSuper)
	c.Settle(time.Second)
	if !strings.Contains(c.Status.Notice, "No super") || !strings.Contains(c.Status.Notice, "still being built") {
		t.Fatalf("notice %q", c.Status.Notice)
	}

	// No index (module disabled): definitions come from the server.
	f.on("nav.definition", func(json.RawMessage) (any, error) {
		return nil, api.Errorf(api.CodeModuleDisabled, "php-index disabled")
	})
	c.Status.LSPRunning = true
	f.answer("lsp.definition", []Location{{Path: "src/C.php", Range: TextRange{Start: TextPos{Offset: 3}}}})
	n.Navigate(cur, JumpDefinition)
	c.Settle(time.Second)
	if f.called("lsp.definition") != 1 || c.ActiveTab().File != filepath.Join(root, "src/C.php") {
		t.Fatalf("lsp fallback: %d %s", f.called("lsp.definition"), c.ActiveTab().File)
	}
}

// Gutter marks load on first use, reload debounced after edits and index
// updates, and their targets open.
func TestGutterMarks(t *testing.T) {
	f := projectFake()
	c, n := navStarted(t, f)
	file := filepath.Join(root, "src/Shape.php")
	f.answer("nav.gutter", map[string]any{"marks": []GutterMark{
		{Line: 3, Kind: "interface", FQN: `App\Shape`, Down: []NavTarget{target("src/Circle.php", 5, 40, "Circle"), target("src/Square.php", 5, 41, "Square")}, DownTotal: 2},
		{Line: 7, Kind: "method", FQN: `App\Shape::area`, Up: []NavTarget{target("src/Base.php", 9, 90, "Base")}},
	}})
	if n.GutterMarks("/x/readme.md") != nil {
		t.Fatal("marks for non-PHP")
	}
	if m := n.GutterMarks(file); m != nil {
		t.Fatalf("marks before load %v", m)
	}
	c.Settle(time.Second)
	marks := n.GutterMarks(file)
	if len(marks) != 2 || len(marks[3].Down) != 2 || marks[3].Down[0].File != filepath.Join(root, "src/Circle.php") {
		t.Fatalf("marks %+v", marks)
	}
	if got := f.called("nav.gutter"); got != 1 {
		t.Fatalf("loaded %d times", got)
	}
	// Three quick edits: one reload.
	for i := 0; i < 3; i++ {
		f.emit(api.TopicDocEdited, map[string]any{"file": file, "path": "src/Shape.php"})
	}
	c.Settle(time.Second)
	time.Sleep(60 * time.Millisecond)
	c.Settle(time.Second)
	if got := f.called("nav.gutter"); got != 2 {
		t.Fatalf("after edits loaded %d times", got)
	}
	f.emit("index.updated", map[string]any{"files": []string{"src/Circle.php"}, "reason": "file", "generation": 3})
	c.Settle(time.Second)
	time.Sleep(60 * time.Millisecond)
	c.Settle(time.Second)
	if got := f.called("nav.gutter"); got != 3 {
		t.Fatalf("after index update loaded %d times", got)
	}
	// Down with two targets: choice; up with one: jump.
	cur := At(file, 0)
	if !n.GutterJump(cur, 3, false) || n.Choice == nil || len(n.Choice.Targets) != 2 || !strings.Contains(n.Choice.Title, "Shape") {
		t.Fatalf("down %+v", n.Choice)
	}
	n.CancelChoice()
	if !n.GutterJump(cur, 7, true) {
		t.Fatal("up")
	}
	c.Settle(time.Second)
	if c.ActiveTab() == nil || c.ActiveTab().File != filepath.Join(root, "src/Base.php") {
		t.Fatalf("up jump %+v", c.ActiveTab())
	}
	if n.GutterJump(cur, 5, true) || n.GutterJump(cur, 7, false) {
		t.Fatal("jump without mark")
	}
	// Progress events feed the status.
	f.emit("index.progress", map[string]any{"phase": "indexing", "done": 10, "total": 100})
	c.Settle(time.Second)
	if !n.Index.Busy() || n.Index.Done != 10 || n.Index.Total != 100 {
		t.Fatalf("index %+v", n.Index)
	}
	f.emit("index.progress", map[string]any{"phase": "ready", "done": 100, "total": 100})
	c.Settle(time.Second)
	if n.Index.Busy() {
		t.Fatal("still busy")
	}
	n.ForgetGutter(file)
	n.Reset()
	if len(n.gutter) != 0 || n.Index.Phase != "" {
		t.Fatal("reset")
	}
}

// Usages are grouped by file in the server's order.
func TestFindUsagesGrouping(t *testing.T) {
	f := projectFake()
	c, n := navStarted(t, f)
	cur := At(filepath.Join(root, "src/Mailer.php"), 20)
	n.FindUsages(cur, "send")
	if !strings.Contains(c.Status.Notice, "language server") {
		t.Fatalf("no server: %q", c.Status.Notice)
	}
	c.Status.LSPRunning = true
	u := func(p string, line, off int, text string) map[string]any {
		return map[string]any{"path": p, "range": TextRange{Start: TextPos{Offset: off, Line: line, Col: 3}}, "lineText": text}
	}
	f.answer("lsp.references", []map[string]any{
		u("src/A.php", 3, 30, "$m->send();\n"), u("src/B.php", 8, 80, "  send(1);"), u("src/A.php", 9, 90, "send(2);"),
	})
	n.FindUsages(cur, "send")
	c.Settle(time.Second)
	us := n.Usages
	if us == nil || us.Total != 3 || len(us.Groups) != 2 || us.Symbol != "send" {
		t.Fatalf("usages %+v", us)
	}
	if g := us.Groups[0]; g.Path != "src/A.php" || g.File != filepath.Join(root, "src/A.php") || len(g.Items) != 2 || g.Items[0].LineText != "$m->send();" || g.Items[1].Range.Start.Line != 9 {
		t.Fatalf("group A %+v", g)
	}
	if p := navParamsOf(f, "lsp.references"); p["includeDeclaration"] != false {
		t.Fatalf("params %v", p)
	}
	f.answer("lsp.references", []map[string]any{})
	n.FindUsages(cur, "nothing")
	c.Settle(time.Second)
	if n.Usages.Total != 0 || !strings.Contains(c.Status.Notice, "No usages of nothing") {
		t.Fatalf("empty %+v %q", n.Usages, c.Status.Notice)
	}
}

// Find in Files streams batches of its own search, can be cancelled and
// previews replacements before applying them.
func TestFindStreamingAndReplace(t *testing.T) {
	f := projectFake()
	c, n := navStarted(t, f)
	f.answer("search.inProject", api.ProjectSearchResult{Running: true})
	f.answer("search.cancel", api.SearchCancelResult{Cancelled: true})
	n.StartFind(FindParams{Pattern: "send", Mask: "*.php", Scope: "project"})
	c.Settle(time.Second)
	st := n.Find
	p := navParamsOf(f, "search.inProject")
	if st == nil || !st.Running || p["stream"] != true || p["id"] != st.ID || p["mask"] != "*.php" || p["scope"] != nil {
		t.Fatalf("start %+v params %v", st, p)
	}
	batch := func(id string, files ...string) api.ProjectSearchEvent {
		e := api.ProjectSearchEvent{ID: id}
		for _, fl := range files {
			e.Files = append(e.Files, api.FileMatches{Path: fl, Matches: []api.SearchMatch{{Line: 1, Col: 1, LineText: "send"}, {Line: 2, Col: 1}}})
		}
		return e
	}
	f.emit(api.TopicSearchResults, batch(st.ID, "a.php"))
	f.emit(api.TopicSearchResults, batch("other", "x.php"))
	f.emit(api.TopicSearchResults, batch(st.ID, "b.php", "c.php"))
	c.Settle(time.Second)
	if len(st.Files) != 3 || st.Matches != 6 || !st.Running {
		t.Fatalf("streamed %d files %d matches", len(st.Files), st.Matches)
	}
	n.CancelFind()
	c.Settle(time.Second)
	if st.Running || navParamsOf(f, "search.cancel")["id"] != st.ID {
		t.Fatalf("cancel %v", navParamsOf(f, "search.cancel"))
	}
	f.emit(api.TopicSearchResults, api.ProjectSearchEvent{ID: st.ID, Done: true, Summary: &api.ProjectSearchResult{Cancelled: true}})
	c.Settle(time.Second)
	if st.Summary == nil || !st.Summary.Cancelled {
		t.Fatalf("summary %+v", st.Summary)
	}

	// Replace: preview after the search, then apply to the files.
	f.on("search.replaceInProject", func(raw json.RawMessage) (any, error) {
		var p api.ProjectReplaceParams
		_ = json.Unmarshal(raw, &p)
		if p.Preview {
			return api.ProjectReplaceResult{Preview: true, Replaced: 2, Files: []api.FileReplace{{Path: "a.php", Replaced: 2,
				Matches: []api.ReplaceMatch{{SearchMatch: api.SearchMatch{Line: 1}, Replacement: "deliver"}}}}}, nil
		}
		return api.ProjectReplaceResult{Replaced: 2, FilesChanged: 1}, nil
	})
	n.StartFind(FindParams{Pattern: "send", Replace: true, Replacement: "deliver"})
	c.Settle(time.Second)
	st2 := n.Find
	if st2 == st || st2.Replace != nil {
		t.Fatal("new search")
	}
	f.emit(api.TopicSearchResults, batch(st2.ID, "a.php"))
	f.emit(api.TopicSearchResults, api.ProjectSearchEvent{ID: st2.ID, Done: true, Summary: &api.ProjectSearchResult{Matches: 2}})
	c.Settle(time.Second)
	if st2.Replace == nil || st2.Replace.Replaced != 2 || st2.Running {
		t.Fatalf("preview %+v", st2.Replace)
	}
	n.ApplyReplace(nil)
	c.Settle(time.Second)
	ap := navParamsOf(f, "search.replaceInProject")
	if ap["preview"] != nil || ap["replacement"] != "deliver" || len(ap["paths"].([]any)) != 1 || st2.Replaced == nil || st2.Replace != nil {
		t.Fatalf("apply %v %+v", ap, st2)
	}
	if !strings.Contains(c.Status.Notice, "Replaced 2 occurrences in 1 files") {
		t.Fatalf("notice %q", c.Status.Notice)
	}
}

// Rename asks prepareRename, previews, then applies; code actions list
// preferred ones first.
func TestRenameAndCodeActions(t *testing.T) {
	f := projectFake()
	c, n := navStarted(t, f)
	c.Status.LSPRunning = true
	cur := At(filepath.Join(root, "src/Mailer.php"), 14)
	f.answer("lsp.prepareRename", map[string]any{"canRename": false})
	n.StartRename(cur, "send")
	c.Settle(time.Second)
	if n.Rename != nil || !strings.Contains(c.Status.Notice, "cannot be renamed") {
		t.Fatalf("not renamable: %+v %q", n.Rename, c.Status.Notice)
	}
	f.answer("lsp.prepareRename", map[string]any{"canRename": true, "placeholder": "send"})
	n.StartRename(cur, "x")
	c.Settle(time.Second)
	if n.Rename == nil || n.Rename.Placeholder != "send" || n.Rename.Stage != RenameInput {
		t.Fatalf("input %+v", n.Rename)
	}
	f.on("lsp.rename", func(raw json.RawMessage) (any, error) {
		var p map[string]any
		_ = json.Unmarshal(raw, &p)
		if p["preview"] == true {
			return map[string]any{"preview": []RenameFile{
				{Kind: "edit", Path: "src/Mailer.php", Edits: []TextEdit{{NewText: "deliver"}, {NewText: "deliver"}}},
				{Kind: "edit", Path: "src/Use.php", Edits: []TextEdit{{NewText: "deliver"}}},
			}}, nil
		}
		return map[string]any{"applied": true, "files": []map[string]string{{"path": "src/Mailer.php"}, {"path": "src/Use.php"}}}, nil
	})
	n.PreviewRename("deliver")
	c.Settle(time.Second)
	rs := n.Rename
	if rs == nil || rs.Stage != RenamePreview || len(rs.Preview) != 2 || rs.Changes() != 3 || rs.NewName != "deliver" {
		t.Fatalf("preview %+v", rs)
	}
	n.ApplyRename()
	c.Settle(time.Second)
	if n.Rename != nil || f.called("lsp.rename") != 2 || !strings.Contains(c.Status.Notice, "Renamed send to deliver in 2 files") {
		t.Fatalf("applied %q", c.Status.Notice)
	}
	if p := navParamsOf(f, "lsp.rename"); p["preview"] != nil || p["newName"] != "deliver" {
		t.Fatalf("rename params %v", p)
	}

	f.answer("lsp.codeActions", []CodeAction{{ID: "1", Title: "Add use"}, {ID: "2", Title: "Import class", Preferred: true}, {ID: "3", Title: "Extract", Disabled: "no selection"}})
	f.answer("lsp.applyCodeAction", map[string]any{"applied": true})
	n.LoadCodeActions(cur.File, 5, 9)
	c.Settle(time.Second)
	if n.Actions == nil || n.Actions.Items[0].ID != "2" || navParamsOf(f, "lsp.codeActions")["end"] == nil {
		t.Fatalf("actions %+v", n.Actions)
	}
	n.ApplyCodeAction(2)
	if !strings.Contains(c.Status.Notice, "no selection") || n.Actions != nil {
		t.Fatalf("disabled %q", c.Status.Notice)
	}
	n.LoadCodeActions(cur.File, 5, 5)
	c.Settle(time.Second)
	n.ApplyCodeAction(0)
	c.Settle(time.Second)
	if navParamsOf(f, "lsp.applyCodeAction")["id"] != "2" || c.Status.Notice != "Applied: Import class" {
		t.Fatalf("apply %v %q", navParamsOf(f, "lsp.applyCodeAction"), c.Status.Notice)
	}

	// Reformat: selection → rangeFormatting, else the file.
	f.answer("lsp.formatting", map[string]any{"edits": []TextEdit{{NewText: " "}}})
	f.answer("lsp.rangeFormatting", map[string]any{"edits": []TextEdit{}})
	n.Reformat(cur.File, 0, 0, 4)
	c.Settle(time.Second)
	if c.Status.Notice != "Reformatted (1 changes)" {
		t.Fatalf("format %q", c.Status.Notice)
	}
	n.Reformat(cur.File, 3, 9, 2)
	c.Settle(time.Second)
	if c.Status.Notice != "Already formatted" || navParamsOf(f, "lsp.rangeFormatting")["tabSize"].(float64) != 2 {
		t.Fatalf("range format %q", c.Status.Notice)
	}

	// Highlights of one document version; older answers are dropped.
	f.answer("lsp.documentHighlight", []map[string]any{
		{"range": TextRange{Start: TextPos{Offset: 1}, End: TextPos{Offset: 4}}, "kind": "read"},
		{"range": TextRange{Start: TextPos{Offset: 10}, End: TextPos{Offset: 13}}, "kind": "write"},
	})
	n.Highlight(cur.File, 2, 7)
	c.Settle(time.Second)
	if h := n.Highlights; h.File != cur.File || h.Version != 7 || len(h.Ranges) != 2 || !h.Ranges[1].Write {
		t.Fatalf("highlights %+v", h)
	}
	f.emit(api.TopicDocEdited, map[string]any{"file": cur.File})
	c.Settle(time.Second)
	if n.Highlights.File != "" {
		t.Fatal("highlights survive an edit")
	}
}

// Symbols come from the index, else from the server; the structure from
// syntax.outline, else from the server.
func TestSymbolsAndStructure(t *testing.T) {
	f := projectFake()
	c, n := navStarted(t, f)
	f.answer("nav.symbols", map[string]any{"symbols": []NavTarget{target("src/Mailer.php", 3, 6, "Mailer")}, "complete": true})
	n.SearchSymbols("mai", []string{"type"}, 50)
	c.Settle(time.Second)
	if s := n.Symbols; s.Query != "mai" || len(s.Items) != 1 || s.Items[0].File != filepath.Join(root, "src/Mailer.php") || !s.Complete {
		t.Fatalf("symbols %+v", s)
	}
	if p := navParamsOf(f, "nav.symbols"); p["kinds"].([]any)[0] != "type" || p["limit"].(float64) != 50 {
		t.Fatalf("params %v", p)
	}
	n.SearchSymbols("", nil, 50)
	if len(n.Symbols.Items) != 0 || f.called("nav.symbols") != 1 {
		t.Fatal("empty query searched")
	}
	f.on("nav.symbols", func(json.RawMessage) (any, error) { return nil, api.Errorf(api.CodeUnavailable, "no cgo") })
	c.Status.LSPRunning = true
	f.answer("lsp.workspaceSymbols", []map[string]any{
		{"name": "Mailer", "kind": "Class", "path": "src/Mailer.php"},
		{"name": "send", "kind": "Method", "container": "Mailer", "path": "src/Mailer.php", "range": TextRange{Start: TextPos{Line: 4}}},
	})
	n.SearchSymbols("mai", []string{"type"}, 50)
	c.Settle(time.Second)
	if s := n.Symbols; len(s.Items) != 1 || s.Items[0].Source != "lsp" || s.Items[0].Kind != "class" {
		t.Fatalf("lsp symbols %+v", s)
	}

	pos := func(off, line int) map[string]int { return map[string]int{"offset": off, "line": line, "col": 1} }
	f.answer("syntax.outline", map[string]any{"symbols": []map[string]any{
		{"kind": "class", "name": "Mailer", "start": pos(6, 3), "nameStart": pos(12, 3), "children": []map[string]any{
			{"kind": "method", "name": "send", "detail": "(): void", "modifiers": []string{"public"}, "start": pos(30, 5), "nameStart": pos(46, 5)},
		}},
	}})
	n.LoadStructure(filepath.Join(root, "src/Mailer.php"))
	c.Settle(time.Second)
	st := n.Structure
	if st == nil || len(st.Nodes) != 1 || st.Nodes[0].Offset != 12 || st.Nodes[0].Children[0].Name != "send" || st.Nodes[0].Children[0].Line != 5 {
		t.Fatalf("structure %+v", st)
	}
	f.on("syntax.outline", func(json.RawMessage) (any, error) { return nil, api.Errorf(api.CodeUnavailable, "no cgo") })
	f.answer("lsp.documentSymbols", []map[string]any{{"name": "Mailer", "kind": "Class", "selectionRange": TextRange{Start: TextPos{Offset: 12, Line: 3}}}})
	n.LoadStructure(filepath.Join(root, "src/Mailer.php"))
	c.Settle(time.Second)
	if st := n.Structure; len(st.Nodes) != 1 || st.Nodes[0].Kind != "class" || st.Err != "" {
		t.Fatalf("lsp structure %+v", st)
	}
}

// The type hierarchy keeps unindexed types without a file.
func TestTypeHierarchy(t *testing.T) {
	f := projectFake()
	c, n := navStarted(t, f)
	f.answer("nav.typeHierarchy", map[string]any{
		"class":      target("src/Circle.php", 3, 6, "Circle"),
		"supertypes": []map[string]any{{"fqn": `App\Shape`, "path": "src/Shape.php", "name": "Shape"}, {"fqn": "Countable", "name": "Countable"}},
		"subtypes":   []map[string]any{},
	})
	n.LoadTypeHierarchy(At(filepath.Join(root, "src/Circle.php"), 8))
	c.Settle(time.Second)
	h := n.Hierarchy
	if h == nil || h.Class.File != filepath.Join(root, "src/Circle.php") || len(h.Supertypes) != 2 || h.Supertypes[0].File != filepath.Join(root, "src/Shape.php") || h.Supertypes[1].File != "" {
		t.Fatalf("hierarchy %+v", h)
	}
	f.on("nav.typeHierarchy", func(json.RawMessage) (any, error) { return nil, api.Errorf(api.CodeNotFound, "no class here") })
	n.LoadTypeHierarchy(At(filepath.Join(root, "src/Circle.php"), 0))
	c.Settle(time.Second)
	if !strings.Contains(c.Status.Notice, "no class here") {
		t.Fatalf("notice %q", c.Status.Notice)
	}
}
