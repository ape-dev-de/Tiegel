package run

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/ape-dev-de/tiegel/internal/core/settings"
)

func coreType(t *testing.T, id string) ConfigType {
	t.Helper()
	for _, ct := range CoreTypes() {
		if ct.ID == id {
			return ct
		}
	}
	t.Fatalf("no core type %s", id)
	return ConfigType{}
}

func TestSettingSpecs(t *testing.T) {
	if registerErr != nil {
		t.Fatal(registerErr)
	}
	s, ok := settings.CoreSpec(KeyConfirmClose)
	if !ok || s.Default != CloseAsk || s.Group != PageRun {
		t.Fatalf("spec %+v", s)
	}
}

func TestCoreTypes(t *testing.T) {
	for _, ct := range CoreTypes() {
		if err := ct.Validate(); err != nil {
			t.Fatal(err)
		}
	}
	root := t.TempDir()
	env := BuildEnv{ProjectDir: root}
	ctx := context.Background()

	php := coreType(t, TypePHPScript)
	cfg, err := php.Check(Config{ID: "c1", Name: " Script ", Values: map[string]any{
		"file": "bin/x.php", "args": `--name "a b"`, "phpOptions": "-d memory_limit=-1", "env": []any{"A=1"}, "bogus": 1}})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Name != "Script" || cfg.Type != TypePHPScript || cfg.Values["bogus"] != nil {
		t.Fatalf("checked %+v", cfg)
	}
	spec, err := php.Build(ctx, php.WithDefaults(cfg), env)
	if err != nil {
		t.Fatal(err)
	}
	if !spec.PHP || spec.Interpreter != InterpreterProject || spec.Config != "c1" ||
		!slices.Equal(spec.Args, []string{"-d", "memory_limit=-1", "bin/x.php", "--name", "a b"}) || !slices.Equal(spec.Env, []string{"A=1"}) {
		t.Fatalf("php spec %+v", spec)
	}
	// A working directory below the file's directory makes it relative
	// to that directory; outside, the path stays absolute.
	cfg.Values["workingDir"] = "bin"
	spec, _ = php.Build(ctx, cfg, env)
	if spec.Dir != filepath.Join(root, "bin") || spec.Args[2] != "x.php" {
		t.Fatalf("working dir %+v", spec)
	}
	cfg.Values["workingDir"] = filepath.Join(root, "other")
	spec, _ = php.Build(ctx, cfg, env)
	if spec.Args[2] != filepath.Join(root, "bin", "x.php") {
		t.Fatalf("outside dir %+v", spec)
	}
	cfg.Values["args"] = `"open`
	if _, err := php.Build(ctx, cfg, env); !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("bad args %v", err)
	}
	cfg.Values["args"], cfg.Values["phpOptions"] = "", `'open`
	if _, err := php.Build(ctx, cfg, env); !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("bad options %v", err)
	}
	for _, bad := range []Config{
		{Name: ""},
		{Name: "x"},
		{Name: "x", Values: map[string]any{"file": " "}},
		{Name: "x", Values: map[string]any{"file": "a.php", "env": 3}},
	} {
		if _, err := php.Check(bad); !errors.Is(err, ErrInvalidConfig) {
			t.Errorf("check %+v: %v", bad, err)
		}
	}

	cmd := coreType(t, TypeCommand)
	spec, err = cmd.Build(ctx, cmd.WithDefaults(Config{Name: "c", Type: TypeCommand, Values: map[string]any{"command": "bin/console cache:clear -v"}}), env)
	if err != nil || spec.Program != "bin/console" || !slices.Equal(spec.Args, []string{"cache:clear", "-v"}) || spec.Interpreter != InterpreterHost {
		t.Fatalf("command %+v %v", spec, err)
	}
	if _, err := cmd.Build(ctx, Config{Values: map[string]any{"command": "  "}}, env); !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("empty command %v", err)
	}
	if _, err := cmd.Build(ctx, Config{Values: map[string]any{"command": `"x`}}, env); !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("bad command %v", err)
	}

	comp := coreType(t, TypeComposerScript)
	spec, err = comp.Build(ctx, comp.WithDefaults(Config{Name: "t", Values: map[string]any{"script": "test", "args": "--filter Foo"}}), env)
	if err != nil || spec.Program != "composer" || !slices.Equal(spec.Args, []string{"run-script", "test", "--", "--filter", "Foo"}) ||
		spec.Interpreter != InterpreterProject || !slices.Contains(spec.Env, "COMPOSER_NO_INTERACTION=1") {
		t.Fatalf("composer %+v %v", spec, err)
	}
	if _, err := comp.Build(ctx, Config{Values: map[string]any{"args": "'x"}}, env); err == nil {
		t.Fatal("bad composer args")
	}
	if err := os.WriteFile(filepath.Join(root, "composer.json"), []byte(`{"scripts":{"test":"phpunit","lint":["a","b"]}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := comp.Suggest(ctx, env, "script"); !slices.Equal(got, []string{"lint", "test"}) {
		t.Fatalf("scripts %v", got)
	}
	if got := comp.Suggest(ctx, env, "interpreter"); !slices.Equal(got, []string{InterpreterProject, InterpreterHost}) {
		t.Fatalf("interpreters %v", got)
	}
	if got := php.Suggest(ctx, env, "file"); got != nil {
		t.Fatalf("file suggestions %v", got)
	}
	if ComposerScripts("") != nil || ComposerScripts(t.TempDir()) != nil {
		t.Fatal("scripts without composer.json")
	}
	bad := t.TempDir()
	_ = os.WriteFile(filepath.Join(bad, "composer.json"), []byte(`{`), 0o644)
	if ComposerScripts(bad) != nil {
		t.Fatal("scripts of broken composer.json")
	}
}

func TestConfigTypeValidate(t *testing.T) {
	build := func(context.Context, Config, BuildEnv) (Spec, error) { return Spec{}, nil }
	for _, bad := range []ConfigType{
		{},
		{ID: "x", Label: "X"},
		{ID: "x", Label: "X", Build: build, Fields: []Field{{Key: ""}}},
		{ID: "x", Label: "X", Build: build, Fields: []Field{{Key: "a", Kind: settings.KindString}, {Key: "a", Kind: settings.KindString}}},
		{ID: "x", Label: "X", Build: build, Fields: []Field{{Key: "a", Kind: "weird"}}},
	} {
		if err := bad.Validate(); err == nil {
			t.Errorf("valid %+v", bad)
		}
	}
}

func TestConfigAccessors(t *testing.T) {
	c := Config{Values: map[string]any{"s": "x", "l": []string{"a"}, "la": []any{"b", 1}, "ls": "c\n\n d ", "b": true}}
	if c.String("s", "") != "x" || c.String("nope", "def") != "def" || !c.Bool("b", false) || c.Bool("nope", true) != true {
		t.Fatal("scalars")
	}
	if !slices.Equal(c.Strings("l"), []string{"a"}) || !slices.Equal(c.Strings("la"), []string{"b"}) ||
		!slices.Equal(c.Strings("ls"), []string{"c", "d"}) || c.Strings("b") != nil {
		t.Fatal("lists")
	}
	if a, b := NewID(), NewID(); a == b || len(a) != 12 {
		t.Fatal("ids")
	}
	root := t.TempDir()
	fs := FileSpec(filepath.Join(root, "src", "a.php"), root, []string{"x"})
	if fs.Name != "a.php" || !fs.PHP || !slices.Equal(fs.Args, []string{"src/a.php", "x"}) {
		t.Fatalf("file spec %+v", fs)
	}
	if ScriptPath("", "", root) != "" {
		t.Fatal("empty script path")
	}
}
