package gui

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"path/filepath"
	"slices"
	"testing"

	"github.com/ape-dev-de/tiegel/internal/app/gui/controller"
	"github.com/ape-dev-de/tiegel/internal/core/settings"
	"github.com/ape-dev-de/tiegel/internal/markdown"
)

func pngBytes(t *testing.T) string {
	t.Helper()
	img := image.NewNRGBA(image.Rect(0, 0, 4, 3))
	img.Set(1, 1, color.NRGBA{R: 255, A: 255})
	var b bytes.Buffer
	if err := png.Encode(&b, img); err != nil {
		t.Fatal(err)
	}
	return b.String()
}

// TestMarkdownPreviewTab: the preview opens to the side of the editor,
// follows edits, resolves links and loads local images only.
func TestMarkdownPreviewTab(t *testing.T) {
	e := newEnvWith(t, true, map[string]string{
		"README.md":     "# Readme\n\nSee [guide](docs/guide.md#usage), [code](src/a.php), [top](#readme), [web](https://example.org).\n\n![logo](logo.png) ![remote](https://example.org/x.png)\n",
		"docs/guide.md": "# Guide\n\n## Usage\n\ntext\n",
		"src/a.php":     "<?php\n",
		"logo.png":      pngBytes(t),
	}, nil)
	// The automatic split is tested in TestMarkdownOpensSplit.
	if err := settings.Set(e.app.Settings(), markdown.KeyOpenMode, markdown.OpenCode); err != nil {
		t.Fatal(err)
	}
	readme := filepath.Join(e.root, "README.md")
	s := e.openEditor("README.md")

	// Context menus offer the preview for Markdown only.
	if items := e.v.Registry.ContextMenu(e.v, ContextEditor, ContextTarget{File: readme}); !slices.ContainsFunc(items, func(m MenuItem) bool { return m.Title == "Open Markdown Preview to the Side" }) {
		t.Fatalf("editor menu %q", menuTitles(items))
	}
	if items := e.v.Registry.ContextMenu(e.v, ContextProjectTree, ContextTarget{Paths: []string{filepath.Join(e.root, "src", "a.php")}}); slices.ContainsFunc(items, func(m MenuItem) bool { return m.Title == "Open Markdown Preview" }) {
		t.Fatal("preview offered for PHP")
	}

	e.v.RunAction(ActMarkdownPreviewSide)
	e.waitFor("preview", func() bool {
		p := e.ctl.Markdown.Preview(readme)
		return p.Doc != nil && len(e.ctl.Groups()) == 2
	})
	tab := e.ctl.ActiveTab()
	if tab == nil || tab.Virtual != controller.TabMarkdown {
		t.Fatalf("active tab %+v", tab)
	}
	e.frame()
	pv := e.v.mdd.views[readme]
	if pv == nil || pv.view.Doc() == nil || pv.view.Doc().Blocks[0].Kind != markdown.Heading {
		t.Fatal("view has no document")
	}
	// The local image loads, the remote one is never asked for.
	e.waitFor("image", func() bool { e.frame(); return e.v.mdd.images[filepath.Join(e.root, "logo.png")] != nil })
	if _, ok := e.v.mdd.image(readme, "https://example.org/x.png"); ok || len(e.v.mdd.loading) != 0 {
		t.Fatal("remote image loaded")
	}
	if _, ok := e.v.mdd.image(readme, "missing.png"); ok {
		t.Fatal("missing image")
	}
	e.waitFor("failed image", func() bool { return e.v.mdd.failed[filepath.Join(e.root, "missing.png")] })

	// Edits in the editor reach the preview.
	v := e.ctl.Markdown.Preview(readme).Version
	s.doc.Lock()
	s.ed.SetCaret(0)
	s.ed.Cursor().Insert("Intro\n\n")
	s.doc.Unlock()
	e.frame()
	e.waitFor("live update", func() bool { return e.ctl.Markdown.Preview(readme).Version > v })

	// Links.
	e.v.mdd.follow(readme, pv, "#readme")
	e.v.mdd.follow(readme, pv, "mailto:x@example.org")
	e.v.mdd.follow(readme, pv, "nope.md")
	if e.ctl.Status.Notice == "" {
		t.Fatal("no notice for a missing target")
	}
	e.v.mdd.follow(readme, pv, "docs/guide.md#usage")
	guide := filepath.Join(e.root, "docs", "guide.md")
	e.waitFor("guide preview", func() bool {
		t := e.ctl.ActiveTab()
		return t != nil && t.Virtual == controller.TabMarkdown && t.Data == guide && e.ctl.Markdown.Preview(guide).Doc != nil
	})
	e.frame()
	if gv := e.v.mdd.views[guide]; gv == nil || gv.anchor != "" {
		t.Fatalf("anchor not applied %+v", gv)
	}
	e.v.mdd.follow(readme, pv, "src/a.php")
	e.waitFor("php file", func() bool { return e.activeFile() == filepath.Join(e.root, "src", "a.php") })

	// Closing the preview tab forgets it.
	e.v.mdd.closeTab(tab)
	if _, ok := e.v.mdd.views[readme]; ok && !e.ctl.FileOpen(controller.VirtualKey(controller.TabMarkdown, readme)) {
		t.Fatal("view kept")
	}

	// The action needs a Markdown file.
	e.openEditor("src/a.php")
	e.v.RunAction(ActMarkdownPreview)
	if e.ctl.Status.Notice == "" {
		t.Fatal("no notice for a PHP file")
	}
	if a, b := localTarget("/p/README.md", "file:///x/y.md#a"); a != "/x/y.md" || b != "a" {
		t.Fatalf("file link %q %q", a, b)
	}
	if a, _ := localTarget("/p/README.md", "docs/a%20b.md"); a != filepath.Join("/p", "docs", "a b.md") {
		t.Fatalf("escaped %q", a)
	}
	if decodeImageFile(filepath.Join(e.root, "README.md")) != nil || decodeImageFile(e.root) != nil {
		t.Fatal("decoded a non-image")
	}
}

// TestMarkdownOpensSplit: opening a Markdown file shows the editor on the
// left and the preview on the right; the editor keeps the focus. With
// markdown.openMode "code" only the editor opens.
func TestMarkdownOpensSplit(t *testing.T) {
	e := newEnvWith(t, true, map[string]string{"README.md": "# Readme\n", "NOTES.md": "# Notes\n"}, nil)
	readme := filepath.Join(e.root, "README.md")
	e.ctl.OpenFile("README.md", nil)
	e.waitFor("split", func() bool { return len(e.ctl.Groups()) == 2 && e.ctl.Markdown.Preview(readme).Doc != nil })
	at := func(g int) *controller.Tab {
		tabs, active := e.ctl.GroupTabs(e.ctl.Groups()[g])
		if active < 0 || active >= len(tabs) {
			return nil
		}
		return tabs[active]
	}
	left, right := at(0), at(1)
	if left == nil || left.File != readme || left.Virtual != "" || right == nil || right.Virtual != controller.TabMarkdown || right.Data != readme {
		t.Fatalf("groups: left %+v right %+v", left, right)
	}
	if a := e.ctl.ActiveTab(); a != left {
		t.Fatalf("focus on %+v, want the editor", a)
	}
	// Opening it again does not add a second preview.
	e.ctl.OpenFile("README.md", nil)
	e.settle()
	n := 0
	for _, g := range e.ctl.Groups() {
		tabs, _ := e.ctl.GroupTabs(g)
		for _, x := range tabs {
			if x.Virtual == controller.TabMarkdown {
				n++
			}
		}
	}
	if n != 1 {
		t.Fatalf("%d previews", n)
	}
	// "code": no preview.
	if err := settings.Set(e.app.Settings(), markdown.KeyOpenMode, markdown.OpenCode); err != nil {
		t.Fatal(err)
	}
	notes := filepath.Join(e.root, "NOTES.md")
	e.ctl.OpenFile("NOTES.md", nil)
	e.waitFor("notes", func() bool { a := e.ctl.ActiveTab(); return a != nil && a.File == notes })
	e.settle()
	for _, g := range e.ctl.Groups() {
		tabs, _ := e.ctl.GroupTabs(g)
		for _, x := range tabs {
			if x.Virtual == controller.TabMarkdown && x.Data == notes {
				t.Fatal("preview opened in code mode")
			}
		}
	}
}
