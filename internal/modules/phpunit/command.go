package phpunit

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"unicode"

	"github.com/ape-dev-de/tiegel/internal/api"
	"github.com/ape-dev-de/tiegel/internal/core/settings"
	"github.com/ape-dev-de/tiegel/internal/run"
)

// Fields of the run configuration type "phpunit".
const (
	fieldScope   = "scope"
	fieldSuite   = "suite"
	fieldPath    = "path"
	fieldClass   = "class"
	fieldMethod  = "method"
	fieldDataSet = "dataSet"
	fieldFilter  = "filter"
	fieldArgs    = "args"
	fieldInterp  = "interpreter"
	fieldEnv     = "env"
)

// Scope values of the configuration type.
const (
	scopeAll    = "all"
	scopeSuite  = "suite"
	scopePath   = "path"
	scopeClass  = "class"
	scopeMethod = "method"
)

// configType is the run configuration type "PHPUnit".
func (m *Module) configType() run.ConfigType {
	return run.ConfigType{
		ID: ID, Label: Framework, Plugin: ID,
		Description: "Run PHPUnit tests (all, a suite, a file or directory, a class or a method) with streamed results.",
		Fields: []run.Field{
			{Key: fieldScope, Kind: settings.KindEnum, Label: "Test scope", Default: scopeAll,
				Options: []string{scopeAll, scopeSuite, scopePath, scopeClass, scopeMethod},
				Help:    "What to run: everything, a test suite of the configuration, a file or directory, a class or a method."},
			{Key: fieldSuite, Kind: settings.KindString, Label: "Test suite", Suggest: true},
			{Key: fieldPath, Kind: settings.KindPath, Label: "File or directory", Help: "Project relative or absolute."},
			{Key: fieldClass, Kind: settings.KindString, Label: "Class", Help: "Fully qualified test class name."},
			{Key: fieldMethod, Kind: settings.KindString, Label: "Method"},
			{Key: fieldDataSet, Kind: settings.KindString, Label: "Data set", Help: "Index (0) or name of one data set of the method."},
			{Key: fieldFilter, Kind: settings.KindString, Label: "Filter", Help: "Raw --filter expression (in addition to the scope)."},
			{Key: fieldArgs, Kind: settings.KindString, Label: "Test runner options", Help: "Extra PHPUnit options; quote with ' or \"."},
			{Key: fieldInterp, Kind: settings.KindString, Label: "Interpreter", Suggest: true,
				Help: "Empty: the plugin setting (project interpreter by default); host or an interpreter ID."},
			{Key: fieldEnv, Kind: settings.KindList, Label: "Environment variables", Help: "KEY=VALUE, one per line."},
		},
		Suggest: func(_ context.Context, env run.BuildEnv, field string) []string {
			switch field {
			case fieldSuite:
				var out []string
				for _, s := range m.detect(env.ProjectDir).Suites {
					out = append(out, s.Name)
				}
				return out
			case fieldInterp:
				return []string{run.InterpreterProject, run.InterpreterHost}
			}
			return nil
		},
		Build: func(_ context.Context, cfg run.Config, env run.BuildEnv) (run.Spec, error) {
			return m.buildConfig(cfg, env.ProjectDir)
		},
	}
}

// scopeOfConfig reads the scope fields of a configuration.
func scopeOfConfig(cfg run.Config) api.TestScope {
	sc := api.TestScope{Filter: cfg.String(fieldFilter, "")}
	switch cfg.String(fieldScope, scopeAll) {
	case scopeSuite:
		sc.Suite = cfg.String(fieldSuite, "")
	case scopePath:
		sc.Path = cfg.String(fieldPath, "")
	case scopeClass:
		sc.Path, sc.Class = cfg.String(fieldPath, ""), cfg.String(fieldClass, "")
	case scopeMethod:
		sc.Path, sc.Class = cfg.String(fieldPath, ""), cfg.String(fieldClass, "")
		sc.Method, sc.DataSet = cfg.String(fieldMethod, ""), cfg.String(fieldDataSet, "")
	}
	return sc
}

func (m *Module) buildConfig(cfg run.Config, root string) (run.Spec, error) {
	sc := scopeOfConfig(cfg)
	if (cfg.String(fieldScope, scopeAll) == scopeClass || cfg.String(fieldScope, scopeAll) == scopeMethod) && sc.Class == "" {
		return run.Spec{}, fmt.Errorf("%w: class is required", run.ErrInvalidConfig)
	}
	if cfg.String(fieldScope, scopeAll) == scopeMethod && sc.Method == "" {
		return run.Spec{}, fmt.Errorf("%w: method is required", run.ErrInvalidConfig)
	}
	extra, err := run.SplitWords(cfg.String(fieldArgs, ""))
	if err != nil {
		return run.Spec{}, fmt.Errorf("%w: test runner options: %w", run.ErrInvalidConfig, err)
	}
	spec, err := m.buildSpec(root, sc, cfg.String(fieldInterp, ""), extra)
	if err != nil {
		return run.Spec{}, err
	}
	spec.Name = cfg.Name
	spec.Env = append(spec.Env, cfg.Strings(fieldEnv)...)
	return spec, nil
}

// errNotInstalled reports a missing runner.
type errNotInstalled struct{ hint string }

func (e errNotInstalled) Error() string { return e.hint }

// buildSpec turns a scope into the run spec: php <phpunit> --teamcity
// [-c CONFIG] [--testsuite S] [--filter F] [PATH] in the interpreter.
func (m *Module) buildSpec(root string, sc api.TestScope, interp string, extra []string) (run.Spec, error) {
	p := m.detect(root)
	if p.Executable == "" {
		st, _ := m.status(context.Background(), struct{}{})
		return run.Spec{}, errNotInstalled{hint: st.Hint}
	}
	exe := p.Executable
	narrow := sc.Class != "" || sc.Method != "" || len(sc.Tests) > 0
	if p.Parallel != "" && !narrow && setting(m, SettingParallel, false) {
		exe = p.Parallel
	}
	args := []string{run.ScriptPath(exe, root, root), "--teamcity"}
	if p.Config != "" {
		args = append(args, "--configuration", run.ScriptPath(p.Config, root, root))
	}
	if sc.Suite != "" {
		args = append(args, "--testsuite", sc.Suite)
	}
	filter, err := FilterFor(sc)
	if err != nil {
		return run.Spec{}, err
	}
	if filter != "" {
		args = append(args, "--filter", filter)
	}
	if def, err := run.SplitWords(setting(m, SettingArgs, "")); err == nil {
		args = append(args, def...)
	}
	args = append(args, extra...)
	if sc.Path != "" {
		args = append(args, run.ScriptPath(abs(root, sc.Path), root, root))
	}
	if interp == "" {
		interp = setting(m, SettingInterp, run.InterpreterProject)
	}
	return run.Spec{
		Name: m.runName(sc), Type: ID, PHP: true, Args: args, Dir: root, Interpreter: interp,
	}, nil
}

// runName is the tab title of a run.
func (m *Module) runName(sc api.TestScope) string {
	short := func(fqn string) string {
		if i := strings.LastIndexByte(fqn, '\\'); i >= 0 {
			return fqn[i+1:]
		}
		return fqn
	}
	switch {
	case len(sc.Tests) > 0:
		return Framework + ": failed tests"
	case sc.Method != "" && sc.DataSet != "":
		return short(sc.Class) + "::" + sc.Method + " " + normDataSet(sc.DataSet)
	case sc.Method != "":
		return short(sc.Class) + "::" + sc.Method
	case sc.Class != "":
		return short(sc.Class)
	case sc.Path != "":
		return Framework + ": " + filepath.Base(sc.Path)
	case sc.Suite != "":
		return Framework + ": " + sc.Suite
	}
	return Framework
}

// FilterFor returns the --filter regular expression of a scope ("" =
// none). Names are anchored and quoted, so a method filter runs exactly
// that method (with all its data sets unless one is given).
func FilterFor(sc api.TestScope) (string, error) {
	var alts []string
	switch {
	case len(sc.Tests) > 0:
		for _, id := range sc.Tests {
			class, method, ds := splitTestID(id)
			if class == "" {
				return "", fmt.Errorf("%w: invalid test %q", run.ErrInvalidConfig, id)
			}
			alts = append(alts, testPattern(class, method, ds))
		}
	case sc.Method != "":
		if sc.Class == "" {
			return "", fmt.Errorf("%w: a method needs its class", run.ErrInvalidConfig)
		}
		alts = append(alts, testPattern(sc.Class, sc.Method, sc.DataSet))
	case sc.Class != "":
		alts = append(alts, testPattern(sc.Class, "", ""))
	}
	if len(alts) == 0 {
		return sc.Filter, nil
	}
	if sc.Filter != "" {
		return "", fmt.Errorf("%w: a raw filter cannot be combined with class or method", run.ErrInvalidConfig)
	}
	return "/^(?:" + strings.Join(alts, "|") + ")$/", nil
}

// splitTestID splits "Ns\Cls::method with data set #0".
func splitTestID(id string) (class, method, dataSet string) {
	class, rest, _ := strings.Cut(strings.TrimPrefix(id, `\`), "::")
	method, dataSet, _ = strings.Cut(rest, " with data set ")
	return class, method, dataSet
}

// testPattern matches the test names of class (and method, data set).
func testPattern(class, method, dataSet string) string {
	p := quote(strings.TrimPrefix(class, `\`)) + "::"
	switch {
	case method == "":
		return p + ".*"
	case dataSet != "":
		return p + quote(method) + quote(" with data set "+normDataSet(dataSet))
	}
	return p + quote(method) + "(?: with data set .*)?"
}

// normDataSet turns "0" into "#0" and a bare name into "\"name\"".
func normDataSet(ds string) string {
	ds = strings.TrimSpace(ds)
	if ds == "" || strings.HasPrefix(ds, "#") || strings.HasPrefix(ds, `"`) {
		return ds
	}
	digits := true
	for _, r := range ds {
		digits = digits && r >= '0' && r <= '9'
	}
	if digits {
		return "#" + ds
	}
	return `"` + ds + `"`
}

// quote escapes s for a PCRE pattern with "/" delimiters: every ASCII
// character except letters, digits, '_' and ' ' is backslash-escaped.
func quote(s string) string {
	var b strings.Builder
	for _, r := range s {
		if r < 0x80 && !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '_' && r != ' ' {
			if r < 0x20 || r == 0x7f {
				fmt.Fprintf(&b, `\x%02x`, r)
				continue
			}
			b.WriteByte('\\')
		}
		b.WriteRune(r)
	}
	return b.String()
}

// scopeOfArgs reads the scope back from the arguments buildSpec made
// (for rerunning the failed tests of any run, also of configurations).
func scopeOfArgs(args []string, root string) api.TestScope {
	var sc api.TestScope
	// Skip the php options of debugged runs and the script.
	i := 0
	for i < len(args) && strings.HasPrefix(args[i], "-d") {
		i++
	}
	i++
	for ; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--testsuite" && i+1 < len(args):
			sc.Suite = args[i+1]
			i++
		case a == "--configuration" || a == "--filter" || a == "-c":
			i++
		case strings.HasPrefix(a, "-"):
		default:
			// Values of the user's own options are no paths.
			if p := abs(root, filepath.FromSlash(a)); isFile(p) || isDir(p) {
				sc.Path = p
			}
		}
	}
	return sc
}

// configFor returns an unsaved run configuration of the plugin's type for
// a scope ("Modify Run Configuration…" at a test in the gutter).
func (m *Module) configFor(_ context.Context, sc api.TestScope) (run.Config, error) {
	root, err := m.root()
	if err != nil {
		return run.Config{}, err
	}
	vals := map[string]any{fieldScope: scopeAll}
	path := ""
	if sc.Path != "" {
		path = rel(root, abs(root, sc.Path))
	}
	switch {
	case sc.Method != "":
		vals[fieldScope], vals[fieldPath], vals[fieldClass], vals[fieldMethod] = scopeMethod, path, sc.Class, sc.Method
		if sc.DataSet != "" {
			vals[fieldDataSet] = sc.DataSet
		}
	case sc.Class != "":
		vals[fieldScope], vals[fieldPath], vals[fieldClass] = scopeClass, path, sc.Class
	case sc.Path != "":
		vals[fieldScope], vals[fieldPath] = scopePath, path
	case sc.Suite != "":
		vals[fieldScope], vals[fieldSuite] = scopeSuite, sc.Suite
	}
	if sc.Filter != "" {
		vals[fieldFilter] = sc.Filter
	}
	return run.Config{Name: m.runName(sc), Type: ID, Values: vals}, nil
}
