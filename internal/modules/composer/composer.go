// Package composer is the Composer plugin (AP17.2): it lists the scripts
// of the project's composer.json (with "scripts-descriptions" and the
// line of each key) and contributes the run configuration type "Composer
// Script", which runs `composer run-script NAME [-- ARGS]` through the
// kernel's run infrastructure – in the project interpreter by default, so
// the script runs in the Compose service, Docker image or WSL like PHP.
//
// API (namespace composer.*): composer.scripts, composer.run.
// Hooks provided: run.configTypes (type "composer-script").
// Settings: composer.path (empty: composer.phar in the project root,
// else composer from PATH), composer.interpreter.
//
// The plugin never downloads Composer: a missing executable is reported
// by the run (D194).
package composer

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"

	"github.com/ape-dev-de/tiegel/internal/api"
	"github.com/ape-dev-de/tiegel/internal/core/hooks"
	"github.com/ape-dev-de/tiegel/internal/core/module"
	"github.com/ape-dev-de/tiegel/internal/core/settings"
	"github.com/ape-dev-de/tiegel/internal/run"
)

// ID is the module ID.
const ID = "composer"

// TypeScript is the run configuration type of Composer scripts (the ID
// predates the plugin, so stored configurations stay valid).
const TypeScript = "composer-script"

// Settings keys (D190: namespace = module ID).
const (
	SettingPath   = ID + ".path"
	SettingInterp = ID + ".interpreter"
)

// Fields of the configuration type.
const (
	FieldScript = "script"
	FieldArgs   = "args"
	FieldInterp = "interpreter"
	FieldEnv    = "env"
)

// pharName is the project-local Composer archive used when composer.path
// is empty.
const pharName = "composer.phar"

// Module is the Composer plugin.
type Module struct {
	mu  sync.Mutex
	app *api.App
}

// New creates the module.
func New() *Module { return &Module{} }

// ID implements module.Module.
func (m *Module) ID() string { return ID }

// DependsOn implements module.Module: the interpreter comes through the
// run infrastructure (optional).
func (m *Module) DependsOn() []string { return nil }

// Claims implements api.Claimer.
func (m *Module) Claims() []string { return []string{"composer.*"} }

// Stop implements module.Module.
func (m *Module) Stop() error { return nil }

// SettingSpecs implements module.SettingsProvider.
func (m *Module) SettingSpecs() []settings.Spec {
	return []settings.Spec{
		{Key: SettingPath, Kind: settings.KindPath, Default: "", Scope: settings.ScopeProject, Label: "Composer executable",
			Help: "composer (from PATH), a path relative to the project or absolute; a .phar runs with php. Empty: composer.phar in the project root if present, else composer."},
		{Key: SettingInterp, Kind: settings.KindString, Default: run.InterpreterProject, Scope: settings.ScopeProject, Label: "Interpreter",
			Help: "Where Composer scripts run: project (the project's interpreter, e.g. a Compose service), host or an interpreter ID."},
	}
}

// Start registers the API and the run configuration type.
func (m *Module) Start(ctx module.Context) error {
	app, ok := api.AppFrom(ctx)
	if !ok {
		return errors.New("composer: needs an api.App context")
	}
	m.mu.Lock()
	m.app = app
	m.mu.Unlock()
	p := api.NewProvider(ctx)
	api.Add(p, "composer.scripts", "Scripts of the project's composer.json with commands, descriptions and lines", m.scripts)
	api.Add(p, "composer.run", "Run a Composer script (composer run-script NAME [-- ARGS]) in the project interpreter; output as run.output events", m.run)
	if err := p.Err(); err != nil {
		return err
	}
	ctx.OnStop(api.HookRunConfigTypes.On(ctx.Hooks(), 50, func(_ context.Context, l []run.ConfigType) ([]run.ConfigType, error) {
		return append(slices.Clone(l), m.configType()), nil
	}, hooks.WithOwner(ID)))
	return nil
}

func (m *Module) application() *api.App {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.app
}

func setting[T any](m *Module, key string, def T) T {
	if app := m.application(); app != nil {
		return api.Setting(app, key, def)
	}
	return def
}

// ScriptsResult is the result of composer.scripts (api contract).
type ScriptsResult = api.ComposerScriptsResult

// RunParams are the params of composer.run (api contract).
type RunParams = api.ComposerRunParams

// Scripts reads the scripts of root's composer.json (nil without file).
func Scripts(root string) ([]Script, error) {
	b, err := os.ReadFile(filepath.Join(root, "composer.json"))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("composer: %w", err)
	}
	return ParseScripts(b)
}

func (m *Module) scripts(_ context.Context, _ struct{}) (ScriptsResult, error) {
	app := m.application()
	root, err := app.RequireProject()
	if err != nil {
		return ScriptsResult{}, err
	}
	file := filepath.Join(root, "composer.json")
	res := ScriptsResult{File: file, Scripts: []Script{}, Command: m.commandText(root), ConfigType: TypeScript, ScriptField: FieldScript}
	if _, err := os.Stat(file); err != nil {
		return res, nil
	}
	res.Exists = true
	list, err := Scripts(root)
	if err != nil {
		return res, api.Errorf(api.CodeFailed, "%w", err)
	}
	res.Scripts = append(res.Scripts, list...)
	return res, nil
}

func (m *Module) run(ctx context.Context, p RunParams) (run.Info, error) {
	app := m.application()
	root, err := app.RequireProject()
	if err != nil {
		return run.Info{}, err
	}
	spec, err := m.buildSpec(root, p.Script, p.Args, p.Interpreter, nil)
	if err != nil {
		return run.Info{}, api.Errorf(api.CodeInvalidParams, "%w", err)
	}
	spec.Type = TypeScript
	var info run.Info
	if err := api.NewInProcess(app).Call(ctx, "run.start", api.RunStartParams{Spec: &spec}, &info); err != nil {
		return run.Info{}, err
	}
	return info, nil
}

// executable returns the Composer executable for project root and
// whether it is a PHP archive (run with php).
func (m *Module) executable(root string) (string, bool) {
	p := strings.TrimSpace(setting(m, SettingPath, ""))
	if p == "" {
		if st, err := os.Stat(filepath.Join(root, pharName)); err == nil && !st.IsDir() {
			return pharName, true
		}
		return "composer", false
	}
	return p, strings.HasSuffix(strings.ToLower(p), ".phar")
}

// commandText renders how Composer is started.
func (m *Module) commandText(root string) string {
	exe, phar := m.executable(root)
	if phar {
		return "php " + exe
	}
	return exe
}

// buildSpec returns the spec of `composer run-script script [-- args]`.
func (m *Module) buildSpec(root, script string, args []string, interp string, env []string) (run.Spec, error) {
	script = strings.TrimSpace(script)
	if script == "" {
		return run.Spec{}, fmt.Errorf("%w: script is required", run.ErrInvalidConfig)
	}
	// Script names come from the repository's composer.json; on Windows
	// composer.bat passes them through cmd.exe, so its metacharacters are
	// refused on every system (no real script name uses them, D463).
	if strings.HasPrefix(script, "-") || strings.ContainsAny(script, " \t\r\n\"%!^&|<>()") {
		return run.Spec{}, fmt.Errorf("%w: invalid script name %q", run.ErrInvalidConfig, script)
	}
	if interp == "" {
		interp = setting(m, SettingInterp, run.InterpreterProject)
	}
	if interp == "" {
		interp = run.InterpreterProject
	}
	exe, phar := m.executable(root)
	argv := []string{"run-script", script}
	if len(args) > 0 {
		argv = append(append(argv, "--"), args...)
	}
	spec := run.Spec{Name: "composer " + script, Type: TypeScript, Dir: root, Interpreter: interp,
		Env: append(slices.Clone(env), "COMPOSER_NO_INTERACTION=1")}
	switch {
	case phar:
		spec.PHP = true
		spec.Args = append([]string{run.ScriptPath(exe, root, root)}, argv...)
	default:
		// On the host a relative path is relative to the project; in the
		// interpreter it stays relative to the (mapped) project root.
		if interp == run.InterpreterHost && !filepath.IsAbs(exe) && strings.ContainsAny(exe, `/\`) {
			exe = filepath.Join(root, filepath.FromSlash(exe))
		}
		spec.Program, spec.Args = exe, argv
	}
	return spec, nil
}

// configType is the run configuration type "Composer Script".
func (m *Module) configType() run.ConfigType {
	return run.ConfigType{
		ID: TypeScript, Label: "Composer Script", Plugin: ID,
		Description: "Run a script of composer.json with Composer (composer run-script) in the project interpreter.",
		Fields: []run.Field{
			{Key: FieldScript, Kind: settings.KindString, Label: "Script", Required: true, Suggest: true, Help: "Name of a script in composer.json."},
			{Key: FieldArgs, Kind: settings.KindString, Label: "Arguments", Help: "Passed to the script after --; quote with ' or \"."},
			{Key: FieldInterp, Kind: settings.KindString, Label: "Interpreter", Suggest: true,
				Help: "Empty: the plugin setting (project interpreter by default); host or an interpreter ID."},
			{Key: FieldEnv, Kind: settings.KindList, Label: "Environment variables", Help: "KEY=VALUE, one per line."},
		},
		Suggest: func(_ context.Context, env run.BuildEnv, field string) []string {
			switch field {
			case FieldScript:
				list, _ := Scripts(env.ProjectDir)
				out := make([]string, 0, len(list))
				for _, s := range list {
					out = append(out, s.Name)
				}
				return out
			case FieldInterp:
				return []string{run.InterpreterProject, run.InterpreterHost}
			}
			return nil
		},
		Build: func(_ context.Context, cfg run.Config, env run.BuildEnv) (run.Spec, error) {
			args, err := run.SplitWords(cfg.String(FieldArgs, ""))
			if err != nil {
				return run.Spec{}, fmt.Errorf("%w: arguments: %w", run.ErrInvalidConfig, err)
			}
			spec, err := m.buildSpec(env.ProjectDir, cfg.String(FieldScript, ""), args, cfg.String(FieldInterp, ""), cfg.Strings(FieldEnv))
			if err != nil {
				return run.Spec{}, err
			}
			spec.Name = cfg.Name
			return spec, nil
		},
	}
}
