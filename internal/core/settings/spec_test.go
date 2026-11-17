package settings

import "testing"

func TestSpecValidateAndCheck(t *testing.T) {
	good := []Spec{
		{Key: "editor.softWrap", Kind: KindBool, Default: false, Scope: ScopeProject, Label: "Soft wrap"},
		{Key: "editor.tabWidth", Kind: KindInt, Default: 4, Scope: ScopeProject, Label: "Tab", Min: IntPtr(1), Max: IntPtr(16)},
		{Key: "lsp.server", Kind: KindEnum, Default: "none", Options: []string{"none", "phpactor"}, Scope: ScopeProject, Label: "Server"},
		{Key: "qa-pint.path", Kind: KindPath, Default: "vendor/bin/pint", Scope: ScopeProject, Label: "Path"},
		{Key: "x.list", Kind: KindList, Default: []string{"a"}, Scope: ScopeGlobal, Label: "L"},
	}
	for _, s := range good {
		if err := s.Validate(); err != nil {
			t.Errorf("%s: %v", s.Key, err)
		}
	}
	bad := []Spec{
		{Key: "nodot", Kind: KindBool, Scope: ScopeGlobal},
		{Key: "a.b", Kind: "weird", Scope: ScopeGlobal},
		{Key: "a.b", Kind: KindEnum, Scope: ScopeGlobal},
		{Key: "a.b", Kind: KindBool, Scope: "x"},
		{Key: "a.b", Kind: KindBool, Scope: ScopeGlobal, Default: "yes"},
	}
	for _, s := range bad {
		if err := s.Validate(); err == nil {
			t.Errorf("%+v: want error", s)
		}
	}
	tab := good[1]
	if err := tab.Check(float64(8)); err != nil {
		t.Error(err)
	}
	for _, v := range []any{0, 17, 2.5, "4"} {
		if err := tab.Check(v); err == nil {
			t.Errorf("tab %v: want error", v)
		}
	}
	if err := good[2].Check("other"); err == nil {
		t.Error("enum: want error")
	}
	if err := good[2].Check(1); err == nil {
		t.Error("enum type: want error")
	}
	if err := good[4].Check([]any{"a", 1}); err == nil {
		t.Error("list: want error")
	}
	if err := good[4].Check([]any{"a"}); err != nil {
		t.Error(err)
	}
	if err := good[4].Check("a"); err == nil {
		t.Error("list type: want error")
	}
	if err := good[3].Check(1); err == nil {
		t.Error("path: want error")
	}
	if err := (Spec{Kind: "x"}).Check(1); err == nil {
		t.Error("unknown kind: want error")
	}
}
