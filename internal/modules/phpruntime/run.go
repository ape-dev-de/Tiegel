package phpruntime

import (
	"context"

	"github.com/ape-dev-de/tiegel/internal/api"
	"github.com/ape-dev-de/tiegel/internal/interp"
)

// runInterpreter answers the run.interpreter filter (AP15.1): the
// interpreter a run uses – the requested ID or the selected one, else the
// first local php – with the user's path mappings for the selected one.
// A run is a user action, so compose interpreters may start containers
// (exec mode setting) and image interpreters run `docker run` (D250).
func (m *Module) runInterpreter(ctx context.Context, q api.RunInterpreterQuery) (api.RunInterpreterQuery, error) {
	if q.Interpreter != nil {
		return q, nil
	}
	selected := api.SelectedInterpreter(m.store())
	id := q.ID
	if id == "" {
		id = selected
	}
	if id == "" {
		if locals := interp.Discover(ctx, m.app.Options().Interp); len(locals) > 0 {
			q.Interpreter = locals[0]
			return q, nil
		}
		q.Err = "no PHP interpreter selected and none found (see interp.list / interp.select)"
		return q, nil
	}
	i, err := m.interpreter(ctx, id)
	if err != nil {
		q.Err = err.Error()
		return q, nil
	}
	q.Interpreter = i
	if id == selected {
		if res, err := m.mappingsGet(ctx, struct{}{}); err == nil {
			q.Mappings = res.Mappings
		}
	}
	return q, nil
}
