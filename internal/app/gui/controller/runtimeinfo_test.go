package controller

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/ape-dev-de/tiegel/internal/api"
)

func TestInterpreterProbe(t *testing.T) {
	f := projectFake()
	c := started(t, f)
	f.answer("interp.list", []Interpreter{{ID: "docker:php:8.3-cli", Kind: "docker", Image: "php:8.3-cli", Context: "orbstack", Selected: true}})
	f.answer("modules.list", []ModuleInfo{})
	f.answer("runtime.mappings.get", Mappings{})
	f.answer("runtime.diagnose", Diagnosis{})
	probes := 0
	f.on("interp.probe", func(raw json.RawMessage) (any, error) {
		var p map[string]any
		_ = json.Unmarshal(raw, &p)
		if p["cachedOnly"] == true {
			return ProbeResult{ID: "docker:php:8.3-cli", Info: ProbeInfo{Version: "8.3.1"}, Cached: true}, nil
		}
		probes++
		if probes > 1 {
			return nil, api.Errorf(api.CodeUnavailable, "engine down")
		}
		if p["refresh"] != true || p["start"] != true {
			t.Errorf("refresh params %v", p)
		}
		return ProbeResult{ID: "docker:php:8.3-cli", Info: ProbeInfo{Version: "8.3.2", Xdebug: "3.3.2"}, ProbedAt: time.Now()}, nil
	})
	c.LoadSettingsData()
	c.Settle(time.Second)
	if c.Interpreters[0].Context != "orbstack" || c.Probe.ID != "docker:php:8.3-cli" || c.Probe.Result == nil || !c.Probe.Result.Cached {
		t.Fatalf("loaded %+v %+v", c.Interpreters, c.Probe)
	}
	f.mu.Lock()
	listParams := string(f.params["interp.list"][len(f.params["interp.list"])-1])
	f.mu.Unlock()
	if !strings.Contains(listParams, `"status":true`) {
		t.Fatalf("interp.list params %s", listParams)
	}
	c.RefreshProbe("docker:php:8.3-cli")
	if !c.Probe.Busy {
		t.Fatal("not busy")
	}
	c.Settle(time.Second)
	if c.Probe.Busy || c.Probe.Result.Info.Xdebug != "3.3.2" || c.Probe.Err != "" {
		t.Fatalf("refreshed %+v", c.Probe)
	}
	c.RefreshProbe("docker:php:8.3-cli")
	c.Settle(time.Second)
	if !strings.Contains(c.Probe.Err, "engine down") || c.Probe.Result == nil {
		t.Fatalf("error keeps last result %+v", c.Probe)
	}
	// No selection: no probe state.
	f.answer("interp.list", []Interpreter{{ID: "local:/usr/bin/php"}})
	c.LoadSettingsData()
	c.Settle(time.Second)
	if c.Probe.ID != "" {
		t.Fatalf("probe without selection %+v", c.Probe)
	}
}
