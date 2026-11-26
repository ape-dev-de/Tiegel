package langphp

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/ape-dev-de/tiegel/internal/lsp/supervisor"
)

func TestPHPScriptRunsWithHostPHP(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell script fake php")
	}
	bin, tools := t.TempDir(), t.TempDir()
	if err := os.WriteFile(filepath.Join(bin, "php"), []byte("#!/bin/sh\nprintf 8.4.1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	phar := filepath.Join(tools, "phpactor")
	if err := os.WriteFile(phar, []byte("#!/usr/bin/env php\n<?php\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	r := resolution{target: supervisor.Target{Name: "Phpactor", Command: phar, Args: []string{"language-server"}}}
	got := phpScript(r, "8.2", []string{bin})
	if got.notFound || got.target.Command != filepath.Join(bin, "php") || strings.Join(got.target.Args, " ") != phar+" language-server" {
		t.Fatalf("%+v", got)
	}
	// Too old: not started, the reason explains it.
	got = phpScript(r, "9.0", []string{bin})
	if !got.notFound || !strings.Contains(got.reason, "needs a local PHP") || !strings.Contains(got.reason, "8.4.1") {
		t.Fatalf("%+v", got)
	}
	// Binaries are left alone.
	bin2 := resolution{target: supervisor.Target{Command: filepath.Join(bin, "php")}}
	if phpScript(bin2, "8.2", nil).target.Command != bin2.target.Command {
		t.Fatal("binary changed")
	}
}
