package phproot

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
)

type composerConfig struct {
	Config struct {
		VendorDir string `json:"vendor-dir"`
		BinDir    string `json:"bin-dir"`
	} `json:"config"`
}

func readConfig(data []byte) composerConfig {
	var c composerConfig
	_ = json.Unmarshal(data, &c)
	return c
}

// VendorDir returns config.vendor-dir of a composer.json (slash
// separated, without trailing slash; "vendor" when unset or invalid).
func VendorDir(composerJSON []byte) string {
	v := strings.TrimRight(strings.TrimSpace(readConfig(composerJSON).Config.VendorDir), `/\`)
	if v == "" {
		return "vendor"
	}
	return filepath.ToSlash(v)
}

// BinDir returns Composer's bin directory of the PHP root dir (absolute):
// config.bin-dir, else <vendor-dir>/bin.
func BinDir(dir string) string {
	b, _ := os.ReadFile(filepath.Join(dir, "composer.json"))
	c := readConfig(b)
	if bd := strings.TrimSpace(c.Config.BinDir); bd != "" {
		return join(dir, bd)
	}
	return filepath.Join(join(dir, VendorDir(b)), "bin")
}

// Vendor returns the vendor directory of the PHP root dir (absolute).
func Vendor(dir string) string {
	b, _ := os.ReadFile(filepath.Join(dir, "composer.json"))
	return join(dir, VendorDir(b))
}

func join(dir, p string) string {
	if filepath.IsAbs(p) {
		return filepath.Clean(p)
	}
	return filepath.Join(dir, filepath.FromSlash(p))
}
