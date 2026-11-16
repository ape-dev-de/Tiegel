package phpruntime

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/ape-dev-de/tiegel/internal/api"
	"github.com/ape-dev-de/tiegel/internal/core/bus"
	"github.com/ape-dev-de/tiegel/internal/core/module"
	"github.com/ape-dev-de/tiegel/internal/core/settings"
	"github.com/ape-dev-de/tiegel/internal/interp"
	"github.com/ape-dev-de/tiegel/internal/modules/modtest"
)

func newApp(t *testing.T, dirs []string, opts Options) (*api.App, *Module) {
	t.Helper()
	m := New(opts)
	a := modtest.NewApp(t, []module.Module{m}, func(o *api.Options) {
		if dirs != nil {
			o.Interp.Dirs = dirs
		}
	})
	return a, m
}

func TestInterpreters(t *testing.T) {
	bin := t.TempDir()
	modtest.FakePHP(t, bin, "php", "8.3.4", "xdebug.mode=debug\nxdebug.client_port=9003\n")
	other := modtest.FakePHP(t, t.TempDir(), "php8.2", "8.2.1", "")
	a, m := newApp(t, []string{bin}, Options{})
	rec := modtest.Record(t, a, "*")

	modtest.WantCode(t, a, "interp.version", InterpVersionParams{}, api.CodeInvalidParams)
	modtest.WantCode(t, a, "interp.select", InterpSelectParams{}, api.CodeInvalidParams)
	modtest.WantCode(t, a, "interp.select", InterpSelectParams{ID: "nope"}, api.CodeNotFound)

	var list []InterpreterInfo
	modtest.Call(t, a, "interp.list", nil, &list)
	if len(list) != 1 || list[0].Version != "8.3.4" || list[0].Kind != "local" || list[0].Selected {
		t.Fatalf("list %+v", list)
	}
	id := list[0].ID
	var sel InterpreterInfo
	modtest.Call(t, a, "interp.select", InterpSelectParams{ID: id}, &sel)
	if !sel.Selected || sel.ID != id {
		t.Fatalf("select %+v", sel)
	}
	ev := rec.Wait(t, api.TopicInterpreterChanged, nil).Payload.(api.InterpreterChangedEvent)
	if ev.ID != id || ev.Previous != "" || ev.Kind != "local" {
		t.Fatalf("event %+v", ev)
	}
	rec.Wait(t, TopicInterpSelected, nil)
	modtest.Call(t, a, "interp.list", nil, &list)
	if !list[0].Selected {
		t.Fatal("selection not persisted")
	}
	var v InterpVersionResult
	modtest.Call(t, a, "interp.version", InterpVersionParams{Modules: true, Ini: true}, &v)
	if v.Version != "8.3.4" || len(v.Modules) != 3 || v.Ini["xdebug.mode"] != "debug" {
		t.Fatalf("version %+v", v)
	}
	modtest.Call(t, a, "interp.version", InterpVersionParams{ID: "local:" + other}, &v)
	if v.Version != "8.2.1" {
		t.Fatalf("ad-hoc local %+v", v)
	}
	modtest.WantCode(t, a, "interp.version", InterpVersionParams{ID: "local:" + filepath.Join(bin, "missing")}, api.CodeNotFound)
	broken := filepath.Join(t.TempDir(), "php")
	if err := os.WriteFile(broken, []byte("#!/bin/sh\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	modtest.WantCode(t, a, "interp.version", InterpVersionParams{ID: "local:" + broken}, api.CodeUnavailable)
	modtest.WantCode(t, a, "interp.version", InterpVersionParams{ID: "local:" + broken, Modules: true}, api.CodeUnavailable)

	root := modtest.Project(t, a, map[string]string{
		"compose.yaml": "services:\n  app:\n    image: php:8.3-fpm\n    volumes:\n      - ./:/var/www/html\n  db:\n    image: mariadb\n",
	})
	modtest.Call(t, a, "interp.list", nil, &list)
	if len(list) != 2 || list[1].Kind != "compose" || list[1].Service != "app" || list[1].Score <= 0 || list[0].Selected {
		t.Fatalf("project list %+v", list)
	}
	modtest.Call(t, a, "interp.select", InterpSelectParams{ID: list[1].ID}, &sel)
	var info api.ProjectInfo
	modtest.Call(t, a, "project.info", nil, &info)
	if info.Interpreter != list[1].ID || info.Root != root {
		t.Fatalf("project interpreter %+v", info)
	}
	noCompose := false
	modtest.Call(t, a, "interp.list", InterpListParams{Compose: &noCompose}, &list)
	if len(list) != 1 {
		t.Fatalf("compose disabled %+v", list)
	}
	// Selecting from an empty cache lists first.
	m.mu.Lock()
	m.interps = map[string]interp.Interpreter{}
	m.mu.Unlock()
	modtest.Call(t, a, "interp.select", InterpSelectParams{ID: id}, &sel)
}

func TestComposeDetect(t *testing.T) {
	a, _ := newApp(t, nil, Options{})
	modtest.WantCode(t, a, "compose.detect", nil, api.CodeNoProject)
	var res ComposeResult
	sail, _ := filepath.Abs("../../compose/testdata/sail")
	modtest.Call(t, a, "compose.detect", ComposeParams{Dir: sail}, &res)
	if !res.Found || len(res.Candidates) == 0 || res.Candidates[0].Service != "laravel.test" || len(res.Candidates[0].Mappings) == 0 {
		t.Fatalf("sail %+v", res)
	}
	modtest.Project(t, a, map[string]string{"README.md": "x"})
	modtest.Call(t, a, "compose.detect", nil, &res)
	if res.Found || res.Candidates == nil {
		t.Fatalf("no compose %+v", res)
	}
	root := modtest.Project(t, a, map[string]string{"compose.yaml": "services: [broken"})
	modtest.WantCode(t, a, "compose.detect", ComposeParams{Dir: root}, api.CodeFailed)
	modtest.Project(t, a, map[string]string{"compose.yaml": "services:\n  php:\n    image: php:8.3-fpm\n"})
	modtest.Call(t, a, "compose.detect", nil, &res)
	if len(res.Candidates) != 1 || len(res.Candidates[0].XdebugHints) == 0 || res.Candidates[0].XdebugHints[0].Code != "no-host-gateway" {
		t.Fatalf("expected xdebug hints on linux: %+v", res)
	}
}

func TestPHPVersionOverrideAndHook(t *testing.T) {
	a, _ := newApp(t, nil, Options{})
	modtest.WantCode(t, a, "php.version", PHPVersionParams{NoInterpreter: true}, api.CodeNotFound)
	// Without a version the hook leaves the query empty.
	if q, _ := api.HookPHPVersion.Apply(context.Background(), a.Hooks(), api.PHPVersionQuery{}); q.Version != "" {
		t.Fatalf("hook %+v", q)
	}
	modtest.Project(t, a, map[string]string{"composer.json": `{"require":{"php":"^8.0"}}`})
	var pv PHPVersionResult
	modtest.Call(t, a, "php.version", nil, &pv)
	if pv.Short != "8.0" || pv.Source != "composer require.php" {
		t.Fatalf("version %+v", pv)
	}
	modtest.Call(t, a, "php.version", PHPVersionParams{Override: "8.3"}, &pv)
	if pv.Version != "8.3.0" || pv.Source != "override" {
		t.Fatalf("override %+v", pv)
	}
	modtest.WantCode(t, a, "php.version", PHPVersionParams{Override: "x"}, api.CodeInvalidParams)
	modtest.WantCode(t, a, "php.version", PHPVersionParams{Interpreter: "nope"}, api.CodeNotFound)
	// The stored override (php.setVersionOverride) applies.
	if err := a.SettingsStore().Delete(SettingVersionOverride); err != nil {
		t.Fatal(err)
	}
	if err := setString(a, SettingVersionOverride, "8.2"); err != nil {
		t.Fatal(err)
	}
	q, err := api.HookPHPVersion.Apply(context.Background(), a.Hooks(), api.PHPVersionQuery{})
	if err != nil || q.Short != "8.2" || q.Source != "override" {
		t.Fatalf("hook %+v %v", q, err)
	}
	// A filled query is left alone.
	if q, _ := api.HookPHPVersion.Apply(context.Background(), a.Hooks(), api.PHPVersionQuery{Version: "7.4.0"}); q.Version != "7.4.0" {
		t.Fatalf("hook overwrote %+v", q)
	}
}

func TestPHPVersionFromInterpreter(t *testing.T) {
	bin := t.TempDir()
	modtest.FakePHP(t, bin, "php", "8.1.2", "")
	a, _ := newApp(t, []string{bin}, Options{})
	var pv PHPVersionResult
	modtest.Call(t, a, "php.version", nil, &pv) // first local binary
	if pv.Version != "8.1.2" || pv.Source != "interpreter" {
		t.Fatalf("version %+v", pv)
	}
}

func setString(a *api.App, key, v string) error {
	return settings.Set(a.SettingsStore(), key, v)
}

// fakeDocker writes a docker stand-in that answers the php queries of a
// compose interpreter (exec mode).
func fakeDocker(t *testing.T, version, ini string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "docker")
	script := `#!/bin/sh
for a in "$@"; do
  case "$a" in
    -m) printf '[PHP Modules]\nCore\nxdebug\n'; exit 0 ;;
    *PHP_VERSION*) printf '` + version + `'; exit 0 ;;
    *ini_get*) printf '` + strings.ReplaceAll(ini, "\n", `\n`) + `'; exit 0 ;;
  esac
done
exit 2
`
	if err := os.WriteFile(p, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestMappingsExecModeAndDiagnose(t *testing.T) {
	docker := fakeDocker(t, "8.2.7", "xdebug.mode=develop\nxdebug.client_host=localhost\nxdebug.client_port=9000\nxdebug.start_with_request=no\n")
	a, _ := newApp(t, nil, Options{Docker: docker})
	rec := modtest.Record(t, a, api.TopicMappingsChanged)

	var mr MappingsResult
	modtest.Call(t, a, "runtime.mappings.get", nil, &mr)
	if mr.Source != "none" || len(mr.Mappings) != 0 {
		t.Fatalf("no project %+v", mr)
	}
	var d Diagnosis
	modtest.Call(t, a, "runtime.diagnose", nil, &d)
	if len(d.Hints) != 1 || d.Hints[0].Code != HintNoInterpreter {
		t.Fatalf("no interpreter %+v", d)
	}

	root := modtest.Project(t, a, map[string]string{
		"compose.yaml": "services:\n  app:\n    image: php:8.3-fpm\n    volumes:\n      - ./:/var/www/html\n",
	})
	modtest.Call(t, a, "runtime.mappings.get", nil, &mr)
	if mr.Source != "compose" || mr.Service != "app" || len(mr.Mappings) != 1 || mr.Mappings[0].Remote != "/var/www/html" || mr.Mappings[0].Host != root {
		t.Fatalf("compose mappings %+v", mr)
	}
	modtest.Call(t, a, "runtime.mappings.set", MappingsSetParams{Mappings: []api.MappingInfo{{Host: "src", Remote: "/app/src"}}}, &mr)
	if mr.Source != "settings" || mr.Mappings[0].Host != filepath.Join(root, "src") {
		t.Fatalf("set %+v", mr)
	}
	ev := rec.Wait(t, api.TopicMappingsChanged, nil).Payload.(api.MappingsChangedEvent)
	if ev.Source != "settings" {
		t.Fatalf("event %+v", ev)
	}
	q, _ := api.HookPathMappings.Apply(context.Background(), a.Hooks(), api.PathMappingsQuery{})
	if q.Source != "settings" || len(q.Mappings) != 1 {
		t.Fatalf("hook %+v", q)
	}
	if q, _ := api.HookPathMappings.Apply(context.Background(), a.Hooks(), api.PathMappingsQuery{Mappings: []api.MappingInfo{{Host: "/h", Remote: "/r"}}}); q.Mappings[0].Host != "/h" {
		t.Fatal("hook overwrote explicit mappings")
	}
	modtest.WantCode(t, a, "runtime.mappings.set", MappingsSetParams{Mappings: []api.MappingInfo{{Host: "x"}}}, api.CodeInvalidParams)
	modtest.Call(t, a, "runtime.mappings.set", MappingsSetParams{Reset: true}, &mr)
	if mr.Source != "compose" {
		t.Fatalf("reset %+v", mr)
	}

	var em ExecModeResult
	modtest.Call(t, a, "runtime.execMode", nil, &em)
	if em.Mode != "auto" {
		t.Fatalf("default mode %+v", em)
	}
	modtest.WantCode(t, a, "runtime.execMode", ExecModeParams{Mode: "nope"}, api.CodeInvalidParams)
	modtest.Call(t, a, "runtime.execMode", ExecModeParams{Mode: "exec"}, &em)
	if em.Mode != "exec" {
		t.Fatalf("mode %+v", em)
	}

	var list []InterpreterInfo
	modtest.Call(t, a, "interp.list", nil, &list)
	modtest.Call(t, a, "interp.select", InterpSelectParams{ID: list[0].ID}, nil)
	// The version comes from the container (ComposeRunner / exec mode).
	var pv PHPVersionResult
	modtest.Call(t, a, "php.version", nil, &pv)
	if pv.Version != "8.2.7" || pv.Source != "interpreter" {
		t.Fatalf("container version %+v", pv)
	}
	modtest.Call(t, a, "runtime.diagnose", nil, &d)
	codes := map[string]bool{}
	for _, h := range d.Hints {
		codes[h.Code] = true
	}
	for _, want := range []string{"no-host-gateway", HintXdebugNoDebug, HintClientPort, HintClientHostLocal, HintStartWithRequest} {
		if !codes[want] {
			t.Errorf("missing hint %s in %+v", want, d.Hints)
		}
	}
	if d.Version != "8.2.7" || d.Ini["xdebug.client_port"] != "9000" || d.Interpreter.Service != "app" || d.ExecMode != "exec" {
		t.Fatalf("diagnosis %+v", d)
	}
	modtest.WantCode(t, a, "runtime.diagnose", DiagnoseParams{Interpreter: "nope"}, api.CodeNotFound)
}

func TestDiagnoseLocal(t *testing.T) {
	bin := t.TempDir()
	modtest.FakePHP(t, bin, "php", "8.3.0", "") // no xdebug ini values
	broken := filepath.Join(t.TempDir(), "php")
	if err := os.WriteFile(broken, []byte("#!/bin/sh\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	a, _ := newApp(t, []string{bin}, Options{})
	var list []InterpreterInfo
	modtest.Call(t, a, "interp.list", nil, &list)
	var d Diagnosis
	modtest.Call(t, a, "runtime.diagnose", DiagnoseParams{Interpreter: list[0].ID}, &d)
	if len(d.Hints) != 1 || d.Hints[0].Code != HintXdebugMissing {
		t.Fatalf("local %+v", d)
	}
	modtest.Call(t, a, "runtime.diagnose", DiagnoseParams{Interpreter: "local:" + broken}, &d)
	if len(d.Hints) != 1 || d.Hints[0].Code != HintInterpreterError {
		t.Fatalf("broken %+v", d)
	}
	if hs := iniHints(map[string]string{"xdebug.mode": "debug", "xdebug.client_port": "9003"}, interp.KindLocal); len(hs) != 0 {
		t.Fatalf("good setup hints %+v", hs)
	}
}

func TestModuleDisabledAndReset(t *testing.T) {
	a, m := newApp(t, nil, Options{})
	if m.ID() != ID || m.DependsOn() != nil || len(m.Claims()) == 0 {
		t.Fatal("identity")
	}
	modtest.Call(t, a, "modules.disable", api.ModuleParams{ID: ID}, nil)
	modtest.WantCode(t, a, "interp.list", nil, api.CodeModuleDisabled)
	modtest.WantCode(t, a, "php.version", nil, api.CodeModuleDisabled)
	if q, _ := api.HookPHPVersion.Apply(context.Background(), a.Hooks(), api.PHPVersionQuery{}); q.Version != "" {
		t.Fatal("hook still active")
	}
	modtest.Call(t, a, "modules.enable", api.ModuleParams{ID: ID}, nil)
	modtest.Call(t, a, "interp.list", nil, nil)
	// project.opened resets the interpreter cache.
	m.mu.Lock()
	m.interps["x"] = nil
	m.mu.Unlock()
	modtest.Project(t, a, nil)
	modtest.Eventually(t, "cache reset", func() bool {
		m.mu.Lock()
		defer m.mu.Unlock()
		_, ok := m.interps["x"]
		return !ok
	})
	if err := m.Start(fakeCtx{}); err == nil {
		t.Fatal("start without App context")
	}
}

type fakeCtx struct{ module.Context }

func (fakeCtx) ModuleID() string { return ID }
func (fakeCtx) Bus() *bus.Bus    { return nil }

// stoppedDocker writes a docker stand-in for a compose project whose
// service is stopped: ps lists nothing, exec fails, run would start a
// container and answer. Every invocation is appended to the returned log.
func stoppedDocker(t *testing.T) (docker, log string) {
	t.Helper()
	dir := t.TempDir()
	docker, log = filepath.Join(dir, "docker"), filepath.Join(dir, "calls.log")
	script := `#!/bin/sh
echo "$*" >> '` + log + `'
mode=""
for a in "$@"; do
  case "$a" in
    ps) exit 0 ;;
    exec) echo 'service "app" is not running' >&2; exit 1 ;;
    run) mode=run ;;
  esac
  if [ "$mode" = run ]; then
    case "$a" in *PHP_VERSION*) printf '8.2.7'; exit 0 ;; esac
  fi
done
exit 2
`
	if err := os.WriteFile(docker, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return docker, log
}

func startedContainer(t *testing.T, log string) bool {
	t.Helper()
	data, _ := os.ReadFile(log)
	for _, l := range strings.Split(string(data), "\n") {
		for _, f := range strings.Fields(l) {
			if f == "run" || f == "up" || f == "start" || f == "pull" {
				return true
			}
		}
	}
	return false
}

// Review 2026-11-16 #7 (privacy): automatic version probes (php.version at
// project open, the php.version hook, runtime.diagnose) ran `docker compose
// run` on a stopped service, which can make Docker pull an image without a
// user action. They now only use exec and fall back to composer.json with
// the hint container-not-running.
func TestAutomaticProbesNeverStartContainers(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell script stand-in")
	}
	docker, log := stoppedDocker(t)
	a, _ := newApp(t, nil, Options{Docker: docker})
	modtest.Project(t, a, map[string]string{
		"compose.yaml":  "services:\n  app:\n    image: php:8.3-fpm\n    volumes:\n      - ./:/var/www/html\n",
		"composer.json": `{"require":{"php":"^8.1"}}`,
	})
	var list []InterpreterInfo
	modtest.Call(t, a, "interp.list", nil, &list)
	var id string
	for _, i := range list {
		if i.Kind == "compose" {
			id = i.ID
		}
	}
	if id == "" {
		t.Fatalf("no compose interpreter: %+v", list)
	}
	modtest.Call(t, a, "interp.select", InterpSelectParams{ID: id}, nil)

	var pv PHPVersionResult
	modtest.Call(t, a, "php.version", nil, &pv)
	if startedContainer(t, log) {
		t.Fatal("php.version started a container")
	}
	if pv.Short != "8.1" || pv.Source != "composer require.php" || pv.Hint == nil || pv.Hint.Code != HintContainerNotRunning {
		t.Fatalf("fallback %+v (hint %+v)", pv, pv.Hint)
	}
	if q, _ := api.HookPHPVersion.Apply(context.Background(), a.Hooks(), api.PHPVersionQuery{}); q.Short != "8.1" {
		t.Fatalf("hook %+v", q)
	}
	var d Diagnosis
	modtest.Call(t, a, "runtime.diagnose", nil, &d)
	if startedContainer(t, log) {
		t.Fatal("hook or diagnose started a container")
	}
	if len(d.Hints) == 0 || d.Hints[len(d.Hints)-1].Code != HintContainerNotRunning {
		t.Fatalf("diagnose hints %+v", d.Hints)
	}

	// An explicit user action may start it.
	modtest.Call(t, a, "php.version", PHPVersionParams{Start: true}, &pv)
	if pv.Version != "8.2.7" || pv.Source != "interpreter" || pv.Hint != nil || !startedContainer(t, log) {
		t.Fatalf("explicit start %+v", pv)
	}
}
