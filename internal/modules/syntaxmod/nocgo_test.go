//go:build !cgo

package syntaxmod

import (
	"testing"

	"github.com/ape-dev-de/tiegel/internal/api"
	"github.com/ape-dev-de/tiegel/internal/modules/modtest"
)

func TestUnavailableWithoutTreeSitter(t *testing.T) {
	a, _ := newApp(t)
	modtest.Project(t, a, map[string]string{"a.php": "<?php echo 1;\n"})
	modtest.WantCode(t, a, "syntax.highlights", HighlightsParams{Source: Source{Path: "a.php"}}, api.CodeUnavailable)
	modtest.WantCode(t, a, "syntax.outline", Source{Text: ptr("{}"), Language: "json"}, api.CodeUnavailable)
	modtest.Call(t, a, "doc.open", api.DocOpenParams{Path: "a.php"}, nil)
	modtest.WantCode(t, a, "syntax.folds", Source{Path: "a.php"}, api.CodeUnavailable)
	// Overriding the language still works.
	var h HighlightsResult
	modtest.Call(t, a, "syntax.highlights", HighlightsParams{Source: Source{Path: "a.php", Language: "plaintext"}}, &h)
	if !h.Open || len(h.Spans) != 0 {
		t.Fatalf("%+v", h)
	}
}
