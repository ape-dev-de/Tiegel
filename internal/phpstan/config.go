package phpstan

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// ConfigFiles are the configuration files PHPStan finds itself, in order.
var ConfigFiles = []string{"phpstan.neon", "phpstan.neon.dist", "phpstan.dist.neon"}

// Vars are the values of PHPStan's placeholders as seen from the host.
type Vars struct {
	// RootDir is %rootDir% (the phpstan/phpstan package directory).
	RootDir string
	// WorkDir is %currentWorkingDirectory% (where PHPStan runs).
	WorkDir string
	// TempDir is %sysGetTempDir% ("" when PHPStan runs where the host
	// cannot see its temporary directory, e.g. in a container).
	TempDir string
	// Env looks up %env.NAME% (nil: none).
	Env func(string) (string, bool)
}

// Config is what Tiegel needs from a PHPStan configuration.
type Config struct {
	// File is the main configuration file; Files all files read
	// (includes first).
	File  string
	Files []string
	// TmpDir and ResultCachePath as configured (absolute when they could
	// be resolved, otherwise as written with placeholders); empty when
	// not set.
	TmpDir          string
	ResultCachePath string
	// Ignore are the ignoreErrors entries (baselines included).
	Ignore []*IgnoreRule
	// Warnings are problems that did not stop loading (missing include,
	// unsupported pattern).
	Warnings []string

	vars Vars
}

// maxIncludes bounds the number of configuration files read.
const maxIncludes = 64

// LoadConfig reads file and its includes.
func LoadConfig(file string, vars Vars) (*Config, error) {
	c := &Config{File: file}
	l := &loader{vars: vars, cfg: c, seen: map[string]bool{}}
	params, err := l.load(file, 0)
	if err != nil {
		return nil, err
	}
	if v, ok := params.Get("tmpDir"); ok {
		c.TmpDir = l.path(str(v), filepath.Dir(l.origin["tmpDir"]))
	}
	if v, ok := params.Get("resultCachePath"); ok {
		c.ResultCachePath = str(v)
		if !strings.HasPrefix(c.ResultCachePath, "%tmpDir%") {
			c.ResultCachePath = l.path(c.ResultCachePath, filepath.Dir(l.origin["resultCachePath"]))
		}
	}
	c.vars = vars
	return c, nil
}

// DefaultConfig is the configuration without a file: PHPStan's defaults
// (result cache in %sysGetTempDir%/phpstan).
func DefaultConfig(vars Vars) *Config { return &Config{vars: vars} }

// CacheFile returns where PHPStan writes its result cache: resultCachePath
// (default %tmpDir%/resultCache.php) with tmpDir defaulting to
// %sysGetTempDir%/phpstan. ok is false when the path depends on a value
// unknown on the host (e.g. the temporary directory of a container).
func (c *Config) CacheFile() (path string, ok bool) {
	tmp := c.TmpDir
	if tmp == "" && c.vars.TempDir != "" {
		tmp = filepath.Join(c.vars.TempDir, "phpstan")
	}
	p := c.ResultCachePath
	if p == "" {
		p = "%tmpDir%/resultCache.php"
	}
	if strings.Contains(p, "%tmpDir%") {
		if tmp == "" {
			return p, false
		}
		p = strings.ReplaceAll(p, "%tmpDir%", tmp)
	}
	if strings.Contains(p, "%") {
		return p, false
	}
	return filepath.Clean(p), true
}

type loader struct {
	vars Vars
	cfg  *Config
	seen map[string]bool
	// origin remembers which file set a scalar parameter.
	origin map[string]string
}

// load reads one file and returns its merged parameters (includes first,
// own values win; ignoreErrors are appended).
func (l *loader) load(file string, depth int) (*Array, error) {
	if l.origin == nil {
		l.origin = map[string]string{}
	}
	abs, _ := filepath.Abs(file)
	if l.seen[abs] {
		return &Array{}, nil
	}
	if len(l.seen) >= maxIncludes || depth > 16 {
		return nil, errors.New("phpstan config: too many includes")
	}
	l.seen[abs] = true
	data, err := os.ReadFile(abs)
	if err != nil {
		return nil, fmt.Errorf("phpstan config: %w", err)
	}
	var root any
	dir := filepath.Dir(abs)
	if strings.EqualFold(filepath.Ext(abs), ".php") {
		root, err = parsePHPConfig(data, dir)
	} else {
		root, err = ParseNeon(data)
	}
	if err != nil {
		return nil, fmt.Errorf("phpstan config %s: %w", filepath.Base(abs), err)
	}
	l.cfg.Files = append(l.cfg.Files, abs)
	out := &Array{}
	if inc, ok := field(root, "includes").(*Array); ok {
		for _, v := range inc.Values {
			name := l.expand(str(v))
			if name == "" || strings.Contains(name, "%") {
				continue
			}
			if !filepath.IsAbs(name) {
				name = filepath.Join(dir, name)
			}
			p, err := l.load(name, depth+1)
			if err != nil {
				l.cfg.Warnings = append(l.cfg.Warnings, err.Error())
				continue
			}
			for i, k := range p.Keys {
				out.set(k, p.Values[i])
			}
		}
	}
	params, _ := field(root, "parameters").(*Array)
	if params == nil {
		params = &Array{}
	}
	for i, k := range params.Keys {
		key := str(k)
		switch strings.TrimSuffix(key, "!") {
		case "ignoreErrors":
			l.addIgnores(params.Values[i], abs)
		case "tmpDir", "resultCachePath":
			out.set(strings.TrimSuffix(key, "!"), params.Values[i])
			l.origin[strings.TrimSuffix(key, "!")] = abs
		}
	}
	return out, nil
}

var rePlaceholder = regexp.MustCompile(`%([\w.-]*)%`)

// expand replaces the placeholders known on the host; unknown ones stay.
func (l *loader) expand(s string) string {
	return rePlaceholder.ReplaceAllStringFunc(s, func(m string) string {
		name := m[1 : len(m)-1]
		switch {
		case name == "rootDir" && l.vars.RootDir != "":
			return l.vars.RootDir
		case name == "currentWorkingDirectory" && l.vars.WorkDir != "":
			return l.vars.WorkDir
		case name == "sysGetTempDir" && l.vars.TempDir != "":
			return l.vars.TempDir
		case strings.HasPrefix(name, "env.") && l.vars.Env != nil:
			if v, ok := l.vars.Env(name[4:]); ok {
				return v
			}
		}
		return m
	})
}

// path expands s and makes it absolute relative to dir (PHPStan resolves
// relative paths against the configuration file that sets them).
func (l *loader) path(s, dir string) string {
	s = l.expand(s)
	if s == "" || strings.Contains(s, "%") || filepath.IsAbs(s) || strings.HasPrefix(s, "/") {
		return filepath.Clean(s)
	}
	return filepath.Join(dir, s)
}

func (l *loader) addIgnores(v any, file string) {
	list, ok := v.(*Array)
	if !ok {
		return
	}
	dir := filepath.Dir(file)
	for _, e := range list.Values {
		r, err := newIgnoreRule(e, func(p string) string { return l.path(p, dir) })
		if err != nil {
			l.cfg.Warnings = append(l.cfg.Warnings, fmt.Sprintf("%s: %v", filepath.Base(file), err))
			continue
		}
		r.Source = file
		l.cfg.Ignore = append(l.cfg.Ignore, r)
	}
}

// parsePHPConfig reads a PHP configuration (e.g. the PHP baseline format
// "$ignoreErrors[] = [...]; return ['parameters' => [...]];") without
// running it: array literals assigned to $ignoreErrors[] are collected,
// and a literal returned directly is the configuration.
func parsePHPConfig(src []byte, dir string) (any, error) {
	marker := []byte("$ignoreErrors[] =")
	if bytes.Contains(src, marker) {
		list := &Array{}
		rest := src
		for {
			i := bytes.Index(rest, marker)
			if i < 0 {
				break
			}
			rest = rest[i+len(marker):]
			p := &litParser{b: rest, dir: dir}
			v, err := p.expr(0)
			if err != nil {
				return nil, err
			}
			list.add(v)
			rest = rest[p.pos:]
		}
		params := &Array{}
		params.set("ignoreErrors", list)
		root := &Array{}
		root.set("parameters", params)
		return root, nil
	}
	i := bytes.Index(src, []byte("return"))
	if i < 0 {
		return &Array{}, nil
	}
	p := &litParser{b: src[i+len("return"):], dir: dir}
	return p.expr(0)
}

// FindConfig returns the first of ConfigFiles in dir ("" if none).
func FindConfig(dir string) string {
	for _, n := range ConfigFiles {
		p := filepath.Join(dir, n)
		if st, err := os.Stat(p); err == nil && !st.IsDir() {
			return p
		}
	}
	return ""
}
