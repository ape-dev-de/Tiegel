package compose

import (
	"reflect"
	"sort"
	"testing"

	"github.com/compose-spec/compose-go/v2/types"
)

func codes(hs []Hint) []string {
	out := make([]string, 0, len(hs))
	for _, h := range hs {
		out = append(out, h.Code)
		if h.Message == "" {
			out = append(out, "EMPTY MESSAGE")
		}
	}
	sort.Strings(out)
	return out
}

func TestXdebugHintsFixtures(t *testing.T) {
	cases := []struct {
		dir, service, goos string
		want               []string
	}{
		{"sail", "laravel.test", "linux", []string{}},
		{"symfony", "php", "linux", []string{HintXdebugModeNoDbg}},
		{"multi", "php", "linux", []string{HintClientHostLocal, HintNoHostGateway}},
		{"multi", "php", "darwin", []string{HintClientHostLocal}},
		{"fpm-nginx", "app", "linux", []string{HintNoHostGateway, HintNoXdebugEnv}},
		{"fpm-nginx", "app", "windows", []string{HintNoXdebugEnv}},
		{"envinterp", "fpm", "darwin", []string{HintNoXdebugEnv, HintProjectNotMapped}},
		{"sail", "nope", "linux", []string{HintUnknownService}},
	}
	for _, c := range cases {
		p := load(t, c.dir, LoadOptions{})
		got := codes(XdebugHints(p, c.service, c.goos))
		want := append([]string{}, c.want...)
		sort.Strings(want)
		if !reflect.DeepEqual(got, want) {
			t.Errorf("%s/%s on %s: hints %v, want %v", c.dir, c.service, c.goos, got, want)
		}
	}
}

func TestXdebugHintsNoMounts(t *testing.T) {
	mode := "debug"
	var svc types.ServiceConfig
	svc.Name = "php"
	svc.Environment = types.MappingWithEquals{"XDEBUG_MODE": &mode, "XDEBUG_SESSION": nil}
	p := &Project{Dir: "/p", Model: &types.Project{Services: types.Services{"php": svc}}}
	got := codes(XdebugHints(p, "php", "darwin"))
	if !reflect.DeepEqual(got, []string{HintNoPathMapping}) {
		t.Errorf("hints = %v", got)
	}
}

func TestSeverityString(t *testing.T) {
	if SeverityInfo.String() != "info" || SeverityWarning.String() != "warning" {
		t.Error("severity names")
	}
}

func TestHasHostGateway(t *testing.T) {
	if hasHostGateway(nil) || hasHostGateway(map[string][]string{"host.docker.internal": {""}}) {
		t.Error("empty mapping is not a gateway")
	}
	if !hasHostGateway(map[string][]string{"host.docker.internal": {"172.17.0.1"}}) {
		t.Error("explicit IP counts")
	}
}
