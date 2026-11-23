package toolfind

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func tree(t *testing.T, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	for rel, c := range files {
		p := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(c), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func TestNestedComposerProject(t *testing.T) {
	root := tree(t, map[string]string{
		"project/composer.json":      `{"config":{"vendor-dir":"Vendor/"}}`,
		"project/Vendor/bin/phpunit": "#!/bin/sh\n",
		"project/Vendor/bin/phpstan": "#!/bin/sh\n",
		"project/phpunit.xml":        "<phpunit/>",
		"project/phpstan.neon":       "",
		"tools/composer.json":        `{"config":{"bin-dir":"bin"}}`,
		"tools/bin/phpunit":          "#!/bin/sh\n",
		"phpunit.xml.dist":           "<phpunit/>",
		"node_modules/x/phpunit.xml": "<phpunit/>",
		"composer.phar":              "",
		"broken/composer.json":       "{",
	})
	php := filepath.Join(root, "project")
	ex := Executables(root, php, "phpunit")
	if len(ex) != 2 || ex[0].Rel != "project/Vendor/bin/phpunit" || !ex[0].InPHPRoot || ex[1].Rel != "tools/bin/phpunit" || ex[1].InPHPRoot {
		t.Fatalf("executables %+v", ex)
	}
	cf := Files(root, php, "phpunit.xml", "phpunit.xml.dist")
	if len(cf) != 2 || cf[0].Rel != "project/phpunit.xml" || cf[1].Rel != "phpunit.xml.dist" {
		t.Fatalf("configs %+v", cf)
	}
	if c := Executables(root, "", "composer"); len(c) != 1 || c[0].Rel != "composer.phar" || !c[0].InPHPRoot {
		t.Fatalf("phar %+v", c)
	}
	if p, ok := ReadComposer(filepath.Join(root, "broken")); !ok || p.BinDir != filepath.Join(root, "broken", "vendor", "bin") {
		t.Fatalf("broken %+v", p)
	}
	if _, ok := ReadComposer(filepath.Join(root, "none")); ok {
		t.Fatal("no composer.json")
	}
	if p, _ := ReadComposer(filepath.Join(root, "project")); p.VendorDir != filepath.Join(root, "project", "Vendor") {
		t.Fatalf("vendor dir %+v", p)
	}
	if len(ComposerProjects(root, php, 1)) != 3 {
		t.Fatalf("projects %+v", ComposerProjects(root, php, 1))
	}
}

func TestShells(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("no /etc/shells")
	}
	dir := t.TempDir()
	sh := filepath.Join(dir, "sh")
	if err := os.WriteFile(sh, nil, 0o755); err != nil {
		t.Fatal(err)
	}
	list := filepath.Join(dir, "shells")
	if err := os.WriteFile(list, []byte("# comment\n"+sh+"\n/nonexistent/sh\n\n"+sh+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := Shells(list); len(got) != 1 || got[0] != sh {
		t.Fatalf("shells %v", got)
	}
	if Shells(filepath.Join(dir, "missing")) != nil {
		t.Fatal("missing file")
	}
	_ = Shells("")
}
