package controller

import (
	"encoding/json"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/ape-dev-de/tiegel/internal/api"
	"github.com/ape-dev-de/tiegel/internal/editor/buffer"
)

// Review 2026-11-16 #2: closing a tab and reopening the file at once let
// the late doc.close (after a slow doc.save) close the reopened document.
// OpenFile now waits for the pending close, and doc.close carries the
// tab's generation.
func TestReopenWaitsForPendingClose(t *testing.T) {
	f := projectFake()
	var mu sync.Mutex
	gen := uint64(0)
	f.on("doc.open", func(raw json.RawMessage) (any, error) {
		var p pathParams
		_ = json.Unmarshal(raw, &p)
		mu.Lock()
		gen++
		g := gen
		mu.Unlock()
		f.mu.Lock()
		f.docs[p.Path] = &fakeDoc{path: p.Path, buf: buffer.NewString("<?php\n")}
		f.mu.Unlock()
		return map[string]any{"path": p.Path, "language": "php", "eol": "lf", "generation": g}, nil
	})
	release := make(chan struct{})
	f.on("doc.save", func(json.RawMessage) (any, error) {
		<-release // a slow save (formatter, network drive, …)
		return DocState{}, nil
	})
	c := started(t, f)
	file := filepath.Join(root, "index.php")
	c.OpenFile(file, nil)
	c.Settle(time.Second)
	tab := c.Tab(file)
	if tab == nil || tab.Generation == 0 {
		t.Fatalf("tab %+v", tab)
	}
	firstGen := tab.Generation
	tab.Modified = true

	c.CloseTab(c.tabIndex(file))
	c.OpenFile(file, nil) // reopened while doc.save/doc.close are pending
	time.AfterFunc(100*time.Millisecond, func() { close(release) })
	c.Settle(3 * time.Second)

	f.mu.Lock()
	lastOpen, lastClose := -1, -1
	for i, m := range f.calls {
		switch m {
		case "doc.open":
			lastOpen = i
		case "doc.close":
			lastClose = i
		}
	}
	closeParams := f.params["doc.close"]
	f.mu.Unlock()
	if lastClose < 0 || lastOpen < lastClose {
		t.Fatalf("reopen ran before the pending close finished: %v", f.calls)
	}
	var cp struct {
		Path       string `json:"path"`
		Generation uint64 `json:"generation"`
		Discard    bool   `json:"discard"`
	}
	_ = json.Unmarshal(closeParams[len(closeParams)-1], &cp)
	if cp.Generation != firstGen || cp.Discard {
		t.Fatalf("doc.close params %+v", cp)
	}
	if nt := c.Tab(file); nt == nil || nt.Generation == firstGen || nt.Doc == nil {
		t.Fatalf("reopened tab %+v", nt)
	}
}

// A close refused because of unsaved changes (edited after the tab's
// modified flag was last mirrored) saves first and retries; changes are
// never discarded.
func TestCloseTabSavesOnUnsavedConflict(t *testing.T) {
	f := projectFake()
	var mu sync.Mutex
	unsaved := true
	f.on("doc.close", func(raw json.RawMessage) (any, error) {
		mu.Lock()
		defer mu.Unlock()
		var p map[string]any
		_ = json.Unmarshal(raw, &p)
		if p["discard"] == true {
			t.Errorf("GUI must never discard: %v", p)
		}
		if unsaved {
			return nil, api.Errorf(api.CodeConflict, "unsaved changes")
		}
		return map[string]any{}, nil
	})
	f.on("doc.save", func(json.RawMessage) (any, error) {
		mu.Lock()
		unsaved = false
		mu.Unlock()
		return DocState{}, nil
	})
	c := started(t, f)
	file := filepath.Join(root, "index.php")
	c.OpenFile(file, nil)
	c.Settle(time.Second)
	c.Status.Notice = ""
	c.CloseTab(c.tabIndex(file)) // Modified=false in the mirror
	c.Settle(time.Second)
	if f.called("doc.save") != 1 || f.called("doc.close") != 2 || c.Status.Notice != "" {
		t.Fatalf("calls %v notice %q", f.calls, c.Status.Notice)
	}
}
