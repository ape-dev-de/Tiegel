package controller

import (
	"encoding/json"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ape-dev-de/tiegel/internal/api"
	"github.com/ape-dev-de/tiegel/internal/markdown"
)

// Previews render through md.render, follow edits and saves, open in a
// tab or to the side, and are forgotten when no tab shows them.
func TestMarkdownPreview(t *testing.T) {
	f := projectFake()
	var n atomic.Int32
	f.on("md.render", func(raw json.RawMessage) (any, error) {
		var p api.MarkdownParams
		_ = json.Unmarshal(raw, &p)
		if p.Path == root+"/missing.md" {
			return nil, api.Errorf(api.CodeNotFound, "missing")
		}
		k := n.Add(1)
		return api.MarkdownResult{Path: p.Path, Doc: &markdown.Doc{Blocks: []markdown.Block{{Kind: markdown.Heading, Level: int(k)}}}}, nil
	})
	c := started(t, f)
	md := c.Markdown
	file := root + "/README.md"
	for f, want := range map[string]bool{"a.md": true, "B.MARKDOWN": true, "x.php": false, "md": false} {
		if IsMarkdown(f) != want {
			t.Errorf("IsMarkdown(%s) != %v", f, want)
		}
	}

	c.OpenFile(file, nil)
	c.Settle(time.Second)
	tab := md.Open(file, true)
	if tab == nil || tab.Virtual != TabMarkdown || tab.Data != file || len(c.Groups()) != 2 {
		t.Fatalf("side preview %+v, %d groups", tab, len(c.Groups()))
	}
	if g := c.Groups()[0]; len(g.Tabs) != 1 || g.Tabs[0].File != file {
		t.Fatalf("editor group %+v", g.Tabs)
	}
	p := md.Preview(file)
	waitFor(t, c, "first render", func() bool { return p.Doc != nil })
	v := p.Version
	f.emit(api.TopicDocEdited, map[string]any{"path": "README.md", "file": file})
	waitFor(t, c, "render after edit", func() bool { return p.Version > v })
	v = p.Version
	f.emit("file.saved", map[string]any{"file": file})
	waitFor(t, c, "render after save", func() bool { return p.Version > v })
	// Edits of other files render nothing.
	calls := f.called("md.render")
	f.emit(api.TopicDocEdited, map[string]any{"path": "x.php", "file": root + "/x.php"})
	c.Settle(200 * time.Millisecond)
	if f.called("md.render") != calls {
		t.Fatal("rendered for another file")
	}
	// A burst of edits renders at most twice more (one running, one stale).
	for range 10 {
		md.FileChanged(file)
	}
	c.Settle(time.Second)
	if got := f.called("md.render") - calls; got < 1 || got > 2 {
		t.Fatalf("%d renders for a burst", got)
	}

	// Side with two groups: the neighbour group takes it.
	c.FocusGroup(0)
	if md.Open(file, true); c.ActiveGroup() != c.Groups()[1] || len(c.Groups()) != 2 {
		t.Fatal("not opened in the neighbour group")
	}
	// The preview stays while a tab shows it, then is forgotten.
	md.Close(file)
	if md.previews[file] == nil {
		t.Fatal("preview dropped while shown")
	}
	c.CloseTab(c.Active)
	md.Close(file)
	if md.previews[file] != nil {
		t.Fatal("preview kept")
	}

	miss := md.Preview(root + "/missing.md")
	waitFor(t, c, "error", func() bool { return miss.Err != "" })
	if tab := md.Open(root+"/other.md", false); tab == nil || tab.Label != "other.md (Preview)" {
		t.Fatalf("tab %+v", tab)
	}
}
