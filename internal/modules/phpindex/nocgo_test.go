//go:build !cgo

package phpindex

import (
	"testing"

	"github.com/ape-dev-de/tiegel/internal/api"
	"github.com/ape-dev-de/tiegel/internal/core/module"
	"github.com/ape-dev-de/tiegel/internal/modules/modtest"
)

func TestUnavailableWithoutTreeSitter(t *testing.T) {
	a := modtest.NewApp(t, []module.Module{New(Options{})}, nil)
	modtest.Project(t, a, map[string]string{"a.php": "<?php class A {}"})
	modtest.WantCode(t, a, "index.status", nil, api.CodeUnavailable)
	modtest.WantCode(t, a, "index.rebuild", nil, api.CodeUnavailable)
	modtest.WantCode(t, a, "nav.symbols", SymbolsParams{Query: "A"}, api.CodeUnavailable)
	modtest.WantCode(t, a, "nav.definition", PosParams{Path: "a.php", At: api.Pos{Line: 1}}, api.CodeUnavailable)
}
