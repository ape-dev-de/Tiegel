// Package lspplugins holds the language server plugins lsp-phpactor,
// lsp-phpantom and lsp-intelephense (decision D192). Each plugin is its
// own module: while it runs it contributes its server (binary names,
// arguments, PHP version options, user settings) through the
// lsp.servers filter and its installation recipe through the
// install.recipes filter. lang-php selects at most one of the contributed
// servers and runs it under the supervisor; the plugins never talk to
// lang-php directly.
//
// Settings of a plugin live in its namespace (decision D190):
//
//	<plugin>.path         executable, overrides the search
//	<plugin>.args         arguments (replace the defaults)
//	<plugin>.initOptions  JSON object merged into initializationOptions
package lspplugins

import (
	"context"
	"encoding/json"
	"errors"
	"slices"

	"github.com/ape-dev-de/tiegel/internal/api"
	"github.com/ape-dev-de/tiegel/internal/core/hooks"
	"github.com/ape-dev-de/tiegel/internal/core/module"
	"github.com/ape-dev-de/tiegel/internal/core/settings"
	"github.com/ape-dev-de/tiegel/internal/install"
	"github.com/ape-dev-de/tiegel/internal/lsp/servers"
)

// Plugin is a language server plugin.
type Plugin struct {
	def        servers.Definition
	order      int
	note       string
	recipe     install.Recipe
	defaultOn  bool
	autoSelect bool

	app *api.App
}

// Module IDs.
const (
	PhpactorID     = "lsp-phpactor"
	PHPantomID     = "lsp-phpantom"
	IntelephenseID = "lsp-intelephense"
)

// Phpactor is the default server (MIT, decision D3); installed via
// Composer, globally or into the project.
func Phpactor() *Plugin {
	return &Plugin{
		def: servers.Definition{
			ID: "phpactor", Name: "Phpactor", Binaries: []string{"phpactor"},
			Args: []string{"language-server"}, License: "MIT",
			PHPVersionOption: "php.version",
		},
		order: 10, defaultOn: true, autoSelect: true,
		recipe: install.Recipe{
			Plugin: PhpactorID, Tool: "Phpactor", Manager: install.ManagerComposer, Package: "phpactor/phpactor",
			Scopes: []install.Scope{install.ScopeGlobal, install.ScopeProject}, Interpreter: install.InterpreterNative,
			Hint: "Alternatively install Phpactor with your package manager (e.g. Homebrew: brew install phpactor) and choose its path.",
		},
	}
}

// PHPantom is the open-source alternative (MIT). It is a native binary
// without a package manager route Tiegel could use, so its recipe is a
// hint only.
func PHPantom() *Plugin {
	return &Plugin{
		def: servers.Definition{
			ID: "phpantom", Name: "PHPantom", Binaries: []string{"phpantom_lsp", "phpantom"}, License: "MIT",
		},
		order: 20, defaultOn: true, autoSelect: true,
		recipe: install.Recipe{
			Plugin: PHPantomID, Tool: "PHPantom",
			Hint: "PHPantom is a native binary: download a release from the project page or build it with cargo, then put it on your PATH or choose its path. Tiegel does not download it.",
		},
	}
}

// Intelephense is proprietary: the plugin is off by default, never picked
// automatically and shows a license note when chosen.
func Intelephense() *Plugin {
	note := "Intelephense is proprietary software (free core, licensed premium features); it is not open source."
	return &Plugin{
		def: servers.Definition{
			ID: "intelephense", Name: "Intelephense", Binaries: []string{"intelephense"},
			Args: []string{"--stdio"}, License: "proprietary", Proprietary: true,
			PHPVersionSetting: "intelephense.environment.phpVersion",
		},
		order: 30, note: note,
		recipe: install.Recipe{
			Plugin: IntelephenseID, Tool: "Intelephense", Manager: install.ManagerNPM, Package: "intelephense",
			Scopes: []install.Scope{install.ScopeGlobal}, Interpreter: install.InterpreterNative, Note: note,
		},
	}
}

// All returns new instances of all language server plugins.
func All() []*Plugin { return []*Plugin{Phpactor(), PHPantom(), Intelephense()} }

// ID implements module.Module.
func (p *Plugin) ID() string { return "lsp-" + p.def.ID }

// DependsOn implements module.Module.
func (p *Plugin) DependsOn() []string { return nil }

// DefaultEnabled tells whether the plugin is on without a stored state.
func (p *Plugin) DefaultEnabled() bool { return p.defaultOn }

// RegisterOption returns the registration option matching DefaultEnabled.
func (p *Plugin) RegisterOption() module.RegisterOption {
	if p.defaultOn {
		return module.DefaultEnabled()
	}
	return module.DefaultDisabled()
}

// Definition returns the server definition.
func (p *Plugin) Definition() servers.Definition { return p.def }

// Recipe returns the installation recipe.
func (p *Plugin) Recipe() install.Recipe { return p.recipe }

// Setting keys of a plugin.
func (p *Plugin) keyPath() string        { return p.ID() + ".path" }
func (p *Plugin) keyArgs() string        { return p.ID() + ".args" }
func (p *Plugin) keyInitOptions() string { return p.ID() + ".initOptions" }

// SettingSpecs implements module.SettingsProvider.
func (p *Plugin) SettingSpecs() []settings.Spec {
	name := p.def.Name
	specs := []settings.Spec{
		{Key: p.keyPath(), Kind: settings.KindPath, Default: "", Scope: settings.ScopeProject, Label: name + " executable", Executable: true,
			Help: "Path of the " + name + " executable; empty searches vendor/bin, PATH, the login shell PATH and the usual package manager directories."},
		{Key: p.keyArgs(), Kind: settings.KindList, Default: append([]string{}, p.def.Args...), Scope: settings.ScopeProject, Label: "Arguments",
			Help: "Arguments that start " + name + " on stdio."},
		{Key: p.keyInitOptions(), Kind: settings.KindString, Default: "", Scope: settings.ScopeProject, Label: "Initialization options",
			Help: "JSON object merged into the initializationOptions sent to " + name + "."},
	}
	if p.note != "" {
		specs[0].Help += " " + p.note
	}
	return specs
}

// Start contributes the server and the recipe.
func (p *Plugin) Start(ctx module.Context) error {
	app, ok := api.AppFrom(ctx)
	if !ok {
		return errors.New(p.ID() + ": needs an api.App context")
	}
	p.app = app
	if p.note != "" {
		ctx.Logger().Info(p.ID()+" enabled", "note", p.note)
	}
	ctx.OnStop(api.HookLSPServers.On(ctx.Hooks(), p.order, func(_ context.Context, list []api.LSPServerDecl) ([]api.LSPServerDecl, error) {
		return append(slices.Clone(list), p.decl()), nil
	}, hooks.WithOwner(p.ID())))
	ctx.OnStop(api.HookInstallRecipes.On(ctx.Hooks(), p.order, func(_ context.Context, list []install.Recipe) ([]install.Recipe, error) {
		return append(slices.Clone(list), p.recipe), nil
	}, hooks.WithOwner(p.ID())))
	return nil
}

// Stop implements module.Module.
func (p *Plugin) Stop() error { return nil }

// decl builds the declaration with the current settings (project values
// override global ones).
func (p *Plugin) decl() api.LSPServerDecl {
	d := api.LSPServerDecl{Definition: p.def, Plugin: p.ID(), Order: p.order, Note: p.note, AutoSelect: p.autoSelect}
	d.Path = lookup(p.app, p.keyPath(), "")
	if args, ok := lookupOK[[]string](p.app, p.keyArgs()); ok {
		d.Args = append([]string{}, args...)
	}
	if raw := lookup(p.app, p.keyInitOptions(), ""); raw != "" {
		var m map[string]any
		if err := json.Unmarshal([]byte(raw), &m); err == nil {
			d.InitOptions = m
		} else {
			p.app.Logger().Warn(p.ID()+": ignoring invalid initOptions", "err", err)
		}
	}
	return d
}

func lookupOK[T any](a *api.App, key string) (T, bool) {
	var zero T
	if a == nil {
		return zero, false
	}
	if ps := a.ProjectSettings(); ps != nil {
		if v, ok, err := settings.Lookup[T](ps, key); ok && err == nil {
			return v, true
		}
	}
	if v, ok, err := settings.Lookup[T](a.Settings(), key); ok && err == nil {
		return v, true
	}
	return zero, false
}

func lookup[T any](a *api.App, key string, def T) T {
	if v, ok := lookupOK[T](a, key); ok {
		return v
	}
	return def
}
