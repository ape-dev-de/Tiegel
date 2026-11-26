package headless

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ape-dev-de/tiegel/internal/acp/acptest"
	"github.com/ape-dev-de/tiegel/internal/api"
	agentmod "github.com/ape-dev-de/tiegel/internal/modules/agent"
)

// phpstanProject is the fixture project of internal/phpstan/testdata
// with PHPStan 2.2's result cache in build/phpstan-tmp.
func phpstanProject(t *testing.T) string {
	t.Helper()
	read := func(n string) string {
		b, err := os.ReadFile(filepath.Join("..", "phpstan", "testdata", n))
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	dir := project(t, map[string]string{
		"composer.json":                     `{}`,
		"src/Greeter.php":                   read("Greeter.php.txt"),
		"src/Shouting.php":                  read("Shouting.php.txt"),
		"phpstan-baseline.neon":             read("phpstan-baseline.neon"),
		"phpstan.neon":                      "includes:\n\t- phpstan-baseline.neon\nparameters:\n\ttmpDir: build/phpstan-tmp\n",
		"build/phpstan-tmp/resultCache.php": read("resultCache-2.2.php"),
		"build/phpstan-checkstyle.xml":      read("report-checkstyle.xml"),
	})
	old := time.Now().Add(-time.Hour)
	for _, f := range []string{"src/Greeter.php", "src/Shouting.php"} {
		_ = os.Chtimes(filepath.Join(dir, f), old, old)
	}
	return dir
}

func TestPHPStanCommands(t *testing.T) {
	dir := phpstanProject(t)
	cli := func(args ...string) (int, string, string) {
		t.Helper()
		var out, errb bytes.Buffer
		env := testEnv(t, strings.NewReader(""), &out, &errb)
		// No agent program anywhere (never the developer's opencode).
		env.Modules.Agent = agentmod.Options{LookPath: func(string) (string, error) { return "", exec.ErrNotFound }, HomeDir: t.TempDir()}
		code := Run(context.Background(), append([]string{"--headless", "--project", dir}, args...), env)
		return code, out.String(), errb.String()
	}
	code, out, errs := cli("analysis", "sources")
	if code != 0 || !strings.Contains(out, "* config  build/phpstan-tmp/resultCache.php  [cache (PHPStan 2.2.17)]") ||
		!strings.Contains(out, "found   build/phpstan-checkstyle.xml  [report checkstyle]") || !strings.Contains(out, "findings: 4 in 2 file(s)") {
		t.Fatalf("%d %s %s", code, out, errs)
	}
	code, out, _ = cli("analysis", "findings")
	if code != ExitError || !strings.Contains(out, "src/Greeter.php:13: Call to an undefined method Acme\\Greeter::missing().  [method.notFound]") ||
		!strings.Contains(out, "    tip: See: https://phpstan.org/") || !strings.Contains(out, "4 finding(s) in 2 file(s)") {
		t.Fatalf("%d %s", code, out)
	}
	code, out, _ = cli("--json", "analysis", "findings", "src/Shouting.php")
	var f api.AnalysisFindings
	if code != ExitError || json.Unmarshal([]byte(out), &f) != nil || f.Count != 1 || f.Files[0].Findings[0].Identifier != "return.type" {
		t.Fatalf("%d %s", code, out)
	}
	if code, out, _ = cli("analysis", "findings", "--stale"); code != 0 || !strings.Contains(out, "no PHPStan findings") {
		t.Fatalf("%d %s", code, out)
	}
	code, out, _ = cli("analysis", "import", "build/phpstan-checkstyle.xml")
	if code != 0 || !strings.Contains(out, "* import  build/phpstan-checkstyle.xml  [report checkstyle]") {
		t.Fatalf("%d %s", code, out)
	}
	if code, out, _ = cli("analysis", "reload", "--reset"); code != 0 || !strings.Contains(out, "* config") {
		t.Fatalf("%d %s", code, out)
	}
	code, out, _ = cli("analysis", "fix", "src/Greeter.php:18", "--dry-run")
	if code != 0 || !strings.Contains(out, "Parameter #1 $string of function strlen expects string, int given.") || !strings.Contains(out, "> 18 |") {
		t.Fatalf("%d %s", code, out)
	}
	// Without an installed agent: unavailable, with the reason.
	code, _, errs = cli("analysis", "fix", "src/Greeter.php:18")
	if code == 0 || !strings.Contains(errs, "Fix with AI") {
		t.Fatalf("%d %s", code, errs)
	}
	for _, args := range [][]string{{"analysis", "fix"}, {"analysis", "fix", "src/Greeter.php"}, {"analysis", "import"}, {"analysis", "bogus"}, {"analysis", "findings", "a", "b"}} {
		if code, _, _ := cli(args...); code != ExitUsage {
			t.Errorf("%v: %d", args, code)
		}
	}
	if code, _, _ := cli("analysis", "fix", "src/Greeter.php:99", "--dry-run"); code == 0 {
		t.Fatal("no finding on line 99")
	}
}

func TestPHPStanFixWithAgent(t *testing.T) {
	dir := phpstanProject(t)
	cfg := filepath.Join(t.TempDir(), "cfg")
	fake := &acptest.Agent{Script: func(_ context.Context, turn *acptest.Turn) string {
		turn.Text("Fixed it.")
		return ""
	}}
	code, out, errs := runAgentCLI(t, cfg, fake, "--headless", "--project", dir, "analysis", "fix", "src/Greeter.php:13")
	if code != 0 || !strings.Contains(out, "Fixed it.") {
		t.Fatalf("%d %s %s", code, out, errs)
	}
	p := fake.Prompts()
	if len(p) != 1 || !strings.Contains(p[0], "Identifier: method.notFound") || !strings.Contains(p[0], "do not commit") {
		t.Fatalf("%q", p)
	}
}
