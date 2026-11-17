package settings

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestKernelSpecsRegistered(t *testing.T) {
	if coreInitErr != nil {
		t.Fatalf("kernel specs: %v", coreInitErr)
	}
	specs := CoreSpecs()
	if len(specs) < len(kernelSpecs) {
		t.Fatalf("got %d specs", len(specs))
	}
	known := map[string]bool{}
	for _, p := range CorePages {
		known[p] = true
	}
	for _, s := range kernelSpecs {
		got, ok := CoreSpec(s.Key)
		if !ok || got.Key != s.Key {
			t.Errorf("%s not registered", s.Key)
		}
		if !known[s.Group] {
			t.Errorf("%s: group %q is not a core page", s.Key, s.Group)
		}
		if s.Label == "" || s.Help == "" {
			t.Errorf("%s: label and help required", s.Key)
		}
	}
	if _, ok := CoreSpec("nope.x"); ok {
		t.Fatal("unknown key found")
	}
}

// TestCoreKeysNameNoTool enforces D190: the core knows no tool by name.
func TestCoreKeysNameNoTool(t *testing.T) {
	tools := []string{"pint", "phpstan", "psalm", "phpunit", "pest", "docker", "compose", "xdebug", "phpactor", "phpantom",
		"intelephense", "git", "composer", "phpcs", "fixer", "phpmd", "rector", "laravel", "symfony", "lsp"}
	for _, s := range CoreSpecs() {
		k := strings.ToLower(s.Key)
		for _, tool := range tools {
			if strings.Contains(k, tool) {
				t.Errorf("core key %q names tool %q (belongs to a plugin)", s.Key, tool)
			}
		}
	}
}

func TestRegisterCore(t *testing.T) {
	defer unregisterCore("test.a", "test.b")
	err := RegisterCore(
		Spec{Key: "test.a", Kind: KindBool, Scope: ScopeGlobal, Label: "A"},
		Spec{Key: "modules.x.enabled", Kind: KindBool, Scope: ScopeGlobal},
		Spec{Key: "bad", Kind: KindBool, Scope: ScopeGlobal},
		Spec{Key: "test.b", Kind: KindList, Default: []string{"x"}, Scope: ScopeGlobal, Label: "B"},
	)
	if err == nil || !strings.Contains(err.Error(), "reserved") || !strings.Contains(err.Error(), "namespaced") {
		t.Fatalf("err %v", err)
	}
	if _, ok := CoreSpec("test.a"); !ok {
		t.Fatal("valid spec of the call not registered")
	}
	if err := RegisterCore(Spec{Key: "test.a", Kind: KindBool, Scope: ScopeGlobal}); err == nil || !strings.Contains(err.Error(), "twice") {
		t.Fatalf("dup: %v", err)
	}
	// Returned specs are copies.
	b, _ := CoreSpec("test.b")
	b.Default.([]string)[0] = "changed"
	if b2, _ := CoreSpec("test.b"); b2.Default.([]string)[0] != "x" {
		t.Fatal("spec not copied")
	}
}

func TestFloatKind(t *testing.T) {
	s := Spec{Key: "a.f", Kind: KindFloat, Default: 1.4, Min: IntPtr(1), Max: IntPtr(3), Scope: ScopeGlobal}
	if err := s.Validate(); err != nil {
		t.Fatal(err)
	}
	for _, v := range []any{1.0, 2.5, 3, float32(1.5), int64(2)} {
		if err := s.Check(v); err != nil {
			t.Errorf("%v: %v", v, err)
		}
	}
	for _, v := range []any{0.5, 3.5, "1"} {
		if err := s.Check(v); err == nil {
			t.Errorf("%v: want error", v)
		}
	}
}

func TestResolveEffectiveMigrate(t *testing.T) {
	dir := t.TempDir()
	g, err := OpenGlobal(dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	p, err := OpenProject(dir, t.TempDir(), nil)
	if err != nil {
		t.Fatal(err)
	}
	spec := Spec{Key: "x.n", Kind: KindInt, Default: 4, Min: IntPtr(1), Scope: ScopeProject}
	if raw, src := Effective(spec, spec.Key, g, p); string(raw) != "4" || src != SourceDefault {
		t.Fatalf("default: %s %s", raw, src)
	}
	_ = Set(g, "x.n", 8)
	if raw, src := Effective(spec, spec.Key, g, p); string(raw) != "8" || src != SourceGlobal {
		t.Fatalf("global: %s %s", raw, src)
	}
	_ = Set(p, "x.n", 2)
	if raw, src := Effective(spec, spec.Key, g, p); string(raw) != "2" || src != SourceProject {
		t.Fatalf("project: %s %s", raw, src)
	}
	// Invalid stored values are skipped.
	_ = Set(p, "x.n", 0)
	if raw, src := Effective(spec, spec.Key, g, p); string(raw) != "8" || src != SourceGlobal {
		t.Fatalf("invalid project: %s %s", raw, src)
	}
	// Global-only specs ignore project values.
	gs := spec
	gs.Scope = ScopeGlobal
	_ = Set(p, "x.n", 3)
	if raw, _ := Effective(gs, gs.Key, g, p); string(raw) != "8" {
		t.Fatalf("global-only: %s", raw)
	}
	// Undeclared: no default.
	if raw, src := Effective(Spec{}, "y.z", g, nil); raw != nil || src != SourceDefault {
		t.Fatalf("undeclared: %s %s", raw, src)
	}
	if v := Resolve("x.n", 0, nil, p, g); v != 3 {
		t.Fatalf("resolve %d", v)
	}
	if v := Resolve("missing.k", 7, p, g); v != 7 {
		t.Fatalf("resolve default %d", v)
	}
	if raw, ok := g.Raw("x.n"); !ok || string(raw) != "8" {
		t.Fatalf("raw %s", raw)
	}
	if _, ok := g.Raw("none.k"); ok {
		t.Fatal("raw missing")
	}

	// MigrateKey moves once and deletes the old key.
	_ = Set(g, "old.k", []string{"a"})
	if moved, err := MigrateKey(g, "old.k", "new.k"); err != nil || !moved {
		t.Fatalf("migrate %v %v", moved, err)
	}
	if g.Has("old.k") || Get[[]string](g, "new.k", nil)[0] != "a" {
		t.Fatal("not migrated")
	}
	_ = Set(g, "old.k", []string{"b"})
	if moved, err := MigrateKey(g, "old.k", "new.k"); err != nil || moved || g.Has("old.k") {
		t.Fatalf("existing target: %v %v", moved, err)
	}
	if moved, err := MigrateKey(g, "old.k", "new.k"); err != nil || moved {
		t.Fatal("nothing to migrate")
	}
	if moved, err := MigrateKey(nil, "a", "b"); err != nil || moved {
		t.Fatal("nil store")
	}
	var v []string
	raw, _ := g.Raw("new.k")
	if json.Unmarshal(raw, &v) != nil || v[0] != "a" {
		t.Fatal("value kept")
	}
}

// TestEditorDisplaySpecs covers the M12.3 editor settings (D285–D287).
func TestEditorDisplaySpecs(t *testing.T) {
	ok := []struct {
		key string
		v   any
	}{
		{KeySoftWrap, SoftWrapAll}, {KeySoftWrapFiles, []string{"*.md"}}, {KeyShowWhitespace, WhitespaceTrailing},
		{KeyRightMargin, 0}, {KeyRightMargin, 80}, {KeyShowRightMargin, false},
	}
	for _, c := range ok {
		sp, found := CoreSpec(c.key)
		if !found || sp.Group != PageEditor {
			t.Fatalf("%s not on the editor page", c.key)
		}
		if err := sp.Check(c.v); err != nil {
			t.Errorf("%s = %v: %v", c.key, c.v, err)
		}
	}
	bad := []struct {
		key string
		v   any
	}{{KeySoftWrap, "sometimes"}, {KeyShowWhitespace, "tabs"}, {KeyRightMargin, -1}, {KeyRightMargin, 5000}}
	for _, c := range bad {
		sp, _ := CoreSpec(c.key)
		if sp.Check(c.v) == nil {
			t.Errorf("%s = %v accepted", c.key, c.v)
		}
	}
	if sp, _ := CoreSpec(KeyRightMargin); sp.Scope != ScopeProject {
		t.Error("right margin must allow project values")
	}
}
