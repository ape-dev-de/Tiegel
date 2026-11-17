package phpruntime

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ape-dev-de/tiegel/internal/api"
	"github.com/ape-dev-de/tiegel/internal/core/module"
	"github.com/ape-dev-de/tiegel/internal/core/settings"
	"github.com/ape-dev-de/tiegel/internal/docker/dockertest"
	dockermod "github.com/ape-dev-de/tiegel/internal/modules/docker"
	"github.com/ape-dev-de/tiegel/internal/modules/modtest"
)

func TestMain(m *testing.M) {
	dockertest.Main()
	os.Exit(m.Run())
}

// newDockerApp starts php-runtime together with the docker module, both
// using the fake docker.
func newDockerApp(t *testing.T, f *dockertest.Fake, cfg string) *api.App {
	t.Helper()
	mods := []module.Module{New(Options{}), dockermod.New(dockermod.Options{Binary: f.Binary, Env: f.Env, NoLoginShell: true})}
	return modtest.NewApp(t, mods, func(o *api.Options) {
		o.Interp.Dirs = []string{}
		if cfg != "" {
			o.ConfigDir = cfg
		}
	})
}

const composeApp = "services:\n  app:\n    image: php:8.3-fpm\n    volumes:\n      - ./:/var/www/html\n"

func TestSettingSpecsPHPRuntime(t *testing.T) {
	specs := New(Options{}).SettingSpecs()
	if len(specs) != 3 {
		t.Fatalf("specs %+v", specs)
	}
	for _, s := range specs {
		if err := s.Validate(); err != nil || !strings.HasPrefix(s.Key, ID+".") {
			t.Errorf("%s: %v", s.Key, err)
		}
	}
	var _ module.SettingsProvider = New(Options{})
}

func TestComposeUsesDockerContext(t *testing.T) {
	t.Parallel()
	f := dockertest.New(t)
	f.Write(t, "ps.json", `{"Name":"p-app-1","Service":"app","State":"running"}`+"\n")
	a := newDockerApp(t, f, "")
	modtest.Project(t, a, map[string]string{"compose.yaml": composeApp})
	modtest.Call(t, a, "docker.setContext", dockermod.SetContextParams{Name: "desktop-linux"}, nil)

	var list []InterpreterInfo
	modtest.Call(t, a, "interp.list", InterpListParams{Status: true}, &list)
	if len(list) != 1 || list[0].Kind != "compose" || list[0].Context != "desktop-linux" || list[0].State != "running" || list[0].Image != "php:8.3-fpm" {
		t.Fatalf("list %+v", list)
	}
	id := list[0].ID
	var v InterpVersionResult
	modtest.Call(t, a, "interp.version", InterpVersionParams{ID: id}, &v)
	if v.Version != "8.3.1" || !f.Called("--context desktop-linux compose", "exec -T app php") {
		t.Fatalf("version %+v calls %v", v, f.Calls())
	}
	var st StatusResult
	modtest.Call(t, a, "interp.status", StatusParams{ID: id}, &st)
	if st.State != "running" || st.Context != "desktop-linux" || st.Service != "app" || len(st.Containers) != 1 {
		t.Fatalf("status %+v", st)
	}
	// Switching the context rebuilds the interpreters.
	modtest.Call(t, a, "docker.setContext", dockermod.SetContextParams{Name: "orbstack"}, nil)
	modtest.Eventually(t, "context applied", func() bool {
		var l []InterpreterInfo
		_ = modtest.TryCall(a, "interp.list", nil, &l)
		return len(l) == 1 && l[0].Context == "orbstack"
	})
	f.Write(t, "ps.json", "broken")
	modtest.Call(t, a, "interp.status", StatusParams{ID: id}, &st)
	if st.State != "error" || st.Error == "" {
		t.Fatalf("status error %+v", st)
	}
	modtest.Call(t, a, "interp.list", InterpListParams{Status: true}, &list)
	if list[0].State != "error" {
		t.Fatalf("list state %+v", list)
	}
}

func TestImageInterpreterAndProbe(t *testing.T) {
	t.Parallel()
	f := dockertest.New(t)
	cfg := t.TempDir()
	a := newDockerApp(t, f, cfg)
	root := modtest.Project(t, a, map[string]string{"composer.json": `{"require":{"php":"^8.1"}}`})

	modtest.WantCode(t, a, "interp.addImage", ImageParams{}, api.CodeInvalidParams)
	modtest.WantCode(t, a, "interp.addImage", ImageParams{Image: "php", Remote: "relative"}, api.CodeInvalidParams)
	modtest.WantCode(t, a, "interp.addImage", ImageParams{Image: "php=/x"}, api.CodeInvalidParams)
	var info InterpreterInfo
	modtest.Call(t, a, "interp.addImage", ImageParams{Image: "php:8.3-cli"}, &info)
	if info.ID != "docker:php:8.3-cli" || info.Kind != "docker" || info.Remote != "/app" {
		t.Fatalf("add %+v", info)
	}
	modtest.Call(t, a, "interp.addImage", ImageParams{Image: "php:8.2-cli", Remote: "/srv/app"}, nil)
	modtest.Call(t, a, "interp.addImage", ImageParams{Image: "php:8.3-cli", Remote: "/var/www"}, &info) // update
	if got := settings.Get(a.ProjectSettings(), SettingImages, []string(nil)); len(got) != 2 || got[0] != "php:8.3-cli=/var/www" || got[1] != "php:8.2-cli=/srv/app" {
		t.Fatalf("stored %v", got)
	}
	var list []InterpreterInfo
	modtest.Call(t, a, "interp.list", nil, &list)
	if len(list) != 2 || list[0].Image != "php:8.3-cli" || list[0].Remote != "/var/www" || list[0].Context != "" {
		t.Fatalf("list %+v", list)
	}
	modtest.Call(t, a, "interp.select", InterpSelectParams{ID: info.ID}, nil)

	var ms MappingsResult
	modtest.Call(t, a, "runtime.mappings.get", nil, &ms)
	if ms.Source != "docker" || ms.Image != "php:8.3-cli" || len(ms.Mappings) != 1 || ms.Mappings[0].Host != root || ms.Mappings[0].Remote != "/var/www" {
		t.Fatalf("mappings %+v", ms)
	}

	// Automatic queries never pull: the image is missing, so php.version
	// falls back to composer.json.
	var pv PHPVersionResult
	modtest.Call(t, a, "php.version", nil, &pv)
	if pv.Source == "interpreter" || f.Called("run --rm -i -v") && !f.Called("--pull never") {
		t.Fatalf("pulled automatically: %+v %v", pv, f.Calls())
	}
	for _, c := range f.Calls() {
		if strings.HasPrefix(c, "run ") && !strings.Contains(c, "--pull never") {
			t.Fatalf("automatic run without --pull never: %s", c)
		}
	}
	var d Diagnosis
	modtest.Call(t, a, "runtime.diagnose", nil, &d)
	if d.Interpreter == nil || d.Interpreter.Image != "php:8.3-cli" || len(d.Hints) == 0 || d.Hints[len(d.Hints)-1].Code != HintImageNotPulled {
		t.Fatalf("diagnose %+v", d)
	}
	modtest.WantCode(t, a, "interp.probe", ProbeParams{CachedOnly: true}, api.CodeNotFound)
	err := modtest.WantCode(t, a, "interp.probe", ProbeParams{}, api.CodeUnavailable)
	if !strings.Contains(err.Error(), "not pulled automatically") {
		t.Fatalf("probe hint %v", err)
	}
	var st StatusResult
	modtest.Call(t, a, "interp.status", nil, &st)
	if st.ImagePresent == nil || *st.ImagePresent || st.Image != "php:8.3-cli" {
		t.Fatalf("status %+v", st)
	}

	// An explicit start may pull.
	var pr ProbeResult
	modtest.Call(t, a, "interp.probe", ProbeParams{Start: true}, &pr)
	if pr.Cached || pr.Info.Version != "8.3.1" || pr.Info.Xdebug != "3.3.2" || pr.Kind != "docker" || pr.ProbedAt.IsZero() {
		t.Fatalf("probe %+v", pr)
	}
	modtest.Call(t, a, "interp.probe", nil, &pr)
	if !pr.Cached || pr.Info.Version != "8.3.1" {
		t.Fatalf("cached %+v", pr)
	}
	f.Write(t, "images", "php:8.3-cli\n")
	f.Write(t, "php-version", "8.3.9")
	modtest.Call(t, a, "interp.probe", ProbeParams{Refresh: true}, &pr)
	if pr.Cached || pr.Info.Version != "8.3.9" {
		t.Fatalf("refresh %+v", pr)
	}
	modtest.Call(t, a, "interp.list", nil, &list)
	if list[0].Version != "8.3.9" {
		t.Fatalf("version from cache %+v", list[0])
	}
	if _, err := os.Stat(filepath.Join(cfg, "cache", "interp-probe.json")); err != nil {
		t.Fatal(err)
	}
	modtest.Call(t, a, "php.version", nil, &pv)
	if pv.Source != "interpreter" || pv.Version != "8.3.9" {
		t.Fatalf("version %+v", pv)
	}

	// The cache survives a restart.
	b := newDockerApp(t, f, cfg)
	modtest.Call(t, b, "interp.probe", ProbeParams{ID: "docker:php:8.3-cli", CachedOnly: true}, &pr)
	if !pr.Cached || pr.Info.Version != "8.3.9" {
		t.Fatalf("persisted %+v", pr)
	}

	modtest.WantCode(t, a, "interp.removeImage", ImageParams{Image: "nope"}, api.CodeNotFound)
	modtest.Call(t, a, "interp.removeImage", ImageParams{Image: "php:8.2-cli"}, nil)
	modtest.Call(t, a, "interp.list", nil, &list)
	if len(list) != 1 {
		t.Fatalf("after remove %+v", list)
	}
	// An ad-hoc image ID works without configuration.
	modtest.Call(t, a, "interp.version", InterpVersionParams{ID: "docker:php:8.3-cli"}, nil)
}

func TestProbeLocalAndErrors(t *testing.T) {
	bin := t.TempDir()
	modtest.FakePHP(t, bin, "php", "8.3.4", "")
	a, _ := newApp(t, []string{bin}, Options{})
	modtest.WantCode(t, a, "interp.probe", nil, api.CodeInvalidParams)
	modtest.WantCode(t, a, "interp.status", nil, api.CodeInvalidParams)
	modtest.WantCode(t, a, "interp.status", StatusParams{ID: "nope"}, api.CodeNotFound)
	modtest.WantCode(t, a, "interp.probe", ProbeParams{ID: "nope"}, api.CodeNotFound)
	var list []InterpreterInfo
	modtest.Call(t, a, "interp.list", nil, &list)
	// The fake php does not know the probe script.
	modtest.WantCode(t, a, "interp.probe", ProbeParams{ID: list[0].ID}, api.CodeUnavailable)
	var st StatusResult
	modtest.Call(t, a, "interp.status", StatusParams{ID: list[0].ID}, &st)
	if st.Kind != "local" || st.State != "" {
		t.Fatalf("local status %+v", st)
	}
}

func TestComposeProbeStopped(t *testing.T) {
	t.Parallel()
	f := dockertest.New(t)
	f.Write(t, "ps.json", `{"Name":"p-app-1","Service":"app","State":"exited"}`+"\n")
	a := newDockerApp(t, f, "")
	modtest.Project(t, a, map[string]string{"compose.yaml": composeApp})
	var list []InterpreterInfo
	modtest.Call(t, a, "interp.list", nil, &list)
	modtest.Call(t, a, "interp.select", InterpSelectParams{ID: list[0].ID}, nil)
	// Automatic probes of a compose interpreter are exec-only (the fake
	// answers exec regardless of the state); `run` would start a container.
	var pr ProbeResult
	modtest.Call(t, a, "interp.probe", nil, &pr)
	if f.Called("compose", " run ") {
		t.Fatalf("automatic probe used run: %v", f.Calls())
	}
	var st StatusResult
	modtest.Call(t, a, "interp.status", nil, &st)
	if st.State != "stopped" {
		t.Fatalf("status %+v", st)
	}
}

func TestLegacySettingKeys(t *testing.T) {
	bin := t.TempDir()
	modtest.FakePHP(t, bin, "php", "8.3.4", "")
	a, _ := newApp(t, []string{bin}, Options{})
	modtest.Project(t, a, map[string]string{"composer.json": "{}"})
	ps := a.ProjectSettings()
	var list []InterpreterInfo
	modtest.Call(t, a, "interp.list", nil, &list)
	id := list[0].ID
	for k, v := range map[string]any{api.SettingInterpreterLegacy: id, legacyExecMode: "run", legacyMappings: []api.MappingInfo{{Host: "/h", Remote: "/r"}}} {
		if err := settings.Set(ps, k, v); err != nil {
			t.Fatal(err)
		}
	}
	modtest.Call(t, a, "interp.list", nil, &list)
	if !list[0].Selected {
		t.Fatal("legacy interpreter key not read")
	}
	var em ExecModeResult
	modtest.Call(t, a, "runtime.execMode", nil, &em)
	if em.Mode != "run" {
		t.Fatalf("legacy exec mode %+v", em)
	}
	var ms MappingsResult
	modtest.Call(t, a, "runtime.mappings.get", nil, &ms)
	if ms.Source != "settings" || ms.Mappings[0].Remote != "/r" {
		t.Fatalf("legacy mappings %+v", ms)
	}
	var pi api.ProjectInfo
	modtest.Call(t, a, "project.info", nil, &pi)
	if pi.Interpreter != id {
		t.Fatalf("project.info %+v", pi)
	}
	// Writing migrates to the new keys.
	modtest.Call(t, a, "interp.select", InterpSelectParams{ID: id}, nil)
	modtest.Call(t, a, "runtime.execMode", ExecModeParams{Mode: "exec"}, nil)
	modtest.Call(t, a, "runtime.mappings.set", MappingsSetParams{Mappings: []api.MappingInfo{{Host: "/h", Remote: "/r2"}}}, nil)
	for _, k := range []string{api.SettingInterpreterLegacy, legacyExecMode, legacyMappings} {
		if ps.Has(k) {
			t.Errorf("legacy key %s kept", k)
		}
	}
	for _, k := range []string{SettingInterpreter, SettingExecMode, SettingMappings} {
		if !ps.Has(k) {
			t.Errorf("new key %s missing", k)
		}
	}
	modtest.Call(t, a, "runtime.mappings.set", MappingsSetParams{Reset: true}, &ms)
	if ms.Source == "settings" {
		t.Fatalf("reset %+v", ms)
	}
	// Changing settings from outside (dialog) is picked up.
	if err := settings.Set(ps, SettingImages, []string{"php:8.4-cli", "bad image"}); err != nil {
		t.Fatal(err)
	}
	modtest.Eventually(t, "image listed", func() bool {
		var l []InterpreterInfo
		_ = modtest.TryCall(a, "interp.list", nil, &l)
		return len(l) == 2 && l[1].Kind == "docker"
	})
}
