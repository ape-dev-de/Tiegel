package controller

import (
	"context"
	"path/filepath"

	"github.com/ape-dev-de/tiegel/internal/modules/composer"
	"github.com/ape-dev-de/tiegel/internal/run"
)

// Composer is the state of the Composer plugin in the window (AP17.2):
// the scripts of the project's composer.json for the gutter run arrows
// and the "Run Composer Script…" chooser. The backend is composer.*.
type Composer struct {
	c *Controller
	// Result is the last composer.scripts answer (empty while the
	// plugin is off or no project is open).
	Result composer.ScriptsResult
	// Version changes with Result.
	Version uint64
	project string
	seq     uint64
}

func newComposer(c *Controller) *Composer { return &Composer{c: c} }

// ProjectChanged reloads the scripts for the open project.
func (m *Composer) ProjectChanged() {
	root := m.c.Project.Root
	if root == m.project {
		return
	}
	m.project = root
	m.Result = composer.ScriptsResult{}
	m.Version++
	if root != "" {
		m.Load(nil)
	}
}

// FileChanged reloads the scripts when composer.json of the project was
// saved.
func (m *Composer) FileChanged(file string) {
	if m.project != "" && filepath.Clean(file) == filepath.Join(m.project, "composer.json") {
		m.Load(nil)
	}
}

// Load reads the scripts; done (optional) runs afterwards on the UI
// goroutine with the error (a disabled plugin is no error for the
// gutter, but the chooser reports it).
func (m *Composer) Load(done func(error)) {
	m.seq++
	seq := m.seq
	m.c.Go(func(ctx context.Context) func() {
		var res composer.ScriptsResult
		err := m.c.call(ctx, "composer.scripts", nil, &res)
		return func() {
			if seq == m.seq {
				if err != nil {
					res = composer.ScriptsResult{}
				}
				m.Result = res
				m.Version++
			}
			if done != nil {
				done(err)
			}
		}
	})
}

// ScriptsAt returns the scripts by 1-based line if file is the project's
// composer.json (nil otherwise).
func (m *Composer) ScriptsAt(file string) map[int]composer.Script {
	if m.Result.File == "" || filepath.Clean(file) != m.Result.File || len(m.Result.Scripts) == 0 {
		return nil
	}
	out := make(map[int]composer.Script, len(m.Result.Scripts))
	for _, s := range m.Result.Scripts {
		if s.Line > 0 {
			out[s.Line] = s
		}
	}
	return out
}

// Run runs script with Composer and shows the run.
func (m *Composer) Run(script string) {
	m.c.Go(func(ctx context.Context) func() {
		var info run.Info
		err := m.c.call(ctx, "composer.run", composer.RunParams{Script: script}, &info)
		return func() {
			if err != nil {
				m.c.Status.Notice = "Composer: " + err.Error()
				return
			}
			m.c.Run.upsert(info)
			m.c.Run.Started = true
		}
	})
}

// ComposerConfigFor returns an unsaved run configuration of script (to edit and
// save in the run configuration editor).
func ComposerConfigFor(script string) run.Config {
	return run.Config{Name: "composer " + script, Type: composer.TypeScript, Values: map[string]any{composer.FieldScript: script}}
}
