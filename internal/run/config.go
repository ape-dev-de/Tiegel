package run

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/ape-dev-de/tiegel/internal/core/settings"
)

// Settings keys of the run infrastructure.
const (
	// KeyConfigurations stores the project's run configurations
	// ([]Config, project settings; edited through run.configs.*).
	KeyConfigurations = "run.configurations"
	// KeySelected stores the ID of the selected configuration (project).
	KeySelected = "run.selected"
	// KeyConfirmClose is what happens to active runs when their project
	// or window closes (ask, terminate, keep).
	KeyConfirmClose = "run.confirmClose"
)

// Values of KeyConfirmClose.
const (
	CloseAsk       = "ask"
	CloseTerminate = "terminate"
	CloseKeep      = "keep"
)

// PageRun is the settings page of the core run settings.
const PageRun = "Run"

// SettingSpecs returns the core settings of the run infrastructure.
func SettingSpecs() []settings.Spec {
	return []settings.Spec{{
		Key: KeyConfirmClose, Kind: settings.KindEnum, Default: CloseAsk, Options: []string{CloseAsk, CloseTerminate, CloseKeep},
		Scope: settings.ScopeGlobal, Group: PageRun, Label: "Running processes on close",
		Help: "When a project or window with running processes closes: ask, terminate them or keep them running.",
	}}
}

// registerErr records a registration problem of SettingSpecs (tested).
var registerErr = settings.RegisterCore(SettingSpecs()...)

// Field declares one field of a configuration type, like a settings spec
// (decision D190): kind, default, label, help, enum options.
type Field struct {
	Key   string        `json:"key"`
	Kind  settings.Kind `json:"kind"`
	Label string        `json:"label"`
	Help  string        `json:"help,omitempty"`
	// Default is used when the configuration has no value.
	Default any `json:"default,omitempty"`
	// Options are the values of a KindEnum field.
	Options []string `json:"options,omitempty"`
	// Required fields must have a non-empty value.
	Required bool `json:"required,omitempty"`
	// Suggest marks fields whose suggestions come from
	// ConfigType.Suggest (e.g. the scripts of composer.json).
	Suggest bool `json:"suggest,omitempty"`
}

// spec returns the settings spec used to validate values of f.
func (f Field) spec() settings.Spec {
	return settings.Spec{Key: "run." + f.Key, Kind: f.Kind, Scope: settings.ScopeProject, Label: f.Label, Options: f.Options, Default: f.Default}
}

// Config is a stored run configuration.
type Config struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	// Type is the ConfigType ID.
	Type string `json:"type"`
	// Values are the field values by Field.Key.
	Values map[string]any `json:"values,omitempty"`
}

// String returns the string value of key (def when missing).
func (c Config) String(key, def string) string {
	if v, ok := c.Values[key].(string); ok {
		return v
	}
	return def
}

// Strings returns a list value of key; a string value is split into lines.
func (c Config) Strings(key string) []string {
	switch v := c.Values[key].(type) {
	case []string:
		return append([]string(nil), v...)
	case []any:
		out := make([]string, 0, len(v))
		for _, e := range v {
			if s, ok := e.(string); ok {
				out = append(out, s)
			}
		}
		return out
	case string:
		var out []string
		for _, l := range strings.Split(v, "\n") {
			if l = strings.TrimSpace(l); l != "" {
				out = append(out, l)
			}
		}
		return out
	}
	return nil
}

// Bool returns the bool value of key (def when missing).
func (c Config) Bool(key string, def bool) bool {
	if v, ok := c.Values[key].(bool); ok {
		return v
	}
	return def
}

// BuildEnv is what a configuration type may know when building a spec.
type BuildEnv struct {
	// ProjectDir is the open project's root.
	ProjectDir string
	// Context is the editor context of the start (current file, caret,
	// selection) for macros such as $FilePath$; nil when the client sent
	// none.
	Context *MacroContext
}

// ConfigType is a kind of run configuration (PHP script, external tool,
// Composer script, PHPUnit, …). The kernel provides the core types; plugins add
// theirs through the filter hook "run.configTypes" (api.HookRunConfigTypes).
type ConfigType struct {
	ID          string `json:"id"`
	Label       string `json:"label"`
	Description string `json:"description,omitempty"`
	// Plugin is the contributing module ("" for the core types).
	Plugin string  `json:"plugin,omitempty"`
	Fields []Field `json:"fields"`
	// Suggest returns suggestions for fields marked Suggest (optional).
	Suggest func(ctx context.Context, env BuildEnv, field string) []string `json:"-"`
	// Build turns a validated configuration into a spec.
	Build func(ctx context.Context, cfg Config, env BuildEnv) (Spec, error) `json:"-"`
}

// Validate checks the declaration.
func (t ConfigType) Validate() error {
	if t.ID == "" || t.Label == "" {
		return errors.New("run: config type needs id and label")
	}
	if t.Build == nil {
		return fmt.Errorf("run: config type %s has no Build", t.ID)
	}
	seen := map[string]bool{}
	for _, f := range t.Fields {
		if f.Key == "" || seen[f.Key] {
			return fmt.Errorf("run: config type %s: empty or duplicate field %q", t.ID, f.Key)
		}
		seen[f.Key] = true
		if err := f.spec().Validate(); err != nil {
			return fmt.Errorf("run: config type %s: field %s: %w", t.ID, f.Key, err)
		}
	}
	return nil
}

// ErrInvalidConfig marks configurations that do not fit their type.
var ErrInvalidConfig = errors.New("run: invalid configuration")

// Check validates cfg against t and returns it with defaults applied
// and unknown fields dropped.
func (t ConfigType) Check(cfg Config) (Config, error) {
	if strings.TrimSpace(cfg.Name) == "" {
		return cfg, fmt.Errorf("%w: name is required", ErrInvalidConfig)
	}
	out := Config{ID: cfg.ID, Name: strings.TrimSpace(cfg.Name), Type: t.ID, Values: map[string]any{}}
	for _, f := range t.Fields {
		v, ok := cfg.Values[f.Key]
		if !ok || v == nil {
			if f.Required {
				return cfg, fmt.Errorf("%w: %s is required", ErrInvalidConfig, f.Label)
			}
			continue
		}
		if err := f.spec().Check(v); err != nil {
			return cfg, fmt.Errorf("%w: %s: %w", ErrInvalidConfig, f.Label, err)
		}
		if s, isStr := v.(string); isStr && f.Required && strings.TrimSpace(s) == "" {
			return cfg, fmt.Errorf("%w: %s is required", ErrInvalidConfig, f.Label)
		}
		out.Values[f.Key] = v
	}
	return out, nil
}

// WithDefaults returns cfg with the defaults of t for missing fields.
func (t ConfigType) WithDefaults(cfg Config) Config {
	vals := make(map[string]any, len(t.Fields))
	for k, v := range cfg.Values {
		vals[k] = v
	}
	for _, f := range t.Fields {
		if _, ok := vals[f.Key]; !ok && f.Default != nil {
			vals[f.Key] = f.Default
		}
	}
	cfg.Values = vals
	return cfg
}

// NewID returns a fresh configuration ID.
func NewID() string {
	var b [6]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

// ---- core types ---------------------------------------------------------------

// Core type IDs.
const (
	TypePHPScript    = "php-script"
	TypeCommand      = "command"
	TypeExternalTool = "external-tool"
)

// Common fields of the core types.
var (
	fieldArgs = Field{Key: "args", Kind: settings.KindString, Label: "Arguments",
		Help: "Arguments, separated by spaces; quote with ' or \"."}
	fieldDir = Field{Key: "workingDir", Kind: settings.KindPath, Label: "Working directory",
		Help: "Absolute or relative to the project root; empty is the project root."}
	fieldEnv = Field{Key: "env", Kind: settings.KindList, Label: "Environment variables",
		Help: "KEY=VALUE, one per line."}
)

func fieldInterpreter(def string) Field {
	return Field{Key: "interpreter", Kind: settings.KindString, Label: "Interpreter", Default: def, Suggest: true,
		Help: "project (the project's interpreter), host (run directly) or an interpreter ID."}
}

// CoreTypes returns the configuration types of the kernel.
func CoreTypes() []ConfigType {
	return []ConfigType{
		{
			ID: TypePHPScript, Label: "PHP Script", Description: "Run a PHP file with the project's (or a chosen) interpreter.",
			Fields: []Field{
				{Key: "file", Kind: settings.KindPath, Label: "File", Required: true, Help: "PHP file, relative to the project root or absolute."},
				fieldArgs,
				{Key: "phpOptions", Kind: settings.KindString, Label: "Interpreter options", Help: "Options for php before the file, e.g. -d memory_limit=-1."},
				fieldInterpreter(InterpreterProject), fieldDir, fieldEnv,
			},
			Suggest: suggestInterpreter,
			Build:   buildPHPScript,
		},
		{
			ID: TypeCommand, Label: "Command", Description: "Run any command on the host or inside the interpreter (container).",
			Fields: []Field{
				{Key: "command", Kind: settings.KindString, Label: "Command", Required: true, Help: "Program and arguments, e.g. bin/console cache:clear."},
				fieldInterpreter(InterpreterHost), fieldDir, fieldEnv,
			},
			Suggest: suggestInterpreter,
			Build:   buildCommand,
		},
		externalToolType(),
	}
}

func suggestInterpreter(_ context.Context, _ BuildEnv, field string) []string {
	if field == "interpreter" {
		return []string{InterpreterProject, InterpreterHost}
	}
	return nil
}

// common reads the fields shared by the core types.
func common(cfg Config, env BuildEnv, defInterp string) (Spec, error) {
	s := Spec{Name: cfg.Name, Config: cfg.ID, Type: cfg.Type, Interpreter: cfg.String("interpreter", defInterp), Env: cfg.Strings("env")}
	if s.Interpreter == "" {
		s.Interpreter = defInterp
	}
	if d := strings.TrimSpace(cfg.String("workingDir", "")); d != "" {
		if !filepath.IsAbs(d) {
			d = filepath.Join(env.ProjectDir, filepath.FromSlash(d))
		}
		s.Dir = d
	}
	return s, nil
}

func buildPHPScript(_ context.Context, cfg Config, env BuildEnv) (Spec, error) {
	s, err := common(cfg, env, InterpreterProject)
	if err != nil {
		return Spec{}, err
	}
	s.PHP = true
	opts, err := SplitWords(cfg.String("phpOptions", ""))
	if err != nil {
		return Spec{}, fmt.Errorf("%w: interpreter options: %w", ErrInvalidConfig, err)
	}
	args, err := SplitWords(cfg.String("args", ""))
	if err != nil {
		return Spec{}, fmt.Errorf("%w: arguments: %w", ErrInvalidConfig, err)
	}
	s.Args = append(append(opts, ScriptPath(cfg.String("file", ""), s.Dir, env.ProjectDir)), args...)
	return s, nil
}

// ScriptPath returns the path of file as passed to php: relative (with
// slashes) to the working directory when the file lies below it, so the
// same command works on the host and inside a container; otherwise
// absolute (container targets map absolute project paths).
func ScriptPath(file, dir, projectDir string) string {
	if file == "" {
		return file
	}
	if dir == "" {
		dir = projectDir
	}
	abs := file
	if !filepath.IsAbs(abs) {
		abs = filepath.Join(projectDir, filepath.FromSlash(file))
	}
	if dir != "" {
		if rel, err := filepath.Rel(dir, abs); err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return filepath.ToSlash(rel)
		}
	}
	return abs
}

func buildCommand(_ context.Context, cfg Config, env BuildEnv) (Spec, error) {
	s, err := common(cfg, env, InterpreterHost)
	if err != nil {
		return Spec{}, err
	}
	words, err := SplitWords(cfg.String("command", ""))
	if err != nil {
		return Spec{}, fmt.Errorf("%w: command: %w", ErrInvalidConfig, err)
	}
	if len(words) == 0 {
		return Spec{}, fmt.Errorf("%w: command is empty", ErrInvalidConfig)
	}
	s.Program, s.Args = words[0], words[1:]
	return s, nil
}

// FileSpec returns the ad-hoc spec that runs file as PHP script with the
// project interpreter ("Run current file").
func FileSpec(file, projectDir string, args []string) Spec {
	name := filepath.Base(file)
	return Spec{Name: name, Type: TypePHPScript, PHP: true, Interpreter: InterpreterProject,
		Args: append([]string{ScriptPath(file, projectDir, projectDir)}, args...)}
}
