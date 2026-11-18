package api

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFileTemplates(t *testing.T) {
	if fileTemplateSpecsErr != nil {
		t.Fatal(fileTemplateSpecsErr)
	}
	a, root := fileProject(t, map[string]string{
		"composer.json": `{
			"autoload": {"psr-4": {"App\\": "src/"}},
			"autoload-dev": {"psr-4": {"App\\Tests\\": "tests/"}}
		}`,
		"src/.keep": "",
	})
	var res PHPClassResult
	call(t, a, "files.createPhpClass", PHPClassParams{Dir: "tests/Unit", Name: "OrderTest", Kind: "test"}, &res)
	got := readFile(t, filepath.Join(root, "tests", "Unit", "OrderTest.php"))
	if !strings.Contains(got, "namespace App\\Tests\\Unit;\n\nuse PHPUnit\\Framework\\TestCase;\n\nfinal class OrderTest extends TestCase\n{") || res.Class != `App\Tests\Unit\OrderTest` {
		t.Fatalf("test file\n%s", got)
	}
	// The project's own TestCase in the test namespace root is the base;
	// a test in that namespace needs no import.
	writeFile(t, filepath.Join(root, "tests", "TestCase.php"), "<?php\n")
	call(t, a, "files.createPhpClass", PHPClassParams{Dir: "tests", Name: "ShopTest", Kind: "test", DryRun: true}, &res)
	if !strings.Contains(res.Content, "namespace App\\Tests;\n\nfinal class ShopTest extends TestCase") || strings.Contains(res.Content, "use ") {
		t.Fatalf("own base\n%s", res.Content)
	}
	if res.Path != "tests/ShopTest.php" || fileExists(filepath.Join(root, "tests", "ShopTest.php")) {
		t.Fatalf("dry run %+v", res)
	}
	call(t, a, "files.createPhpClass", PHPClassParams{Dir: "tests/Unit", Name: "CartTest", Kind: "test", DryRun: true}, &res)
	if !strings.Contains(res.Content, "use App\\Tests\\TestCase;\n\nfinal class CartTest extends TestCase") {
		t.Fatalf("own base from a sub namespace\n%s", res.Content)
	}
	// A user template (one line with \n escapes); without namespace the
	// namespace line and its blank line go.
	raw, _ := json.Marshal(`<?php\n\nnamespace ${NAMESPACE};\n\n/** ${FQN} by ${USER} ${UNKNOWN} */\nclass ${NAME} {}`)
	call(t, a, "settings.set", SettingsSetParams{Key: "fileTemplates.class", Value: raw}, nil)
	t.Setenv("USER", "dev")
	empty := ""
	call(t, a, "files.createPhpClass", PHPClassParams{Name: "Plain", Namespace: &empty, DryRun: true}, &res)
	if res.Content != "<?php\n\n/** Plain by dev ${UNKNOWN} */\nclass Plain {}\n" {
		t.Fatalf("user template %q", res.Content)
	}
	var list []FileTemplateInfo
	call(t, a, "files.templates", nil, &list)
	if len(list) != 6 || list[0].Kind != "class" || !list[0].Custom || list[5].Kind != "test" || list[5].Custom || list[0].Setting != "fileTemplates.class" {
		t.Fatalf("list %+v", list)
	}
	wantCode(t, a, "files.createPhpClass", PHPClassParams{Name: "X", Kind: "struct"}, CodeInvalidParams)
	if _, err := a.renderFileTemplate("nope", nil); err == nil {
		t.Fatal("unknown kind rendered")
	}
}

func TestRenderTemplate(t *testing.T) {
	got := renderTemplate("a\n\nuse ${X};\n\nb ${Y}\n${X}\nc", map[string]string{"X": "", "Y": "y"})
	if got != "a\n\nb y\nc" {
		t.Fatalf("%q", got)
	}
}

func fileExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}
