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
		{"bin/tool", "#!/bin/sh\necho hi\n", PlainText},
		{"bin/tool", "#!/usr/bin/env phpunit\n", PlainText},
		{"bin/tool", "#!/usr/bin/env python\n", PlainText},
		{"legacy/functions.inc", "<?php function x() {}", PHP},
		{"legacy/data.inc", "plain", PlainText},
		{"page", "<html><body><?PHP echo 1; ?></body></html>", PHP},
		{"short", "  <?= $x ?>", PHP},
		{"feed", "\xEF\xBB\xBF<?xml version=\"1.0\"?><rss/>", XML},
		{"Makefile", "all:\n", PlainText},
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
	if len(All) != 6 {
		t.Fatal("All")
	}
}
