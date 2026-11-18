package run

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/ape-dev-de/tiegel/internal/core/settings"
)

// External tools (AP17.2): a core configuration type that runs any
// program with arguments and a working directory in which macros such as
// $FilePath$ are replaced by the backend with the editor context the
// client sends (MacroContext). The program runs on the host by default or
// in the project's interpreter; the run can stay without console and
// trigger a file synchronisation when it ends.

// MacroContext is the editor context of a start: the client (GUI,
// headless flags) passes what is current; the backend expands the macros.
type MacroContext struct {
	// File is the current file (absolute or relative to the project).
	File string `json:"file,omitempty"`
	// Line and Column are the 1-based caret position (0: unknown).
	Line   int `json:"line,omitempty"`
	Column int `json:"column,omitempty"`
	// SelectedText is the selection of the current editor.
	SelectedText string `json:"selectedText,omitempty"`
}

// Macro describes one macro of external tools.
type Macro struct {
	// Name is the macro with its dollar signs, e.g. "$FilePath$".
	Name        string `json:"name"`
	Description string `json:"description"`
	// NeedsFile marks macros that fail without a current file.
	NeedsFile bool `json:"needsFile,omitempty"`
}

// macroDef is a macro with its expansion.
type macroDef struct {
	Macro
	value func(m macroEnv) (string, bool)
}

// macroEnv holds the resolved context of an expansion.
type macroEnv struct {
	root string
	file string // absolute, "" without
	ctx  MacroContext
}

func (e macroEnv) rel(p string) string {
	if e.root == "" {
		return filepath.ToSlash(p)
	}
	r, err := filepath.Rel(e.root, p)
	if err != nil || r == ".." || strings.HasPrefix(r, ".."+string(filepath.Separator)) {
		return filepath.ToSlash(p)
	}
	return filepath.ToSlash(r)
}

func withFile(f func(e macroEnv) string) func(macroEnv) (string, bool) {
	return func(e macroEnv) (string, bool) {
		if e.file == "" {
			return "", false
		}
		return f(e), true
	}
}

// macroDefs is the macro set (decision D413), in documentation order.
var macroDefs = []macroDef{
	{Macro{Name: "$ProjectFileDir$", Description: "Project root directory (absolute)"},
		func(e macroEnv) (string, bool) { return e.root, true }},
	{Macro{Name: "$ProjectName$", Description: "Name of the project directory"},
		func(e macroEnv) (string, bool) { return filepath.Base(e.root), e.root != "" }},
	{Macro{Name: "$FilePath$", Description: "Current file (absolute path)", NeedsFile: true},
		withFile(func(e macroEnv) string { return e.file })},
	{Macro{Name: "$FileDir$", Description: "Directory of the current file (absolute)", NeedsFile: true},
		withFile(func(e macroEnv) string { return filepath.Dir(e.file) })},
	{Macro{Name: "$FileName$", Description: "File name of the current file with extension", NeedsFile: true},
		withFile(func(e macroEnv) string { return filepath.Base(e.file) })},
	{Macro{Name: "$FileNameWithoutExtension$", Description: "File name of the current file without extension", NeedsFile: true},
		withFile(func(e macroEnv) string {
			b := filepath.Base(e.file)
			return strings.TrimSuffix(b, filepath.Ext(b))
		})},
	{Macro{Name: "$FileExt$", Description: "Extension of the current file without dot", NeedsFile: true},
		withFile(func(e macroEnv) string { return strings.TrimPrefix(filepath.Ext(e.file), ".") })},
	{Macro{Name: "$FileRelativePath$", Description: "Current file relative to the project root (slashes)", NeedsFile: true},
		withFile(func(e macroEnv) string { return e.rel(e.file) })},
	{Macro{Name: "$FileDirRelativePath$", Description: "Directory of the current file relative to the project root (slashes; . for the root)", NeedsFile: true},
		withFile(func(e macroEnv) string { return e.rel(filepath.Dir(e.file)) })},
	{Macro{Name: "$LineNumber$", Description: "Caret line, 1-based (empty when unknown)"},
		func(e macroEnv) (string, bool) { return positive(e.ctx.Line), true }},
	{Macro{Name: "$ColumnNumber$", Description: "Caret column, 1-based (empty when unknown)"},
		func(e macroEnv) (string, bool) { return positive(e.ctx.Column), true }},
	{Macro{Name: "$SelectedText$", Description: "Selected text of the current editor (empty without selection)"},
		func(e macroEnv) (string, bool) { return e.ctx.SelectedText, true }},
}

func positive(n int) string {
	if n <= 0 {
		return ""
	}
	return strconv.Itoa(n)
}

// Macros returns the macros external tools understand.
func Macros() []Macro {
	out := make([]Macro, len(macroDefs))
	for i, d := range macroDefs {
		out[i] = d.Macro
	}
	return out
}

// macroPattern finds macro candidates: $Name$ with letters only.
var macroPattern = regexp.MustCompile(`\$[A-Za-z]+\$`)

// ErrMacroNeedsFile is returned when a file macro is used without a
// current file.
var ErrMacroNeedsFile = errors.New("run: macro needs a current file")

// ExpandMacros replaces the known macros in s with the values of ctx
// (nil: no editor context) for project root root. Unknown $Words$ stay
// as they are. A file macro without a current file fails with
// ErrMacroNeedsFile.
func ExpandMacros(s, root string, ctx *MacroContext) (string, error) {
	env := macroEnv{root: root}
	if ctx != nil {
		env.ctx = *ctx
		if f := strings.TrimSpace(ctx.File); f != "" {
			if !filepath.IsAbs(f) && root != "" {
				f = filepath.Join(root, filepath.FromSlash(f))
			}
			env.file = filepath.Clean(f)
		}
	}
	var err error
	out := macroPattern.ReplaceAllStringFunc(s, func(m string) string {
		for _, d := range macroDefs {
			if d.Name != m {
				continue
			}
			v, ok := d.value(env)
			if !ok && err == nil {
				err = fmt.Errorf("%w: %s", ErrMacroNeedsFile, m)
			}
			return v
		}
		return m
	})
	if err != nil {
		return "", err
	}
	return out, nil
}

// UsesMacros reports whether s contains a known macro.
func UsesMacros(s string) bool {
	for _, m := range macroPattern.FindAllString(s, -1) {
		for _, d := range macroDefs {
			if d.Name == m {
				return true
			}
		}
	}
	return false
}

// Fields of the external tool type.
const (
	ToolFieldProgram     = "program"
	ToolFieldArgs        = "args"
	ToolFieldDir         = "workingDir"
	ToolFieldInterpreter = "interpreter"
	ToolFieldEnv         = "env"
	ToolFieldConsole     = "openConsole"
	ToolFieldSync        = "syncFiles"
	ToolFieldDescription = "description"
)

func externalToolType() ConfigType {
	return ConfigType{
		ID: TypeExternalTool, Label: "External Tool", Tool: true,
		Description: "Run a program with macros ($FilePath$, $LineNumber$, $SelectedText$, …) filled in from the current editor; listed under Tools › External Tools.",
		Fields: []Field{
			{Key: ToolFieldProgram, Kind: settings.KindString, Label: "Program", Required: true,
				Help: "Program to run: a name from PATH, a path relative to the project root or absolute. Macros allowed."},
			{Key: ToolFieldArgs, Kind: settings.KindString, Label: "Arguments",
				Help: "Arguments, separated by spaces; quote with ' or \". Macros are replaced per word, so $SelectedText$ stays one argument."},
			{Key: ToolFieldDir, Kind: settings.KindString, Label: "Working directory",
				Help: "Absolute or relative to the project root (macros allowed, e.g. $FileDir$); empty is the project root."},
			{Key: ToolFieldInterpreter, Kind: settings.KindString, Label: "Run in", Default: InterpreterHost, Suggest: true,
				Help: "host (directly) or project (the project's interpreter, e.g. the Compose service; absolute project paths are mapped)."},
			fieldEnv,
			{Key: ToolFieldConsole, Kind: settings.KindBool, Label: "Open console for tool output", Default: true,
				Help: "Show the Run tool window when the tool starts; off runs it quietly (the run is listed nevertheless)."},
			{Key: ToolFieldSync, Kind: settings.KindBool, Label: "Synchronize files after execution", Default: true,
				Help: "When the tool ends, rescan the project and reload open documents the tool changed."},
			{Key: ToolFieldDescription, Kind: settings.KindString, Label: "Description", Help: "Shown in the External Tools menu."},
		},
		Suggest: suggestInterpreter,
		Build:   buildExternalTool,
	}
}

func buildExternalTool(_ context.Context, cfg Config, env BuildEnv) (Spec, error) {
	expand := func(what, s string) (string, error) {
		v, err := ExpandMacros(s, env.ProjectDir, env.Context)
		if err != nil {
			return "", fmt.Errorf("%w: %s: %w", ErrInvalidConfig, what, err)
		}
		return v, nil
	}
	prog, err := expand("program", strings.TrimSpace(cfg.String(ToolFieldProgram, "")))
	if err != nil {
		return Spec{}, err
	}
	if prog == "" {
		return Spec{}, fmt.Errorf("%w: program is empty", ErrInvalidConfig)
	}
	interp := cfg.String(ToolFieldInterpreter, InterpreterHost)
	if interp == "" {
		interp = InterpreterHost
	}
	// On the host a relative path with a separator is relative to the
	// project; inside an interpreter it stays relative to the working
	// directory there.
	if interp == InterpreterHost && !filepath.IsAbs(prog) && strings.ContainsAny(prog, `/\`) && env.ProjectDir != "" {
		prog = filepath.Join(env.ProjectDir, filepath.FromSlash(prog))
	}
	words, err := SplitWords(cfg.String(ToolFieldArgs, ""))
	if err != nil {
		return Spec{}, fmt.Errorf("%w: arguments: %w", ErrInvalidConfig, err)
	}
	args := make([]string, 0, len(words))
	for _, w := range words {
		v, err := expand("arguments", w)
		if err != nil {
			return Spec{}, err
		}
		args = append(args, v)
	}
	s := Spec{Name: cfg.Name, Config: cfg.ID, Type: cfg.Type, Program: prog, Args: args,
		Interpreter: interp, Env: cfg.Strings(ToolFieldEnv),
		Quiet: !cfg.Bool(ToolFieldConsole, true), SyncFiles: cfg.Bool(ToolFieldSync, true)}
	if d := strings.TrimSpace(cfg.String(ToolFieldDir, "")); d != "" {
		if d, err = expand("working directory", d); err != nil {
			return Spec{}, err
		}
		if !filepath.IsAbs(d) {
			d = filepath.Join(env.ProjectDir, filepath.FromSlash(d))
		}
		s.Dir = filepath.Clean(d)
	}
	return s, nil
}
