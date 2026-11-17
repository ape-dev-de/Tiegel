package api

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync"

	"github.com/ape-dev-de/tiegel/internal/core/settings"
)

// Settings schema (decisions D190, D210–D212): core settings are declared
// in internal/core/settings (RegisterCore), module settings by the modules
// (module.SettingsProvider). "settings.schema" returns both with the
// current values, the dialog and "tiegel --headless settings" are built on
// it, and "settings.set" validates declared keys against their spec.

// SchemaParams are the params of "settings.schema".
type SchemaParams struct {
	// Query filters by key, label, help, group or module ID
	// (case-insensitive substring; all words must match).
	Query string `json:"query,omitempty"`
	// Module limits the result to one module ID, or "core" for the core
	// settings only.
	Module string `json:"module,omitempty"`
}

// SettingInfo is a declared setting with its stored values.
type SettingInfo struct {
	settings.Spec
	// Global and Project are the stored values (absent when not set).
	Global  json.RawMessage `json:"global,omitempty"`
	Project json.RawMessage `json:"project,omitempty"`
	// Value is the effective value (project over global over default) and
	// Source where it comes from: default, global or project.
	Value  json.RawMessage `json:"value,omitempty"`
	Source string          `json:"source"`
	// Invalid describes a stored value that fails the spec (it is ignored).
	Invalid string `json:"invalid,omitempty"`
}

// ModuleSettings are the settings of one module (plugin).
type ModuleSettings struct {
	ID        string        `json:"id"`
	Enabled   bool          `json:"enabled"`
	Running   bool          `json:"running"`
	Error     string        `json:"error,omitempty"`
	DependsOn []string      `json:"dependsOn"`
	Settings  []SettingInfo `json:"settings"`
}

// SettingsSchema is the result of "settings.schema".
type SettingsSchema struct {
	// Project is the open project ("" if none: project values are absent).
	Project string `json:"project,omitempty"`
	// Pages lists the core pages in dialog order (settings.CorePages plus
	// further groups of registered core specs).
	Pages []string `json:"pages"`
	// Core are the core settings; Spec.Group names their page.
	Core []SettingInfo `json:"core"`
	// Modules are all registered modules (also disabled ones) with their
	// declared settings, sorted by ID.
	Modules []ModuleSettings `json:"modules"`
	// Problems lists rejected module setting declarations.
	Problems []string `json:"problems,omitempty"`
}

// lookupSpec returns the declaration of key (core first, then modules).
func (a *App) lookupSpec(key string) (settings.Spec, string, bool) {
	if s, ok := settings.CoreSpec(key); ok {
		return s, "", true
	}
	if s, id, ok := a.modules.SettingSpec(key); ok {
		return s, id, true
	}
	return settings.Spec{}, "", false
}

// Setting returns the effective value of a declared (or undeclared) key
// decoded as T: project over global over the spec default, def if none
// applies or the value cannot be decoded. Modules use it to read their
// declared settings. A stored value that fails the spec is ignored; it is
// logged (once per value) and listed in settings.schema (Invalid and
// Problems), so it is never ignored silently (D249).
func Setting[T any](a *App, key string, def T) T {
	spec, _, _ := a.lookupSpec(key)
	project := a.ProjectSettings()
	if spec.Kind != "" {
		a.warnInvalidSetting(spec, project)
	}
	raw, _ := settings.Effective(spec, key, a.global, project)
	if raw == nil {
		return def
	}
	var v T
	if err := json.Unmarshal(raw, &v); err != nil {
		return def
	}
	return v
}

// warnedSettings remembers the invalid stored values already logged
// (key, scope and raw value), so each is logged once.
var warnedSettings sync.Map

// warnInvalidSetting logs stored values of spec that fail its check.
func (a *App) warnInvalidSetting(spec settings.Spec, project *settings.Store) {
	for _, sc := range []struct {
		name string
		st   *settings.Store
	}{{"global", a.global}, {"project", project}} {
		if sc.st == nil {
			continue
		}
		raw, ok := sc.st.Raw(spec.Key)
		if !ok {
			continue
		}
		if err := checkRaw(spec, raw); err != nil {
			if _, seen := warnedSettings.LoadOrStore(spec.Key+"\x00"+sc.name+"\x00"+string(raw), true); !seen {
				a.log.Warn("stored setting ignored", "key", spec.Key, "scope", sc.name, "value", string(raw), "err", err)
			}
		}
	}
}

// checkRaw checks a stored JSON value against spec.
func checkRaw(spec settings.Spec, raw json.RawMessage) error {
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return err
	}
	return spec.Check(v)
}

func (a *App) settingInfo(spec settings.Spec, project *settings.Store) SettingInfo {
	si := SettingInfo{Spec: spec}
	check := func(raw json.RawMessage) {
		if err := checkRaw(spec, raw); err != nil && si.Invalid == "" {
			si.Invalid = err.Error()
		}
	}
	if raw, ok := a.global.Raw(spec.Key); ok {
		si.Global = raw
		check(raw)
	}
	if project != nil {
		if raw, ok := project.Raw(spec.Key); ok {
			si.Project = raw
			check(raw)
		}
	}
	raw, src := settings.Effective(spec, spec.Key, a.global, project)
	si.Value, si.Source = raw, string(src)
	return si
}

func (a *App) settingsSchema(_ context.Context, p SchemaParams) (SettingsSchema, error) {
	project := a.ProjectSettings()
	res := SettingsSchema{Core: []SettingInfo{}, Modules: []ModuleSettings{}}
	if project != nil {
		res.Project = a.ProjectRoot()
	}
	words := strings.Fields(strings.ToLower(p.Query))
	match := func(owner string, s settings.Spec) bool {
		hay := strings.ToLower(strings.Join([]string{owner, s.Key, s.Label, s.Help, s.Group}, "\x00"))
		for _, w := range words {
			if !strings.Contains(hay, w) {
				return false
			}
		}
		return true
	}
	seen := map[string]bool{}
	for _, pg := range settings.CorePages {
		seen[pg] = true
		res.Pages = append(res.Pages, pg)
	}
	if p.Module == "" || p.Module == "core" {
		for _, s := range settings.CoreSpecs() {
			if s.Group != "" && !seen[s.Group] {
				seen[s.Group] = true
				res.Pages = append(res.Pages, s.Group)
			}
			if match("core", s) {
				res.Core = append(res.Core, a.settingInfo(s, project))
			}
		}
	}
	if p.Module != "core" {
		infos := a.modules.Modules()
		sort.SliceStable(infos, func(i, j int) bool { return infos[i].ID < infos[j].ID })
		for _, m := range infos {
			if p.Module != "" && m.ID != p.Module {
				continue
			}
			ms := ModuleSettings{ID: m.ID, Enabled: m.Enabled, Running: m.Running, DependsOn: append([]string{}, m.DependsOn...), Settings: []SettingInfo{}}
			if m.Err != nil {
				ms.Error = m.Err.Error()
			}
			for _, s := range a.modules.SettingSpecs(m.ID) {
				if match(m.ID, s) {
					ms.Settings = append(ms.Settings, a.settingInfo(s, project))
				}
			}
			if len(words) > 0 && len(ms.Settings) == 0 {
				continue
			}
			res.Modules = append(res.Modules, ms)
		}
		if p.Module != "" && len(res.Modules) == 0 && len(words) == 0 {
			return res, Errorf(CodeNotFound, "unknown module %q", p.Module)
		}
	}
	for _, err := range a.modules.SpecErrors() {
		res.Problems = append(res.Problems, err.Error())
	}
	// Stored values that fail their spec are ignored: list them.
	invalid := func(si SettingInfo) {
		if si.Invalid != "" {
			res.Problems = append(res.Problems, fmt.Sprintf("%s: stored value ignored (default or other scope applies): %s", si.Key, si.Invalid))
		}
	}
	for _, si := range res.Core {
		invalid(si)
	}
	for _, m := range res.Modules {
		for _, si := range m.Settings {
			invalid(si)
		}
	}
	return res, nil
}

// validateSetting checks a value about to be written to key in scope.
// Undeclared keys are only accepted in the client namespaces.
func (a *App) validateSetting(key, scope string, value json.RawMessage) error {
	spec, _, declared := a.lookupSpec(key)
	if !declared {
		for _, pre := range clientSettingPrefixes {
			if strings.HasPrefix(key, pre) && len(key) > len(pre) {
				return nil
			}
		}
		return Errorf(CodeInvalidParams, "settings.set: %q is not a declared setting (see settings.schema) and not in a client namespace (%s*)", key, strings.Join(clientSettingPrefixes, "*, "))
	}
	if spec.ReadOnly {
		hint := spec.ReadOnlyHint
		if hint == "" {
			hint = "it is changed through its plugin's own method"
		}
		return Errorf(CodeInvalidParams, "settings.set: %s is read-only here: %s", key, hint)
	}
	if scope == string(settings.ScopeProject) && spec.Scope == settings.ScopeGlobal {
		return Errorf(CodeInvalidParams, "settings.set: %s is a global setting (no project value)", key)
	}
	if value == nil {
		return nil
	}
	var v any
	if err := json.Unmarshal(value, &v); err != nil {
		return Errorf(CodeInvalidParams, "settings.set: %s: %w", key, err)
	}
	if err := spec.Check(v); err != nil {
		return Errorf(CodeInvalidParams, "settings.set: %s: %w", key, err)
	}
	return nil
}
