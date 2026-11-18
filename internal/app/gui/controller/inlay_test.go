package controller

import (
	"encoding/json"
	"errors"
	"testing"
	"time"
)

func TestLoadInlayHints(t *testing.T) {
	f := projectFake()
	c, _ := navStarted(t, f)
	var got InlayHints
	c.Status.LSPRunning = false
	c.LoadInlayHints("/p/A.php", func(h InlayHints) { got = h })
	if got.Supported {
		t.Fatal("without server")
	}
	c.Status.LSPRunning = true
	f.answer("lsp.inlayHints", map[string]any{"supported": true, "version": 3, "hints": []map[string]any{{"offset": 7, "line": 1, "col": 8, "label": "x:", "kind": "parameter", "paddingRight": true}}})
	c.LoadInlayHints("/p/A.php", func(h InlayHints) { got = h })
	c.Settle(time.Second)
	if !got.Supported || got.Version != 3 || len(got.Hints) != 1 || got.Hints[0].Offset != 7 || !got.Hints[0].PadRight || navParamsOf(f, "lsp.inlayHints")["path"] != "/p/A.php" {
		t.Fatalf("%+v", got)
	}
	f.on("lsp.inlayHints", func(json.RawMessage) (any, error) { return nil, errors.New("down") })
	c.LoadInlayHints("/p/A.php", func(h InlayHints) { got = h })
	c.Settle(time.Second)
	if got.Supported {
		t.Fatal("error answer")
	}
	if !c.Prefs.InlayHints {
		t.Fatal("default on")
	}
}
