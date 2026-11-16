package api

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
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
const SettingAutosaveIdle = "autosave.idleSeconds"

// clientSettingPrefixes are the key namespaces clients may write with
// "settings.set". Module-owned keys (interpreter, overrides, mappings) are
// changed through their own methods so that the matching events fire.
var clientSettingPrefixes = []string{"ui.", "editor.", "update."}

// SettingsParams address a setting. Scope is "global" (default) or
// "project".
type SettingsParams struct {
	Key   string `json:"key"`
	Scope string `json:"scope,omitempty"`
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
}

// AutosaveTriggerParams are the params of "autosave.trigger".
type AutosaveTriggerParams struct {
	// Trigger is focusLost (default), idle, beforeRun, beforeDebug or
	// beforeCommit; it is recorded in Local History and file.saved.
	Trigger string `json:"trigger,omitempty"`
}

// AutosaveConfig is the params and result of "autosave.config".
type AutosaveConfig struct {
	// IdleSeconds sets the idle delay (0 = off); nil only reads it.
	IdleSeconds *int `json:"idleSeconds,omitempty"`
}

func (a *App) registerSettings() {
	mustHandle(a, "settings.get", "Read an internal setting (scope global or project)", a.settingsGet)
	mustHandle(a, "settings.set", "Write a client setting (keys ui.*, editor.*, update.*; null deletes)", a.settingsSet)
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
	v, ok, err := settings.Lookup[json.RawMessage](st, p.Key)
	if err != nil {
		return SettingValue{}, Errorf(CodeFailed, "settings: %w", err)
	}
	return SettingValue{Key: p.Key, Scope: scope, Found: ok, Value: v}, nil
}

func (a *App) settingsSet(_ context.Context, p SettingsSetParams) (SettingValue, error) {
	allowed := false
	for _, pre := range clientSettingPrefixes {
		allowed = allowed || strings.HasPrefix(p.Key, pre)
	}
	if !allowed || len(p.Key) < 3 {
		return SettingValue{}, Errorf(CodeInvalidParams, "settings.set only writes client keys (%s)", strings.Join(clientSettingPrefixes, "*, ")+"*")
	}
	st, scope, err := a.scopeStore(p.Scope)
	if err != nil {
		return SettingValue{}, err
	}
	val := bytes.TrimSpace(p.Value)
	if len(val) == 0 || bytes.Equal(val, []byte("null")) {
		if err := st.Delete(p.Key); err != nil {
			return SettingValue{}, Errorf(CodeFailed, "settings: %w", err)
		}
		return SettingValue{Key: p.Key, Scope: scope}, nil
	}
	if !json.Valid(val) {
		return SettingValue{}, Errorf(CodeInvalidParams, "value is not valid JSON")
	}
	if err := settings.Set(st, p.Key, json.RawMessage(val)); err != nil {
		return SettingValue{}, Errorf(CodeFailed, "settings: %w", err)
	}
	return SettingValue{Key: p.Key, Scope: scope, Found: true, Value: val}, nil
}

var autosaveTriggers = map[string]autosave.Trigger{
	"":             autosave.TriggerFocusLost,
	"focusLost":    autosave.TriggerFocusLost,
	"idle":         autosave.TriggerIdle,
	"beforeRun":    autosave.TriggerBeforeRun,
	"beforeDebug":  autosave.TriggerBeforeDebug,
	"beforeCommit": autosave.TriggerBeforeCommit,
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
		return res, Errorf(CodeFailed, "autosave: %w", err)
	}
	return res, nil
}

func (a *App) autosaveConfig(_ context.Context, p AutosaveConfig) (AutosaveConfig, error) {
	if p.IdleSeconds != nil {
		secs := *p.IdleSeconds
		if secs < 0 {
			return AutosaveConfig{}, Errorf(CodeInvalidParams, "idleSeconds must be >= 0")
		}
		if err := settings.Set(a.global, SettingAutosaveIdle, secs); err != nil {
			return AutosaveConfig{}, Errorf(CodeFailed, "settings: %w", err)
		}
		a.saver.SetIdleDelay(time.Duration(secs) * time.Second)
	}
	secs := int(a.saver.IdleDelay() / time.Second)
	return AutosaveConfig{IdleSeconds: &secs}, nil
}
