package module

import (
	"errors"
	"testing"

	"github.com/ape-dev-de/tiegel/internal/core/settings"
)

type specModule struct {
	fakeModule
	specs []settings.Spec
	panic bool
}

func (m *specModule) SettingSpecs() []settings.Spec {
	if m.panic {
		panic("specs")
	}
	return m.specs
}

func TestModuleSettingSpecs(t *testing.T) {
	r := NewRegistry(Env{})
	j := &journal{}
	good := settings.Spec{Key: "qa-tool.path", Kind: settings.KindPath, Default: "vendor/bin/tool", Scope: settings.ScopeProject, Label: "Path"}
	m := &specModule{fakeModule: fakeModule{id: "qa-tool", j: j}, specs: []settings.Spec{
		good,
		{Key: "editor.tabWidth", Kind: settings.KindInt, Scope: settings.ScopeGlobal, Label: "x"}, // foreign namespace
		{Key: "qa-tool.", Kind: settings.KindBool, Scope: settings.ScopeGlobal},                   // empty name
		{Key: "qa-toolx.a", Kind: settings.KindBool, Scope: settings.ScopeGlobal},                 // prefix trick
		{Key: "qa-tool.bad", Kind: "nope", Scope: settings.ScopeGlobal},                           // invalid
		good, // duplicate
	}}
	mustRegister(t, r, m)
	mustRegister(t, r, newMod(j, "plain"))
	if got := r.SettingSpecs("qa-tool"); len(got) != 1 || got[0].Key != good.Key {
		t.Fatalf("specs %+v", got)
	}
	if r.SettingSpecs("plain") != nil || r.SettingSpecs("missing") != nil {
		t.Fatal("unexpected specs")
	}
	errs := r.SpecErrors()
	if len(errs) != 5 {
		t.Fatalf("errors %v", errs)
	}
	for _, err := range errs {
		if !errors.Is(err, ErrSpec) {
			t.Fatalf("%v does not wrap ErrSpec", err)
		}
	}
	if s, id, ok := r.SettingSpec("qa-tool.path"); !ok || id != "qa-tool" || s.Kind != settings.KindPath {
		t.Fatalf("lookup %v %s %v", s, id, ok)
	}
	if _, _, ok := r.SettingSpec("editor.tabWidth"); ok {
		t.Fatal("rejected spec found")
	}

	// A panicking SettingSpecs is reported, the module still registers.
	pm := &specModule{fakeModule: fakeModule{id: "boom", j: j}, panic: true}
	mustRegister(t, r, pm)
	if len(r.SpecErrors()) != 6 || r.SettingSpecs("boom") != nil {
		t.Fatalf("panic: %v", r.SpecErrors())
	}
}
