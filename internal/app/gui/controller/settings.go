package controller

import (
	"context"
	"sort"
)

// LoadSettingsData loads modules, interpreters, mappings and the Xdebug
// diagnosis for the settings dialog.
func (c *Controller) LoadSettingsData() {
	c.Go(func(ctx context.Context) func() {
		var mods []ModuleInfo
		merr := c.call(ctx, "modules.list", nil, &mods)
		var interps []Interpreter
		var maps Mappings
		var diag Diagnosis
		if c.Project.Open {
			_ = c.call(ctx, "interp.list", nil, &interps)
			_ = c.call(ctx, "runtime.mappings.get", nil, &maps)
			_ = c.call(ctx, "runtime.diagnose", nil, &diag)
		}
		return func() {
			if merr == nil {
				sort.SliceStable(mods, func(i, j int) bool { return mods[i].ID < mods[j].ID })
				c.Modules = mods
			}
			c.Interpreters, c.Mappings, c.Diagnosis = interps, maps, diag
		}
	})
}

// Dependents returns the enabled modules that depend on id.
func (c *Controller) Dependents(id string) []string {
	var out []string
	for _, m := range c.Modules {
		for _, d := range m.DependsOn {
			if d == id && m.Enabled {
				out = append(out, m.ID)
			}
		}
	}
	return out
}

// SetModuleEnabled enables (with its dependencies) or disables a module.
func (c *Controller) SetModuleEnabled(id string, on bool) {
	method := "modules.disable"
	if on {
		method = "modules.enable"
	}
	c.Go(func(ctx context.Context) func() {
		err := c.call(ctx, method, map[string]any{"id": id, "withDependencies": on}, nil)
		return func() {
			c.notice(err)
			c.LoadSettingsData()
			if on && id == "lang-php" {
				c.startLSP()
			}
		}
	})
}

// SelectInterpreter selects the project interpreter.
func (c *Controller) SelectInterpreter(id string) {
	c.Go(func(ctx context.Context) func() {
		err := c.call(ctx, "interp.select", map[string]string{"id": id}, nil)
		return func() {
			c.notice(err)
			c.LoadSettingsData()
			c.refreshRuntime()
		}
	})
}

// SetMappings stores corrected path mappings; reset restores the ones
// derived from compose.
func (c *Controller) SetMappings(ms []Mapping, reset bool) {
	c.Mappings.Mappings = ms
	c.Go(func(ctx context.Context) func() {
		var res Mappings
		params := map[string]any{"mappings": ms}
		if reset {
			params = map[string]any{"reset": true}
		}
		err := c.call(ctx, "runtime.mappings.set", params, &res)
		return func() {
			if err != nil {
				c.notice(err)
				return
			}
			c.Mappings = res
		}
	})
}
