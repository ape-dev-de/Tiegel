package interp

import (
	"context"
	"os/exec"
	"strings"
	"testing"
	"time"
)

func TestParseProbe(t *testing.T) {
	out := []byte("Warning: noise from an entrypoint\n__TIEGEL_PROBE__" +
		`{"version":"8.3.4","sapi":"cli","os":"Linux","binary":"/usr/local/bin/php","extensions":["json","Core","xdebug"],"zendExtensions":["Xdebug","Zend OPcache"],"xdebug":"3.3.1","iniFile":"","iniScanned":["/usr/local/etc/php/conf.d/xdebug.ini"],"ini":{"xdebug.mode":"debug"}}` + "\n")
	info, err := ParseProbe(out)
	if err != nil {
		t.Fatal(err)
	}
	if info.Version != "8.3.4" || info.Xdebug != "3.3.1" || info.Extensions[0] != "Core" || info.Ini["xdebug.mode"] != "debug" ||
		len(info.IniScanned) != 1 || info.IniFile != "" || info.OS != "Linux" {
		t.Fatalf("info %+v", info)
	}
	if !info.HasExtension("XDEBUG") || !info.HasExtension("zend opcache") || info.HasExtension("redis") {
		t.Fatal("HasExtension")
	}
	// Empty ini object from PHP ([] for an empty array is avoided by (object)).
	info, err = ParseProbe([]byte(`__TIEGEL_PROBE__{"version":"8.1.0","extensions":null,"ini":{}}`))
	if err != nil || info.Extensions == nil || len(info.Ini) != 0 {
		t.Fatalf("minimal %+v %v", info, err)
	}
	for _, bad := range []string{"", "8.3.4", "__TIEGEL_PROBE__{", `__TIEGEL_PROBE__{"sapi":"cli"}`} {
		if _, err := ParseProbe([]byte(bad)); err == nil {
			t.Errorf("ParseProbe(%q) accepted", bad)
		}
	}
}

func TestProbeScript(t *testing.T) {
	s := ProbeScript()
	for _, want := range []string{"'xdebug.mode'", "'xdebug.client_port'", probeMarker, "php_ini_loaded_file", "phpversion('xdebug')"} {
		if !strings.Contains(s, want) {
			t.Errorf("script lacks %q", want)
		}
	}
	if strings.Contains(s, "%") {
		t.Error("unformatted verb in script")
	}
}

// TestProbeRealPHP runs the script against a php on PATH (skipped when
// there is none).
func TestProbeRealPHP(t *testing.T) {
	path, err := exec.LookPath("php")
	if err != nil {
		t.Skip("php not installed")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	info, err := Probe(ctx, NewLocal(path))
	if err != nil {
		t.Fatal(err)
	}
	if info.Version == "" || info.SAPI != "cli" || !info.HasExtension("Core") {
		t.Fatalf("info %+v", info)
	}
	if (info.Xdebug != "") != info.HasExtension("xdebug") {
		t.Fatalf("xdebug %q vs extensions %v %v", info.Xdebug, info.Extensions, info.ZendExtensions)
	}
}

func TestProbeError(t *testing.T) {
	if _, err := Probe(context.Background(), NewLocal("/nonexistent/php")); err == nil {
		t.Fatal("missing binary accepted")
	}
}
