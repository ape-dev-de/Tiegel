package servers

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestIsPHPScript(t *testing.T) {
	dir := t.TempDir()
	write := func(name, content string) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte(content), 0o755); err != nil {
			t.Fatal(err)
		}
		return p
	}
	cases := map[string]bool{
		write("tool.phar", "anything"):                      true,
		write("phpactor", "#!/usr/bin/env php\n<?php\n"):    true,
		write("direct", "#!/opt/php/bin/php -d x=1\n"):      true,
		write("versioned", "#!/usr/bin/env php8.3\n"):       true,
		write("shell", "#!/bin/sh\nexec php x\n"):           false,
		write("node", "#!/usr/bin/env node\n"):              false,
		write("binary", "\x7fELF..."):                       false,
		write("phpish", "#!/usr/bin/env phpstan-wrapper\n"): false,
		filepath.Join(dir, "missing"):                       false,
	}
	for p, want := range cases {
		if got := IsPHPScript(p); got != want {
			t.Errorf("%s: %v, want %v", filepath.Base(p), got, want)
		}
	}
}

func TestVersionAtLeast(t *testing.T) {
	for _, c := range []struct {
		v, min string
		want   bool
	}{{"8.5.10", "8.2", true}, {"8.2.0", "8.2", true}, {"8.1.30", "8.2", false}, {"7.4.33", "8.2", false},
		{"8.10.0", "8.9", true}, {"9.0.0-dev", "8.2", true}, {"8.2.0RC1", "8.2", true}} {
		if got := versionAtLeast(c.v, c.min); got != c.want {
			t.Errorf("%s >= %s: %v", c.v, c.min, got)
		}
	}
}

func TestHostPHP(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell script fake php")
	}
	old, oldNew, mid := t.TempDir(), t.TempDir(), t.TempDir()
	fake := func(dir, version string) {
		if err := os.WriteFile(filepath.Join(dir, "php"), []byte("#!/bin/sh\nprintf "+version+"\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	fake(old, "7.4.33")
	fake(mid, "8.1.2")
	fake(oldNew, "8.5.10")
	// The first new enough one in PATH order wins.
	got, err := HostPHP([]string{"", old, filepath.Join(old, "none"), mid, oldNew}, "8.2")
	if err != nil || got != filepath.Join(oldNew, "php") {
		t.Fatalf("got %q %v", got, err)
	}
	// Cached: a second call gives the same answer.
	if again, _ := HostPHP([]string{oldNew}, "8.2"); again != got {
		t.Fatal("cache")
	}
	_, err = HostPHP([]string{old, mid}, "8.2")
	if !errors.Is(err, ErrNoHostPHP) || !strings.Contains(err.Error(), "7.4.33") {
		t.Fatalf("too old: %v", err)
	}
	if _, err := HostPHP(nil, "8.2"); !errors.Is(err, ErrNoHostPHP) {
		t.Fatalf("none: %v", err)
	}
}
