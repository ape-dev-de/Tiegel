package headless

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFileTemplateCommands(t *testing.T) {
	t.Parallel()
	dir := project(t, map[string]string{
		"composer.json": `{"autoload": {"psr-4": {"App\\": "src/"}}, "autoload-dev": {"psr-4": {"App\\Tests\\": "tests/"}}}`,
	})
	cli := func(args ...string) (int, string, string) {
		return run(t, "", append(args, "--project", dir)...)
	}
	code, out, errs := cli("new-file", "tests/Unit/OrderTest.php", "--template", "test")
	if code != ExitOK || !strings.Contains(out, `test App\Tests\Unit\OrderTest`) {
		t.Fatalf("new-file %d %q %q", code, out, errs)
	}
	b, _ := os.ReadFile(filepath.Join(dir, "tests", "Unit", "OrderTest.php"))
	if !strings.Contains(string(b), "final class OrderTest extends TestCase") {
		t.Fatalf("test file %q", b)
	}
	code, out, _ = cli("new-class", "src/Model", "Order", "--kind", "interface", "--dry-run")
	if code != ExitOK || !strings.Contains(out, "namespace App\\Model;\n\ninterface Order") {
		t.Fatalf("dry run %q", out)
	}
	if _, err := os.Stat(filepath.Join(dir, "src", "Model", "Order.php")); err == nil {
		t.Fatal("dry run created the file")
	}
	if code, _, _ = cli("new-file", "x.php", "--template", "class", "--content", "x"); code != ExitUsage {
		t.Fatalf("content and template: %d", code)
	}
	code, out, _ = cli("file-templates")
	if code != ExitOK || !strings.Contains(out, "enum") || !strings.Contains(out, "built-in") {
		t.Fatalf("list %q", out)
	}
	code, out, _ = cli("file-templates", "trait")
	if code != ExitOK || !strings.Contains(out, "trait ${NAME}") {
		t.Fatalf("show %q", out)
	}
	if code, _, _ = cli("file-templates", "nope"); code != ExitUsage {
		t.Fatalf("unknown kind %d", code)
	}
}
