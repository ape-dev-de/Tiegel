package langdetect

import (
	"strings"
	"testing"
)

func TestDetect(t *testing.T) {
	cases := []struct {
		path, head string
		want       Language
	}{
		{"src/Kernel.php", "", PHP},
		{"templates/x.phtml", "", PHP},
		{"Foo.PHP", "", PHP},
		{"tests/x.phpt", "", PHP},
		{"composer.json", "", JSON},
		{"composer.lock", "", JSON},
		{"docker-compose.yml", "", YAML},
		{"config/services.yaml", "", YAML},
		{"phpstan.neon", "", YAML},
		{"phpunit.xml", "", XML},
		{"phpunit.xml.dist", "", XML},
		{"phpstan.neon.dist", "", YAML},
		{".php_cs.dist", "", PHP},
		{".env", "", DotEnv},
		{".env.local", "", DotEnv},
		{"/proj/.env.example", "", DotEnv},
		{"deploy/prod.env", "", DotEnv},
		{"README.txt", "<?php", PlainText}, // explicit extension wins
		{"bin/console", "#!/usr/bin/env php\n<?php\n", PHP},
		{"bin/tool", "#!/usr/bin/php\n", PHP},
		{"bin/tool", "#!/usr/local/bin/php8.3 -d memory_limit=-1\r\n", PHP},
		{"bin/tool", "#! /usr/bin/env -S php -dxdebug.mode=off\n", PHP},
		{"bin/tool", "#!/opt/homebrew/bin/php", PHP},
		{"bin/tool", "#!/bin/sh\necho hi\n", Shell},
		{"bin/tool", "#!/usr/bin/env bash\n", Shell},
		{"bin/tool", "#!/bin/zsh -f\n", Shell},
		{"bin/tool", "#!/usr/bin/make -f\n", Makefile},
		{"bin/tool", "#!/usr/bin/env phpunit\n", PlainText},
		{"bin/tool", "#!/usr/bin/env python\n", PlainText},
		{"legacy/functions.inc", "<?php function x() {}", PHP},
		{"legacy/data.inc", "plain", PlainText},
		{"page", "<html><body><?PHP echo 1; ?></body></html>", PHP},
		{"short", "  <?= $x ?>", PHP},
		{"feed", "\xEF\xBB\xBF<?xml version=\"1.0\"?><rss/>", XML},
		{"Makefile", "all:\n", Makefile},
		{"GNUmakefile", "", Makefile},
		{"build/rules.mk", "", Makefile},
		{"Dockerfile", "", Dockerfile},
		{"docker/php/Dockerfile", "", Dockerfile},
		{"Containerfile", "", Dockerfile},
		{"Dockerfile.dev", "", Dockerfile},
		{"dockerfile.prod", "", Dockerfile},
		{"Containerfile.ci", "", Dockerfile},
		{"php.dockerfile", "", Dockerfile},
		{"app.Dockerfile", "", Dockerfile},
		{"build/image", "# syntax=docker/dockerfile:1.7\nFROM alpine\n", Dockerfile},
		{"build/image", "#syntax = docker.io/docker/dockerfile:1\n", Dockerfile},
		{"notes", "# syntax is fine\n", PlainText},
		{".dockerignore", "", Ignore},
		{".gitignore", "", Ignore},
		{"web/.npmignore", "", Ignore},
		{"deploy.sh", "", Shell},
		{"x.BASH", "", Shell},
		{".zshrc", "", Shell},
		{"docker/php.ini", "", INI},
		{"php.ini-development", "", INI},
		{"nginx/site.conf", "", INI},
		{"my.cnf", "", INI},
		{".editorconfig", "", INI},
		{"schema.sql", "", SQL},
		{"dump.sql.dist", "", SQL},
		{"noext", "", PlainText},
	}
	for _, c := range cases {
		if got := Detect(c.path, []byte(c.head)); got != c.want {
			t.Errorf("Detect(%q, %q) = %s, want %s", c.path, c.head, got, c.want)
		}
	}
}

func TestByContentOnlyLooksAtHead(t *testing.T) {
	head := strings.Repeat("x", HeadSize) + "<?php"
	if _, ok := ByContent([]byte(head)); ok {
		t.Fatal("marker beyond HeadSize must be ignored")
	}
}

func TestHelpersReportUnknown(t *testing.T) {
	if _, ok := ByPath("file.unknown"); ok {
		t.Fatal("ByPath")
	}
	if _, ok := ByShebang([]byte("no shebang")); ok {
		t.Fatal("ByShebang")
	}
	if _, ok := ByContent(nil); ok {
		t.Fatal("ByContent")
	}
	if len(All) != 12 {
		t.Fatal("All")
	}
}
