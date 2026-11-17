package phpruntime

import (
	"context"
	"strings"
	"testing"

	"github.com/ape-dev-de/tiegel/internal/api"
	"github.com/ape-dev-de/tiegel/internal/docker/dockertest"
	"github.com/ape-dev-de/tiegel/internal/modules/modtest"
)

// containerRuns counts the logged `docker run` invocations of f.
func containerRuns(f *dockertest.Fake) int {
	n := 0
	for _, c := range f.Calls() {
		if strings.HasPrefix(c, "run ") || strings.Contains(c, " run ") {
			n++
		}
	}
	return n
}

// TestAutomaticQueriesStartNoImageContainer: for a Docker image
// interpreter, automatic queries (version hook, php.version,
// runtime.diagnose, interp.list/status, interp.probe without refresh)
// never `docker run` – not even with --pull never (D250). Only an explicit
// re-read does; afterwards the automatic queries answer from the cache.
func TestAutomaticQueriesStartNoImageContainer(t *testing.T) {
	t.Parallel()
	f := dockertest.New(t)
	f.Write(t, "images", "php:8.3-cli\n") // present locally: --pull never would succeed
	a := newDockerApp(t, f, t.TempDir())
	modtest.Project(t, a, map[string]string{"composer.json": `{"require":{"php":"^8.1"}}`})
	var info InterpreterInfo
	modtest.Call(t, a, "interp.addImage", ImageParams{Image: "php:8.3-cli"}, &info)
	modtest.Call(t, a, "interp.select", InterpSelectParams{ID: info.ID}, nil)

	var pv PHPVersionResult
	modtest.Call(t, a, "php.version", nil, &pv)
	if pv.Source == "interpreter" || pv.Hint == nil || pv.Hint.Code != HintVersionUnknown || !strings.Contains(pv.Hint.Message, "Re-read") {
		t.Fatalf("php.version %+v hint %+v", pv, pv.Hint)
	}
	q, err := api.HookPHPVersion.Apply(context.Background(), a.Hooks(), api.PHPVersionQuery{})
	if err != nil || q.Source == "interpreter" || q.Version != "8.1.0" {
		t.Fatalf("hook %+v %v", q, err)
	}
	var d Diagnosis
	modtest.Call(t, a, "runtime.diagnose", nil, &d)
	if d.Version != "" || len(d.Hints) == 0 || d.Hints[len(d.Hints)-1].Code != HintVersionUnknown {
		t.Fatalf("diagnose %+v", d)
	}
	var list []InterpreterInfo
	modtest.Call(t, a, "interp.list", InterpListParams{Status: true}, &list)
	var st StatusResult
	modtest.Call(t, a, "interp.status", nil, &st)
	if st.ImagePresent == nil || !*st.ImagePresent {
		t.Fatalf("status %+v", st)
	}
	modtest.WantCode(t, a, "interp.probe", ProbeParams{CachedOnly: true}, api.CodeNotFound)
	perr := modtest.WantCode(t, a, "interp.probe", ProbeParams{}, api.CodeUnavailable)
	if e, ok := perr.(*api.Error); !ok || !strings.Contains(e.Message, "unknown") {
		t.Fatalf("probe %v", perr)
	}
	if n := containerRuns(f); n != 0 {
		t.Fatalf("automatic queries started %d containers: %v", n, f.Calls())
	}

	// Re-read is a user action: one container.
	var pr ProbeResult
	modtest.Call(t, a, "interp.probe", ProbeParams{Refresh: true}, &pr)
	if pr.Info.Version != "8.3.1" || containerRuns(f) != 1 || !f.Called("run ", "--pull never") {
		t.Fatalf("re-read %+v %v", pr, f.Calls())
	}

	// From now on the automatic queries use the cache, still no container.
	modtest.Call(t, a, "php.version", nil, &pv)
	if pv.Source != "interpreter" || pv.Version != "8.3.1" || pv.Hint != nil {
		t.Fatalf("cached php.version %+v", pv)
	}
	modtest.Call(t, a, "runtime.diagnose", nil, &d)
	if d.Version != "8.3.1" || d.Ini["xdebug.mode"] != "debug" {
		t.Fatalf("cached diagnose %+v", d)
	}
	modtest.Call(t, a, "interp.list", nil, &list)
	if len(list) != 1 || list[0].Version != "8.3.1" {
		t.Fatalf("list %+v", list)
	}
	if n := containerRuns(f); n != 1 {
		t.Fatalf("cached queries started containers: %v", f.Calls())
	}

	// An explicit start may run (and pull).
	modtest.Call(t, a, "php.version", PHPVersionParams{Start: true}, &pv)
	if pv.Source != "interpreter" || containerRuns(f) != 2 {
		t.Fatalf("start %+v %v", pv, f.Calls())
	}
}

func TestCachedImageInterpreter(t *testing.T) {
	ctx := context.Background()
	empty := &cachedImage{}
	if _, err := empty.Version(ctx); err == nil {
		t.Fatal("version without cache")
	}
	if _, err := empty.Modules(ctx); err == nil {
		t.Fatal("modules without cache")
	}
	if _, err := empty.Ini(ctx); err == nil {
		t.Fatal("ini without cache")
	}
	if _, err := empty.Command(ctx, "-v"); err == nil {
		t.Fatal("command must never run")
	}
	c := &cachedImage{cached: true}
	c.entry.Info.Version = "8.3.1"
	c.entry.Info.Extensions = []string{"Core"}
	c.entry.Info.ZendExtensions = []string{"Xdebug"}
	if mods, err := c.Modules(ctx); err != nil || len(mods) != 2 {
		t.Fatalf("modules %v %v", mods, err)
	}
	if out, err := c.RunPHP(ctx, "-r", "echo PHP_VERSION;"); err != nil || string(out) != "8.3.1" {
		t.Fatalf("run %q %v", out, err)
	}
	if _, err := c.RunPHP(ctx, "-m"); err == nil {
		t.Fatal("other php calls must fail")
	}
}
