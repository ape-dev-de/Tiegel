package qakit

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"
)

// Windows command building, independent of the system running the test
// (review wave 7, #5: batch files get no repository file names).
func TestToolCommandWindows(t *testing.T) {
	files := map[string]bool{ // file → is a PHP script
		"C:/proj/vendor/bin/pint":        true,
		"C:/proj/vendor/bin/pint.bat":    false,
		`C:\tools\fixer.bat`:             false,
		`C:\composer\bin\phpstan`:        true,
		`C:\composer\bin\phpstan.bat`:    false,
		"C:/proj/vendor/bin/sh-tool":     false, // a shell proxy, not PHP
		"C:/proj/vendor/bin/sh-tool.bat": false,
	}
	// Paths compare separator-neutrally: filepath.Join yields "\" on
	// Windows and "/" elsewhere.
	norm := func(p string) string { return strings.ReplaceAll(p, `\`, "/") }
	known := map[string]bool{}
	for k, v := range files {
		known[norm(k)] = v
	}
	fsys := hostFS{
		Look: func(name string) (string, error) {
			switch name {
			case "phpstan":
				return `C:\composer\bin\phpstan.bat`, nil
			case "fixer":
				return `C:\tools\fixer.bat`, nil
			case "tool":
				return `C:\tools\tool.exe`, nil
			}
			return "", errors.New("not found")
		},
		IsFile: func(p string) bool { _, ok := known[norm(p)]; return ok },
		IsPHP:  func(p string) bool { return known[norm(p)] },
	}
	evil := "src/a&calc&.php"
	cases := []struct {
		name, program string
		args          []string
		want          toolCmd
		err           string
	}{
		// Composer's proxy script runs with php, the .bat is never used.
		{"vendor script", "vendor/bin/pint", []string{evil}, toolCmd{PHP: true, Args: []string{"vendor/bin/pint", evil}}, ""},
		{"explicit bat with script", "vendor/bin/pint.bat", []string{evil}, toolCmd{PHP: true, Args: []string{"C:/proj/vendor/bin/pint", evil}}, ""},
		{"bare name in PATH", "phpstan", []string{evil}, toolCmd{PHP: true, Args: []string{`C:\composer\bin\phpstan`, evil}}, ""},
		{"phar", `C:\tools\php-cs-fixer.phar`, []string{evil}, toolCmd{PHP: true, Args: []string{`C:\tools\php-cs-fixer.phar`, evil}}, ""},
		// A batch file without PHP script: only harmless arguments.
		{"bat refuses &", "fixer", []string{"fix", evil}, toolCmd{}, "batch file"},
		{"bat refuses %", `C:\tools\fixer.bat`, []string{"src/%PATH%.php"}, toolCmd{}, "batch file"},
		{"bat refuses quote", `C:\tools\fixer.bat`, []string{`a".php`}, toolCmd{}, "batch file"},
		{"bat plain args", `C:\tools\fixer.bat`, []string{"fix", "src/My File.php"}, toolCmd{Program: `C:\tools\fixer.bat`, Args: []string{"fix", "src/My File.php"}}, ""},
		{"shell proxy resolves to bat", "vendor/bin/sh-tool", []string{evil}, toolCmd{}, "batch file"},
		{"exe unchanged", "tool", []string{evil}, toolCmd{Program: "tool", Args: []string{evil}}, ""},
		{"unknown left to run", "missing", []string{evil}, toolCmd{Program: "missing", Args: []string{evil}}, ""},
	}
	for _, c := range cases {
		got, err := toolCommand("windows", true, "C:/proj", c.program, c.args, fsys)
		if c.err != "" {
			if err == nil || !strings.Contains(err.Error(), c.err) {
				t.Errorf("%s: err %v, want %q", c.name, err, c.err)
			}
			continue
		}
		normAll := func(l []string) []string {
			out := make([]string, len(l))
			for i, a := range l {
				out[i] = norm(a)
			}
			return out
		}
		if err != nil || norm(got.Program) != norm(c.want.Program) || got.PHP != c.want.PHP || !slices.Equal(normAll(got.Args), normAll(c.want.Args)) {
			t.Errorf("%s: %+v %v, want %+v", c.name, got, err, c.want)
		}
	}
	// Containers, WSL and other systems: unchanged.
	for _, goos := range []string{"linux", "darwin"} {
		if got, _ := toolCommand(goos, true, "/p", "vendor/bin/pint", []string{evil}, fsys); got.PHP || got.Program != "vendor/bin/pint" {
			t.Errorf("%s: %+v", goos, got)
		}
	}
	if got, _ := toolCommand("windows", false, "C:/proj", "fixer", []string{evil}, fsys); got.PHP || got.Program != "fixer" {
		t.Errorf("container: %+v", got)
	}
}

// The formatter's copy keeps the base name and the relative path (review
// wave 7, #1) and leaves nothing behind.
func TestFormatCopy(t *testing.T) {
	root := t.TempDir()
	file := filepath.Join(root, "src", "Service", "UserService.php")
	if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	// Host run: private directory outside the project.
	c, err := newFormatCopy(root, file, []byte("new"), true)
	if err != nil {
		t.Fatal(err)
	}
	if within(root, c.path) || !strings.HasSuffix(filepath.ToSlash(c.path), "/src/Service/UserService.php") || c.arg != c.path {
		t.Fatalf("private copy %+v", c)
	}
	if b, _ := os.ReadFile(c.path); string(b) != "new" {
		t.Fatalf("content %q", b)
	}
	if st, _ := os.Stat(c.path); runtime.GOOS != "windows" && st.Mode().Perm() != 0o600 {
		t.Fatalf("mode %v", st.Mode())
	}
	c.remove()
	if _, err := os.Stat(c.dir); !os.IsNotExist(err) {
		t.Fatalf("private dir left: %v", err)
	}
	// Container run: hidden directory in the project, relative argument.
	c, err = newFormatCopy(root, file, []byte("new"), false)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(c.arg, copyDirPrefix) || !strings.HasSuffix(c.arg, "/src/Service/UserService.php") || filepath.Join(root, filepath.FromSlash(c.arg)) != c.path {
		t.Fatalf("project copy %+v", c)
	}
	c.remove()
	entries, _ := os.ReadDir(root)
	if len(entries) != 1 || entries[0].Name() != "src" {
		t.Fatalf("left in the project: %v", entries)
	}
	// Errors leave nothing either.
	if _, err := newFormatCopy(root, filepath.Join(t.TempDir(), "x.php"), nil, false); err == nil {
		t.Fatal("outside the project")
	}
	// The relative path cannot be created after the copy directory was.
	for _, private := range []bool{false, true} {
		if c, err := newFormatCopy(root, filepath.Join(root, "bad\x00dir", "x.php"), nil, private); err == nil {
			c.remove()
			t.Fatal("invalid path copied")
		}
	}
	entries, _ = os.ReadDir(root)
	if len(entries) != 1 {
		t.Fatalf("left after error: %v", entries)
	}
	// Leftovers of a crash are removed by the next run, fresh ones kept.
	old := filepath.Join(root, copyDirPrefix+"old")
	fresh := filepath.Join(root, copyDirPrefix+"fresh")
	for _, d := range []string{old, fresh} {
		if err := os.Mkdir(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	past := time.Now().Add(-3 * time.Hour)
	_ = os.Chtimes(old, past, past)
	removeStaleCopies(root, time.Now())
	if _, err := os.Stat(old); !os.IsNotExist(err) {
		t.Fatal("stale copy kept")
	}
	if _, err := os.Stat(fresh); err != nil {
		t.Fatal("fresh copy removed")
	}
}
