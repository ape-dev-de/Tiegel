package run

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/ape-dev-de/tiegel/internal/interp"
)

// norm compares paths separator-neutrally: filepath.Join yields "\" on
// Windows and "/" elsewhere.
func norm(p string) string { return strings.ReplaceAll(p, `\`, "/") }

func normAll(l []string) []string {
	out := make([]string, len(l))
	for i, a := range l {
		out[i] = norm(a)
	}
	return out
}

// fakeWindows is a Windows host with Composer proxies, batch files and an
// .exe (file → is a PHP script).
func fakeWindows() BatchFS {
	files := map[string]bool{
		"C:/proj/vendor/bin/pint":        true,
		"C:/proj/vendor/bin/pint.bat":    false,
		`C:\tools\fixer.bat`:             false,
		`C:\composer\bin\phpstan`:        true,
		`C:\composer\bin\phpstan.bat`:    false,
		"C:/proj/vendor/bin/sh-tool":     false, // a shell proxy, not PHP
		"C:/proj/vendor/bin/sh-tool.bat": false,
	}
	known := map[string]bool{}
	for k, v := range files {
		known[norm(k)] = v
	}
	return BatchFS{
		Look: func(name string) (string, error) {
			switch name {
			case "phpstan":
				return `C:\composer\bin\phpstan.bat`, nil
			case "fixer":
				return `C:\tools\fixer.bat`, nil
			case "tool":
				return `C:\tools\tool.exe`, nil
			}
			return "", errors.New("not found")
		},
		IsFile: func(p string) bool { _, ok := known[norm(p)]; return ok },
		IsPHP:  func(p string) bool { return known[norm(p)] },
	}
}

// Windows command building, independent of the system running the test
// (D362: batch files get no repository file names).
func TestWindowsBatchSafe(t *testing.T) {
	fsys := fakeWindows()
	evil := "src/a&calc&.php"
	cases := []struct {
		name, program string
		args          []string
		want          BatchCommand
		err           string
	}{
		// Composer's proxy script runs with php, the .bat is never used.
		{"vendor script", "vendor/bin/pint", []string{evil}, BatchCommand{PHP: true, Args: []string{"vendor/bin/pint", evil}}, ""},
		{"explicit bat with script", "vendor/bin/pint.bat", []string{evil}, BatchCommand{PHP: true, Args: []string{"C:/proj/vendor/bin/pint", evil}}, ""},
		{"bare name in PATH", "phpstan", []string{evil}, BatchCommand{PHP: true, Args: []string{`C:\composer\bin\phpstan`, evil}}, ""},
		{"phar", `C:\tools\php-cs-fixer.phar`, []string{evil}, BatchCommand{PHP: true, Args: []string{`C:\tools\php-cs-fixer.phar`, evil}}, ""},
		// A batch file without PHP script: only harmless arguments.
		{"bat refuses &", "fixer", []string{"fix", evil}, BatchCommand{}, "batch file"},
		{"bat refuses %", `C:\tools\fixer.bat`, []string{"src/%PATH%.php"}, BatchCommand{}, "batch file"},
		{"bat refuses quote", `C:\tools\fixer.bat`, []string{`a".php`}, BatchCommand{}, "batch file"},
		{"bat refuses newline", `C:\tools\fixer.bat`, []string{"a\nb"}, BatchCommand{}, "batch file"},
		{"bat plain args", `C:\tools\fixer.bat`, []string{"fix", "src/My File.php"}, BatchCommand{Program: `C:\tools\fixer.bat`, Args: []string{"fix", "src/My File.php"}}, ""},
		{"shell proxy resolves to bat", "vendor/bin/sh-tool", []string{evil}, BatchCommand{}, "batch file"},
		{"exe unchanged", "tool", []string{evil}, BatchCommand{Program: "tool", Args: []string{evil}}, ""},
		{"unknown left to run", "missing", []string{evil}, BatchCommand{Program: "missing", Args: []string{evil}}, ""},
	}
	for _, c := range cases {
		got, err := WindowsBatchSafe("windows", "C:/proj", c.program, c.args, fsys)
		if c.err != "" {
			if err == nil || !strings.Contains(err.Error(), c.err) {
				t.Errorf("%s: err %v, want %q", c.name, err, c.err)
			}
			continue
		}
		if err != nil || norm(got.Program) != norm(c.want.Program) || got.PHP != c.want.PHP || !slices.Equal(normAll(got.Args), normAll(c.want.Args)) {
			t.Errorf("%s: %+v %v, want %+v", c.name, got, err, c.want)
		}
	}
	for _, goos := range []string{"linux", "darwin"} {
		if got, _ := WindowsBatchSafe(goos, "/p", "vendor/bin/pint", []string{evil}, fsys); got.PHP || got.Program != "vendor/bin/pint" {
			t.Errorf("%s: %+v", goos, got)
		}
	}
	// Without PATH lookup a bare name stays as is.
	if got, err := WindowsBatchSafe("windows", "C:/proj", "fixer", []string{evil}, BatchFS{}); err != nil || got.Program != "fixer" {
		t.Errorf("no lookup: %+v %v", got, err)
	}
}

// External Tools on a Windows host: macro values from the editor or file
// names of the repository never reach cmd.exe through a batch file
// (review wave 8, #5).
func TestGuardBatchExternalTool(t *testing.T) {
	fsys := fakeWindows()
	cfg := Config{Name: "fix", Type: TypeExternalTool, Values: map[string]any{
		ToolFieldProgram: "fixer", ToolFieldArgs: "fix $SelectedText$"}}
	spec, err := buildExternalTool(context.Background(), cfg, BuildEnv{ProjectDir: "C:/proj",
		Context: &MacroContext{File: "C:/proj/src/A.php", SelectedText: "x&calc"}})
	if err != nil {
		t.Fatal(err)
	}
	spec.Type = TypeExternalTool
	host := Target{ProjectDir: "C:/proj"}
	if _, err := guardBatch("windows", spec, host, "C:/proj", fsys); !errors.Is(err, ErrBatchArgument) || !strings.Contains(err.Error(), "x&calc") {
		t.Fatalf("selection with & passed to a batch file: %v", err)
	}
	// A local php is the host as well.
	local := Target{Interp: &interp.Local{Path: `C:\php\php.exe`}}
	if _, err := guardBatch("windows", spec, local, "C:/proj", fsys); err == nil {
		t.Fatal("local interpreter not guarded")
	}
	// Composer proxies run as PHP script.
	spec.Program, spec.Args = "C:/proj/vendor/bin/pint", []string{"src/a&calc&.php"}
	got, err := guardBatch("windows", spec, host, "C:/proj", fsys)
	if err != nil || !got.PHP || got.Program != "" || norm(got.Args[0]) != "C:/proj/vendor/bin/pint" || got.Args[1] != "src/a&calc&.php" {
		t.Fatalf("proxy %+v %v", got, err)
	}
	if err := got.Validate(); err != nil {
		t.Fatal(err)
	}
	// Unchanged: other systems, containers, other run types.
	spec.Program, spec.Args = "fixer", []string{"x&calc"}
	if got, err := guardBatch("linux", spec, host, "/p", fsys); err != nil || got.Program != "fixer" {
		t.Fatalf("linux %+v %v", got, err)
	}
	if got, err := guardBatch("windows", spec, Target{Interp: &interp.WSL{}}, "C:/proj", fsys); err != nil || got.Program != "fixer" {
		t.Fatalf("wsl %+v %v", got, err)
	}
	other := spec
	other.Type = TypeCommand
	if got, err := guardBatch("windows", other, host, "C:/proj", fsys); err != nil || got.Program != "fixer" {
		t.Fatalf("command %+v %v", got, err)
	}
}

// Prepare applies the guard: on this system an External Tool runs
// unchanged unless it is Windows.
func TestPrepareGuardsExternalTool(t *testing.T) {
	dir := t.TempDir()
	spec := Spec{Name: "t", Type: TypeExternalTool, Program: filepath.Join(dir, "tool.bat"), Args: []string{"a&b"}}
	if err := os.WriteFile(spec.Program, []byte("@echo off\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	_, err := Prepare(context.Background(), spec, Target{ProjectDir: dir})
	if isWindows := filepath.Separator == '\\'; isWindows != errors.Is(err, ErrBatchArgument) {
		t.Fatalf("windows %v, err %v", isWindows, err)
	}
}

func TestIsPHPScript(t *testing.T) {
	dir := t.TempDir()
	for name, c := range map[string]struct {
		text string
		want bool
	}{
		"proxy": {"#!/usr/bin/env php\n<?php\n", true},
		"tag":   {"<?php\ninclude 'x';\n", true},
		"sh":    {"#!/usr/bin/env sh\necho <?php\n", false},
		"bin":   {"MZ\x00\x01", false},
	} {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte(c.text), 0o644); err != nil {
			t.Fatal(err)
		}
		if IsPHPScript(p) != c.want {
			t.Errorf("%s: want %v", name, c.want)
		}
	}
	if IsPHPScript(filepath.Join(dir, "missing")) {
		t.Error("missing file")
	}
	if ExtAny(`C:\a.b\c`) != "" || ExtAny("x/y.BAT") != ".BAT" || ExtAny(".hidden") != "" {
		t.Error("ExtAny")
	}
	if !IsAbsAny(`C:\x`) || !IsAbsAny(`\\srv\x`) || IsAbsAny("vendor/bin/x") {
		t.Error("IsAbsAny")
	}
	if !isRegularFile(filepath.Join(dir, "tag")) || isRegularFile(dir) {
		t.Error("isRegularFile")
	}
}
