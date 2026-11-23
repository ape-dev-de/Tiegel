package gui

import (
	"bytes"
	"context"
	"image"
	// Image formats of local Markdown images.
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"gioui.org/layout"
	_ "golang.org/x/image/webp"

	"github.com/ape-dev-de/tiegel/internal/app/gui/controller"
	"github.com/ape-dev-de/tiegel/internal/editor/keymap"
	"github.com/ape-dev-de/tiegel/internal/ui/atoms"
	"github.com/ape-dev-de/tiegel/internal/ui/organisms/mdview"
	"github.com/ape-dev-de/tiegel/internal/ui/tokens"
)

// Markdown reader (D534): a read-only preview tab of Markdown files drawn
// natively with the mdview organism (no WebView), following unsaved
// edits. "Open Markdown Preview" (in place or to the side) from the View
// menu, Find Action and the context menus of editor and project tree.
// Links: "#anchor" scrolls, relative Markdown files open their preview,
// other project files open in the editor, http(s) links open in the
// browser on click. Only local images are shown; remote images are never
// fetched (privacy).

// Markdown preview actions.
const (
	ActMarkdownPreview     keymap.Action = "markdown.preview"
	ActMarkdownPreviewSide keymap.Action = "markdown.previewSide"
)

// maxMarkdownImageBytes bounds the size of a local image file shown.
const maxMarkdownImageBytes = 20 << 20

type markdownDock struct {
	v     *View
	views map[string]*mdPreviewView
	// images are the decoded local images by absolute path (nil while
	// loading or failed; see failed).
	images  map[string]image.Image
	loading map[string]bool
	failed  map[string]bool
}

type mdPreviewView struct {
	view mdview.View
	seen uint64
	// anchor is scrolled to once the document arrived.
	anchor string
}

func installMarkdown(v *View) *markdownDock {
	d := &markdownDock{v: v, views: map[string]*mdPreviewView{}, images: map[string]image.Image{},
		loading: map[string]bool{}, failed: map[string]bool{}}
	r := v.Registry
	r.AddTabType(TabType{Kind: controller.TabMarkdown, Glyph: atoms.GlyphFileText, Layout: d.layoutTab, OnClose: d.closeTab})
	r.AddAction(Action{ID: ActMarkdownPreview, Title: "Open Markdown Preview", Menu: "View", Section: "markdown",
		Run: func(v *View) { d.openActive(false) }})
	r.AddAction(Action{ID: ActMarkdownPreviewSide, Title: "Open Markdown Preview to the Side", Menu: "View", Section: "markdown",
		Run: func(v *View) { d.openActive(true) }})
	menu := func(_ *View, t ContextTarget) []MenuItem {
		file := t.File
		if len(t.Paths) > 0 {
			file = t.Paths[0]
		}
		if !controller.IsMarkdown(file) {
			return nil
		}
		return []MenuItem{
			{Title: "Open Markdown Preview", Icon: "file-text", Run: func() { d.open(file, false) }},
			{Title: "Open Markdown Preview to the Side", Run: func() { d.open(file, true) }},
		}
	}
	r.AddContextMenu(ContextEditor, menu)
	r.AddContextMenu(ContextProjectTree, menu)
	return d
}

// openActive previews the file of the active editor.
func (d *markdownDock) openActive(side bool) {
	t := d.v.ctl.ActiveTab()
	if t == nil || t.Virtual != "" || !controller.IsMarkdown(t.File) {
		d.v.ctl.Status.Notice = "Markdown Preview: the active file is not a Markdown file"
		return
	}
	d.open(t.File, side)
}

func (d *markdownDock) open(file string, side bool) {
	d.v.ctl.Markdown.Open(file, side)
	d.v.focusEditor = true
}

func (d *markdownDock) closeTab(tab *controller.Tab) {
	file, _ := tab.Data.(string)
	d.v.ctl.Markdown.Close(file)
	for _, t := range d.v.ctl.Groups() {
		for _, o := range t.Tabs {
			if o.Virtual == controller.TabMarkdown && o.Data == file && o != tab {
				return
			}
		}
	}
	delete(d.views, file)
}

func (d *markdownDock) layoutTab(gtx layout.Context, th *tokens.Theme, tab *controller.Tab) layout.Dimensions {
	file, _ := tab.Data.(string)
	p := d.v.ctl.Markdown.Preview(file)
	pv := d.views[file]
	if pv == nil {
		pv = &mdPreviewView{}
		pv.view.LoadImage = func(src string) (image.Image, bool) { return d.image(file, src) }
		d.views[file] = pv
	}
	if pv.seen != p.Version && p.Doc != nil {
		pv.seen = p.Version
		pv.view.SetDoc(p.Doc)
		if pv.anchor != "" {
			pv.view.ScrollToAnchor(pv.anchor)
			pv.anchor = ""
		}
	}
	for _, e := range pv.view.Update(gtx) {
		switch e := e.(type) {
		case mdview.LinkEvent:
			d.follow(file, pv, e.Dest)
		case mdview.CopyEvent:
			d.v.CopyText(e.Text)
			d.v.ctl.Status.Notice = "Copied code block"
		}
	}
	if p.Doc == nil && p.Err != "" {
		return layout.UniformInset(th.Space.M).Layout(gtx, atoms.Muted("Cannot show "+filepath.Base(file)+": "+p.Err).Fn(th))
	}
	return pv.view.Layout(gtx, th)
}

// follow handles a clicked link of the preview of file.
func (d *markdownDock) follow(file string, pv *mdPreviewView, dest string) {
	dest = strings.TrimSpace(dest)
	if strings.HasPrefix(dest, "#") {
		if !pv.view.ScrollToAnchor(dest[1:]) {
			d.v.ctl.Status.Notice = "No heading " + dest
		}
		return
	}
	if u, err := url.Parse(dest); err == nil && u.Scheme != "" && u.Scheme != "file" {
		switch strings.ToLower(u.Scheme) {
		case "http", "https":
			d.v.ctl.Terminals.OpenURL(dest)
		default:
			d.v.ctl.Status.Notice = "Links of type " + u.Scheme + ": are not opened"
		}
		return
	}
	target, anchor := localTarget(file, dest)
	if target == "" {
		return
	}
	if _, err := os.Stat(target); err != nil {
		d.v.ctl.Status.Notice = "Link target not found: " + d.v.ctl.Rel(target)
		return
	}
	if controller.IsMarkdown(target) {
		d.v.ctl.Markdown.Open(target, false)
		if anchor != "" {
			npv := d.views[target]
			if npv == nil {
				npv = &mdPreviewView{}
				npv.view.LoadImage = func(src string) (image.Image, bool) { return d.image(target, src) }
				d.views[target] = npv
			}
			if !npv.view.ScrollToAnchor(anchor) {
				npv.anchor = anchor
			}
		}
		return
	}
	d.v.ctl.OpenFile(target, nil)
}

// localTarget resolves a relative (or file:) link of the Markdown file
// to an absolute path and the anchor after "#".
func localTarget(file, dest string) (string, string) {
	anchor := ""
	if i := strings.IndexByte(dest, '#'); i >= 0 {
		dest, anchor = dest[:i], dest[i+1:]
	}
	if u, err := url.Parse(dest); err == nil && u.Scheme == "file" {
		dest = u.Path
	} else if p, err := url.PathUnescape(dest); err == nil {
		dest = p
	}
	if dest == "" {
		return "", anchor
	}
	if !filepath.IsAbs(dest) {
		dest = filepath.Join(filepath.Dir(file), filepath.FromSlash(dest))
	}
	return filepath.Clean(dest), anchor
}

// image returns a local image of the Markdown file, loading it in the
// background on first use. Remote images are never fetched.
func (d *markdownDock) image(file, src string) (image.Image, bool) {
	if u, err := url.Parse(src); err != nil || (u.Scheme != "" && u.Scheme != "file") {
		return nil, false
	}
	path, _ := localTarget(file, src)
	if path == "" {
		return nil, false
	}
	if img := d.images[path]; img != nil {
		return img, true
	}
	if d.loading[path] || d.failed[path] {
		return nil, false
	}
	d.loading[path] = true
	d.v.ctl.Go(func(context.Context) func() {
		img := decodeImageFile(path)
		return func() {
			delete(d.loading, path)
			if img == nil {
				d.failed[path] = true
				return
			}
			d.images[path] = img
			for _, pv := range d.views {
				pv.view.ReloadImages()
			}
			d.v.Invalidate()
		}
	})
	return nil, false
}

// decodeImageFile decodes a local PNG, JPEG, GIF or WebP file (nil on
// failure or when it is too large).
func decodeImageFile(path string) image.Image {
	st, err := os.Stat(path)
	if err != nil || st.IsDir() || st.Size() > maxMarkdownImageBytes {
		return nil
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	img, _, err := image.Decode(bytes.NewReader(b))
	if err != nil {
		return nil
	}
	return img
}
