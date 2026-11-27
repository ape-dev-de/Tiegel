package orchestrate

import (
	"fmt"
	"os"
	"path/filepath"
)

func fileExists(dir, rel string) bool {
	_, err := os.Stat(filepath.Join(dir, filepath.FromSlash(rel)))
	return err == nil
}

func readFile(dir, rel string) ([]byte, error) {
	b, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(rel)))
	if err != nil {
		return nil, fmt.Errorf("orchestrate: read %s: %w", rel, err)
	}
	return b, nil
}

func writeFile(dir, rel string, data []byte) error {
	p := filepath.Join(dir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return fmt.Errorf("orchestrate: write %s: %w", rel, err)
	}
	if err := os.WriteFile(p, data, 0o644); err != nil {
		return fmt.Errorf("orchestrate: write %s: %w", rel, err)
	}
	return nil
}
