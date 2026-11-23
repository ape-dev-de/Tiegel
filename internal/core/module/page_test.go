package module

import "testing"

type pageModule struct{ *fakeModule }

func (pageModule) SettingsPage() (string, string) { return "Tests", "PHPUnit" }

func TestPageProvider(t *testing.T) {
	r := NewRegistry(Env{})
	j := &journal{}
	mustRegister(t, r, pageModule{newMod(j, "phpunit")})
	mustRegister(t, r, newMod(j, "plain"))
	got := map[string]Info{}
	for _, i := range r.Modules() {
		got[i.ID] = i
	}
	if i := got["phpunit"]; i.Section != "Tests" || i.Title != "PHPUnit" {
		t.Fatalf("page %+v", i)
	}
	if i := got["plain"]; i.Section != "" || i.Title != "" {
		t.Fatalf("plain %+v", i)
	}
}
