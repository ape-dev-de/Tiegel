//go:build !cgo

package phpfeatures

import (
	"testing"
	"time"

	"github.com/ape-dev-de/tiegel/internal/api"
	"github.com/ape-dev-de/tiegel/internal/core/module"
	"github.com/ape-dev-de/tiegel/internal/modules/modtest"
	"github.com/ape-dev-de/tiegel/internal/modules/phpruntime"
)

// Without Tree-sitter the module runs, checks are unavailable and the
// version override still works.
func TestWithoutTreeSitter(t *testing.T) {
	m := New(Options{Debounce: 5 * time.Millisecond})
	a := modtest.NewApp(t, []module.Module{phpruntime.New(phpruntime.Options{}), m}, nil)
	modtest.WantCode(t, a, "php.check", CheckParams{Path: "x.php"}, api.CodeUnavailable)
	modtest.WantCode(t, a, "php.diagnostics", api.PathParams{Path: "x.php"}, api.CodeUnavailable)
	var or OverrideResult
	modtest.Call(t, a, "php.setVersionOverride", OverrideParams{Version: "8.1"}, &or)
	if or.Override == "" {
		t.Fatalf("override %+v", or)
	}
}
