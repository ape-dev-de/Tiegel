package run

import (
	"context"
	"errors"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestExpandMacros(t *testing.T) {
	root := filepath.Join(t.TempDir(), "proj")
	file := filepath.Join(root, "src", "Foo.Bar.php")
	ctx := &MacroContext{File: file, Line: 12, Column: 3, SelectedText: "a b"}
	cases := map[string]string{
		"$ProjectFileDir$":            root,
		"$ProjectName$":               "proj",
		"$FilePath$":                  file,
		"$FileDir$":                   filepath.Join(root, "src"),
		"$FileName$":                  "Foo.Bar.php",
		"$FileNameWithoutExtension$":  "Foo.Bar",
		"$FileExt$":                   "php",
		"$FileRelativePath$":          "src/Foo.Bar.php",
		"$FileDirRelativePath$":       "src",
		"$LineNumber$:$ColumnNumber$": "12:3",
		"[$SelectedText$]":            "[a b]",
		"$Unknown$ $HOME and $$":      "$Unknown$ $HOME and $$",
	}
	for in, want := range cases {
		got, err := ExpandMacros(in, root, ctx)
		if err != nil || got != want {
			t.Errorf("%s: %q, %v (want %q)", in, got, err, want)
		}
	}
	// Relative file, no position, file outside the project.
	got, _ := ExpandMacros("$FilePath$ $LineNumber$|", root, &MacroContext{File: "a/b.php"})
	if got != filepath.Join(root, "a", "b.php")+" |" {
		t.Fatalf("relative %q", got)
	}
	out := filepath.Join(t.TempDir(), "x.php")
	if got, _ := ExpandMacros("$FileRelativePath$", root, &MacroContext{File: out}); got != filepath.ToSlash(out) {
		t.Fatalf("outside %q", got)
	}
	if got, _ := ExpandMacros("$FileDirRelativePath$", root, &MacroContext{File: filepath.Join(root, "x.php")}); got != "." {
		t.Fatalf("root dir %q", got)
	}
	// Without a file the file macros fail, the others work.
	if _, err := ExpandMacros("x $FileName$", root, nil); !errors.Is(err, ErrMacroNeedsFile) || !strings.Contains(err.Error(), "$FileName$") {
		t.Fatalf("no file: %v", err)
	}
	if got, err := ExpandMacros("$ProjectFileDir$ $SelectedText$", root, nil); err != nil || got != root+" " {
		t.Fatalf("no context: %q %v", got, err)
	}
	if !UsesMacros("x $FilePath$") || UsesMacros("$Nope$ $HOME") {
		t.Fatal("UsesMacros")
	}
	if len(Macros()) != len(macroDefs) || Macros()[0].Name != "$ProjectFileDir$" {
		t.Fatal("Macros")
	}
}

func TestExternalToolType(t *testing.T) {
	root := t.TempDir()
	tt := coreType(t, TypeExternalTool)
	if err := tt.Validate(); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(root, "src", "A.php")
	env := BuildEnv{ProjectDir: root, Context: &MacroContext{File: file, Line: 7, SelectedText: "hello world"}}
	ctx := context.Background()
	cfg, err := tt.Check(Config{ID: "t1", Name: "Grep", Values: map[string]any{
		ToolFieldProgram: "bin/tool", ToolFieldArgs: `--line=$LineNumber$ $SelectedText$ "$FileName$"`,
		ToolFieldDir: "$FileDir$", ToolFieldEnv: []any{"A=1"}}})
	if err != nil {
		t.Fatal(err)
	}
	spec, err := tt.Build(ctx, tt.WithDefaults(cfg), env)
	if err != nil {
		t.Fatal(err)
	}
	if spec.Program != filepath.Join(root, "bin", "tool") || !slices.Equal(spec.Args, []string{"--line=7", "hello world", "A.php"}) ||
		spec.Dir != filepath.Join(root, "src") || spec.Interpreter != InterpreterHost || spec.Quiet || !spec.SyncFiles ||
		spec.Config != "t1" || spec.Name != "Grep" || !slices.Equal(spec.Env, []string{"A=1"}) {
		t.Fatalf("spec %+v", spec)
	}
	// In the interpreter a relative program stays relative; options off.
	cfg.Values[ToolFieldInterpreter] = InterpreterProject
	cfg.Values[ToolFieldConsole] = false
	cfg.Values[ToolFieldSync] = false
	cfg.Values[ToolFieldDir] = "sub"
	spec, err = tt.Build(ctx, tt.WithDefaults(cfg), env)
	if err != nil || spec.Program != "bin/tool" || !spec.Quiet || spec.SyncFiles || spec.Interpreter != InterpreterProject || spec.Dir != filepath.Join(root, "sub") {
		t.Fatalf("interpreter spec %+v %v", spec, err)
	}
	// Errors: file macros without a file, empty program, bad quoting.
	for _, vals := range []map[string]any{
		{ToolFieldProgram: "echo", ToolFieldArgs: "$FilePath$"},
		{ToolFieldProgram: "$FilePath$"},
		{ToolFieldProgram: "echo", ToolFieldDir: "$FileDir$"},
		{ToolFieldProgram: "  "},
		{ToolFieldProgram: "echo", ToolFieldArgs: `"open`},
	} {
		if _, err := tt.Build(ctx, Config{Name: "x", Values: vals}, BuildEnv{ProjectDir: root}); !errors.Is(err, ErrInvalidConfig) {
			t.Errorf("%v: %v", vals, err)
		}
	}
	// A plain program name keeps PATH lookup.
	spec, err = tt.Build(ctx, Config{Name: "x", Values: map[string]any{ToolFieldProgram: "echo", ToolFieldInterpreter: ""}}, BuildEnv{ProjectDir: root})
	if err != nil || spec.Program != "echo" || spec.Interpreter != InterpreterHost || spec.Dir != "" {
		t.Fatalf("plain %+v %v", spec, err)
	}
}
