package langphp

import (
	"path/filepath"
	"testing"

	"github.com/ape-dev-de/tiegel/internal/api"
	"github.com/ape-dev-de/tiegel/internal/modules/modtest"
)

func TestInlayHints(t *testing.T) {
	a, _, f, dir := setup(t)
	modtest.WriteFile(t, filepath.Join(dir, "h.php"), "<?php\ncall($a);\n$typed = 1;\n")
	modtest.Call(t, a, "doc.open", api.DocOpenParams{Path: "h.php"}, nil)
	modtest.Call(t, a, "lsp.start", StartParams{Command: "fake"}, nil)
	waitSynced(t, a, f, dir, "h.php")
	var res InlayHintsResult
	modtest.Call(t, a, "lsp.inlayHints", InlayHintsParams{Path: "h.php"}, &res)
	if !res.Supported || len(res.Hints) != 2 {
		t.Fatalf("%+v", res)
	}
	h := res.Hints[0]
	if h.Label != "arg:" || h.Kind != "parameter" || h.Offset != 11 || h.Line != 2 || h.Col != 6 || !h.PaddingRight {
		t.Fatalf("parameter %+v", h)
	}
	if res.Hints[1].Label != ": int" || res.Hints[1].Kind != "type" || res.Hints[1].Offset != 22 {
		t.Fatalf("type %+v", res.Hints[1])
	}
	modtest.Call(t, a, "lsp.inlayHints", InlayHintsParams{Path: "h.php", Start: &api.Pos{Line: 3}, End: &api.Pos{Line: 3, Col: 20}}, &res)
	if len(res.Hints) != 1 || res.Hints[0].Kind != "type" {
		t.Fatalf("range %+v", res.Hints)
	}
	modtest.WantCode(t, a, "lsp.inlayHints", InlayHintsParams{Path: "h.php", Start: &api.Pos{Offset: 999}}, api.CodeInvalidParams)
	modtest.WantCode(t, a, "lsp.inlayHints", InlayHintsParams{Path: "h.php", End: &api.Pos{Offset: 999}}, api.CodeInvalidParams)
}
