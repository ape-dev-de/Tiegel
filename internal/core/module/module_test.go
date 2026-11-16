package module

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"testing"

	"github.com/ape-dev-de/tiegel/internal/core/bus"
	"github.com/ape-dev-de/tiegel/internal/core/hooks"
	"github.com/ape-dev-de/tiegel/internal/core/settings"
)

// journal records lifecycle calls across modules.
type journal struct {
	mu    sync.Mutex
	calls []string
}

func (j *journal) add(s string) {
	j.mu.Lock()
	j.calls = append(j.calls, s)
	j.mu.Unlock()
}

func (j *journal) String() string {
	j.mu.Lock()
	defer j.mu.Unlock()
	return strings.Join(j.calls, ",")
}

func (j *journal) reset() {
	j.mu.Lock()
	j.calls = nil
	j.mu.Unlock()
}

type fakeModule struct {
	id        string
	deps      []string
	j         *journal
	startErr  error
	stopErr   error
	panicIn   string
	onStart   func(Context)
	lastCtx   Context
	startRuns int
}

func (m *fakeModule) ID() string          { return m.id }
func (m *fakeModule) DependsOn() []string { return m.deps }
func (m *fakeModule) Start(ctx Context) error {
	m.startRuns++
	m.lastCtx = ctx
	if m.panicIn == "start" {
		panic("start panic")
	}
	if m.onStart != nil {
		m.onStart(ctx)
	}
	if m.startErr != nil {
		return m.startErr
	}
	m.j.add("start:" + m.id)
	return nil
}
func (m *fakeModule) Stop() error {
	if m.panicIn == "stop" {
		panic("stop panic")
	}
	m.j.add("stop:" + m.id)
	return m.stopErr
}

func newMod(j *journal, id string, deps ...string) *fakeModule {
	return &fakeModule{id: id, deps: deps, j: j}
}

func mustRegister(t *testing.T, r *Registry, m Module, opts ...RegisterOption) {
	t.Helper()
	if err := r.Register(m, opts...); err != nil {
		t.Fatal(err)
	}
}

func TestRegisterValidation(t *testing.T) {
	r := NewRegistry(Env{})
	j := &journal{}
	if err := r.Register(newMod(j, "")); !errors.Is(err, ErrInvalidID) {
		t.Fatal(err)
	}
	if err := r.Register(newMod(j, "a b")); !errors.Is(err, ErrInvalidID) {
		t.Fatal(err)
	}
	mustRegister(t, r, newMod(j, "a"))
	if err := r.Register(newMod(j, "a")); !errors.Is(err, ErrDuplicate) {
		t.Fatal(err)
	}
	if err := r.Enable("zzz"); !errors.Is(err, ErrUnknown) {
		t.Fatal(err)
	}
	if err := r.Disable("zzz"); !errors.Is(err, ErrUnknown) {
		t.Fatal(err)
	}
	if err := r.EnableWithDependencies("zzz"); !errors.Is(err, ErrUnknown) {
		t.Fatal(err)
	}
}

func TestStartOrderAndReverseStop(t *testing.T) {
	j := &journal{}
	r := NewRegistry(Env{})
	// registered out of dependency order on purpose
	mustRegister(t, r, newMod(j, "debug-xdebug", "php-runtime"), DefaultEnabled())
	mustRegister(t, r, newMod(j, "php-features", "php-runtime", "lang-php"), DefaultEnabled())
	mustRegister(t, r, newMod(j, "php-runtime"), DefaultEnabled())
	mustRegister(t, r, newMod(j, "lang-php"), DefaultEnabled())
	mustRegister(t, r, newMod(j, "git"))

	order, err := r.StartOrder()
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(order, ","); got != "php-runtime,debug-xdebug,lang-php,php-features" {
		t.Fatalf("order = %s", got)
	}
	if err := r.Start(); err != nil {
		t.Fatal(err)
	}
	if err := r.Start(); err != nil { // idempotent
		t.Fatal(err)
	}
	if got := j.String(); got != "start:php-runtime,start:debug-xdebug,start:lang-php,start:php-features" {
		t.Fatalf("start = %s", got)
	}
	if !r.IsRunning("lang-php") || r.IsRunning("git") || r.IsEnabled("git") || !r.IsEnabled("lang-php") {
		t.Fatal("state flags")
	}
	j.reset()
	if err := r.Stop(); err != nil {
		t.Fatal(err)
	}
	if got := j.String(); got != "stop:php-features,stop:lang-php,stop:debug-xdebug,stop:php-runtime" {
		t.Fatalf("stop = %s", got)
	}
	for _, info := range r.Modules() {
		if info.Running {
			t.Fatalf("%s still running", info.ID)
		}
	}
}

func TestEnableRequiresDependencies(t *testing.T) {
	j := &journal{}
	r := NewRegistry(Env{})
	mustRegister(t, r, newMod(j, "debug", "runtime"))
	if err := r.Enable("debug"); !errors.Is(err, ErrMissingDependency) {
		t.Fatalf("missing: %v", err)
	}
	mustRegister(t, r, newMod(j, "runtime"))
	if err := r.Enable("debug"); !errors.Is(err, ErrDependencyDisabled) {
		t.Fatalf("disabled: %v", err)
	}
	if err := r.Enable("runtime"); err != nil {
		t.Fatal(err)
	}
	if err := r.Enable("debug"); err != nil {
		t.Fatal(err)
	}
	if err := r.Enable("debug"); err != nil { // idempotent
		t.Fatal(err)
	}
	if err := r.Disable("runtime"); !errors.Is(err, ErrRequired) || !strings.Contains(err.Error(), "debug") {
		t.Fatalf("disable required: %v", err)
	}
	if got := r.Dependents("runtime"); len(got) != 1 || got[0] != "debug" {
		t.Fatalf("dependents = %v", got)
	}
	if err := r.Disable("debug"); err != nil {
		t.Fatal(err)
	}
	if err := r.Disable("debug"); err != nil { // idempotent
		t.Fatal(err)
	}
	if err := r.Disable("runtime"); err != nil {
		t.Fatal(err)
	}
}

func TestEnableWithDependencies(t *testing.T) {
	j := &journal{}
	r := NewRegistry(Env{})
	mustRegister(t, r, newMod(j, "c", "b"))
	mustRegister(t, r, newMod(j, "b", "a"))
	mustRegister(t, r, newMod(j, "a"))
	if err := r.Start(); err != nil {
		t.Fatal(err)
	}
	if err := r.EnableWithDependencies("c"); err != nil {
		t.Fatal(err)
	}
	if got := j.String(); got != "start:a,start:b,start:c" {
		t.Fatalf("runtime enable order = %s", got)
	}
	j.reset()
	if err := r.Disable("c"); err != nil {
		t.Fatal(err)
	}
	if got := j.String(); got != "stop:c" {
		t.Fatalf("disable = %s", got)
	}
	mustRegister(t, r, newMod(j, "d", "nope"))
	if err := r.EnableWithDependencies("d"); !errors.Is(err, ErrMissingDependency) {
		t.Fatal(err)
	}
}

func TestCycleDetection(t *testing.T) {
	j := &journal{}
	st := memState{"a": true, "b": true, "c": true, "ok": true}
	r := NewRegistry(Env{State: st})
	mustRegister(t, r, newMod(j, "a", "b"))
	mustRegister(t, r, newMod(j, "b", "c"))
	mustRegister(t, r, newMod(j, "c", "a"))
	mustRegister(t, r, newMod(j, "ok"))
	_, err := r.StartOrder()
	var ce *CycleError
	if !errors.As(err, &ce) || !errors.Is(err, ErrCycle) {
		t.Fatalf("err = %v", err)
	}
	if got := strings.Join(ce.Path, ">"); got != "a>b>c>a" {
		t.Fatalf("path = %s", got)
	}
	if !strings.Contains(ce.Error(), "a → b → c → a") {
		t.Fatal(ce.Error())
	}
	err = r.Start()
	if !errors.Is(err, ErrCycle) {
		t.Fatalf("start err = %v", err)
	}
	if !r.IsRunning("ok") || r.IsRunning("a") {
		t.Fatal("independent module should start, cyclic ones not")
	}
	if err := r.EnableWithDependencies("a"); !errors.Is(err, ErrCycle) {
		t.Fatal(err)
	}

	r2 := NewRegistry(Env{})
	mustRegister(t, r2, newMod(j, "self", "self"))
	if err := r2.Enable("self"); !errors.Is(err, ErrCycle) {
		t.Fatal(err)
	}
}

func TestStartFailuresSkipDependents(t *testing.T) {
	j := &journal{}
	r := NewRegistry(Env{})
	bad := newMod(j, "bad")
	bad.startErr = errors.New("no php found")
	mustRegister(t, r, newMod(j, "child", "bad"), DefaultEnabled())
	mustRegister(t, r, bad, DefaultEnabled())
	mustRegister(t, r, newMod(j, "grandchild", "child"), DefaultEnabled())
	panicky := newMod(j, "panicky")
	panicky.panicIn = "start"
	mustRegister(t, r, panicky, DefaultEnabled())
	mustRegister(t, r, newMod(j, "fine"), DefaultEnabled())

	err := r.Start()
	if !errors.Is(err, bad.startErr) || !errors.Is(err, ErrDependencyFailed) || !errors.Is(err, ErrPanic) {
		t.Fatalf("err = %v", err)
	}
	if bad.startRuns != 1 {
		t.Fatalf("failing module started %d times", bad.startRuns)
	}
	if got := j.String(); got != "start:fine" {
		t.Fatalf("started = %s", got)
	}
	infos := map[string]Info{}
	for _, in := range r.Modules() {
		infos[in.ID] = in
	}
	if !errors.Is(infos["grandchild"].Err, ErrDependencyFailed) || infos["fine"].Err != nil {
		t.Fatalf("infos = %+v", infos)
	}

	// Fix the module and retry by enabling again.
	bad.startErr = nil
	if err := r.Enable("bad"); err != nil {
		t.Fatal(err)
	}
	if err := r.Enable("grandchild"); err != nil {
		t.Fatal(err)
	}
	if !r.IsRunning("child") || !r.IsRunning("grandchild") {
		t.Fatal("retry did not start dependents")
	}
}

func TestStopErrorsAndPanics(t *testing.T) {
	j := &journal{}
	r := NewRegistry(Env{})
	a := newMod(j, "a")
	a.stopErr = errors.New("stop failed")
	b := newMod(j, "b")
	b.panicIn = "stop"
	mustRegister(t, r, a, DefaultEnabled())
	mustRegister(t, r, b, DefaultEnabled())
	if err := r.Start(); err != nil {
		t.Fatal(err)
	}
	err := r.Stop()
	if !errors.Is(err, a.stopErr) || !errors.Is(err, ErrPanic) {
		t.Fatalf("err = %v", err)
	}
	if r.IsRunning("a") || r.IsRunning("b") {
		t.Fatal("modules still running after failed stop")
	}
	// Disable of a running module whose Stop fails reports the error.
	if err := r.Start(); err != nil {
		t.Fatal(err)
	}
	if err := r.Disable("a"); !errors.Is(err, a.stopErr) {
		t.Fatalf("disable: %v", err)
	}
	if r.IsEnabled("a") {
		t.Fatal("still enabled")
	}
}

func TestRegisterWhileRunning(t *testing.T) {
	j := &journal{}
	r := NewRegistry(Env{})
	if err := r.Start(); err != nil {
		t.Fatal(err)
	}
	mustRegister(t, r, newMod(j, "late"), DefaultEnabled())
	if !r.IsRunning("late") {
		t.Fatal("late module not started")
	}
}

type memState map[string]bool

func (m memState) ModuleEnabled(id string) (bool, bool) { v, ok := m[id]; return v, ok }
func (m memState) SetModuleEnabled(id string, on bool) error {
	m[id] = on
	return nil
}

type failingState struct{}

func (failingState) ModuleEnabled(string) (bool, bool) { return false, false }
func (failingState) SetModuleEnabled(string, bool) error {
	return errors.New("disk full")
}

func TestStatePersistence(t *testing.T) {
	base := t.TempDir()
	st, err := settings.OpenGlobal(base, nil)
	if err != nil {
		t.Fatal(err)
	}
	j := &journal{}
	r := NewRegistry(Env{Settings: st})
	mustRegister(t, r, newMod(j, "git"), DefaultEnabled())
	mustRegister(t, r, newMod(j, "debug"))
	if err := r.Disable("git"); err != nil {
		t.Fatal(err)
	}
	if err := r.Enable("debug"); err != nil {
		t.Fatal(err)
	}

	st2, _ := settings.OpenGlobal(base, nil)
	r2 := NewRegistry(Env{Settings: st2})
	mustRegister(t, r2, newMod(j, "git"), DefaultEnabled())
	mustRegister(t, r2, newMod(j, "debug"))
	if r2.IsEnabled("git") || !r2.IsEnabled("debug") {
		t.Fatal("states not restored from settings")
	}

	r3 := NewRegistry(Env{State: failingState{}})
	mustRegister(t, r3, newMod(j, "x"))
	if err := r3.Enable("x"); err == nil || r3.IsEnabled("x") {
		t.Fatal("persist failure must keep module disabled")
	}
	mustRegister(t, r3, newMod(j, "y"), DefaultEnabled())
	if err := r3.Disable("y"); err == nil || !r3.IsEnabled("y") {
		t.Fatal("persist failure must keep module enabled")
	}
}

// extCtx is an example of a later UI layer extending the context.
type extCtx struct {
	Context
	menus *[]string
}

func (c extCtx) AddMenu(name string) { *c.menus = append(*c.menus, c.ModuleID()+":"+name) }

func TestContextServicesAndCleanup(t *testing.T) {
	b := bus.New()
	h := hooks.New()
	st, _ := settings.OpenGlobal(t.TempDir(), nil)
	var menus []string
	r := NewRegistry(Env{
		Bus: b, Hooks: h, Settings: st, Logger: slog.New(slog.DiscardHandler),
		Extend: func(base Context) Context { return extCtx{Context: base, menus: &menus} },
	})
	j := &journal{}
	m := newMod(j, "git")
	var events int
	var order []string
	saved := hooks.NewAction[string]("file.saved")
	m.onStart = func(ctx Context) {
		if ctx.Bus() != b || ctx.Hooks() != h || ctx.Settings() != st || ctx.Logger() == nil {
			t.Error("services not wired")
		}
		ctx.(interface{ AddMenu(string) }).AddMenu("VCS")
		ctx.OnStop(ctx.Bus().Subscribe("x", func(bus.Event) { events++ }))
		ctx.OnStop(func() { order = append(order, "first") })
		ctx.OnStop(func() { order = append(order, "second") })
		ctx.OnStop(func() { panic("cleanup panic") })
		ctx.OnStop(nil)
		saved.On(ctx.Hooks(), 0, func(context.Context, string) error { return nil }, hooks.WithOwner(ctx.ModuleID()))
	}
	mustRegister(t, r, m, DefaultEnabled())
	if err := r.Start(); err != nil {
		t.Fatal(err)
	}
	_ = b.PublishSync("x", nil)
	if events != 1 || h.Count("file.saved") != 1 || len(menus) != 1 || menus[0] != "git:VCS" {
		t.Fatalf("events=%d hooks=%d menus=%v", events, h.Count("file.saved"), menus)
	}
	if err := r.Stop(); err != nil {
		t.Fatal(err)
	}
	_ = b.PublishSync("x", nil)
	if events != 1 {
		t.Fatal("subscription not removed on stop")
	}
	if h.Count("file.saved") != 0 {
		t.Fatal("hooks not removed on stop")
	}
	if strings.Join(order, ",") != "second,first" {
		t.Fatalf("cleanup order = %v", order)
	}
}

func TestFailedStartCleansUp(t *testing.T) {
	h := hooks.New()
	r := NewRegistry(Env{Hooks: h})
	j := &journal{}
	m := newMod(j, "m")
	m.startErr = errors.New("nope")
	m.onStart = func(ctx Context) {
		hooks.NewAction[int]("a").On(ctx.Hooks(), 0, func(context.Context, int) error { return nil }, hooks.WithOwner(ctx.ModuleID()))
	}
	mustRegister(t, r, m, DefaultEnabled())
	if err := r.Start(); err == nil {
		t.Fatal("expected error")
	}
	if h.Count("a") != 0 {
		t.Fatal("hooks of failed module not removed")
	}
}

func TestConcurrentQueries(t *testing.T) {
	j := &journal{}
	r := NewRegistry(Env{})
	for _, id := range []string{"a", "b", "c"} {
		mustRegister(t, r, newMod(j, id), DefaultEnabled())
	}
	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for k := 0; k < 50; k++ {
				_ = r.Start()
				_ = r.Modules()
				_, _ = r.StartOrder()
				_ = r.Disable("b")
				_ = r.Enable("b")
				_ = r.Stop()
			}
		}()
	}
	wg.Wait()
}
