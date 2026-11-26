package phpstan

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestRealCache reads the result cache named by TIEGEL_PHPSTAN_CACHE (a
// real project's resultCache.php; skipped without) and, with
// TIEGEL_PHPSTAN_CONFIG, applies its ignoreErrors.
func TestRealCache(t *testing.T) {
	file := os.Getenv("TIEGEL_PHPSTAN_CACHE")
	if file == "" {
		t.Skip("TIEGEL_PHPSTAN_CACHE not set")
	}
	start := time.Now()
	c, err := ReadResultCache(file)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("%s %s (%s): %d errors in %s", c.Format, c.Version, c.CacheVersion, len(c.Errors), time.Since(start))
	cfgFile := os.Getenv("TIEGEL_PHPSTAN_CONFIG")
	if cfgFile == "" {
		return
	}
	root := filepath.Dir(cfgFile)
	cfg, err := LoadConfig(cfgFile, Vars{WorkDir: root})
	if err != nil {
		t.Fatal(err)
	}
	res := Resolver{Base: filepath.Join(root, "vendor", "phpstan", "phpstan")}
	var errs []Raw
	missing := 0
	for _, e := range c.Errors {
		var ok bool
		if e.Path, ok = res.Resolve(e.Path); !ok {
			if missing < 3 {
				t.Log("unresolved:", e.Path)
			}
			missing++
		}
		e.FilePath, _ = res.Resolve(e.FilePath)
		if e.TraitPath != "" {
			e.TraitPath, _ = res.Resolve(e.TraitPath)
		}
		errs = append(errs, e)
	}
	kept, msgs := Ignore(errs, cfg.Ignore)
	t.Logf("%d config files, %d ignore rules, %d warnings; %d kept, %d unresolved, %d count messages, total %s",
		len(cfg.Files), len(cfg.Ignore), len(cfg.Warnings), len(kept), missing, len(msgs), time.Since(start))
	for _, w := range cfg.Warnings {
		t.Log("warning:", w)
	}
}
