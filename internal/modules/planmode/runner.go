package planmode

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/ape-dev-de/tiegel/internal/api"
	"github.com/ape-dev-de/tiegel/internal/orchestrate"
	"github.com/ape-dev-de/tiegel/internal/run"
	"github.com/ape-dev-de/tiegel/internal/teamcity"
)

// Generated files inside a plan worktree (excluded from git).
const (
	bootstrapFile = ".tiegel-plan-bootstrap.php"
	coverageFile  = ".tiegel-plan-coverage.xml"
)

// testRunner returns the runner of the plan's tests.
func (m *Module) testRunner(root string) orchestrate.TestRunner {
	if m.opts.Tests != nil {
		return m.opts.Tests
	}
	return &phpunitRunner{m: m, root: root}
}

// phpunitRunner runs PHPUnit through the run infrastructure in the
// project interpreter (host, Docker compose service or image), with
// TeamCity output. In a plan worktree the main checkout's vendor/ is
// used through a generated bootstrap (D569); all paths on the command
// line are relative to the PHP root, so a container that mounts the
// project runs it as well.
type phpunitRunner struct {
	m    *Module
	root string
}

var bootstrapAttr = regexp.MustCompile(`\bbootstrap\s*=\s*"([^"]+)"`)

// Run implements orchestrate.TestRunner.
func (r *phpunitRunner) Run(ctx context.Context, req orchestrate.TestRequest) (orchestrate.TestReport, error) {
	app := r.m.application()
	mainPHP := app.PHPRoot()
	if mainPHP == "" {
		mainPHP = r.root
	}
	phpRel, _ := filepath.Rel(r.root, mainPHP)
	dirPHP := filepath.Join(req.Dir, phpRel)
	phpunit := filepath.Join(mainPHP, "vendor", "bin", "phpunit")
	if _, err := os.Stat(phpunit); err != nil {
		return orchestrate.TestReport{Error: "PHPUnit not found (vendor/bin/phpunit in " + mainPHP + "; composer install)"}, nil
	}
	exe, _ := filepath.Rel(dirPHP, phpunit)
	args := []string{exe, "--teamcity", "--colors=never"}
	cfg := ""
	for _, n := range []string{"phpunit.xml", "phpunit.xml.dist", "phpunit.dist.xml"} {
		if _, err := os.Stat(filepath.Join(dirPHP, n)); err == nil {
			cfg = n
			break
		}
	}
	if cfg != "" {
		args = append(args, "-c", cfg)
	}
	if filepath.Clean(dirPHP) != filepath.Clean(mainPHP) {
		// A worktree: dependencies of the main checkout, code of the
		// worktree.
		orig := ""
		if cfg != "" {
			if b, err := os.ReadFile(filepath.Join(dirPHP, cfg)); err == nil {
				if mm := bootstrapAttr.FindSubmatch(b); mm != nil {
					orig = string(mm[1])
				}
			}
		}
		cj, _ := os.ReadFile(filepath.Join(dirPHP, "composer.json"))
		vendorRel, _ := filepath.Rel(dirPHP, filepath.Join(mainPHP, "vendor"))
		boot, err := orchestrate.WorktreeBootstrap(vendorRel, cj, orig)
		if err != nil {
			return orchestrate.TestReport{Error: err.Error()}, nil
		}
		if err := os.WriteFile(filepath.Join(dirPHP, bootstrapFile), []byte(boot), 0o644); err != nil {
			return orchestrate.TestReport{}, fmt.Errorf("planmode: bootstrap: %w", err)
		}
		args = append(args, "--bootstrap", bootstrapFile)
	}
	covPath := filepath.Join(dirPHP, coverageFile)
	_ = os.Remove(covPath)
	if req.Coverage {
		args = append(args, "--coverage-clover", coverageFile)
	}
	for _, f := range req.Files {
		rel, err := filepath.Rel(dirPHP, filepath.Join(req.Dir, filepath.FromSlash(f)))
		if err != nil {
			rel = f
		}
		args = append(args, filepath.ToSlash(rel))
	}
	spec := &run.Spec{Name: "Plan tests", PHP: true, Args: args, Dir: dirPHP, Interpreter: run.InterpreterProject, Quiet: true}
	var info run.Info
	if err := app.CallLocal(ctx, "run.start", api.RunStartParams{Spec: spec}, &info); err != nil {
		return orchestrate.TestReport{Error: "start PHPUnit: " + err.Error()}, nil
	}
	wctx, cancel := context.WithTimeout(ctx, 15*time.Minute)
	defer cancel()
	if err := app.CallLocal(wctx, "run.wait", api.RunIDParams{RunID: info.ID}, &info); err != nil {
		_ = app.CallLocal(context.WithoutCancel(ctx), "run.stop", api.RunIDParams{RunID: info.ID}, nil)
		if ctx.Err() != nil {
			return orchestrate.TestReport{}, ctx.Err()
		}
		return orchestrate.TestReport{Error: "PHPUnit: " + err.Error()}, nil
	}
	var page run.Page
	_ = app.CallLocal(ctx, "run.output", api.RunOutputParams{RunID: info.ID}, &page)
	_ = app.CallLocal(ctx, "run.remove", api.RunIDParams{RunID: info.ID}, nil)
	var ms []api.MappingInfo
	var mres struct {
		Mappings []api.MappingInfo `json:"mappings"`
	}
	if app.CallLocal(ctx, "runtime.mappings.get", struct{}{}, &mres) == nil {
		ms = mres.Mappings
	}
	var lines []string
	for _, l := range page.Lines {
		lines = append(lines, l.Text)
	}
	rep := Parse(lines, req.Dir, ms)
	if req.Coverage {
		if b, err := os.ReadFile(covPath); err == nil {
			_ = os.Remove(covPath)
			cov, err := orchestrate.ParseClover(b, func(p string) string { return orchestrate.RelativeTo(req.Dir, p, ms) })
			if err == nil {
				rep.Coverage = cov
			}
		} else if strings.Contains(strings.Join(lines, "\n"), "No code coverage driver") {
			rep.CoverageNote = "no coverage driver (pcov or Xdebug) in the interpreter"
		} else {
			rep.CoverageNote = "PHPUnit wrote no coverage report"
		}
	}
	if len(rep.Tests) == 0 && rep.Error == "" && info.ExitCode != 0 {
		rep.Error = fmt.Sprintf("PHPUnit exited with %d: %s", info.ExitCode, tail(lines, 8))
	}
	return rep, nil
}

func tail(lines []string, n int) string {
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.TrimSpace(strings.Join(lines, "\n"))
}

// Parse turns PHPUnit's TeamCity output into a test report; paths are
// made relative to dir.
func Parse(lines []string, dir string, ms []api.MappingInfo) orchestrate.TestReport {
	tree := teamcity.NewTree()
	for _, l := range lines {
		tree.Feed(l)
	}
	tree.Finish()
	var rep orchestrate.TestReport
	var walk func(ns []teamcity.Node)
	walk = func(ns []teamcity.Node) {
		for _, n := range ns {
			if n.Kind.IsTest() {
				tr := orchestrate.TestResult{ID: strings.TrimPrefix(n.ID, `\`), File: orchestrate.RelativeTo(dir, n.File, ms), Message: n.Message, Details: n.Details,
					FailFile: orchestrate.RelativeTo(dir, n.FailFile, ms), FailLine: n.FailLine}
				switch n.Status {
				case teamcity.StatusPassed:
					tr.Status = orchestrate.TestPassed
				case teamcity.StatusIgnored:
					tr.Status = orchestrate.TestIgnored
				default:
					tr.Status = orchestrate.TestFailed
				}
				rep.Tests = append(rep.Tests, tr)
			}
			var kids []teamcity.Node
			for _, c := range n.Children {
				kids = append(kids, *c)
			}
			walk(kids)
		}
	}
	walk(tree.Snapshot())
	return rep
}
