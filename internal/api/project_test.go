package api

import (
	"encoding/base64"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

func TestProjectLifecycle(t *testing.T) {
	a := newTestApp(t)
	dir := t.TempDir()
	wantCode(t, a, "project.open", PathParams{}, CodeInvalidParams)
	wantCode(t, a, "project.open", PathParams{Path: filepath.Join(dir, "missing")}, CodeNotFound)
	writeFile(t, filepath.Join(dir, "file.txt"), "x")
	wantCode(t, a, "project.open", PathParams{Path: filepath.Join(dir, "file.txt")}, CodeInvalidParams)

	var info ProjectInfo
	call(t, a, "project.info", nil, &info)
	if info.Open {
		t.Fatal("no project expected")
	}
	if err := os.Mkdir(filepath.Join(dir, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(dir, "composer.json"), "{}")
	writeFile(t, filepath.Join(dir, "compose.yaml"), "services:\n  php:\n    image: php:8.3-fpm\n")
	call(t, a, "project.open", PathParams{Path: dir}, &info)
	if !info.Open || !info.Git || !info.Composer || len(info.ComposeFiles) != 1 || info.Name != filepath.Base(dir) || info.SettingsDir == "" {
		t.Fatalf("info %+v", info)
	}
	if a.ProjectSettings() == nil || a.ProjectRoot() != info.Root {
		t.Fatal("project accessors")
	}
	call(t, a, "project.close", nil, &info)
	if info.Open || a.ProjectRoot() != "" {
		t.Fatal("still open")
	}
	wantCode(t, a, "files.list", nil, CodeNoProject)
	wantCode(t, a, "history.list", PathParams{Path: "/x"}, CodeNoProject)
}

// paths flattens a tree into "path[/][!ignored][@library][+collapsed]".
func paths(n *FileNode) []string {
	var out []string
	var walk func(n *FileNode)
	walk = func(n *FileNode) {
		for _, c := range n.Children {
			s := c.Path
			if c.Dir {
				s += "/"
			}
			if c.Ignored {
				s += "!"
			}
			if c.Library {
				s += "@"
			}
			if c.Collapsed {
				s += "+"
			}
			out = append(out, s)
			walk(c)
		}
	}
	walk(n)
	return out
}

func TestFilesList(t *testing.T) {
	a := newTestApp(t)
	dir := t.TempDir()
	for name, content := range map[string]string{
		".gitignore":             "# comment\n/build/\n*.log\n!keep.log\ncache/**\n",
		"index.php":              "<?php",
		"debug.log":              "x",
		"keep.log":               "x",
		"build/out.txt":          "x",
		"cache/a/b.txt":          "x",
		"src/App.php":            "<?php",
		"src/.gitignore":         "generated.php\n",
		"src/generated.php":      "<?php",
		"src/build/x.php":        "<?php", // not ignored: /build/ is anchored at the root
		"vendor/autoload.php":    "<?php",
		"node_modules/x/i.js":    "",
		".git/HEAD":              "ref",
		"docs/deep/er/file.md":   "#",
		".env":                   "A=1",
		"src/Domain/Model/M.php": "<?php",
	} {
		writeFile(t, filepath.Join(dir, filepath.FromSlash(name)), content)
	}
	call(t, a, "project.open", PathParams{Path: dir}, nil)

	var res FilesListResult
	call(t, a, "files.list", nil, &res)
	got := strings.Join(paths(res.Tree), " ")
	want := "cache/ docs/ docs/deep/ docs/deep/er/ docs/deep/er/file.md node_modules/@+ src/ src/build/ src/build/x.php src/Domain/ src/Domain/Model/ src/Domain/Model/M.php " +
		"src/.gitignore src/App.php vendor/@+ .env .gitignore index.php keep.log"
	if got != want {
		t.Fatalf("tree:\n got %s\nwant %s", got, want)
	}
	if res.Files != 9 || res.Dirs != 10 || res.Truncated {
		t.Fatalf("counts %d files %d dirs truncated=%v", res.Files, res.Dirs, res.Truncated)
	}

	call(t, a, "files.list", FilesListParams{Ignored: true, Libraries: true, Depth: 2}, &res)
	all := paths(res.Tree)
	sort.Strings(all)
	joined := " " + strings.Join(all, " ") + " "
	for _, w := range []string{" debug.log! ", " build/! ", " build/out.txt! ", " cache/a/!+ ", " vendor/@ ", " vendor/autoload.php@ ", " node_modules/x/@+ ", " docs/deep/+ ", " src/generated.php! "} {
		if !strings.Contains(joined, w) {
			t.Errorf("missing %q in %s", w, joined)
		}
	}

	call(t, a, "files.list", FilesListParams{Path: "src/Domain"}, &res)
	if got := strings.Join(paths(res.Tree), " "); got != "src/Domain/Model/ src/Domain/Model/M.php" || res.Tree.Name != "Domain" {
		t.Fatalf("subdir: %s", got)
	}
	call(t, a, "files.list", FilesListParams{Path: "src", Limit: 2}, &res)
	if !res.Truncated || res.Files+res.Dirs != 2 {
		t.Fatalf("limit: %+v", res)
	}
	call(t, a, "files.list", FilesListParams{Path: "."}, &res)
	if res.Tree.Path != "" {
		t.Fatal("dot path")
	}
	wantCode(t, a, "files.list", FilesListParams{Path: "../x"}, CodeInvalidParams)
	wantCode(t, a, "files.list", FilesListParams{Path: "nope"}, CodeNotFound)
	wantCode(t, a, "files.list", FilesListParams{Path: "index.php"}, CodeInvalidParams)
}

func TestFilesReadAndLanguage(t *testing.T) {
	a := newTestApp(t)
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "a.php"), "<?php echo 'ü';")
	writeFile(t, filepath.Join(dir, "bin.dat"), "\xff\xfe\x00")
	writeFile(t, filepath.Join(dir, "script"), "#!/usr/bin/env php\n<?php\n")
	call(t, a, "project.open", PathParams{Path: dir}, nil)

	var fc FileContent
	call(t, a, "files.read", FilesReadParams{Path: "a.php"}, &fc)
	if fc.Language != "php" || fc.Encoding != "utf-8" || fc.Content != "<?php echo 'ü';" || fc.Path != "a.php" || fc.Truncated {
		t.Fatalf("read %+v", fc)
	}
	call(t, a, "files.read", FilesReadParams{Path: filepath.Join(dir, "bin.dat")}, &fc)
	if b, _ := base64.StdEncoding.DecodeString(fc.Content); fc.Encoding != "base64" || string(b) != "\xff\xfe\x00" {
		t.Fatalf("binary %+v", fc)
	}
	call(t, a, "files.read", FilesReadParams{Path: "a.php", MaxBytes: 5}, &fc)
	if fc.Content != "<?php" || !fc.Truncated || fc.Size != int64(len("<?php echo 'ü';")) {
		t.Fatalf("truncated %+v", fc)
	}
	wantCode(t, a, "files.read", FilesReadParams{Path: "missing"}, CodeNotFound)
	wantCode(t, a, "files.read", FilesReadParams{Path: "."}, CodeInvalidParams)
	wantCode(t, a, "files.read", FilesReadParams{}, CodeInvalidParams)

	var lr LanguageResult
	call(t, a, "files.detectLanguage", PathParams{Path: "script"}, &lr)
	if lr.Language != "php" {
		t.Fatalf("shebang %+v", lr)
	}
	call(t, a, "files.detectLanguage", PathParams{Path: "new/config.yaml"}, &lr)
	if lr.Language != "yaml" || lr.Path != "new/config.yaml" {
		t.Fatalf("by path %+v", lr)
	}
	content := "<?xml version=\"1.0\"?><a/>"
	call(t, a, "files.detectLanguage", DetectLanguageParams{Path: "noext", Content: &content}, &lr)
	if lr.Language != "xml" {
		t.Fatalf("by content %+v", lr)
	}
	// Outside the project the absolute path is shown. A rooted path counts
	// as absolute on Windows too (current drive), it is never joined with
	// the project root.
	call(t, a, "files.detectLanguage", PathParams{Path: "/elsewhere/x.json"}, &lr)
	if lr.Path != filepath.FromSlash("/elsewhere/x.json") || lr.Language != "json" {
		t.Fatalf("outside %+v", lr)
	}
}

func TestResolveWithoutProject(t *testing.T) {
	a := newTestApp(t)
	abs, err := a.resolvePath("rel.php")
	if err != nil || !filepath.IsAbs(abs) {
		t.Fatalf("%q %v", abs, err)
	}
	if a.displayPath(abs) != abs {
		t.Fatal("displayPath without project")
	}
}
