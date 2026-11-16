package api

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ape-dev-de/tiegel/internal/core/autosave"
	"github.com/ape-dev-de/tiegel/internal/core/history"
)

func openProject(t *testing.T, a *App, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for name, content := range files {
		writeFile(t, filepath.Join(dir, filepath.FromSlash(name)), content)
	}
	var info ProjectInfo
	call(t, a, "project.open", PathParams{Path: dir}, &info)
	return info.Root
}

func docText(t *testing.T, a *App, path string) string {
	t.Helper()
	var dt DocText
	call(t, a, "doc.text", DocTextParams{Path: path}, &dt)
	return dt.Text
}

func TestDocEditing(t *testing.T) {
	a := newTestApp(t)
	root := openProject(t, a, map[string]string{"a.php": "<?php\necho 'ä';\n"})

	var open DocOpenResult
	call(t, a, "doc.open", DocOpenParams{Path: "a.php", Text: true}, &open)
	if open.Text != "<?php\necho 'ä';\n" || open.Language != "php" || open.Lines != 3 || open.Modified || open.EOL != "lf" {
		t.Fatalf("open %+v", open)
	}
	call(t, a, "doc.open", DocOpenParams{Path: filepath.Join(root, "a.php")}, &open)
	if !open.AlreadyOpen {
		t.Fatal("second open should report alreadyOpen")
	}
	wantCode(t, a, "doc.open", DocOpenParams{Path: "missing.php"}, CodeNotFound)

	var st DocState
	// Line 2, column 7 (after the multi-byte ä): characters, not bytes.
	call(t, a, "doc.insert", DocInsertParams{Path: "a.php", At: Pos{Line: 2, Col: 8}, Text: "!"}, &st)
	if got := docText(t, a, "a.php"); got != "<?php\necho 'ä!';\n" || !st.Modified || st.Version == 0 {
		t.Fatalf("insert: %q %+v", got, st)
	}
	call(t, a, "doc.insert", DocInsertParams{Path: "a.php", At: Pos{Offset: 0}, Text: "#", Version: VersionOf(st.Version)}, &st)
	wantCode(t, a, "doc.insert", DocInsertParams{Path: "a.php", Text: "x", Version: VersionOf(1)}, CodeConflict)
	wantCode(t, a, "doc.insert", DocInsertParams{Path: "a.php", At: Pos{Line: 99}}, CodeInvalidParams)
	wantCode(t, a, "doc.insert", DocInsertParams{Path: "a.php", At: Pos{Offset: -1}}, CodeInvalidParams)

	call(t, a, "doc.delete", DocDeleteParams{Path: "a.php", Start: Pos{Offset: 0}, End: Pos{Offset: 1}}, &st)
	if got := docText(t, a, "a.php"); got != "<?php\necho 'ä!';\n" {
		t.Fatalf("delete: %q", got)
	}
	wantCode(t, a, "doc.delete", DocDeleteParams{Path: "a.php", Start: Pos{Offset: 3}, End: Pos{Offset: 1}}, CodeInvalidParams)
	wantCode(t, a, "doc.delete", DocDeleteParams{Path: "a.php", Start: Pos{Offset: 0}, End: Pos{Line: 50}}, CodeInvalidParams)

	call(t, a, "doc.replace", DocReplaceParams{Path: "a.php", Start: &Pos{Line: 2, Col: 1}, End: &Pos{Line: 2, Col: 5}, Text: "print"}, &st)
	if got := docText(t, a, "a.php"); got != "<?php\nprint 'ä!';\n" {
		t.Fatalf("replace: %q", got)
	}
	before := st.Version
	call(t, a, "doc.replace", DocReplaceParams{Path: "a.php", Start: &Pos{Line: 2, Col: 1}, End: &Pos{Line: 2, Col: 6}, Text: "print"}, &st)
	if st.Version != before {
		t.Fatal("identical replace changed the version")
	}

	var dt DocText
	call(t, a, "doc.text", DocTextParams{Path: "a.php", Start: &Pos{Line: 2}, End: &Pos{Line: 2, Col: 6}}, &dt)
	if dt.Text != "print" {
		t.Fatalf("range text %q", dt.Text)
	}
	wantCode(t, a, "doc.text", DocTextParams{Path: "a.php", Start: &Pos{Offset: 999}}, CodeInvalidParams)

	var ur UndoResult
	call(t, a, "doc.undo", PathParams{Path: "a.php"}, &ur)
	if !ur.Done || docText(t, a, "a.php") != "<?php\necho 'ä!';\n" || !ur.CanRedo {
		t.Fatalf("undo %+v", ur)
	}
	call(t, a, "doc.redo", PathParams{Path: "a.php"}, &ur)
	if !ur.Done || docText(t, a, "a.php") != "<?php\nprint 'ä!';\n" {
		t.Fatalf("redo %+v", ur)
	}
	call(t, a, "doc.redo", PathParams{Path: "a.php"}, &ur)
	if ur.Done {
		t.Fatal("nothing to redo expected")
	}

	call(t, a, "doc.replace", DocReplaceParams{Path: "a.php", Text: "<?php\n"}, &st)
	if docText(t, a, "a.php") != "<?php\n" {
		t.Fatal("whole replace")
	}

	var list []DocState
	call(t, a, "doc.list", nil, &list)
	if len(list) != 1 || list[0].Path != "a.php" {
		t.Fatalf("list %+v", list)
	}
	var cr DocCloseResult
	wantCode(t, a, "doc.close", PathParams{Path: "a.php"}, CodeConflict) // unsaved changes
	call(t, a, "doc.close", DocCloseParams{Path: "a.php", Discard: true}, &cr)
	if !cr.Discarded {
		t.Fatal("close should report discarded changes")
	}
	wantCode(t, a, "doc.text", DocTextParams{Path: "a.php"}, CodeNotFound)
	wantCode(t, a, "doc.close", PathParams{Path: "a.php"}, CodeNotFound)
	wantCode(t, a, "doc.close", PathParams{}, CodeInvalidParams)
	if data, _ := os.ReadFile(filepath.Join(root, "a.php")); string(data) != "<?php\necho 'ä';\n" {
		t.Fatalf("disk changed without save: %q", data)
	}
}

func TestDocSaveHistoryAndRestore(t *testing.T) {
	a := newTestApp(t)
	root := openProject(t, a, map[string]string{"a.php": "<?php\n", "dos.txt": "a\r\nb\r\n"})
	path := filepath.Join(root, "a.php")
	if err := os.Chmod(path, 0o600); err != nil {
		t.Fatal(err)
	}
	// A formatter-like filter upper-cases the content.
	autosave.BeforeSave.On(a.Hooks(), 0, func(_ context.Context, e autosave.SaveEvent) (autosave.SaveEvent, error) {
		if strings.HasSuffix(e.Path, "a.php") {
			e.Content = []byte(strings.ToUpper(string(e.Content)))
		}
		return e, nil
	})

	call(t, a, "doc.open", DocOpenParams{Path: "a.php"}, nil)
	call(t, a, "doc.insert", DocInsertParams{Path: "a.php", At: Pos{Line: 2}, Text: "echo 1;\n"}, nil)
	var sr DocSaveResult
	call(t, a, "doc.save", PathParams{Path: "a.php"}, &sr)
	if sr.Modified || sr.Revision != 1 || sr.Size != len("<?PHP\nECHO 1;\n") {
		t.Fatalf("save %+v", sr)
	}
	data, _ := os.ReadFile(path)
	if string(data) != "<?PHP\nECHO 1;\n" || docText(t, a, "a.php") != string(data) {
		t.Fatalf("filtered save: disk %q buffer %q", data, docText(t, a, "a.php"))
	}
	if st, _ := os.Stat(path); st.Mode().Perm() != 0o600 {
		t.Fatalf("mode %v", st.Mode())
	}

	call(t, a, "doc.insert", DocInsertParams{Path: "a.php", At: Pos{Line: 3}, Text: "X"}, nil)
	var all SaveAllResult
	call(t, a, "doc.saveAll", nil, &all)
	if len(all.Saved) != 1 || all.Saved[0] != "a.php" {
		t.Fatalf("saveAll %+v", all)
	}

	var revs []history.Revision
	call(t, a, "history.list", PathParams{Path: "a.php"}, &revs)
	if len(revs) != 2 || revs[0].ID != 2 {
		t.Fatalf("revisions %+v", revs)
	}
	var rc RevisionContent
	call(t, a, "history.content", RevisionParams{Path: "a.php", ID: 1}, &rc)
	if rc.Text != "<?PHP\nECHO 1;\n" {
		t.Fatalf("content %q", rc.Text)
	}
	wantCode(t, a, "history.content", RevisionParams{Path: "a.php", ID: 99}, CodeNotFound)
	var rev history.Revision
	call(t, a, "history.restore", RevisionParams{Path: "a.php", ID: 1}, &rev)
	if rev.ID != 3 {
		t.Fatalf("restore %+v", rev)
	}
	if docText(t, a, "a.php") != "<?PHP\nECHO 1;\n" {
		t.Fatal("open document not reloaded after restore")
	}
	wantCode(t, a, "history.restore", RevisionParams{Path: "a.php", ID: 42}, CodeNotFound)
	call(t, a, "history.list", PathParams{Path: "never-saved.php"}, &revs)
	if len(revs) != 0 {
		t.Fatal("expected empty history")
	}

	var open DocOpenResult
	call(t, a, "doc.open", DocOpenParams{Path: "dos.txt"}, &open)
	if open.EOL != "crlf" {
		t.Fatalf("eol %+v", open)
	}

	// New files can be created.
	call(t, a, "doc.open", DocOpenParams{Path: "new/b.php", Create: true}, &open)
	if open.Length != 0 || open.Language != "php" {
		t.Fatalf("create %+v", open)
	}
	call(t, a, "doc.insert", DocInsertParams{Path: "new/b.php", Text: "<?php"}, nil)
	// The directory does not exist: saving fails.
	wantCode(t, a, "doc.save", PathParams{Path: "new/b.php"}, CodeFailed)
	wantCode(t, a, "doc.saveAll", nil, CodeFailed)
	wantCode(t, a, "doc.save", PathParams{Path: "nope.php"}, CodeNotFound)
}

func TestDocSaveWithoutProject(t *testing.T) {
	a := newTestApp(t)
	path := filepath.Join(t.TempDir(), "x.json")
	writeFile(t, path, "{}")
	call(t, a, "doc.open", DocOpenParams{Path: path}, nil)
	call(t, a, "doc.replace", DocReplaceParams{Path: path, Text: `{"a":1}`}, nil)
	var sr DocSaveResult
	call(t, a, "doc.save", PathParams{Path: path}, &sr)
	if sr.Revision != 0 || sr.Path != path {
		t.Fatalf("save %+v", sr)
	}
	if data, _ := os.ReadFile(path); string(data) != `{"a":1}` {
		t.Fatalf("disk %q", data)
	}
	// Closing the project discards documents; the saver ignores unknown docs.
	if err := (docSet{a}).Save(context.Background(), autosave.Doc{Path: filepath.Join(filepath.Dir(path), "other"), Content: []byte("x")}); err != nil {
		t.Fatal(err)
	}
	if err := writeFileAtomic(filepath.Join(path, "below-file"), nil); err == nil {
		t.Fatal("expected error writing below a file")
	}
}

func TestDocSaveVersionMovedDuringSave(t *testing.T) {
	a := newTestApp(t)
	path := filepath.Join(t.TempDir(), "x.txt")
	writeFile(t, path, "a")
	call(t, a, "doc.open", DocOpenParams{Path: path}, nil)
	call(t, a, "doc.insert", DocInsertParams{Path: path, Text: "b"}, nil)
	// Simulate a stale snapshot: the document stays modified.
	if err := (docSet{a}).Save(context.Background(), autosave.Doc{Path: path, Content: []byte("old"), Version: 999}); err != nil {
		t.Fatal(err)
	}
	var st []DocState
	call(t, a, "doc.list", nil, &st)
	if !st[0].Modified {
		t.Fatal("document must stay modified")
	}
}

func TestSearch(t *testing.T) {
	a := newTestApp(t)
	openProject(t, a, map[string]string{"a.php": "<?php\n$foo = 1;\n$Foo = 2; // föo foo\n"})
	var res SearchResult
	call(t, a, "search.inDoc", SearchParams{Path: "a.php", Pattern: "foo"}, &res)
	if len(res.Matches) != 3 || res.Version != 0 {
		t.Fatalf("disk search %+v", res)
	}
	m := res.Matches[2]
	if m.Line != 3 || m.Col != 18 || m.Text != "foo" || m.LineText != "$Foo = 2; // föo foo" {
		t.Fatalf("match %+v", m)
	}
	call(t, a, "search.inDoc", SearchParams{Path: "a.php", Pattern: "foo", CaseSensitive: true, Limit: 1}, &res)
	if len(res.Matches) != 1 || !res.Limited {
		t.Fatalf("limited %+v", res)
	}
	call(t, a, "search.inDoc", SearchParams{Path: "a.php", Pattern: `\$\w+`, Regex: true}, &res)
	if len(res.Matches) != 2 {
		t.Fatalf("regex %+v", res)
	}
	wantCode(t, a, "search.inDoc", SearchParams{Path: "a.php", Pattern: "(", Regex: true}, CodeInvalidParams)
	wantCode(t, a, "search.inDoc", SearchParams{Path: "a.php"}, CodeInvalidParams)
	wantCode(t, a, "search.inDoc", SearchParams{Path: "missing", Pattern: "x"}, CodeNotFound)
	wantCode(t, a, "search.inDoc", SearchParams{Pattern: "x"}, CodeInvalidParams)

	call(t, a, "doc.open", DocOpenParams{Path: "a.php"}, nil)
	var rr ReplaceResult
	call(t, a, "search.replaceInDoc", ReplaceParams{SearchParams: SearchParams{Path: "a.php", Pattern: "foo"}, Replacement: "bar"}, &rr)
	if rr.Replaced != 3 || !rr.Modified {
		t.Fatalf("replace %+v", rr)
	}
	call(t, a, "search.inDoc", SearchParams{Path: "a.php", Pattern: "bar"}, &res)
	if len(res.Matches) != 3 || res.Version == 0 {
		t.Fatalf("search in open doc %+v", res)
	}
	var ur UndoResult
	call(t, a, "doc.undo", PathParams{Path: "a.php"}, &ur)
	if !strings.Contains(docText(t, a, "a.php"), "$foo") {
		t.Fatal("replace all should be one undo step")
	}
	wantCode(t, a, "search.replaceInDoc", ReplaceParams{SearchParams: SearchParams{Path: "a.php", Pattern: ""}}, CodeInvalidParams)
}
