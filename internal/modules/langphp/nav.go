package langphp

import (
	"context"
	"time"

	"github.com/ape-dev-de/tiegel/internal/api"
)

// navTimeout bounds the nav.lsp hook (a slow server must not hold back the
// index results of nav.definition/nav.implementations for long).
const navTimeout = 5 * time.Second

// navLSP provides the nav.lsp hook: definitions or implementations from the
// running language server, for php-index to merge with its own results.
func (m *Module) navLSP(ctx context.Context, q api.NavLSPQuery) (api.NavLSPQuery, error) {
	if q.Answered || m.current() == nil {
		return q, nil
	}
	var (
		locs []Location
		err  error
	)
	p := PosParams{Path: q.Path, At: q.At}
	switch q.Kind {
	case "definition":
		locs, err = m.lspDefinition(ctx, p)
	case "implementation":
		locs, err = m.lspImplementation(ctx, p)
	default:
		return q, nil
	}
	if err != nil {
		q.Err = err.Error()
		return q, nil
	}
	q.Answered = true
	for _, l := range locs {
		q.Locations = append(q.Locations, api.NavLSPLocation{Path: l.Path, File: l.File, Range: l.Range})
	}
	return q, nil
}
