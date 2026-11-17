package debugxdebug

import (
	"encoding/json"
	"net"
	"strconv"
	"strings"
	"testing"

	"github.com/ape-dev-de/tiegel/internal/api"
	"github.com/ape-dev-de/tiegel/internal/modules/modtest"
)

func TestPortSetting(t *testing.T) {
	a := newApp(t, "")
	modtest.Project(t, a, map[string]string{"index.php": "<?php\n"})
	// A free port.
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Skipf("no loopback: %v", err)
	}
	port := l.Addr().(*net.TCPAddr).Port
	l.Close()
	modtest.WantCode(t, a, "settings.set", api.SettingsSetParams{Key: SettingPort, Scope: "project", Value: json.RawMessage(`70000`)}, api.CodeInvalidParams)
	modtest.Call(t, a, "settings.set", api.SettingsSetParams{Key: SettingPort, Scope: "project", Value: json.RawMessage(strconv.Itoa(port))}, nil)
	var res DebugListenResult
	modtest.Call(t, a, "debug.listen", DebugListenParams{}, &res)
	defer modtest.Call(t, a, "debug.stop", nil, nil)
	if !res.Listening || !strings.HasSuffix(res.Addr, ":"+strconv.Itoa(port)) || !strings.HasPrefix(res.Addr, "127.0.0.1:") {
		t.Fatalf("listen %+v (want port %d)", res, port)
	}
}
