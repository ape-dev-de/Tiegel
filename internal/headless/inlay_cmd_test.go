package headless

import (
	"testing"
)

func TestInlayHintsCommand(t *testing.T) {
	t.Parallel()
	fake := buildFakeLSP(t)
	dir := project(t, map[string]string{"h.php": "<?php\ncall($a);\n$typed = 1;\n"})
	code, out, errs := run(t, "", "inlay-hints", "h.php", "--command", fake, "--project", dir)
	if code != ExitOK || out != "h.php:2:6: [parameter] arg:\nh.php:3:7: [type] : int\n" {
		t.Fatalf("%d %q %q", code, out, errs)
	}
}
