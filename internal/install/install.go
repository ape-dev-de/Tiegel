// Package install installs the tools plugins need (language servers,
// later QA tools and PHPUnit) through a package manager, and only on an
// explicit user action (decision D194).
//
// A plugin declares a Recipe (package manager, package, possible targets,
// interpreter). MakePlan turns it into the exact command line, working
// directory and side effects (does composer.json change?) so the user can
// confirm it; Runner executes a confirmed plan visibly: output is streamed
// line by line and the run can be cancelled. Nothing here runs on its own,
// in the background or downloads anything outside the package manager;
// a missing package manager is reported as hint, never fetched.
//
// The package imports no Gio and no API code.
package install

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// Manager is a package manager.
type Manager string

// Package managers.
const (
	// ManagerNone marks hint-only recipes (no clean package manager route).
	ManagerNone     Manager = ""
	ManagerComposer Manager = "composer"
	ManagerNPM      Manager = "npm"
)

// Scope is where a package is installed.
type Scope string

// Scopes.
const (
	// ScopeGlobal installs for the user ("composer global require",
	// "npm install -g").
	ScopeGlobal Scope = "global"
	// ScopeProject installs into the project as dev dependency
	// ("composer require --dev"); composer.json and composer.lock change.
	ScopeProject Scope = "project"
)

// Interpreter tells where the package manager runs.
type Interpreter string

// Interpreters.
const (
	// InterpreterNative runs the package manager of the host (language
	// servers run natively, D192).
	InterpreterNative Interpreter = "native"
	// InterpreterProject runs it in the project's PHP interpreter (local,
	// Compose service, …) – for tool plugins such as qa-pint or phpunit.
	// Needs Env.Wrap (provided by the run infrastructure).
	InterpreterProject Interpreter = "project"
)

// Recipe declares how a plugin's tool is installed.
type Recipe struct {
	// Plugin is the ID of the declaring module (e.g. "lsp-phpactor").
	Plugin string `json:"plugin"`
	// Tool is the display name ("Phpactor").
	Tool string `json:"tool"`
	// Manager and Package name the package ("composer",
	// "phpactor/phpactor"). ManagerNone makes the recipe a hint only.
	Manager Manager `json:"manager,omitempty"`
	Package string  `json:"package,omitempty"`
	// Scopes are the allowed targets; the first is the default.
	Scopes []Scope `json:"scopes,omitempty"`
	// Interpreter tells where the manager runs (default native).
	Interpreter Interpreter `json:"interpreter,omitempty"`
	// Hint explains manual installation (always shown for hint-only
	// recipes, as alternative otherwise).
	Hint string `json:"hint,omitempty"`
	// Note is shown before the installation (e.g. a license remark for
	// proprietary tools).
	Note string `json:"note,omitempty"`
}

// Validate checks the declaration.
func (r Recipe) Validate() error {
	if r.Plugin == "" || r.Tool == "" {
		return errors.New("install: recipe needs plugin and tool")
	}
	switch r.Manager {
	case ManagerNone:
		if r.Hint == "" {
			return fmt.Errorf("install: hint-only recipe of %s needs a hint", r.Plugin)
		}
		return nil
	case ManagerComposer, ManagerNPM:
	default:
		return fmt.Errorf("install: recipe of %s: unknown manager %q", r.Plugin, r.Manager)
	}
	if r.Package == "" || len(r.Scopes) == 0 {
		return fmt.Errorf("install: recipe of %s needs package and scopes", r.Plugin)
	}
	for _, s := range r.Scopes {
		if s != ScopeGlobal && s != ScopeProject {
			return fmt.Errorf("install: recipe of %s: unknown scope %q", r.Plugin, s)
		}
		if s == ScopeProject && r.Manager == ManagerNPM {
			return fmt.Errorf("install: recipe of %s: npm only supports global installs", r.Plugin)
		}
	}
	switch r.Interpreter {
	case "", InterpreterNative, InterpreterProject:
	default:
		return fmt.Errorf("install: recipe of %s: unknown interpreter %q", r.Plugin, r.Interpreter)
	}
	return nil
}

// Env is the environment a plan is made for.
type Env struct {
	// ProjectDir is the open project (needed for ScopeProject).
	ProjectDir string
	// LookPath finds a package manager executable; nil searches PATH.
	LookPath func(name string) (string, error)
	// PathEnv is the PATH the command runs with (e.g. including the login
	// shell PATH on macOS); empty keeps the process PATH.
	PathEnv string
	// Wrap runs a command in the project interpreter (InterpreterProject);
	// nil means not available yet.
	Wrap func(argv []string, dir string) ([]string, error)
}

// Plan is the exact, previewable installation of a recipe.
type Plan struct {
	Plugin string `json:"plugin"`
	Tool   string `json:"tool"`
	// Available tells whether the plan can run; otherwise Hint says why
	// (package manager missing, hint-only recipe, no project, …).
	Available bool `json:"available"`
	// Manager, Package, Scope, Interpreter repeat the chosen route.
	Manager     Manager     `json:"manager,omitempty"`
	Package     string      `json:"package,omitempty"`
	Scope       Scope       `json:"scope,omitempty"`
	Scopes      []Scope     `json:"scopes,omitempty"`
	Interpreter Interpreter `json:"interpreter,omitempty"`
	// Command is the argv that runs; Display the same as shell text.
	Command []string `json:"command,omitempty"`
	Display string   `json:"display,omitempty"`
	// Dir is the working directory.
	Dir string `json:"dir,omitempty"`
	// ChangesComposerJSON is true when composer.json/composer.lock of
	// the project change.
	ChangesComposerJSON bool `json:"changesComposerJson"`
	// Effects describe the changes in words (target directory, files).
	Effects []string `json:"effects,omitempty"`
	Hint    string   `json:"hint,omitempty"`
	Note    string   `json:"note,omitempty"`
	// ID identifies exactly what runs (Fingerprint); install.run refuses
	// to execute a plan whose ID differs from the confirmed preview.
	ID string `json:"id,omitempty"`

	env []string
}

// Fingerprint hashes everything that decides what a plan executes:
// plugin, scope, command, working directory and environment.
func (p Plan) Fingerprint() string {
	b, _ := json.Marshal(struct {
		Plugin    string
		Scope     Scope
		Available bool
		Command   []string
		Dir       string
		Env       []string
	}{p.Plugin, p.Scope, p.Available, p.Command, p.Dir, p.env})
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:12])
}

// ErrUnknownScope is returned for a scope the recipe does not offer.
var ErrUnknownScope = errors.New("install: scope not offered by the recipe")

// MakePlan computes the plan of r for scope ("" = the recipe's default).
// It never runs anything except looking up the package manager.
func MakePlan(r Recipe, scope Scope, env Env) (Plan, error) {
	if err := r.Validate(); err != nil {
		return Plan{}, err
	}
	p := Plan{Plugin: r.Plugin, Tool: r.Tool, Manager: r.Manager, Package: r.Package, Hint: r.Hint, Note: r.Note,
		Scopes: append([]Scope(nil), r.Scopes...), Interpreter: r.Interpreter}
	if p.Interpreter == "" {
		p.Interpreter = InterpreterNative
	}
	if r.Manager == ManagerNone {
		return p, nil // hint only
	}
	if scope == "" {
		scope = r.Scopes[0]
	}
	ok := false
	for _, s := range r.Scopes {
		ok = ok || s == scope
	}
	if !ok {
		return Plan{}, fmt.Errorf("%w: %s offers %v, not %q", ErrUnknownScope, r.Plugin, r.Scopes, scope)
	}
	p.Scope = scope

	var argv []string
	switch {
	case r.Manager == ManagerComposer && scope == ScopeGlobal:
		argv = []string{"composer", "global", "require", r.Package}
		p.Effects = []string{"installs " + r.Package + " for your user (Composer global vendor/bin)"}
	case r.Manager == ManagerComposer:
		argv = []string{"composer", "require", "--dev", r.Package}
		p.ChangesComposerJSON = true
	case r.Manager == ManagerNPM:
		argv = []string{"npm", "install", "--global", r.Package}
		p.Effects = []string{"installs " + r.Package + " for your user (npm global prefix)"}
	}
	if scope == ScopeProject {
		if env.ProjectDir == "" {
			p.Hint = joinHint("Open a project to install " + r.Tool + " into it.")
			return p, nil
		}
		p.Dir = env.ProjectDir
		cj := filepath.Join(env.ProjectDir, "composer.json")
		if _, err := os.Stat(cj); err == nil {
			p.Effects = []string{"adds " + r.Package + " to require-dev in " + cj + " and updates composer.lock and vendor/"}
		} else {
			p.Effects = []string{"creates " + cj + " with " + r.Package + " in require-dev, composer.lock and vendor/"}
		}
	} else {
		if home, err := os.UserHomeDir(); err == nil {
			p.Dir = home
		}
	}

	if p.Interpreter == InterpreterProject {
		if env.Wrap == nil {
			p.Hint = joinHint("Installing into the project interpreter needs the run infrastructure, which is not available yet.", r.Hint)
			p.Command, p.Display = argv, Quote(argv)
			return p, nil
		}
		wrapped, err := env.Wrap(argv, p.Dir)
		if err != nil {
			return Plan{}, fmt.Errorf("install: project interpreter: %w", err)
		}
		p.Command, p.Display, p.Available = wrapped, Quote(wrapped), true
		return p, nil
	}

	look := env.LookPath
	if look == nil {
		look = defaultLookPath(env.PathEnv)
	}
	exe, err := look(string(r.Manager))
	p.Display = Quote(argv)
	if err != nil {
		p.Command = argv
		p.Hint = joinHint(managerMissing(r.Manager), r.Hint)
		return p, nil
	}
	argv[0] = exe
	p.Command, p.Available = argv, true
	if env.PathEnv != "" {
		p.env = []string{"PATH=" + env.PathEnv}
	}
	return p, nil
}

func managerMissing(m Manager) string {
	switch m {
	case ManagerComposer:
		return "Composer was not found. Install Composer with your package manager (e.g. Homebrew: brew install composer) or from getcomposer.org; Tiegel does not download it."
	case ManagerNPM:
		return "npm was not found. Install Node.js with npm (e.g. Homebrew: brew install node); Tiegel does not download it."
	}
	return string(m) + " was not found."
}

func joinHint(parts ...string) string {
	var out []string
	for _, p := range parts {
		if p != "" {
			out = append(out, p)
		}
	}
	return strings.Join(out, " ")
}

func defaultLookPath(pathEnv string) func(string) (string, error) {
	return func(name string) (string, error) {
		if pathEnv == "" {
			pathEnv = os.Getenv("PATH")
		}
		exts := []string{""}
		if runtime.GOOS == "windows" {
			exts = []string{".exe", ".bat", ".cmd", ""}
		}
		for _, d := range filepath.SplitList(pathEnv) {
			if d == "" {
				continue
			}
			for _, e := range exts {
				p := filepath.Join(d, name+e)
				if fi, err := os.Stat(p); err == nil && !fi.IsDir() && (runtime.GOOS == "windows" || fi.Mode()&0o111 != 0) {
					return p, nil
				}
			}
		}
		return "", fmt.Errorf("install: %s not found in PATH", name)
	}
}

// Quote renders argv as shell text (single quotes where needed).
func Quote(argv []string) string {
	out := make([]string, len(argv))
	for i, a := range argv {
		if a != "" && !strings.ContainsAny(a, " \t\n'\"\\$`;&|<>(){}[]*?!#~") {
			out[i] = a
			continue
		}
		out[i] = "'" + strings.ReplaceAll(a, "'", `'\''`) + "'"
	}
	return strings.Join(out, " ")
}
