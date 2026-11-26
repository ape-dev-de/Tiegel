package phpstanresults

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/ape-dev-de/tiegel/internal/api"
	"github.com/ape-dev-de/tiegel/internal/compose"
	"github.com/ape-dev-de/tiegel/internal/core/tasks"
	"github.com/ape-dev-de/tiegel/internal/core/toolfind"
	"github.com/ape-dev-de/tiegel/internal/phpstan"
	"github.com/ape-dev-de/tiegel/internal/run"
)

// state is the store of one project.
type state struct {
	root    string
	sources []api.AnalysisSource
	used    *api.AnalysisSource
	config  string
	// usedMod/usedSize identify the loaded file version (ifChanged).
	usedMod  time.Time
	usedSize int64
	// imported is a file chosen with phpstan.import.
	imported string
	// raw are the findings per absolute host file as read (Line,
	// Message, Identifier, Tip); files the current view with ranges and
	// stale marks (refreshFiles).
	raw   map[string][]api.AnalysisFinding
	files map[string][]api.AnalysisFinding
	// runs are findings of Tiegel's own PHPStan runs per file (newer than
	// the source; an empty list: the run found nothing).
	runs     map[string]runEntry
	messages []string
	err      string
	loaded   time.Time
	version  uint64
}

type runEntry struct {
	at       time.Time
	findings []api.AnalysisFinding
}

// loadOpts select what load does.
type loadOpts struct {
	ifChanged bool
}

// load determines the sources and reads the one in use (unless it did
// not change and ifChanged is set). It publishes phpstan.changed when
// something changed.
func (m *Module) load(ctx context.Context, o loadOpts) (api.AnalysisStatus, error) {
	app := m.application()
	if app == nil {
		return api.AnalysisStatus{}, errors.New("phpstan: not started")
	}
	m.loadMu.Lock()
	defer m.loadMu.Unlock()
	root := app.ProjectRoot()
	if root == "" {
		m.clear()
		return m.statusNow(ctx), nil
	}
	m.mu.Lock()
	if m.st.root != root {
		v := m.st.version
		m.st = state{root: root, version: v}
	}
	imported := m.st.imported
	prevUsed, prevMod, prevSize := m.st.used, m.st.usedMod, m.st.usedSize
	m.mu.Unlock()

	env := m.environment(ctx, app, root)
	sources, cfgFile, cfg := m.discover(env, imported)
	var used *api.AnalysisSource
	for i := range sources {
		if sources[i].Used {
			used = &sources[i]
			break
		}
	}
	var mod time.Time
	var size int64
	if used != nil {
		if st, err := os.Stat(used.File); err == nil {
			mod, size = st.ModTime(), st.Size()
		}
	}
	if o.ifChanged && sameSource(prevUsed, used) && mod.Equal(prevMod) && size == prevSize {
		return m.statusNow(ctx), nil
	}
	var raw map[string][]api.AnalysisFinding
	var msgs []string
	var loadErr error
	if used != nil {
		task := app.BeginTask(ctx, tasks.Options{Title: "Reading PHPStan results", Module: ID, Text: used.Path})
		raw, msgs, loadErr = read(used, env, cfg)
		task.End(nil)
	}
	if cfg != nil {
		msgs = append(msgs, cfg.Warnings...)
	}
	m.mu.Lock()
	if m.st.root != root {
		m.mu.Unlock()
		return m.statusNow(ctx), nil // the project changed meanwhile
	}
	m.st.sources, m.st.used, m.st.config = sources, used, cfgFile
	m.st.usedMod, m.st.usedSize = mod, size
	m.st.raw, m.st.messages, m.st.err = raw, msgs, ""
	if loadErr != nil {
		m.st.err = loadErr.Error()
	}
	m.st.loaded = time.Now()
	// Runs older than the source are part of it now.
	for f, r := range m.st.runs {
		if !r.at.After(mod) {
			delete(m.st.runs, f)
		}
	}
	m.mu.Unlock()
	m.refreshFiles(nil)
	st := m.statusNow(ctx)
	if loadErr != nil {
		return st, api.Errorf(api.CodeFailed, "%s: %v", used.Path, loadErr).WithData(st)
	}
	return st, nil
}

func sameSource(a, b *api.AnalysisSource) bool {
	if a == nil || b == nil {
		return a == b
	}
	return a.File == b.File && a.Kind == b.Kind
}

// env is what loading needs to know about the project.
type env struct {
	root, phpRoot string
	// anchor is PHPStan's %rootDir% on the host (vendor/phpstan/phpstan).
	anchor   string
	mappings compose.Mappings
	onHost   bool
	display  func(string) string
}

func (m *Module) environment(ctx context.Context, app *api.App, root string) env {
	e := env{root: root, phpRoot: app.PHPRoot(), onHost: true, display: app.DisplayPath}
	if e.phpRoot == "" {
		e.phpRoot = root
	}
	vendor := filepath.Join(e.phpRoot, "vendor")
	if cp, ok := toolfind.ReadComposer(e.phpRoot); ok {
		vendor = cp.VendorDir
	}
	e.anchor = filepath.Join(vendor, "phpstan", "phpstan")
	tctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if t, err := app.RunTarget(tctx, run.InterpreterProject); err == nil {
		e.mappings, e.onHost = t.PathMappings(), t.OnHost()
	}
	return e
}

func (e env) toHost(p string) (string, bool) {
	if len(e.mappings) == 0 {
		return "", false
	}
	return e.mappings.ToHost(p)
}

// reportNames are conventional report locations (relative to the PHP
// root and the project root); the candidates are listed, never used
// without the user's choice.
var reportDirs = []string{"build", "build/logs", "build/reports", "var", "var/reports", "reports", "report", "tmp", "."}

type foundReport struct {
	path, rel, format string
	mod               time.Time
}

// findReports lists PHPStan reports in conventional places: files named
// phpstan*.json/.xml whose content is a PHPStan report.
func findReports(root, phpRoot string) []foundReport {
	if root == "" {
		return nil
	}
	if phpRoot == "" {
		phpRoot = root
	}
	seen := map[string]bool{}
	var out []foundReport
	for _, base := range []string{phpRoot, root} {
		for _, d := range reportDirs {
			ents, err := os.ReadDir(filepath.Join(base, d))
			if err != nil {
				continue
			}
			for _, de := range ents {
				n := strings.ToLower(de.Name())
				if de.IsDir() || !strings.Contains(n, "phpstan") || !(strings.HasSuffix(n, ".json") || strings.HasSuffix(n, ".xml")) {
					continue
				}
				p := filepath.Join(base, d, de.Name())
				if seen[p] {
					continue
				}
				seen[p] = true
				f, err := os.Open(p)
				if err != nil {
					continue
				}
				head := make([]byte, 2048)
				n2, _ := f.Read(head)
				f.Close()
				format := phpstan.DetectFormat(head[:n2])
				if format == "" || format == phpstan.FormatCache {
					continue
				}
				info, _ := de.Info()
				rel, err := filepath.Rel(root, p)
				if err != nil || strings.HasPrefix(rel, "..") {
					rel = p
				}
				fr := foundReport{path: p, rel: filepath.ToSlash(rel), format: format}
				if info != nil {
					fr.mod = info.ModTime()
				}
				out = append(out, fr)
			}
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].mod.After(out[j].mod) })
	return out
}

// discover lists the sources: an imported file, the configured report,
// the result cache, and reports found in conventional places. The first
// usable of imported, setting and cache is used.
func (m *Module) discover(e env, imported string) ([]api.AnalysisSource, string, *phpstan.Config) {
	var out []api.AnalysisSource
	add := func(s api.AnalysisSource) {
		for _, o := range out {
			if o.File != "" && o.File == s.File {
				return
			}
		}
		if s.File != "" {
			if st, err := os.Stat(s.File); err == nil && !st.IsDir() {
				s.Exists, s.Modified = true, st.ModTime()
			} else if s.Reason == "" {
				s.Reason = "not found"
			}
		}
		if s.Path == "" && s.File != "" {
			s.Path = e.display(s.File)
		}
		out = append(out, s)
	}
	abs := func(p string) string {
		if filepath.IsAbs(p) {
			return filepath.Clean(p)
		}
		return filepath.Join(e.root, p)
	}
	if imported != "" {
		add(api.AnalysisSource{Kind: kindOf(imported), File: imported, Origin: "import"})
	}
	if rf := strings.TrimSpace(setting(m, SettingReportFile, "")); rf != "" {
		add(api.AnalysisSource{Kind: kindOf(abs(rf)), File: abs(rf), Origin: "setting"})
	}
	// The result cache of the PHPStan configuration.
	cfgFile := strings.TrimSpace(setting(m, SettingConfig, ""))
	if cfgFile == "" {
		cfgFile = strings.TrimSpace(setting(m, "qa-phpstan.config", ""))
	}
	if cfgFile != "" {
		cfgFile = abs(cfgFile)
	} else {
		cfgFile = phpstan.FindConfig(e.phpRoot)
		if cfgFile == "" && e.phpRoot != e.root {
			cfgFile = phpstan.FindConfig(e.root)
		}
	}
	vars := phpstan.Vars{RootDir: e.anchor, WorkDir: e.phpRoot, Env: func(string) (string, bool) { return "", false }}
	if e.onHost {
		vars.TempDir = filepath.Clean(os.TempDir())
	}
	var cfg *phpstan.Config
	cache := api.AnalysisSource{Kind: api.AnalysisSourceCache, Format: phpstan.FormatCache, Origin: "config"}
	if cfgFile != "" {
		c, err := phpstan.LoadConfig(cfgFile, vars)
		if err != nil {
			cache.Reason = err.Error()
		} else {
			cfg = c
		}
	} else {
		cache.Origin = "default"
		cfg = phpstan.DefaultConfig(vars)
	}
	if cfg != nil {
		p, ok := cfg.CacheFile()
		switch {
		case ok && fileExists(p):
			cache.File = p
		case ok:
			// A container path (tmpDir: /tmp/phpstan) visible through a
			// mapping, or simply not written yet.
			h, mapped := e.toHost(filepath.ToSlash(p))
			switch {
			case mapped:
				cache.File = h
			case !e.onHost && !within(e.root, p):
				cache.Path = p
				cache.Reason = "the result cache is inside the container (" + p + ") – set parameters.tmpDir to a folder in the project (e.g. var/phpstan) to share it"
			default:
				cache.File = p
			}
		default:
			cache.Path = p
			cache.Reason = "PHPStan runs in the project interpreter (container): its temporary directory is not visible – set parameters.tmpDir to a folder in the project (e.g. var/phpstan) to share the result cache"
		}
	}
	if !setting(m, SettingUseCache, true) && cache.Reason == "" {
		cache.Reason = "reading the result cache is off (" + SettingUseCache + ")"
	}
	add(cache)
	for _, f := range findReports(e.root, e.phpRoot) {
		add(api.AnalysisSource{Kind: api.AnalysisSourceReport, File: f.path, Path: f.rel, Format: f.format, Origin: "found"})
	}
	// The first usable of import, setting and cache.
	for i := range out {
		s := &out[i]
		if s.Origin == "found" || !s.Exists {
			continue
		}
		if s.Kind == api.AnalysisSourceCache && s.Origin != "import" && !setting(m, SettingUseCache, true) {
			continue
		}
		s.Used = true
		break
	}
	return out, cfgFile, cfg
}

func fileExists(p string) bool {
	st, err := os.Stat(p)
	return err == nil && !st.IsDir()
}

// kindOf tells a cache from a report by name and content.
func kindOf(file string) string {
	f, err := os.Open(file)
	if err == nil {
		head := make([]byte, 4096)
		n, _ := f.Read(head)
		f.Close()
		if phpstan.DetectFormat(head[:n]) == phpstan.FormatCache {
			return api.AnalysisSourceCache
		}
		return api.AnalysisSourceReport
	}
	if strings.EqualFold(filepath.Ext(file), ".php") {
		return api.AnalysisSourceCache
	}
	return api.AnalysisSourceReport
}

// read parses the source and returns the findings per host file.
func read(src *api.AnalysisSource, e env, cfg *phpstan.Config) (map[string][]api.AnalysisFinding, []string, error) {
	var errs []phpstan.Raw
	var msgs []string
	res := phpstan.Resolver{Base: e.phpRoot, ToHost: e.toHost}
	if src.Kind == api.AnalysisSourceCache {
		c, err := phpstan.ReadResultCache(src.File)
		if err != nil {
			return nil, nil, err
		}
		src.Format, src.Version = phpstan.FormatCache, c.Version
		res.Base = e.anchor
		errs = resolveAll(c.Errors, res)
		if cfg != nil {
			errs, msgs = phpstan.Ignore(errs, cfg.Ignore)
		}
	} else {
		data, err := os.ReadFile(src.File)
		if err != nil {
			return nil, nil, fmt.Errorf("report: %w", err)
		}
		r, err := phpstan.ParseReport(data)
		if err != nil {
			return nil, nil, err
		}
		src.Format = r.Format
		errs, msgs = resolveAll(r.Errors, res), r.Messages
	}
	out := map[string][]api.AnalysisFinding{}
	for _, er := range errs {
		if er.Path == "" || !filepath.IsAbs(er.Path) {
			continue
		}
		out[er.Path] = append(out[er.Path], api.AnalysisFinding{Line: er.Line, Message: er.Message, Identifier: er.Identifier, Tip: er.Tip})
	}
	return out, msgs, nil
}

// resolveAll maps the paths to host files and drops errors of files that
// do not exist (any more) on the host.
func resolveAll(errs []phpstan.Raw, res phpstan.Resolver) []phpstan.Raw {
	out := make([]phpstan.Raw, 0, len(errs))
	for _, e := range errs {
		p, ok := res.Resolve(e.Path)
		if !ok {
			continue
		}
		e.Path = p
		if fp, ok := res.Resolve(e.FilePath); ok {
			e.FilePath = fp
		}
		if e.TraitPath != "" {
			if tp, ok := res.Resolve(e.TraitPath); ok {
				e.TraitPath = tp
			}
		}
		out = append(out, e)
	}
	return out
}

// refreshFiles recomputes ranges and stale marks of files (nil: all) from
// the current text, and drops findings on lines that no longer exist.
func (m *Module) refreshFiles(files []string) {
	app := m.application()
	if app == nil {
		return
	}
	m.mu.Lock()
	since := m.st.usedMod
	full := files == nil
	if full {
		files = make([]string, 0, len(m.st.raw)+len(m.st.runs))
		for f := range m.st.raw {
			files = append(files, f)
		}
		for f := range m.st.runs {
			if _, ok := m.st.raw[f]; !ok {
				files = append(files, f)
			}
		}
	}
	type job struct {
		file string
		fs   []api.AnalysisFinding
		at   time.Time
	}
	jobs := make([]job, 0, len(files))
	for _, f := range files {
		j := job{file: f, fs: m.st.raw[f], at: since}
		if r, ok := m.st.runs[f]; ok {
			j.fs, j.at = r.findings, r.at
		}
		jobs = append(jobs, j)
	}
	m.mu.Unlock()
	results := make(map[string][]api.AnalysisFinding, len(jobs))
	for _, j := range jobs {
		results[j.file] = place(app, j.file, j.fs, j.at)
	}
	m.mu.Lock()
	// A full refresh replaces the store at once: readers never see it
	// half filled.
	if full || m.st.files == nil {
		m.st.files = make(map[string][]api.AnalysisFinding, len(results))
	}
	for f, fs := range results {
		if len(fs) == 0 {
			delete(m.st.files, f)
		} else {
			m.st.files[f] = fs
		}
	}
	m.st.version++
	m.mu.Unlock()
	m.publishStatus()
}

// place fills Start/End from the current text of file and marks findings
// stale when the file changed after since (zero: never).
func place(app *api.App, file string, fs []api.AnalysisFinding, since time.Time) []api.AnalysisFinding {
	if len(fs) == 0 {
		return nil
	}
	text, _, err := app.ReadText(file)
	if err != nil {
		return nil // deleted
	}
	stale := false
	if st, err := os.Stat(file); err == nil && !since.IsZero() && st.ModTime().After(since) {
		stale = true
	}
	starts := lineStarts(text)
	out := make([]api.AnalysisFinding, 0, len(fs))
	for _, f := range fs {
		f.Stale = stale
		if f.Line <= 0 {
			f.Start, f.End = 0, 0
			out = append(out, f)
			continue
		}
		if f.Line > len(starts) {
			continue // the line is gone
		}
		ls := starts[f.Line-1]
		le := len(text)
		if f.Line < len(starts) {
			le = starts[f.Line] - 1
		}
		line := string(bytes.TrimRight(text[ls:le], "\r"))
		if s, e, ok := phpstan.Locate(line, f.Message); ok {
			f.Start, f.End = ls+s, ls+e
		} else {
			f.Start, f.End = ls, ls
		}
		out = append(out, f)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Line < out[j].Line })
	return out
}

// lineStarts returns the byte offset of every line start.
func lineStarts(text []byte) []int {
	out := []int{0}
	for i, c := range text {
		if c == '\n' && i+1 <= len(text) {
			out = append(out, i+1)
		}
	}
	if len(text) > 0 && text[len(text)-1] == '\n' {
		out = out[:len(out)-1]
	}
	return out
}

// foldRun takes the findings of Tiegel's own PHPStan run (qa.diagnostics
// of the QA tool) for the run's paths, then re-checks the source (the
// run may have rewritten the cache).
func (m *Module) foldRun(ctx context.Context, paths []string) {
	app := m.application()
	if app == nil {
		return
	}
	var res []api.QADiagnosticsResult
	if err := app.CallLocal(ctx, "qa.diagnostics", api.QADiagnosticsParams{Tool: qaTool}, &res); err != nil {
		return
	}
	var scope []string
	for _, p := range paths {
		if a, err := app.ResolvePath(p); err == nil {
			scope = append(scope, a)
		}
	}
	root := app.ProjectRoot()
	now := time.Now()
	runs := map[string]runEntry{}
	for _, r := range res {
		if !inScope(r.File, scope, root) {
			continue
		}
		var fs []api.AnalysisFinding
		for _, d := range r.Diagnostics {
			fs = append(fs, api.AnalysisFinding{Line: d.Line, Message: d.Message, Identifier: d.Code})
		}
		runs[r.File] = runEntry{at: now, findings: fs}
	}
	m.mu.Lock()
	if m.st.runs == nil {
		m.st.runs = map[string]runEntry{}
	}
	// Files of the scope without findings are clean now.
	for f := range m.st.raw {
		if _, ok := runs[f]; !ok && inScope(f, scope, root) {
			runs[f] = runEntry{at: now}
		}
	}
	for f, r := range runs {
		m.st.runs[f] = r
	}
	m.mu.Unlock()
	if _, err := m.load(ctx, loadOpts{ifChanged: true}); err != nil {
		return
	}
	files := make([]string, 0, len(runs))
	for f := range runs {
		files = append(files, f)
	}
	m.refreshFiles(files)
}

// inScope reports whether file is below one of scope (empty: the project).
func inScope(file string, scope []string, root string) bool {
	if len(scope) == 0 {
		return root == "" || within(root, file)
	}
	for _, s := range scope {
		if file == s || within(s, file) {
			return true
		}
	}
	return false
}

func within(dir, p string) bool {
	rel, err := filepath.Rel(dir, p)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel)
}
