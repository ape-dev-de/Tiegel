package langphp

import (
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/ape-dev-de/tiegel/internal/api"
	"github.com/ape-dev-de/tiegel/internal/core/settings"
	"github.com/ape-dev-de/tiegel/internal/lsp/supervisor"
	"github.com/ape-dev-de/tiegel/internal/modules/modtest"
)

func lastArgs(f *fakes) []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.cfgs[len(f.cfgs)-1].Args
}

// TestDefaultArgsOfEveryPlugin: a server started from a plugin gets the
// plugin's default arguments (Phpactor "language-server", Intelephense
// "--stdio"); a stored <plugin>.args override still wins.
func TestDefaultArgsOfEveryPlugin(t *testing.T) {
	a, _, f, _, _ := pluginApp(t, Options{}, "phpactor", "phpantom_lsp", "intelephense")
	modtest.Call(t, a, "modules.enable", api.ModuleParams{ID: "lsp-intelephense"}, nil)

	var st Status
	// auto picks Phpactor.
	modtest.Call(t, a, "lsp.start", nil, &st)
	if st.Server != "phpactor" || !reflect.DeepEqual(lastArgs(f), []string{"language-server"}) || !reflect.DeepEqual(st.Args, []string{"language-server"}) {
		t.Fatalf("auto phpactor: args %q, status %+v", lastArgs(f), st)
	}
	for _, c := range []struct {
		server string
		want   []string
	}{
		{"phpactor", []string{"language-server"}},
		{"phpantom", nil},
		{"intelephense", []string{"--stdio"}},
	} {
		modtest.Call(t, a, "lsp.select", SelectParams{Server: c.server}, &st)
		if st.Server != c.server || !st.Running {
			t.Fatalf("%s: %+v", c.server, st)
		}
		if got := lastArgs(f); len(got) != len(c.want) || (len(c.want) > 0 && !reflect.DeepEqual(got, c.want)) {
			t.Fatalf("%s: args %q, want %q", c.server, got, c.want)
		}
	}
	// User override wins.
	if err := settings.Set(a.ProjectSettings(), "lsp-intelephense.args", []string{"--stdio", "--log=x"}); err != nil {
		t.Fatal(err)
	}
	waitState(t, a, "override applied", func(s Status) bool {
		return s.Running && reflect.DeepEqual(s.Args, []string{"--stdio", "--log=x"})
	})
	if got := lastArgs(f); !reflect.DeepEqual(got, []string{"--stdio", "--log=x"}) {
		t.Fatalf("override args %q", got)
	}
}

// TestNoneWinsOverCustomCommand: lang-php.server = none stops the server
// even if a custom command (lang-php.command or the former lsp.command) is
// configured.
func TestNoneWinsOverCustomCommand(t *testing.T) {
	for _, key := range []string{SettingCommand, legacyCommand} {
		t.Run(key, func(t *testing.T) {
			a, m, f, _, bin := pluginApp(t, Options{}, "phpactor")
			if err := settings.Set(a.ProjectSettings(), key, filepath.Join(bin, "phpactor")); err != nil {
				t.Fatal(err)
			}
			var st Status
			modtest.Call(t, a, "lsp.start", nil, &st)
			if !st.Running || st.Server != "custom" {
				t.Fatalf("custom %+v", st)
			}
			n := f.count()
			modtest.Call(t, a, "lsp.disable", nil, &st)
			if st.State != supervisor.StateOff || !strings.Contains(st.Reason, "switched off") || m.current() != nil {
				t.Fatalf("disable with custom command: %+v", st)
			}
			modtest.WantCode(t, a, "lsp.start", nil, api.CodeUnavailable)
			if f.count() != n {
				t.Fatal("custom command started while switched off")
			}
			var doc Doctor
			modtest.Call(t, a, "lsp.doctor", nil, &doc)
			if doc.Resolved != "" || !strings.Contains(doc.Reason, "switched off") {
				t.Fatalf("doctor %+v", doc)
			}
		})
	}
}
