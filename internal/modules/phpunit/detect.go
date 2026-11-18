package phpunit

import (
	"context"
	"encoding/json"
	"encoding/xml"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/ape-dev-de/tiegel/internal/api"
)

// configNames are the configuration files PHPUnit looks for, in order.
var configNames = []string{"phpunit.xml", "phpunit.xml.dist", "phpunit.dist.xml"}

// project is what the plugin found in a project root.
type project struct {
	Root string
	// Executable is the absolute path of the phpunit script ("" = not
	// found), Parallel the one of paratest.
	Executable string
	Parallel   string
	Version    string
	// Config is the absolute path of the configuration file.
	Config string
	Suites []suite
	// BinDir is composer's bin directory (absolute).
	BinDir string
}

// suite is a <testsuite> of the configuration.
type suite struct {
	Name     string
	Dirs     []string // absolute
	Files    []string // absolute
	Suffixes []string
	Excludes []string // absolute
}

// detect inspects root with the current settings.
func (m *Module) detect(root string) project {
	p := project{Root: root, BinDir: binDir(root)}
	if exe := strings.TrimSpace(setting(m, SettingExecutable, "")); exe != "" {
		p.Executable = abs(root, exe)
		if !isFile(p.Executable) {
			p.Executable = ""
		}
	} else if f := filepath.Join(p.BinDir, "phpunit"); isFile(f) {
		p.Executable = f
	}
	if f := filepath.Join(p.BinDir, "paratest"); isFile(f) {
		p.Parallel = f
	}
	if cfg := strings.TrimSpace(setting(m, SettingConfig, "")); cfg != "" {
		if f := abs(root, cfg); isFile(f) {
			p.Config = f
		}
	} else {
		for _, n := range configNames {
			if f := filepath.Join(root, n); isFile(f) {
				p.Config = f
				break
			}
		}
	}
	if p.Config != "" {
		p.Suites = readSuites(p.Config)
	}
	p.Version = installedVersion(root, "phpunit/phpunit")
	return p
}

// testDirs are the directories that hold tests: those of the
// configuration's suites, else tests/ or test/ if present.
func (p project) testDirs() []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range p.Suites {
		for _, d := range s.Dirs {
			if !seen[d] && isDir(d) {
				seen[d] = true
				out = append(out, d)
			}
		}
	}
	if len(out) == 0 {
		for _, n := range []string{"tests", "test"} {
			if d := filepath.Join(p.Root, n); isDir(d) {
				out = append(out, d)
			}
		}
	}
	return out
}

func (m *Module) status(_ context.Context, _ struct{}) (api.TestStatus, error) {
	root, err := m.root()
	if err != nil {
		return api.TestStatus{}, err
	}
	p := m.detect(root)
	st := api.TestStatus{Framework: Framework, Available: p.Executable != "", Version: p.Version}
	if p.Executable != "" {
		st.Executable = rel(root, p.Executable)
	}
	if p.Parallel != "" && setting(m, SettingParallel, false) {
		st.Parallel = rel(root, p.Parallel)
	}
	if p.Config != "" {
		st.Config = rel(root, p.Config)
	}
	for _, s := range p.Suites {
		st.Suites = append(st.Suites, s.Name)
	}
	for _, d := range p.testDirs() {
		st.TestDirs = append(st.TestDirs, rel(root, d))
	}
	if !st.Available {
		if exe := strings.TrimSpace(setting(m, SettingExecutable, "")); exe != "" {
			st.Hint = "The PHPUnit script " + exe + " (setting " + SettingExecutable + ") does not exist."
		} else {
			st.Hint = "PHPUnit is not installed in this project (" + rel(root, filepath.Join(p.BinDir, "phpunit")) + " is missing). Install it as dev dependency with Composer."
			st.Install = ID
		}
	}
	return st, nil
}

// root returns the open project root.
func (m *Module) root() (string, error) {
	m.mu.Lock()
	app := m.app
	m.mu.Unlock()
	return app.RequireProject()
}

// binDir returns composer's bin directory (config.bin-dir, else
// vendor-dir/bin).
func binDir(root string) string {
	var cj struct {
		Config struct {
			BinDir    string `json:"bin-dir"`
			VendorDir string `json:"vendor-dir"`
		} `json:"config"`
	}
	if b, err := os.ReadFile(filepath.Join(root, "composer.json")); err == nil {
		_ = json.Unmarshal(b, &cj)
	}
	if cj.Config.BinDir != "" {
		return abs(root, cj.Config.BinDir)
	}
	vendor := "vendor"
	if cj.Config.VendorDir != "" {
		vendor = cj.Config.VendorDir
	}
	return filepath.Join(abs(root, vendor), "bin")
}

// installedVersion reads the version of pkg from vendor/composer/
// installed.json (Composer 1 and 2 formats); "" when unknown.
func installedVersion(root, pkg string) string {
	b, err := os.ReadFile(filepath.Join(root, "vendor", "composer", "installed.json"))
	if err != nil {
		return ""
	}
	type entry struct {
		Name    string `json:"name"`
		Version string `json:"version"`
	}
	var v2 struct {
		Packages []entry `json:"packages"`
	}
	var list []entry
	if json.Unmarshal(b, &v2) == nil && v2.Packages != nil {
		list = v2.Packages
	} else if json.Unmarshal(b, &list) != nil {
		return ""
	}
	for _, e := range list {
		if strings.EqualFold(e.Name, pkg) {
			return strings.TrimPrefix(e.Version, "v")
		}
	}
	return ""
}

// readSuites reads the <testsuite> elements of a configuration file.
func readSuites(cfg string) []suite {
	b, err := os.ReadFile(cfg)
	if err != nil {
		return nil
	}
	var doc struct {
		Suites []struct {
			Name string `xml:"name,attr"`
			Dirs []struct {
				Suffix string `xml:"suffix,attr"`
				Path   string `xml:",chardata"`
			} `xml:"directory"`
			Files    []string `xml:"file"`
			Excludes []string `xml:"exclude"`
		} `xml:"testsuites>testsuite"`
	}
	if xml.Unmarshal(b, &doc) != nil {
		return nil
	}
	base := filepath.Dir(cfg)
	out := make([]suite, 0, len(doc.Suites))
	for _, s := range doc.Suites {
		su := suite{Name: s.Name}
		for _, d := range s.Dirs {
			if p := strings.TrimSpace(d.Path); p != "" {
				su.Dirs = append(su.Dirs, abs(base, p))
				suffix := strings.TrimSpace(d.Suffix)
				if suffix == "" {
					suffix = "Test.php"
				}
				su.Suffixes = append(su.Suffixes, suffix)
			}
		}
		for _, f := range s.Files {
			if f = strings.TrimSpace(f); f != "" {
				su.Files = append(su.Files, abs(base, f))
			}
		}
		for _, e := range s.Excludes {
			if e = strings.TrimSpace(e); e != "" {
				su.Excludes = append(su.Excludes, abs(base, e))
			}
		}
		out = append(out, su)
	}
	return out
}

// suiteOf returns the names of the suites that contain file.
func (p project) suiteOf(file string) []string {
	var out []string
	for _, s := range p.Suites {
		if s.contains(file) {
			out = append(out, s.Name)
		}
	}
	sort.Strings(out)
	return out
}

func (s suite) contains(file string) bool {
	for _, e := range s.Excludes {
		if within(e, file) {
			return false
		}
	}
	for _, f := range s.Files {
		if f == file {
			return true
		}
	}
	for _, d := range s.Dirs {
		if within(d, file) {
			return true
		}
	}
	return false
}

// within reports whether p is dir or below it.
func within(dir, p string) bool {
	r, err := filepath.Rel(dir, p)
	return err == nil && r != ".." && !strings.HasPrefix(r, ".."+string(filepath.Separator))
}

func abs(root, p string) string {
	if filepath.IsAbs(p) {
		return filepath.Clean(p)
	}
	return filepath.Join(root, filepath.FromSlash(p))
}

// rel returns p relative to root with slashes when it lies below it.
func rel(root, p string) string {
	if within(root, p) {
		if r, err := filepath.Rel(root, p); err == nil {
			return filepath.ToSlash(r)
		}
	}
	return p
}

func isFile(p string) bool {
	st, err := os.Stat(p)
	return err == nil && !st.IsDir()
}

func isDir(p string) bool {
	st, err := os.Stat(p)
	return err == nil && st.IsDir()
}
