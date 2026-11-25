package main

import (
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRender(t *testing.T) {
	img := render(design(), 128)
	if got := img.Bounds().Dx(); got != 128 {
		t.Fatalf("width = %d", got)
	}
	// Corners lie outside the rounded tile and stay transparent.
	if _, _, _, a := img.At(1, 1).RGBA(); a != 0 {
		t.Errorf("corner alpha = %d, want 0", a)
	}
	light := func(x, y int, what string) {
		t.Helper()
		r, g, b, a := img.At(x, y).RGBA()
		if a != 0xffff || r < 0xd000 || g < 0xd000 || b < 0xd000 {
			t.Errorf("%s pixel = %x %x %x %x, want light grey", what, r, g, b, a)
		}
	}
	light(64, 78, "body")
	light(69, 40, "heat")
	// The tile is dark green.
	r, g, b, _ := img.At(20, 64).RGBA()
	if g <= r || g <= b || g > 0x6000 {
		t.Errorf("tile pixel = %x %x %x, want dark green", r, g, b)
	}
}

func TestRunWritesFiles(t *testing.T) {
	dir := t.TempDir()
	pngPath := filepath.Join(dir, "icon.png")
	svgPath := filepath.Join(dir, "icon.svg")
	if err := run(pngPath, 64, svgPath); err != nil {
		t.Fatal(err)
	}
	f, err := os.Open(pngPath)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	img, err := png.Decode(f)
	if err != nil {
		t.Fatal(err)
	}
	if img.Bounds().Dx() != 64 || img.Bounds().Dy() != 64 {
		t.Fatalf("bounds = %v", img.Bounds())
	}
	svg, err := os.ReadFile(svgPath)
	if err != nil {
		t.Fatal(err)
	}
	s := string(svg)
	for _, want := range []string{"<svg", "viewBox=\"0 0 1024 1024\"", "<linearGradient", "<path", "Z\""} {
		if !strings.Contains(s, want) {
			t.Errorf("svg lacks %q", want)
		}
	}
}

func TestRunErrors(t *testing.T) {
	if err := run("", 64, ""); err == nil {
		t.Error("expected error without outputs")
	}
	if err := run("x.png", 8, ""); err == nil {
		t.Error("expected error for tiny size")
	}
	bad := filepath.Join(t.TempDir(), "missing", "icon.png")
	if err := run(bad, 64, ""); err == nil {
		t.Error("expected error for unwritable path")
	}
	if err := run("", 64, bad); err == nil {
		t.Error("expected error for unwritable svg path")
	}
}
