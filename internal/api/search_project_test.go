package api

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func searchProject(t *testing.T) (*App, string) {
	t.Helper()
	a := newTestApp(t)
	root := openProject(t, a, map[string]string{
		".gitignore":            "build/\n*.log\n",
		"src/A.php":             "<?php\nclass Foo {}\n$foo = new Foo();\n",
		"src/B.php":             "<?php\n// foo bar\nfunction fooBar() {}\n",
		"src/sub/C.inc":         "Foo foo FOO\n",
		"src/min.js":            strings.Repeat("x", 600) + "Foo" + strings.Repeat("y", 600) + "\n",
		"vendor/lib/V.php":      "<?php class Foo {}\n",
		"node_modules/m/i.js":   "Foo\n",
		"build/out.php":         "Foo\n",
		"debug.log":             "Foo\n",
		"bin.dat":               "Foo\x00\x01",
		"README.md":             "Ünïcode Foo\r\n",
		"src/sub/.gitignore":    "secret.php\n",
		"src/sub/secret.php":    "Foo\n",
		".git/HEAD":             "Foo\n",
		"src/deep/nested/D.php": "<?php\nnamespace Deep;\nFoo::bar();\n",
	})
	return a, root
}

func filesOf(r ProjectSearchResult) []string {
	var out []string
	for _, f := range r.Files {
		out = append(out, f.Path)
	}
	return out
}

func TestSearchInProject(t *testing.T) {
	a, _ := searchProject(t)
	var res ProjectSearchResult
	call(t, a, "search.inProject", ProjectSearchParams{Pattern: "Foo", CaseSensitive: true}, &res)
	want := "README.md,src/A.php,src/deep/nested/D.php,src/min.js,src/sub/C.inc"
	if got := strings.Join(filesOf(res), ","); got != want {
		t.Fatalf("files %s, want %s", got, want)
	}
	if res.SkippedBinary != 1 || res.Matches != 6 || res.FilesMatched != 5 || res.ID == "" || res.Limited || res.Cancelled {
		t.Fatalf("summary %+v", res)
	}
	a1 := res.Files[1]
	if len(a1.Matches) != 2 || a1.Matches[0].Line != 2 || a1.Matches[0].Col != 7 || a1.Matches[1].Line != 3 || a1.Matches[1].LineText != "$foo = new Foo();" {
		t.Fatalf("A.php %+v", a1)
	}
	readme := res.Files[0].Matches[0]
	if readme.Col != 9 || readme.LineText != "Ünïcode Foo" {
		t.Fatalf("readme %+v", readme)
	}
	mj := res.Files[3].Matches[0]
	if mj.PreviewStart == 0 || len(mj.LineText) > maxPreviewBytes || !strings.Contains(mj.LineText, "Foo") {
		t.Fatalf("preview %d %q", mj.PreviewStart, mj.LineText)
	}

	// Case-insensitive, whole word, mask with exclusion.
	call(t, a, "search.inProject", ProjectSearchParams{Pattern: "foo", WholeWord: true, Mask: "*.php, *.inc, !B.php"}, &res)
	if got := strings.Join(filesOf(res), ","); got != "src/A.php,src/deep/nested/D.php,src/sub/C.inc" {
		t.Fatalf("mask files %s", got)
	}
	if len(res.Files[2].Matches) != 3 {
		t.Fatalf("C.inc %+v", res.Files[2])
	}
	// Libraries and ignored files on request; directory scope; path masks.
	call(t, a, "search.inProject", ProjectSearchParams{Pattern: "Foo", CaseSensitive: true, Libraries: true, Ignored: true, Mask: "*.php,*.log,*.js"}, &res)
	got := strings.Join(filesOf(res), ",")
	for _, f := range []string{"vendor/lib/V.php", "node_modules/m/i.js", "build/out.php", "debug.log", "src/sub/secret.php"} {
		if !strings.Contains(got, f) {
			t.Errorf("missing %s in %s", f, got)
		}
	}
	if strings.Contains(got, ".git/") {
		t.Errorf(".git searched: %s", got)
	}
	call(t, a, "search.inProject", ProjectSearchParams{Pattern: "Foo", Dir: "src/sub", Ignored: true}, &res)
	if got := strings.Join(filesOf(res), ","); got != "src/sub/C.inc,src/sub/secret.php" {
		t.Fatalf("dir scope %s", got)
	}
	call(t, a, "search.inProject", ProjectSearchParams{Pattern: "Foo", Dir: "src/sub"}, &res)
	if got := strings.Join(filesOf(res), ","); got != "src/sub/C.inc" {
		t.Fatalf("nested gitignore %s", got)
	}
	call(t, a, "search.inProject", ProjectSearchParams{Pattern: "Foo", Mask: "src/deep/*/*.php"}, &res)
	if got := strings.Join(filesOf(res), ","); got != "src/deep/nested/D.php" {
		t.Fatalf("path mask %s", got)
	}
	// Regex, limit, max file size.
	call(t, a, "search.inProject", ProjectSearchParams{Pattern: `^<\?php$`, Regex: true, Limit: 2}, &res)
	if res.Matches != 2 || !res.Limited || res.Cancelled {
		t.Fatalf("limit %+v", res)
	}
	call(t, a, "search.inProject", ProjectSearchParams{Pattern: "Foo", MaxFileSize: 100}, &res)
	if res.SkippedLarge != 1 {
		t.Fatalf("large %+v", res)
	}
	// Explicit paths ignore mask and scope.
	call(t, a, "search.inProject", ProjectSearchParams{Pattern: "Foo", Paths: []string{"vendor/lib/V.php"}, Mask: "*.txt"}, &res)
	if got := strings.Join(filesOf(res), ","); got != "vendor/lib/V.php" {
		t.Fatalf("paths %s", got)
	}

	wantCode(t, a, "search.inProject", ProjectSearchParams{Pattern: ""}, CodeInvalidParams)
	wantCode(t, a, "search.inProject", ProjectSearchParams{Pattern: "(", Regex: true}, CodeInvalidParams)
	wantCode(t, a, "search.inProject", ProjectSearchParams{Pattern: "x", Mask: "[", Scope: ScopeProject}, CodeInvalidParams)
	wantCode(t, a, "search.inProject", ProjectSearchParams{Pattern: "x", Scope: "galaxy"}, CodeInvalidParams)
	wantCode(t, a, "search.inProject", ProjectSearchParams{Pattern: "x", Dir: "../up"}, CodeInvalidParams)
	wantCode(t, a, "search.inProject", ProjectSearchParams{Pattern: "x", Dir: "nope"}, CodeNotFound)
	wantCode(t, a, "search.inProject", ProjectSearchParams{Pattern: "x", Dir: "README.md"}, CodeInvalidParams)
	wantCode(t, a, "search.cancel", SearchCancelParams{}, CodeInvalidParams)
	var cr SearchCancelResult
	call(t, a, "search.cancel", SearchCancelParams{ID: "none"}, &cr)
	if cr.Cancelled {
		t.Fatal("cancelled unknown search")
	}
}

func TestSearchOpenDocuments(t *testing.T) {
	a, root := searchProject(t)
	call(t, a, "doc.open", DocOpenParams{Path: "src/B.php"}, nil)
	call(t, a, "doc.replace", DocReplaceParams{Path: "src/B.php", Text: "<?php\nFoo in buffer\n"}, nil)
	call(t, a, "doc.open", DocOpenParams{Path: "src/New.php", Create: true}, nil)
	call(t, a, "doc.insert", DocInsertParams{Path: "src/New.php", Text: "Foo new"}, nil)
	var res ProjectSearchResult
	call(t, a, "search.inProject", ProjectSearchParams{Pattern: "Foo", CaseSensitive: true, Dir: "src"}, &res)
	byPath := map[string]FileMatches{}
	for _, f := range res.Files {
		byPath[f.Path] = f
	}
	if b := byPath["src/B.php"]; !b.Open || b.Version == 0 || len(b.Matches) != 1 || b.Matches[0].LineText != "Foo in buffer" {
		t.Fatalf("buffer not searched: %+v", b)
	}
	if n := byPath["src/New.php"]; !n.Open || len(n.Matches) != 1 {
		t.Fatalf("unsaved new doc: %+v", n)
	}
	call(t, a, "search.inProject", ProjectSearchParams{Pattern: "Foo", Scope: ScopeOpenFiles}, &res)
	if got := strings.Join(filesOf(res), ","); got != "src/B.php,src/New.php" {
		t.Fatalf("open files %s", got)
	}
	call(t, a, "search.inProject", ProjectSearchParams{Pattern: "Foo", Scope: ScopeOpenFiles, Mask: "B.*"}, &res)
	if len(res.Files) != 1 {
		t.Fatalf("open files mask %+v", res)
	}
	_ = root

	// Without a project only open files (or explicit paths) can be searched.
	b := newTestApp(t)
	wantCode(t, b, "search.inProject", ProjectSearchParams{Pattern: "x"}, CodeNoProject)
	wantCode(t, b, "search.replaceInProject", ProjectReplaceParams{ProjectSearchParams: ProjectSearchParams{Pattern: "x"}}, CodeNoProject)
	call(t, b, "search.inProject", ProjectSearchParams{Pattern: "x", Scope: ScopeOpenFiles}, &res)
	if len(res.Files) != 0 {
		t.Fatal("open files without docs")
	}
}

func TestSearchStreamAndCancel(t *testing.T) {
	a := newTestApp(t)
	files := map[string]string{}
	for i := range 300 {
		files["f/"+strings.Repeat("d", i%7)+"/x"+itoa3(i)+".php"] = strings.Repeat("needle\n", 5)
	}
	openProject(t, a, files)

	var mu sync.Mutex
	var events []ProjectSearchEvent
	done := make(chan struct{})
	s := a.NewSession(func(e Event) {
		var ev ProjectSearchEvent
		if json.Unmarshal(e.Payload, &ev) != nil {
			return
		}
		mu.Lock()
		events = append(events, ev)
		mu.Unlock()
		if ev.Done {
			close(done)
		}
	})
	defer s.Close()
	s.Subscribe("search.*")
	raw, _ := json.Marshal(ProjectSearchParams{Pattern: "needle", Stream: true, ID: "q1"})
	r, err := s.Call(context.Background(), "search.inProject", raw)
	if err != nil {
		t.Fatal(err)
	}
	if res := r.(ProjectSearchResult); !res.Running || res.ID != "q1" {
		t.Fatalf("ack %+v", res)
	}
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("no done event")
	}
	mu.Lock()
	total, files2 := 0, 0
	var last ProjectSearchEvent
	for _, ev := range events {
		if ev.ID != "q1" {
			t.Fatalf("id %q", ev.ID)
		}
		files2 += len(ev.Files)
		for _, f := range ev.Files {
			total += len(f.Matches)
		}
		last = ev
	}
	mu.Unlock()
	if total != 1500 || files2 != 300 || last.Summary == nil || last.Summary.Matches != 1500 || last.Summary.FilesMatched != 300 || len(events) < 2 {
		t.Fatalf("stream: total %d files %d events %d last %+v", total, files2, len(events), last.Summary)
	}

	// A cancelled search reports cancelled; a duplicate ID is refused.
	sid, sctx, release, err := a.searches.start(context.Background(), "busy")
	if err != nil || sid != "busy" {
		t.Fatal(err)
	}
	wantCode(t, a, "search.inProject", ProjectSearchParams{Pattern: "x", ID: "busy"}, CodeConflict)
	var cr SearchCancelResult
	call(t, a, "search.cancel", SearchCancelParams{ID: "busy"}, &cr)
	if !cr.Cancelled || sctx.Err() == nil {
		t.Fatal("not cancelled")
	}
	release()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	res, err := a.searchInProject(ctx, ProjectSearchParams{Pattern: "needle"})
	if err != nil || !res.Cancelled {
		t.Fatalf("cancelled search %+v %v", res, err)
	}
}

func itoa3(i int) string {
	return string([]byte{byte('0' + i/100), byte('0' + i/10%10), byte('0' + i%10)})
}

func TestReplaceInProject(t *testing.T) {
	a, root := searchProject(t)
	call(t, a, "doc.open", DocOpenParams{Path: "src/A.php"}, nil)

	params := ProjectReplaceParams{
		ProjectSearchParams: ProjectSearchParams{Pattern: `class (\w+)`, Regex: true, Mask: "*.php"},
		Replacement:         "final class ${1}Impl",
		Preview:             true,
	}
	var res ProjectReplaceResult
	call(t, a, "search.replaceInProject", params, &res)
	if !res.Preview || res.Replaced != 1 || len(res.Files) != 1 || res.Files[0].Matches[0].Replacement != "final class FooImpl" || !res.Files[0].Open {
		t.Fatalf("preview %+v", res)
	}
	if docText(t, a, "src/A.php") != "<?php\nclass Foo {}\n$foo = new Foo();\n" {
		t.Fatal("preview changed the document")
	}

	// Apply: open document as undoable edit, closed files rewritten with
	// Local History.
	params = ProjectReplaceParams{ProjectSearchParams: ProjectSearchParams{Pattern: "Foo", CaseSensitive: true, Mask: "*.php,*.inc", Libraries: true}, Replacement: "Bar"}
	call(t, a, "search.replaceInProject", params, &res)
	if res.Replaced != 5 || res.FilesChanged != 4 || res.Failed != 0 {
		t.Fatalf("replace %+v", res)
	}
	if got := docText(t, a, "src/A.php"); got != "<?php\nclass Bar {}\n$foo = new Bar();\n" {
		t.Fatalf("doc %q", got)
	}
	data, _ := os.ReadFile(filepath.Join(root, "src/A.php"))
	if strings.Contains(string(data), "Bar") {
		t.Fatal("open document was written to disk")
	}
	data, _ = os.ReadFile(filepath.Join(root, "src/sub/C.inc"))
	if string(data) != "Bar foo FOO\n" {
		t.Fatalf("C.inc %q", data)
	}
	var revs []json.RawMessage
	call(t, a, "history.list", PathParams{Path: "src/sub/C.inc"}, &revs)
	if len(revs) != 2 || !strings.Contains(string(revs[0]), `"replace in path"`) || !strings.Contains(string(revs[1]), "before replace in path") {
		t.Fatalf("history %s", revs)
	}
	var ur UndoResult
	call(t, a, "doc.undo", PathParams{Path: "src/A.php"}, &ur)
	if !ur.Done || docText(t, a, "src/A.php") != "<?php\nclass Foo {}\n$foo = new Foo();\n" {
		t.Fatal("undo did not restore the document in one step")
	}

	// A file that cannot be written is reported, others continue.
	ro := filepath.Join(root, "src/deep/nested")
	if err := os.Chmod(ro, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(ro, 0o755) })
	call(t, a, "search.replaceInProject", ProjectReplaceParams{ProjectSearchParams: ProjectSearchParams{Pattern: "Bar", CaseSensitive: true}, Replacement: "Baz"}, &res)
	if os.Getuid() != 0 && (res.Failed != 1 || res.FilesChanged == 0) {
		t.Fatalf("partial failure %+v", res)
	}
}

func TestApplyFileOps(t *testing.T) {
	a := newTestApp(t)
	root := openProject(t, a, map[string]string{"a.txt": "hello world\n", "d/b.txt": "b\n", "e/x.txt": "x"})
	ctx := context.Background()
	call(t, a, "doc.open", DocOpenParams{Path: "d/b.txt"}, nil)
	call(t, a, "doc.insert", DocInsertParams{Path: "d/b.txt", Text: "modified "}, nil)
	edits := func(cs ...TextChange) func([]byte) ([]TextChange, error) {
		return func([]byte) ([]TextChange, error) { return cs, nil }
	}
	res, err := a.ApplyFileOps(ctx, "test", []FileOp{
		{Path: "a.txt", Changes: edits(TextChange{Start: 6, End: 11, Text: "there"}, TextChange{Start: 0, End: 0, Text: ">"}, TextChange{Start: 0, End: 0, Text: ">"})},
		{Kind: FileOpCreate, Path: "new/c.txt"},
		{Kind: FileOpCreate, Path: "a.txt", IgnoreIfExists: true},
		{Kind: FileOpRename, Path: "d/b.txt", NewPath: "d2/b.txt"},
		{Kind: FileOpDelete, Path: "e", Recursive: true},
		{Kind: FileOpDelete, Path: "gone", IgnoreIfNotExists: true},
		{Kind: FileOpRename, Path: "x", NewPath: "a.txt", IgnoreIfExists: true},
	})
	if err != nil || len(res) != 7 {
		t.Fatalf("%v %+v", err, res)
	}
	if data, _ := os.ReadFile(filepath.Join(root, "a.txt")); string(data) != ">>hello there\n" {
		t.Fatalf("a.txt %q", data)
	}
	if !exists(filepath.Join(root, "new/c.txt")) || exists(filepath.Join(root, "e")) || !res[2].Skipped || !res[5].Skipped || !res[6].Skipped {
		t.Fatalf("ops %+v", res)
	}
	if data, _ := os.ReadFile(filepath.Join(root, "d2/b.txt")); string(data) != "b\n" || !res[3].Open {
		t.Fatalf("renamed %q %+v", data, res[3])
	}
	if docText(t, a, "d2/b.txt") != "modified b\n" {
		t.Fatal("renamed document lost its buffer")
	}
	wantCode(t, a, "doc.text", DocTextParams{Path: "d/b.txt"}, CodeNotFound)

	// Failures name the step and stop.
	v := uint64(999)
	res, err = a.ApplyFileOps(ctx, "", []FileOp{
		{Kind: FileOpCreate, Path: "a.txt"},
	})
	if ErrorCode(err) != CodeConflict || !strings.Contains(err.Error(), "step 1") || len(res) != 0 {
		t.Fatalf("create existing: %v", err)
	}
	for _, op := range []FileOp{
		{Path: "d2/b.txt", Version: &v, Changes: edits()},
		{Path: "a.txt", Changes: edits(TextChange{Start: 0, End: 5}, TextChange{Start: 2, End: 3})},
		{Path: "a.txt", Changes: edits(TextChange{Start: 0, End: 500})},
		{Path: "a.txt"},
		{Path: "missing.txt", Changes: edits()},
		{Kind: "copy", Path: "a.txt"},
		{Kind: FileOpRename, Path: "a.txt", NewPath: "new/c.txt"},
		{Kind: FileOpRename, Path: "missing", NewPath: "m2"},
		{Kind: FileOpDelete, Path: "missing"},
		{Kind: FileOpDelete, Path: "new"},
	} {
		if _, err := a.ApplyFileOps(ctx, "", []FileOp{op}); err == nil {
			t.Errorf("%+v: expected error", op)
		}
	}
	// Open documents: delete closes, edit is undoable.
	call(t, a, "doc.open", DocOpenParams{Path: "a.txt"}, nil)
	res, err = a.ApplyFileOps(ctx, "", []FileOp{{Path: "a.txt", Changes: edits(TextChange{Start: 0, End: 2, Text: ""})}})
	if err != nil || !res[0].Open || res[0].Edits != 1 {
		t.Fatalf("doc edit %v %+v", err, res)
	}
	res, err = a.ApplyFileOps(ctx, "", []FileOp{{Kind: FileOpDelete, Path: "a.txt"}})
	if err != nil || !res[0].Open || exists(filepath.Join(root, "a.txt")) {
		t.Fatalf("delete open %v %+v", err, res)
	}
	if out, err := ApplyChanges([]byte("abc"), []TextChange{{Start: 1, End: 2, Text: "X"}}); err != nil || string(out) != "aXc" {
		t.Fatal(string(out), err)
	}
	if err := a.SaveAllDocuments(ctx, "test"); err != nil {
		t.Fatal(err)
	}
	cctx, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := a.ApplyFileOps(cctx, "", []FileOp{{Path: "x"}}); err == nil {
		t.Fatal("cancelled ctx")
	}
}
