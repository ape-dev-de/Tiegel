package headless

import (
	"strings"
	"testing"
)

// Dockerfile detection and highlighting work headless in every build (the
// lexer needs no Tree-sitter).
func TestDockerfileCommands(t *testing.T) {
	t.Parallel()
	dir := project(t, map[string]string{
		"Dockerfile":            "FROM php:8.3 AS base\n# c\nRUN apt-get update \\\n    && echo \"$X\"\n",
		"docker/Dockerfile.dev": "FROM base\n",
		"app.dockerfile":        "FROM base\n",
		".dockerignore":         "vendor/\n!keep\n",
		"Makefile":              "test:\n\tphpunit\n",
		"bin/deploy":            "#!/usr/bin/env bash\necho hi\n",
	})
	code, out, _ := run(t, "", "lang", "--project", dir, "Dockerfile", "docker/Dockerfile.dev", "app.dockerfile", ".dockerignore", "Makefile", "bin/deploy")
	for _, want := range []string{"Dockerfile\tdockerfile", "docker/Dockerfile.dev\tdockerfile", "app.dockerfile\tdockerfile",
		".dockerignore\tignore", "Makefile\tmakefile", "bin/deploy\tshell"} {
		if code != ExitOK || !strings.Contains(out, want) {
			t.Fatalf("lang: missing %q in %d %q", want, code, out)
		}
	}
	code, out, _ = run(t, "", "highlights", "Dockerfile", "--project", dir)
	for _, want := range []string{"0\t4\tkeyword\t\"FROM\"", "\ttype\t\"php:8.3\"", "\tkeyword\t\"AS\"", "\tconstant\t\"base\"",
		"\tcomment\t\"# c\"", "\tkeyword\t\"RUN\"", "\tpunctuation\t\"\\\\\"", "\toperator\t\"&&\"", "\tvariable\t\"$X\""} {
		if code != ExitOK || !strings.Contains(out, want) {
			t.Fatalf("highlights: missing %q in %d %q", want, code, out)
		}
	}
	_, out, _ = run(t, "", "highlights", "Dockerfile", "--json", "--project", dir)
	if !strings.Contains(out, `"language": "dockerfile"`) {
		t.Fatalf("json %q", out)
	}
	_, out, _ = run(t, "", "highlights", "Makefile", "--project", dir)
	if !strings.Contains(out, "0\t4\tfunction\t\"test\"") {
		t.Fatalf("makefile %q", out)
	}
	// --language overrides detection.
	_, out, _ = run(t, "", "highlights", "Makefile", "--language", "dockerfile", "--project", dir)
	if strings.Contains(out, "function") {
		t.Fatalf("forced language %q", out)
	}
}
