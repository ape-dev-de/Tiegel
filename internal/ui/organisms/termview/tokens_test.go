package termview

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// TestNoRawDp: sizes come from the tokens, not from dp literals
// (review wave 9 U10).
func TestNoRawDp(t *testing.T) {
	raw := regexp.MustCompile(`(gtx\.Dp|unit\.Dp)\(\s*[0-9]`)
	files, _ := filepath.Glob("*.go")
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		for i, line := range strings.Split(string(b), "\n") {
			if raw.MatchString(line) {
				t.Errorf("%s:%d: raw dp value: %s", f, i+1, strings.TrimSpace(line))
			}
		}
	}
}
