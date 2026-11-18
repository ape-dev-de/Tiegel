//go:build !windows

package qa

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ape-dev-de/tiegel/internal/api"
	"github.com/ape-dev-de/tiegel/internal/core/bus"
	"github.com/ape-dev-de/tiegel/internal/core/module"
	"github.com/ape-dev-de/tiegel/internal/install"
	"github.com/ape-dev-de/tiegel/internal/modules/modtest"
)

// Fake tools (shell scripts in vendor/bin) with the real plugin
// definitions: they run through the run infrastructure on the host (no
// php-runtime module: no interpreter provider).
const (
	fakePint = `#!/bin/sh
for a in "$@"; do f="$a"; done
sed 's/array(1,2)/[1, 2]/' "$f" > "$f.out" && mv "$f.out" "$f"
`
	fakePHPCBF = `#!/bin/sh
sed 's/array(1,2)/[1,2]/'
exit 1
`
	// fakePHPStan reports line 2 of every path unless clean exists.
	fakePHPStan = `#!/bin/sh
if [ -f clean ]; then echo '{"totals":{},"files":{},"errors":[]}'; exit 0; fi
if [ -f slow ]; then sleep 30; fi
printf '{"totals":{},"files":{'
sep=
for a in "$@"; do
  case "$a" in -*|analyse|.) continue;; esac
  printf '%s"%s/%s":{"messages":[{"message":"Bad thing","line":2,"identifier":"bad.thing"}]}' "$sep" "$PWD" "$a"
  sep=,
done
printf '},"errors":[]}\n'
exit 1
`
)

const source = "<?php\n$x = array(1,2);\n"

func newApp(t *testing.T) (*api.App, string) {
	t.Helper()
	var mods []module.Module
	for _, p := range All() {
		mods = append(mods, p)
	}
	a := modtest.NewApp(t, mods, nil)
	dir := modtest.Project(t, a, map[string]string{
		"a.php": source, "b.php": source,
		"vendor/bin/pint": fakePint, "vendor/bin/phpcbf": fakePHPCBF, "vendor/bin/phpcs": "#!/bin/sh\nexit 0\n",
		"vendor/bin/phpstan": fakePHPStan,
	})
	for _, f := range []string{"pint", "phpcbf", "phpcs", "phpstan"} {
		if err := os.Chmod(filepath.Join(dir, "vendor", "bin", f), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return a, dir
}

func set(t *testing.T, a *api.App, key string, v any) {
	t.Helper()
	raw, _ := json.Marshal(v)
	modtest.Call(t, a, "settings.set", api.SettingsSetParams{Key: key, Scope: "project", Value: raw}, nil)
}

func docText(t *testing.T, a *api.App, path string) string {
	t.Helper()
	var r struct {
		Text string `json:"text"`
	}
	modtest.Call(t, a, "doc.text", api.DocTextParams{Path: path}, &r)
	return r.Text
}

func TestListAndInstall(t *testing.T) {
	a, _ := newApp(t)
	var list []api.QAToolInfo
	modtest.Call(t, a, "qa.list", nil, &list)
	if len(list) != 6 {
		t.Fatalf("tools %+v", list)
	}
	byID := map[string]api.QAToolInfo{}
	for _, i := range list {
		byID[i.Plugin] = i
		if !i.Install {
			t.Errorf("%s has no recipe", i.Plugin)
		}
	}
	if p := byID[PintID]; !p.Available || !p.Formatter || p.Analyzer || !p.Reformat || p.Command != "vendor/bin/pint" {
		t.Fatalf("pint %+v", p)
	}
	if p := byID[PHPCSFixerID]; p.Available || !strings.Contains(p.Reason, "vendor/bin/php-cs-fixer not found") {
		t.Fatalf("fixer %+v", p)
	}
	if p := byID[PHPCSID]; !p.Available || !p.Formatter || !p.Analyzer {
		t.Fatalf("phpcs %+v", p)
	}
	if p := byID[PsalmID]; p.Available {
		t.Fatalf("psalm %+v", p)
	}
	// The install recipe: composer require --dev in the project interpreter,
	// shown as plan only (never run here).
	var recipes []install.Recipe
	modtest.Call(t, a, "install.recipes", nil, &recipes)
	found := false
	for _, r := range recipes {
		if r.Plugin == PsalmID {
			found = r.Package == "vimeo/psalm" && r.Manager == install.ManagerComposer && r.Interpreter == install.InterpreterProject && r.Scopes[0] == install.ScopeProject
		}
	}
	if !found {
		t.Fatalf("recipes %+v", recipes)
	}
	// A missing tool: the error names the reason for the Install button.
	err := modtest.WantCode(t, a, "qa.run", api.QARunParams{Tool: PsalmID}, api.CodeUnavailable)
	if api.QAErrorReason(err) != "missing" {
		t.Fatalf("reason of %v", err)
	}
	modtest.WantCode(t, a, "qa.format", api.QAFormatParams{Tool: PHPCSFixerID, Path: "a.php"}, api.CodeUnavailable)
	modtest.WantCode(t, a, "qa.run", api.QARunParams{Tool: PintID}, api.CodeInvalidParams)
	modtest.WantCode(t, a, "qa.run", api.QARunParams{Tool: "qa-nope"}, api.CodeNotFound)

	// Each plugin can be switched off on its own.
	modtest.Call(t, a, "modules.disable", api.ModuleParams{ID: PintID}, nil)
	modtest.Call(t, a, "qa.list", nil, &list)
	if len(list) != 5 {
		t.Fatalf("after disable %+v", list)
	}
	for _, i := range list {
		if i.Plugin == PintID {
			t.Fatal("disabled plugin listed")
		}
	}
	// Reformat now falls to the next formatter (phpcbf).
	var fr api.QAFormatResult
	modtest.Call(t, a, "qa.format", api.QAFormatParams{Path: "a.php", Apply: new(bool)}, &fr)
	if fr.Tool != PHPCSID || fr.Text != "<?php\n$x = [1,2];\n" {
		t.Fatalf("fallback formatter %+v", fr)
	}
	modtest.Call(t, a, "modules.disable", api.ModuleParams{ID: PHPCSID}, nil)
	err = modtest.WantCode(t, a, "qa.format", api.QAFormatParams{Path: "a.php"}, api.CodeUnavailable)
	if api.QAErrorReason(err) != "noFormatter" {
		t.Fatalf("reason %v", err)
	}
}

func TestFormatOpenDocumentIsUndoable(t *testing.T) {
	a, dir := newApp(t)
	modtest.Call(t, a, "doc.open", api.DocOpenParams{Path: "a.php"}, nil)
	rec := modtest.Record(t, a, "qa.*")
	var r api.QAFormatResult
	modtest.Call(t, a, "qa.format", api.QAFormatParams{Path: "a.php"}, &r)
	if r.Tool != PintID || !r.Changed || !r.Open || !r.Applied || r.Edits != 1 {
		t.Fatalf("result %+v", r)
	}
	if got := docText(t, a, "a.php"); got != "<?php\n$x = [1, 2];\n" {
		t.Fatalf("buffer %q", got)
	}
	// The file on disk is untouched, no temporary copy remains.
	if b, _ := os.ReadFile(filepath.Join(dir, "a.php")); string(b) != source {
		t.Fatalf("disk %q", b)
	}
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), "__tiegel_") {
			t.Fatalf("temporary copy left: %s", e.Name())
		}
	}
	modtest.Call(t, a, "doc.undo", api.PathParams{Path: "a.php"}, nil)
	if got := docText(t, a, "a.php"); got != source {
		t.Fatalf("after undo %q", got)
	}
	rec.Wait(t, api.TopicQAFinished, func(e bus.Event) bool {
		ev := e.Payload.(api.QARunEvent)
		return ev.Kind == api.QAKindFormat && ev.Count == 1 && ev.Error == ""
	})
	// Stdin formatter of a closed file: the file is rewritten.
	modtest.Call(t, a, "qa.format", api.QAFormatParams{Tool: PHPCSID, Path: "b.php"}, &r)
	if r.Open || !r.Changed {
		t.Fatalf("closed %+v", r)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "b.php")); string(b) != "<?php\n$x = [1,2];\n" {
		t.Fatalf("closed file %q", b)
	}
	// Already formatted: nothing changes.
	modtest.Call(t, a, "qa.format", api.QAFormatParams{Tool: PHPCSID, Path: "b.php"}, &r)
	if r.Changed || r.Edits != 0 {
		t.Fatalf("second run %+v", r)
	}
	// A stale version is refused.
	modtest.WantCode(t, a, "qa.format", api.QAFormatParams{Path: "a.php", Version: 999}, api.CodeConflict)
}

func TestFormatOnSave(t *testing.T) {
	a, dir := newApp(t)
	modtest.Call(t, a, "doc.open", api.DocOpenParams{Path: "a.php"}, nil)
	modtest.Call(t, a, "doc.insert", map[string]any{"path": "a.php", "at": map[string]int{"offset": len(source)}, "text": "// x\n"}, nil)
	// Off by default: saved as typed.
	modtest.Call(t, a, "doc.save", api.DocSaveParams{Path: "a.php"}, nil)
	if b, _ := os.ReadFile(filepath.Join(dir, "a.php")); string(b) != source+"// x\n" {
		t.Fatalf("default %q", b)
	}
	set(t, a, PintID+".formatOnSave", true)
	modtest.Call(t, a, "doc.insert", map[string]any{"path": "a.php", "at": map[string]int{"offset": 0}, "text": "\n"}, nil)
	modtest.Call(t, a, "doc.save", api.DocSaveParams{Path: "a.php"}, nil)
	want := "\n<?php\n$x = [1, 2];\n// x\n"
	if b, _ := os.ReadFile(filepath.Join(dir, "a.php")); string(b) != want {
		t.Fatalf("saved %q", b)
	}
	if got := docText(t, a, "a.php"); got != want {
		t.Fatalf("buffer %q", got)
	}
	// The formatting is an undo step of the document.
	modtest.Call(t, a, "doc.undo", api.PathParams{Path: "a.php"}, nil)
	if got := docText(t, a, "a.php"); got != "\n"+source+"// x\n" {
		t.Fatalf("undo %q", got)
	}
	// A failing formatter never blocks the save.
	modtest.WriteFile(t, filepath.Join(dir, "vendor", "bin", "pint"), "#!/bin/sh\necho broken >&2\nexit 3\n")
	_ = os.Chmod(filepath.Join(dir, "vendor", "bin", "pint"), 0o755)
	rec := modtest.Record(t, a, api.TopicQAFinished)
	modtest.Call(t, a, "doc.save", api.DocSaveParams{Path: "a.php"}, nil)
	if b, _ := os.ReadFile(filepath.Join(dir, "a.php")); string(b) != "\n"+source+"// x\n" {
		t.Fatalf("saved despite error %q", b)
	}
	rec.Wait(t, api.TopicQAFinished, func(e bus.Event) bool {
		return strings.Contains(e.Payload.(api.QARunEvent).Error, "broken")
	})
}

func TestInspect(t *testing.T) {
	a, dir := newApp(t)
	rec := modtest.Record(t, a, api.TopicQADiagnostics)
	var res api.QARunResult
	modtest.Call(t, a, "qa.run", api.QARunParams{Tool: PHPStanID, Paths: []string{"a.php"}}, &res)
	if res.Count != 1 || len(res.Files) != 1 || res.Files[0].Path != "a.php" {
		t.Fatalf("result %+v", res)
	}
	d := res.Files[0].Diagnostics[0]
	// Line 2 without column: from the first non-blank to the line end.
	if d.Source != PHPStanID || d.Line != 2 || d.Start != len("<?php\n") || d.End != len("<?php\n$x = array(1,2);") || d.Severity != "error" || d.Code != "bad.thing" {
		t.Fatalf("diagnostic %+v", d)
	}
	rec.Wait(t, api.TopicQADiagnostics, func(e bus.Event) bool {
		ev := e.Payload.(api.QADiagnosticsEvent)
		return ev.Path == "a.php" && ev.Source == PHPStanID && len(ev.Diagnostics) == 1
	})
	// Project run (all available analyzers; phpcs reports nothing here).
	modtest.Call(t, a, "qa.run", api.QARunParams{}, &res)
	if len(res.Tools) != 2 {
		t.Fatalf("tools %+v", res.Tools)
	}
	// The project run (the fake reports nothing for ".") replaced the
	// findings of the whole project.
	var stored []api.QADiagnosticsResult
	modtest.Call(t, a, "qa.diagnostics", api.QADiagnosticsParams{Tool: PHPStanID}, &stored)
	if len(stored) != 0 || res.Count != 0 {
		t.Fatalf("stored %+v", stored)
	}
	modtest.Call(t, a, "qa.run", api.QARunParams{Tool: PHPStanID, Paths: []string{"a.php", "b.php"}}, &res)
	modtest.Call(t, a, "qa.diagnostics", api.QADiagnosticsParams{Path: "a.php"}, &stored)
	if res.Count != 2 || len(stored) != 1 || stored[0].Path != "a.php" {
		t.Fatalf("stored %+v", stored)
	}
	// Severity setting overrides the tool's.
	set(t, a, PHPStanID+".severity", "info")
	modtest.Call(t, a, "qa.run", api.QARunParams{Tool: PHPStanID, Paths: []string{"a.php"}}, &res)
	if res.Files[0].Diagnostics[0].Severity != "info" {
		t.Fatalf("severity %+v", res.Files[0])
	}
	// Fixed: the file's findings are cleared.
	modtest.WriteFile(t, filepath.Join(dir, "clean"), "")
	modtest.Call(t, a, "qa.run", api.QARunParams{Tool: PHPStanID, Paths: []string{"a.php"}}, &res)
	if res.Count != 0 {
		t.Fatalf("clean %+v", res)
	}
	rec.Wait(t, api.TopicQADiagnostics, func(e bus.Event) bool {
		ev := e.Payload.(api.QADiagnosticsEvent)
		return ev.Path == "a.php" && len(ev.Diagnostics) == 0
	})
	// Only a.php was inspected again; b.php keeps its findings.
	modtest.Call(t, a, "qa.diagnostics", nil, &stored)
	if len(stored) != 1 || stored[0].Path != "b.php" {
		t.Fatalf("after clean %+v", stored)
	}
	modtest.WantCode(t, a, "qa.run", api.QARunParams{Tool: PHPStanID, Paths: []string{"/elsewhere/x.php"}}, api.CodeInvalidParams)
}

func TestInspectOnSaveAndDisable(t *testing.T) {
	a, _ := newApp(t)
	rec := modtest.Record(t, a, api.TopicQADiagnostics)
	set(t, a, PHPStanID+".inspectOnSave", true)
	modtest.Call(t, a, "doc.open", api.DocOpenParams{Path: "b.php"}, nil)
	modtest.Call(t, a, "doc.insert", map[string]any{"path": "b.php", "at": map[string]int{"offset": 0}, "text": " "}, nil)
	modtest.Call(t, a, "doc.save", api.DocSaveParams{Path: "b.php"}, nil)
	rec.Wait(t, api.TopicQADiagnostics, func(e bus.Event) bool {
		ev := e.Payload.(api.QADiagnosticsEvent)
		return ev.Path == "b.php" && len(ev.Diagnostics) == 1
	})
	// Switching the plugin off clears its findings.
	modtest.Call(t, a, "modules.disable", api.ModuleParams{ID: PHPStanID}, nil)
	rec.Wait(t, api.TopicQADiagnostics, func(e bus.Event) bool {
		ev := e.Payload.(api.QADiagnosticsEvent)
		return ev.Path == "b.php" && len(ev.Diagnostics) == 0
	})
	var stored []api.QADiagnosticsResult
	modtest.Call(t, a, "qa.diagnostics", nil, &stored)
	if len(stored) != 0 {
		t.Fatalf("stored %+v", stored)
	}
}

func TestTimeout(t *testing.T) {
	a, dir := newApp(t)
	set(t, a, PHPStanID+".timeout", 1)
	modtest.WriteFile(t, filepath.Join(dir, "slow"), "")
	start := time.Now()
	err := modtest.WantCode(t, a, "qa.run", api.QARunParams{Tool: PHPStanID, Paths: []string{"a.php"}}, api.CodeFailed)
	if !strings.Contains(err.Error(), "timeout") || time.Since(start) > 15*time.Second {
		t.Fatalf("%v after %v", err, time.Since(start))
	}
}

// fakeFixer behaves like PHP-CS-Fixer's psr_autoloading rule: it renames
// the class after the file it is given and notes the directory.
const fakeFixer = `#!/bin/sh
for a in "$@"; do f="$a"; done
name=$(basename "$f" .php)
dir=$(basename "$(dirname "$f")")
sed "s/^final class [A-Za-z0-9_]*/final class $name/" "$f" > "$f.out" && mv "$f.out" "$f"
echo "// in $dir" >> "$f"
if [ -f fail ]; then echo broken >&2; exit 4; fi
`

// noCopies fails if a temporary copy of a formatter is left in dir.
func noCopies(t *testing.T, dir string) {
	t.Helper()
	_ = filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err == nil && (strings.Contains(d.Name(), ".tiegel-") || strings.HasPrefix(d.Name(), "__tiegel_")) {
			t.Errorf("temporary copy left: %s", p)
		}
		return nil
	})
}

// The formatter sees the file under its real name and directory, so
// file-name dependent rules keep the class name (review wave 7, #1).
func TestFormatKeepsFileName(t *testing.T) {
	a, dir := newApp(t)
	src := "<?php\nnamespace App\\Service;\n\nfinal class UserService\n{\n}\n"
	modtest.WriteFile(t, filepath.Join(dir, "src", "Service", "UserService.php"), src)
	modtest.WriteFile(t, filepath.Join(dir, "vendor", "bin", "php-cs-fixer"), fakeFixer)
	if err := os.Chmod(filepath.Join(dir, "vendor", "bin", "php-cs-fixer"), 0o755); err != nil {
		t.Fatal(err)
	}
	var r api.QAFormatResult
	modtest.Call(t, a, "qa.format", api.QAFormatParams{Tool: PHPCSFixerID, Path: "src/Service/UserService.php", Apply: new(bool)}, &r)
	if want := src + "// in Service\n"; r.Text != want {
		t.Fatalf("formatted %q, want %q", r.Text, want)
	}
	noCopies(t, dir)
	// A failing run leaves nothing behind either.
	modtest.WriteFile(t, filepath.Join(dir, "fail"), "")
	modtest.WantCode(t, a, "qa.format", api.QAFormatParams{Tool: PHPCSFixerID, Path: "src/Service/UserService.php"}, api.CodeFailed)
	noCopies(t, dir)
}

// Format on save runs under the plugin's timeout, not the hooks' default
// of 2 s (review wave 7, #2).
func TestFormatOnSaveSlowTool(t *testing.T) {
	a, dir := newApp(t)
	modtest.WriteFile(t, filepath.Join(dir, "vendor", "bin", "pint"), "#!/bin/sh\nsleep 3\n"+strings.TrimPrefix(fakePint, "#!/bin/sh\n"))
	_ = os.Chmod(filepath.Join(dir, "vendor", "bin", "pint"), 0o755)
	set(t, a, PintID+".formatOnSave", true)
	modtest.Call(t, a, "doc.open", api.DocOpenParams{Path: "a.php"}, nil)
	modtest.Call(t, a, "doc.insert", map[string]any{"path": "a.php", "at": map[string]int{"offset": 0}, "text": "\n"}, nil)
	modtest.Call(t, a, "doc.save", api.DocSaveParams{Path: "a.php"}, nil)
	if b, _ := os.ReadFile(filepath.Join(dir, "a.php")); string(b) != "\n<?php\n$x = [1, 2];\n" {
		t.Fatalf("saved %q", b)
	}
}
