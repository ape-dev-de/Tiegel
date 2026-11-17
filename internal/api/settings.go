package api

import (
	"bytes"
	"context"
	"encoding/json"
	"strconv"
	"time"

	"github.com/ape-dev-de/tiegel/internal/core/autosave"
	"github.com/ape-dev-de/tiegel/internal/core/settings"
)

// Client settings ("settings.get"/"settings.set") and autosave control
// ("autosave.*"). Clients such as the GUI keep their preferences (theme,
// tab width, recently opened projects) in the internal settings store
// instead of own files, and trigger autosave through the kernel so that
// Local History and the file.beforeSave/file.saved hooks always run.

// SettingAutosaveIdle is the global setting holding the autosave idle delay
// in seconds (0 = off).
const SettingAutosaveIdle = settings.KeyAutosaveIdle

// clientSettingPrefixes are the key namespaces in which clients may write
// undeclared keys (client state such as recent projects or the tool window
// layout) with "settings.set". Declared keys (settings.schema) are always
// writable and validated. Module-owned state (interpreter, overrides,
// mappings) is changed through its own methods so that the matching events
// fire.
var clientSettingPrefixes = []string{"ui.", "editor.", "update.", "watch."}

// SettingsParams address a setting. Scope is "global" (default) or
// "project".
type SettingsParams struct {
	Key   string `json:"key"`
	Scope string `json:"scope,omitempty"`
	// Effective returns the value that applies (project over global over
	// the declared default) instead of the stored value of Scope.
	Effective bool `json:"effective,omitempty"`
}

// SettingsSetParams are the params of "settings.set". A null or missing
// Value deletes the key.
type SettingsSetParams struct {
	Key   string          `json:"key"`
	Scope string          `json:"scope,omitempty"`
	Value json.RawMessage `json:"value,omitempty"`
}

// SettingValue is the result of "settings.get" and "settings.set".
type SettingValue struct {
	Key   string          `json:"key"`
	Scope string          `json:"scope"`
	Found bool            `json:"found"`
	Value json.RawMessage `json:"value,omitempty"`
	// Source is set for effective reads: default, global or project.
	Source string `json:"source,omitempty"`
}

// AutosaveTriggerParams are the params of "autosave.trigger".
type AutosaveTriggerParams struct {
	// Trigger is focusLost (default), idle, beforeRun, beforeDebug,
	// beforeCommit or close; it is recorded in Local History and
	// file.saved. With close, documents that cannot be saved are
	// preserved as recovery points in Local History (history.recovery).
	Trigger string `json:"trigger,omitempty"`
}

// AutosaveConfig is the params and result of "autosave.config".
type AutosaveConfig struct {
	// IdleSeconds sets the idle delay (0 = off); nil only reads it.
	IdleSeconds *int `json:"idleSeconds,omitempty"`
}

func (a *App) registerSettings() {
	mustHandle(a, "settings.get", "Read a setting (scope global or project; effective: value that applies)", a.settingsGet)
	mustHandle(a, "settings.set", "Write a setting (declared keys are validated, see settings.schema; undeclared only in ui.*, editor.*, update.*, watch.*; null deletes = reset)", a.settingsSet)
	mustHandle(a, "settings.schema", "Declared settings of the core and of every module with stored and effective values (query, module filters)", a.settingsSchema)
	mustHandle(a, "autosave.trigger", "Save all modified documents now (trigger focusLost, idle, beforeRun, …)", a.autosaveTrigger)
	mustHandle(a, "autosave.config", "Read or set the autosave idle delay (idleSeconds, 0 = off)", a.autosaveConfig)
	if secs := settings.Get(a.global, SettingAutosaveIdle, 0); secs > 0 {
		a.saver.SetIdleDelay(time.Duration(secs) * time.Second)
	}
}

func (a *App) scopeStore(scope string) (*settings.Store, string, error) {
	switch scope {
	case "", "global":
		return a.global, "global", nil
	case "project":
		ps := a.ProjectSettings()
		if ps == nil {
			return nil, "", Errorf(CodeNoProject, "no project open")
		}
		return ps, "project", nil
	}
	return nil, "", Errorf(CodeInvalidParams, "unknown scope %q (global, project)", scope)
}

func (a *App) settingsGet(_ context.Context, p SettingsParams) (SettingValue, error) {
	if p.Key == "" {
		return SettingValue{}, Errorf(CodeInvalidParams, "key is required")
	}
	st, scope, err := a.scopeStore(p.Scope)
	if err != nil {
		return SettingValue{}, err
	}
	if p.Effective {
		spec, _, _ := a.lookupSpec(p.Key)
		raw, src := settings.Effective(spec, p.Key, a.global, a.ProjectSettings())
		return SettingValue{Key: p.Key, Scope: string(src), Found: raw != nil, Value: raw, Source: string(src)}, nil
	}
	v, ok, err := settings.Lookup[json.RawMessage](st, p.Key)
	if err != nil {
		return SettingValue{}, Errorf(CodeFailed, "settings: %w", err)
	}
	return SettingValue{Key: p.Key, Scope: scope, Found: ok, Value: v}, nil
}

func (a *App) settingsSet(_ context.Context, p SettingsSetParams) (SettingValue, error) {
	if p.Key == "" {
		return SettingValue{}, Errorf(CodeInvalidParams, "key is required")
	}
	st, scope, err := a.scopeStore(p.Scope)
	if err != nil {
		return SettingValue{}, err
	}
	val := bytes.TrimSpace(p.Value)
	if len(val) == 0 || bytes.Equal(val, []byte("null")) {
		if err := a.validateSetting(p.Key, scope, nil); err != nil {
			return SettingValue{}, err
		}
		if err := st.Delete(p.Key); err != nil {
			return SettingValue{}, Errorf(CodeFailed, "settings: %w", err)
		}
		a.settingApplied(p.Key)
		return SettingValue{Key: p.Key, Scope: scope}, nil
	}
	if !json.Valid(val) {
		return SettingValue{}, Errorf(CodeInvalidParams, "value is not valid JSON")
	}
	if err := a.validateSetting(p.Key, scope, val); err != nil {
		return SettingValue{}, err
	}
	if err := settings.Set(st, p.Key, json.RawMessage(val)); err != nil {
		return SettingValue{}, Errorf(CodeFailed, "settings: %w", err)
	}
	a.settingApplied(p.Key)
	return SettingValue{Key: p.Key, Scope: scope, Found: true, Value: val}, nil
}

// settingApplied applies kernel settings that take effect immediately.
func (a *App) settingApplied(key string) {
	if key == SettingAutosaveIdle {
		a.saver.SetIdleDelay(time.Duration(settings.Get(a.global, SettingAutosaveIdle, 0)) * time.Second)
	}
}

var autosaveTriggers = map[string]autosave.Trigger{
	"":             autosave.TriggerFocusLost,
	"focusLost":    autosave.TriggerFocusLost,
	"idle":         autosave.TriggerIdle,
	"beforeRun":    autosave.TriggerBeforeRun,
	"beforeDebug":  autosave.TriggerBeforeDebug,
	"beforeCommit": autosave.TriggerBeforeCommit,
	"close":        autosave.TriggerClose,
}

func (a *App) autosaveTrigger(ctx context.Context, p AutosaveTriggerParams) (SaveAllResult, error) {
	tr, ok := autosaveTriggers[p.Trigger]
	if !ok {
		return SaveAllResult{}, Errorf(CodeInvalidParams, "unknown trigger %q", p.Trigger)
	}
	res := SaveAllResult{Saved: []string{}}
	for _, d := range (docSet{a}).Dirty() {
		res.Saved = append(res.Saved, a.displayPath(d.Path))
	}
	if err := a.saver.SaveAll(ctx, tr); err != nil {
		if tr == autosave.TriggerClose {
			// The client is about to go away: keep what could not be saved.
			pts := a.preserveUnsaved("save failed on close", err)
			return res, Errorf(CodeFailed, "autosave: %w (%d document(s) preserved in Local History, see history.recovery)", err, len(pts)).WithData(pts)
		}
		return res, Errorf(CodeFailed, "autosave: %w", err)
	}
	return res, nil
}

func (a *App) autosaveConfig(_ context.Context, p AutosaveConfig) (AutosaveConfig, error) {
	if p.IdleSeconds != nil {
		secs := *p.IdleSeconds
		if err := a.validateSetting(SettingAutosaveIdle, "global", []byte(strconv.Itoa(secs))); err != nil {
			return AutosaveConfig{}, err
		}
		if err := settings.Set(a.global, SettingAutosaveIdle, secs); err != nil {
			return AutosaveConfig{}, Errorf(CodeFailed, "settings: %w", err)
		}
		a.saver.SetIdleDelay(time.Duration(secs) * time.Second)
	}
	secs := int(a.saver.IdleDelay() / time.Second)
	return AutosaveConfig{IdleSeconds: &secs}, nil
}
