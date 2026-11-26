package servers

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"
)

// PHP scripts as servers (D548): a PHAR such as phpactor.phar starts with
// "#!/usr/bin/env php", which fails when the GUI runs with a minimal PATH
// ("env: php: no such file or directory") or picks an old PHP. Tiegel runs
// such servers explicitly with a host PHP that is new enough. The
// project's PHP version (7.4, a container, …) does not matter here: it is
// passed to the server as an option; this PHP only runs the tool.

// IsPHPScript reports whether path is a PHP script or PHAR (by extension
// or a shebang naming php).
func IsPHPScript(path string) bool {
	if strings.EqualFold(filepath.Ext(path), ".phar") {
		return true
	}
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()
	line, err := bufio.NewReader(f).ReadString('\n')
	if err != nil && line == "" {
		return false
	}
	if !strings.HasPrefix(line, "#!") {
		return false
	}
	fields := strings.Fields(strings.TrimPrefix(line, "#!"))
	for _, f := range fields {
		if b := filepath.Base(f); b == "php" || strings.HasPrefix(b, "php") && isDigits(strings.TrimPrefix(strings.TrimPrefix(b, "php"), "-")) {
			return true
		}
	}
	return false
}

func isDigits(s string) bool {
	s = strings.ReplaceAll(s, ".", "")
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// ErrNoHostPHP is returned when no PHP new enough to run a server exists.
var ErrNoHostPHP = errors.New("no local PHP to run the server")

// phpVersionOf runs php to read its version (cached per path and mtime).
var phpVersionOf = func(php string) (string, error) {
	st, err := os.Stat(php)
	if err != nil {
		return "", err
	}
	key := php + "|" + st.ModTime().String()
	versionMu.Lock()
	v, ok := versionCache[key]
	versionMu.Unlock()
	if ok {
		return v, nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, php, "-n", "-r", "echo PHP_VERSION;").Output()
	if err != nil {
		return "", fmt.Errorf("servers: %s: %w", php, err)
	}
	v = strings.TrimSpace(string(out))
	versionMu.Lock()
	versionCache[key] = v
	versionMu.Unlock()
	return v, nil
}

var (
	versionMu    sync.Mutex
	versionCache = map[string]string{}
)

// HostPHP returns the first php executable in dirs (PATH order) whose
// version is at least minVersion ("8.2").
func HostPHP(dirs []string, minVersion string) (string, error) {
	name := "php"
	if runtime.GOOS == "windows" {
		name = "php.exe"
	}
	var seen []string
	for _, d := range dirs {
		if d == "" {
			continue
		}
		p := filepath.Join(d, name)
		st, err := os.Stat(p)
		if err != nil || st.IsDir() || (runtime.GOOS != "windows" && st.Mode()&0o111 == 0) {
			continue
		}
		v, err := phpVersionOf(p)
		if err != nil {
			continue
		}
		if versionAtLeast(v, minVersion) {
			return p, nil
		}
		seen = append(seen, p+" ("+v+")")
	}
	if len(seen) > 0 {
		return "", fmt.Errorf("%w: needs PHP %s or newer, found %s", ErrNoHostPHP, minVersion, strings.Join(seen, ", "))
	}
	return "", fmt.Errorf("%w: needs PHP %s or newer, none found", ErrNoHostPHP, minVersion)
}

// versionAtLeast compares dotted versions numerically ("8.10" > "8.9").
func versionAtLeast(v, min string) bool {
	vs, ms := strings.Split(v, "."), strings.Split(min, ".")
	for i := range ms {
		a, b := 0, 0
		if i < len(vs) {
			a, _ = strconv.Atoi(strings.TrimRightFunc(vs[i], func(r rune) bool { return r < '0' || r > '9' }))
		}
		b, _ = strconv.Atoi(ms[i])
		if a != b {
			return a > b
		}
	}
	return true
}
