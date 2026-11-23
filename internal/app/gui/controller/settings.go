package controller

import (
	"context"
	"encoding/json"
	"sort"
)

// SettingSpec mirrors a declared setting of settings.schema with its
// stored and effective values.
type SettingSpec struct {
	Key     string          `json:"key"`
	Kind    string          `json:"kind"`
	Default json.RawMessage `json:"default,omitempty"`
	Scope   string          `json:"scope"`
	Label   string          `json:"label"`
	Help    string          `json:"help,omitempty"`
	Group   string          `json:"group,omitempty"`
	Options []string        `json:"options,omitempty"`
	Min     *int            `json:"min,omitempty"`
	Max     *int            `json:"max,omitempty"`
	Global  json.RawMessage `json:"global,omitempty"`
	Project json.RawMessage `json:"project,omitempty"`
	Value   json.RawMessage `json:"value,omitempty"`
	Source  string          `json:"source"`
	Invalid string          `json:"invalid,omitempty"`
	// ReadOnly settings are shown but not editable (ReadOnlyHint says
	// how to change them).
	ReadOnly     bool   `json:"readOnly,omitempty"`
	ReadOnlyHint string `json:"readOnlyHint,omitempty"`
	// Candidates describes where values can be discovered
	// (settings.candidates, D521); empty without a provider.
	Candidates string `json:"candidates,omitempty"`
}

// ModuleSettings are the settings of one plugin.
type ModuleSettings struct {
	ID        string        `json:"id"`
	Enabled   bool          `json:"enabled"`
	Running   bool          `json:"running"`
	Error     string        `json:"error"`
	DependsOn []string      `json:"dependsOn"`
	Settings  []SettingSpec `json:"settings"`
	// Section and Title name the settings page (D525).
	Section string `json:"section"`
	Title   string `json:"title"`
}

// SettingsSchema is the result of settings.schema.
type SettingsSchema struct {
	Project string           `json:"project"`
	Pages   []string         `json:"pages"`
	Core    []SettingSpec    `json:"core"`
	Modules []ModuleSettings `json:"modules"`
}

// LoadSettingsData loads the settings schema, modules, interpreters,
// mappings and the Xdebug diagnosis for the settings dialog.
func (c *Controller) LoadSettingsData() {
	c.Go(func(ctx context.Context) func() {
		var schema SettingsSchema
		serr := c.call(ctx, "settings.schema", nil, &schema)
		var mods []ModuleInfo
		merr := c.call(ctx, "modules.list", nil, &mods)
		var interps []Interpreter
		var maps Mappings
		var diag Diagnosis
		if c.Project.Open {
			_ = c.call(ctx, "interp.list", map[string]bool{"status": true}, &interps)
			_ = c.call(ctx, "runtime.mappings.get", nil, &maps)
			_ = c.call(ctx, "runtime.diagnose", nil, &diag)
		}
		return func() {
			if serr == nil {
				c.Schema = &schema
			}
			if merr == nil {
				sort.SliceStable(mods, func(i, j int) bool { return mods[i].ID < mods[j].ID })
				c.Modules = mods
			}
			c.Interpreters, c.Mappings, c.Diagnosis = interps, maps, diag
			c.loadProbe()
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

// SetSettingValue writes a declared setting (validated by the kernel) and
// reloads the schema and the preferences; value nil resets it (deletes the
// value of scope).
func (c *Controller) SetSettingValue(key, scope string, value any) {
	params := map[string]any{"key": key, "scope": scope}
	if value != nil {
		raw, err := json.Marshal(value)
		if err != nil {
			c.notice(err)
			return
		}
		params["value"] = json.RawMessage(raw)
	}
	c.Go(func(ctx context.Context) func() {
		err := c.call(ctx, "settings.set", params, nil)
		p := c.loadPrefs(ctx)
		return func() {
			c.notice(err)
			c.Prefs = p
			c.LoadSettingsData()
		}
	})
}

// ResetSetting removes the value of key in scope.
func (c *Controller) ResetSetting(key, scope string) { c.SetSettingValue(key, scope, nil) }

// EffectiveSetting returns the effective value of key from the loaded
// schema (nil if unknown or not loaded).
func (c *Controller) EffectiveSetting(key string) json.RawMessage {
	if c.Schema == nil {
		return nil
	}
	for _, s := range c.Schema.Core {
		if s.Key == key {
			return s.Value
		}
	}
	for _, m := range c.Schema.Modules {
		for _, s := range m.Settings {
			if s.Key == key {
				return s.Value
			}
		}
	}
	return nil
}
